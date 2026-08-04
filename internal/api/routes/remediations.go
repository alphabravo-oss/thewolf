package routes

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/remediate"
	"github.com/alphabravocompany/thewolf/internal/wolflog"
)

// RemediationRunner drives agentic remediation sessions. Set by
// api.NewServer in production, mirroring SSEBroker's package-level wiring;
// tests may assign their own Runner (a fake driver, a specific Config) to
// avoid the docker-backed default.
var RemediationRunner *remediate.Runner

// RemediationConfig is the Config RemediationRunner was built with, kept
// alongside it so handlers can read the same immutable, process-wide gate
// settings (AllowYolo, the default provider/model, the master kill switch)
// the Runner itself consults — without needing an exported getter on Runner.
var RemediationConfig remediate.Config

// createRemediationRequest is the body of POST /remediations. ScanID and
// RepoID are required; everything else falls back to RemediationConfig's
// defaults when omitted. The gate flags are pointers so "omitted" (fail
// closed: gate on) is distinguishable from an explicit `false` (opt out).
type createRemediationRequest struct {
	ScanID           string `json:"scan_id"`
	RepoID           string `json:"repo_id"`
	PlanGateEnabled  *bool  `json:"plan_gate_enabled,omitempty"`
	PatchGateEnabled *bool  `json:"patch_gate_enabled,omitempty"`
	Provider         string `json:"provider,omitempty"`
	Model            string `json:"model,omitempty"`
	MaxTurns         int    `json:"max_turns,omitempty"`
}

// remediationApprovalRequest is the optional body of the two reject
// endpoints, carrying the human's stated reason. It has no required fields:
// a reject with no body (or an empty one) is valid.
type remediationApprovalRequest struct {
	Reason string `json:"reason,omitempty"`
}

// CreateRemediation handles POST /remediations — create a session and start
// it running in the background.
func CreateRemediation(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing user context")
		return
	}
	h := DefaultHandler
	if h == nil || h.Store == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}
	if RemediationRunner == nil {
		response.WriteError(w, http.StatusServiceUnavailable, "unavailable", "remediation runner not initialized")
		return
	}
	if !RemediationConfig.Enabled {
		response.WriteError(w, http.StatusForbidden, "remediation_disabled",
			"agentic remediation is disabled; enable WOLF_REMEDIATE_ENABLED to use this endpoint")
		return
	}

	var req createRemediationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	req.ScanID = strings.TrimSpace(req.ScanID)
	req.RepoID = strings.TrimSpace(req.RepoID)
	if req.ScanID == "" || req.RepoID == "" {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "scan_id and repo_id are required")
		return
	}

	planGate, patchGate := true, true
	if req.PlanGateEnabled != nil {
		planGate = *req.PlanGateEnabled
	}
	if req.PatchGateEnabled != nil {
		patchGate = *req.PatchGateEnabled
	}
	// Mirrors Runner.Run's own check (session.go) exactly: gates disabled but
	// no yolo opt-in is refused. Safe to duplicate here — unlike a session's
	// mutable status, AllowYolo is immutable process config that can't drift
	// between this check and the one Run performs itself moments later in the
	// background goroutine below, so there's no TOCTOU window to reopen.
	// Catching it here turns what would otherwise be a 201 immediately
	// followed by a silent async failure into an honest 403.
	//
	// Checked before the repo/scan existence lookups below: a caller who
	// isn't allowed to run yolo at all should get that answer regardless of
	// whether the referenced repo/scan happen to exist, not a 404 that
	// leaks existence information for a request that was going to be
	// refused either way.
	if (!planGate || !patchGate) && !RemediationConfig.AllowYolo {
		response.WriteError(w, http.StatusForbidden, "yolo_not_allowed",
			"one or both gates are disabled but WOLF_REMEDIATE_ALLOW_YOLO=false")
		return
	}

	if _, ok := loadRepoForCaller(w, r, h.Store, req.RepoID, claims); !ok {
		return
	}
	scan, ok := loadScanForCaller(w, r, h.Store, req.ScanID, claims)
	if !ok {
		return
	}
	if scan.RepoID != req.RepoID {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "scan_id does not belong to repo_id")
		return
	}

	provider := strings.TrimSpace(req.Provider)
	if provider == "" {
		provider = RemediationConfig.DefaultProvider
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = RemediationConfig.DefaultModel
	}

	now := time.Now()
	sess := &models.RemediationSession{
		ID:               uuid.New().String(),
		UserID:           claims.UserID,
		RepoID:           req.RepoID,
		ScanID:           req.ScanID,
		Status:           models.RemediationPending,
		PlanGateEnabled:  planGate,
		PatchGateEnabled: patchGate,
		MaxTurns:         RemediationConfig.ClampTurns(req.MaxTurns),
		Provider:         provider,
		Model:            model,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := h.Store.CreateRemediationSession(r.Context(), sess); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to create remediation session")
		return
	}

	// Runs to completion or its first gate in the background, detached from
	// the request context — like executeScan, since the driver call behind
	// it is container-backed and can run for minutes.
	runner := RemediationRunner
	go func(sessionID string) {
		if err := runner.Run(context.Background(), sessionID); err != nil {
			wolflog.L().Error().Err(err).Str("session", sessionID).Msg("remediation run failed")
		}
	}(sess.ID)

	response.WriteJSON(w, http.StatusCreated, response.SuccessResponse{Data: sess})
}

// ListRemediations handles GET /remediations — list the caller's sessions,
// optionally narrowed by repo_id, scan_id, or status query params.
func ListRemediations(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing user context")
		return
	}
	h := DefaultHandler
	if h == nil || h.Store == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}

	sessions, err := h.Store.ListRemediationSessions(r.Context(), claims.UserID)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to list remediation sessions")
		return
	}

	repoID := r.URL.Query().Get("repo_id")
	scanID := r.URL.Query().Get("scan_id")
	status := r.URL.Query().Get("status")
	if repoID != "" || scanID != "" || status != "" {
		filtered := make([]models.RemediationSession, 0, len(sessions))
		for _, s := range sessions {
			if repoID != "" && s.RepoID != repoID {
				continue
			}
			if scanID != "" && s.ScanID != scanID {
				continue
			}
			if status != "" && string(s.Status) != status {
				continue
			}
			filtered = append(filtered, s)
		}
		sessions = filtered
	}

	response.WriteJSON(w, http.StatusOK, response.ListResponse{
		Data: sessions,
		Meta: response.ListMeta{Total: len(sessions), Page: 1, PerPage: len(sessions)},
	})
}

// GetRemediation handles GET /remediations/{id}.
func GetRemediation(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing user context")
		return
	}
	h := DefaultHandler
	if h == nil || h.Store == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}
	sess, ok := loadRemediationForCaller(w, r, h.Store, chi.URLParam(r, "id"), claims)
	if !ok {
		return
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: sess})
}

// GetRemediationPlan handles GET /remediations/{id}/plan.
func GetRemediationPlan(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing user context")
		return
	}
	h := DefaultHandler
	if h == nil || h.Store == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}
	sess, ok := loadRemediationForCaller(w, r, h.Store, chi.URLParam(r, "id"), claims)
	if !ok {
		return
	}
	plan, err := h.Store.GetRemediationPlan(r.Context(), sess.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			response.WriteError(w, http.StatusNotFound, "not_found", "no plan recorded yet for this remediation session")
			return
		}
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to load remediation plan")
		return
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: plan})
}

// ApproveRemediationPlan handles POST /remediations/{id}/plan/approve. The
// call resumes the session straight into its execute phase (session.go's
// ApprovePlan), so this can block for as long as that phase's driver call
// runs — there is no background worker for remediation yet to hand it off
// to. Bounded by the session's MaxTurns/SessionTimeout either way.
func ApproveRemediationPlan(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing user context")
		return
	}
	h := DefaultHandler
	if h == nil || h.Store == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}
	if RemediationRunner == nil {
		response.WriteError(w, http.StatusServiceUnavailable, "unavailable", "remediation runner not initialized")
		return
	}
	id := chi.URLParam(r, "id")
	if _, ok := loadRemediationForCaller(w, r, h.Store, id, claims); !ok {
		return
	}

	if err := RemediationRunner.ApprovePlan(r.Context(), id, claims.UserID); err != nil {
		writeRemediationRunnerError(w, err)
		return
	}
	writeReloadedRemediationSession(w, r, h, id)
}

// RejectRemediationPlan handles POST /remediations/{id}/plan/reject.
func RejectRemediationPlan(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing user context")
		return
	}
	h := DefaultHandler
	if h == nil || h.Store == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}
	if RemediationRunner == nil {
		response.WriteError(w, http.StatusServiceUnavailable, "unavailable", "remediation runner not initialized")
		return
	}
	id := chi.URLParam(r, "id")
	if _, ok := loadRemediationForCaller(w, r, h.Store, id, claims); !ok {
		return
	}
	req, ok := decodeRemediationApproval(w, r)
	if !ok {
		return
	}

	if err := RemediationRunner.RejectPlan(r.Context(), id, claims.UserID, req.Reason); err != nil {
		writeRemediationRunnerError(w, err)
		return
	}
	writeReloadedRemediationSession(w, r, h, id)
}

// ListRemediationPatches handles GET /remediations/{id}/patches.
func ListRemediationPatches(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing user context")
		return
	}
	h := DefaultHandler
	if h == nil || h.Store == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}
	sess, ok := loadRemediationForCaller(w, r, h.Store, chi.URLParam(r, "id"), claims)
	if !ok {
		return
	}
	patches, err := h.Store.ListRemediationPatches(r.Context(), sess.ID)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to list remediation patches")
		return
	}
	response.WriteJSON(w, http.StatusOK, response.ListResponse{
		Data: patches,
		Meta: response.ListMeta{Total: len(patches), Page: 1, PerPage: len(patches)},
	})
}

// ApproveRemediationPatches handles POST /remediations/{id}/patches/approve.
// Resumes into the landing phase (session.go's ApprovePatches); see
// ApproveRemediationPlan's comment on why this call is synchronous.
func ApproveRemediationPatches(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing user context")
		return
	}
	h := DefaultHandler
	if h == nil || h.Store == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}
	if RemediationRunner == nil {
		response.WriteError(w, http.StatusServiceUnavailable, "unavailable", "remediation runner not initialized")
		return
	}
	id := chi.URLParam(r, "id")
	if _, ok := loadRemediationForCaller(w, r, h.Store, id, claims); !ok {
		return
	}

	if err := RemediationRunner.ApprovePatches(r.Context(), id, claims.UserID); err != nil {
		writeRemediationRunnerError(w, err)
		return
	}
	writeReloadedRemediationSession(w, r, h, id)
}

// RejectRemediationPatches handles POST /remediations/{id}/patches/reject.
func RejectRemediationPatches(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing user context")
		return
	}
	h := DefaultHandler
	if h == nil || h.Store == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}
	if RemediationRunner == nil {
		response.WriteError(w, http.StatusServiceUnavailable, "unavailable", "remediation runner not initialized")
		return
	}
	id := chi.URLParam(r, "id")
	if _, ok := loadRemediationForCaller(w, r, h.Store, id, claims); !ok {
		return
	}
	req, ok := decodeRemediationApproval(w, r)
	if !ok {
		return
	}

	if err := RemediationRunner.RejectPatches(r.Context(), id, claims.UserID, req.Reason); err != nil {
		writeRemediationRunnerError(w, err)
		return
	}
	writeReloadedRemediationSession(w, r, h, id)
}

// CancelRemediation handles DELETE /remediations/{id}. There is no Runner
// entry point for cancellation (Task 9's interface is Run/ApprovePlan/
// RejectPlan/ApprovePatches/RejectPatches only), so this transitions the
// session directly through the same compare-and-swap primitive Runner's own
// transition uses — it marks the row cancelled but does not reach into a
// goroutine that may currently be inside a driver call to stop it; there is
// no such registry for remediation yet (scans.go has activeScanCtxs, nothing
// equivalent exists here).
func CancelRemediation(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing user context")
		return
	}
	h := DefaultHandler
	if h == nil || h.Store == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}
	sess, ok := loadRemediationForCaller(w, r, h.Store, chi.URLParam(r, "id"), claims)
	if !ok {
		return
	}
	if isTerminalRemediationStatus(sess.Status) {
		response.WriteError(w, http.StatusConflict, "conflict", "remediation session has already finished")
		return
	}

	from := sess.Status
	next := *sess
	next.Status = models.RemediationCancelled
	now := time.Now()
	next.UpdatedAt = now
	next.CompletedAt = &now
	if err := h.Store.TransitionRemediationSession(r.Context(), &next, from); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			response.WriteError(w, http.StatusConflict, "conflict", "remediation session state changed concurrently")
			return
		}
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to cancel remediation session")
		return
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: &next})
}

// StreamRemediation handles GET /remediations/{id}/stream — SSE replay of
// the session's redacted event log, following the same replay-then-poll
// shape as scan_events.go's streamDurableScanEvents: drain everything after
// Last-Event-ID, then poll ListRemediationEvents for what's new until the
// session reaches a terminal state.
func StreamRemediation(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing user context")
		return
	}
	h := DefaultHandler
	if h == nil || h.Store == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}
	sess, ok := loadRemediationForCaller(w, r, h.Store, chi.URLParam(r, "id"), claims)
	if !ok {
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "streaming not supported")
		return
	}

	after := 0
	if raw := r.Header.Get("Last-Event-ID"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed >= 0 {
			after = parsed
		}
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		events, err := h.Store.ListRemediationEvents(r.Context(), sess.ID, after)
		if err != nil {
			return
		}
		for _, e := range events {
			fmt.Fprintf(w, "id: %d\n", e.Seq)             // nosemgrep: go.lang.security.audit.xss.no-fprintf-to-responsewriter.no-fprintf-to-responsewriter
			fmt.Fprintf(w, "data: %s\n\n", e.PayloadJSON) // nosemgrep: go.lang.security.audit.xss.no-fprintf-to-responsewriter.no-fprintf-to-responsewriter
			flusher.Flush()
			after = e.Seq
		}

		current, err := h.Store.GetRemediationSession(r.Context(), sess.ID)
		if err != nil {
			return
		}
		if isTerminalRemediationStatus(current.Status) && len(events) == 0 {
			return
		}

		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
	}
}

// loadRemediationForCaller loads a session and reports whether the caller
// may see it, writing the response and returning false otherwise. Not-owned
// is reported as 404 rather than 403 — matching fixes.go's ownsFixJob
// pattern — so a caller can't enumerate which session IDs exist for other
// tenants.
func loadRemediationForCaller(w http.ResponseWriter, r *http.Request, store db.Store, id string, claims *auth.Claims) (*models.RemediationSession, bool) {
	sess, err := store.GetRemediationSession(r.Context(), id)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", fmt.Sprintf("remediation session %s not found", id))
		return nil, false
	}
	if !canModifyOwned(claims, sess.UserID) {
		response.WriteError(w, http.StatusNotFound, "not_found", fmt.Sprintf("remediation session %s not found", id))
		return nil, false
	}
	return sess, true
}

// writeReloadedRemediationSession reloads a session after a Runner call
// mutated it and writes the current row — the approve/reject handlers' own
// sess pointer is stale by the time the Runner call returns.
func writeReloadedRemediationSession(w http.ResponseWriter, r *http.Request, h *Handler, id string) {
	sess, err := h.Store.GetRemediationSession(r.Context(), id)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to reload remediation session")
		return
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: sess})
}

// writeRemediationRunnerError maps a Runner error to an HTTP response.
// remediate.ErrWrongSessionState is the sentinel Task 9 added specifically
// so this layer never has to substring-match error text or re-read a
// session's status itself — the Runner's own compare-and-swap is the single
// source of truth for "was this the right state to call this from".
func writeRemediationRunnerError(w http.ResponseWriter, err error) {
	if errors.Is(err, remediate.ErrWrongSessionState) {
		response.WriteError(w, http.StatusConflict, "conflict", err.Error())
		return
	}
	response.WriteError(w, http.StatusInternalServerError, "server_error", "remediation runner call failed")
}

// decodeRemediationApproval reads the optional reason body shared by the two
// reject endpoints. An empty body (io.EOF) is valid — reason is optional.
func decodeRemediationApproval(w http.ResponseWriter, r *http.Request) (remediationApprovalRequest, bool) {
	var req remediationApprovalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return req, false
	}
	return req, true
}

// isTerminalRemediationStatus reports whether a session has reached a final
// state and will not advance further on its own.
func isTerminalRemediationStatus(status models.RemediationStatus) bool {
	switch status {
	case models.RemediationCompleted, models.RemediationFailed,
		models.RemediationCancelled, models.RemediationExhausted,
		models.RemediationRejected:
		return true
	default:
		return false
	}
}
