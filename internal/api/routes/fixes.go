package routes

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/models"
)

type createFixRequest struct {
	ScanID     string   `json:"scan_id"`
	FindingIDs []string `json:"finding_ids"`
	Severity   []string `json:"severity"`
}

// CreateFix handles POST /api/fixes — start a fix operation.
func CreateFix(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing user context")
		return
	}
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}

	var req createFixRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}

	if req.ScanID == "" && len(req.FindingIDs) == 0 {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "scan_id or finding_ids required")
		return
	}

	now := time.Now()
	fix := &models.Fix{
		ID:             uuid.New().String(),
		UserID:         claims.UserID,
		ScanID:         req.ScanID,
		Status:         models.FixStatusPending,
		SeverityFilter: toJSON(req.Severity),
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	if err := h.Store.CreateFix(r.Context(), fix); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to create fix")
		return
	}

	response.WriteJSON(w, http.StatusCreated, response.SuccessResponse{Data: fix})
}

// ListFixes handles GET /api/fixes — list all fixes for the user.
func ListFixes(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing user context")
		return
	}
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}

	fixes, err := h.Store.ListFixesByUser(r.Context(), claims.UserID)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to list fixes")
		return
	}

	response.WriteJSON(w, http.StatusOK, response.ListResponse{
		Data: fixes,
		Meta: response.ListMeta{Total: len(fixes), Page: 1, PerPage: len(fixes)},
	})
}

// GetFix handles GET /api/fixes/:id — fix detail with item breakdown.
func GetFix(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing user context")
		return
	}
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}

	id := chi.URLParam(r, "id")
	fix, err := h.Store.GetFixByID(r.Context(), id)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", fmt.Sprintf("fix %s not found", id))
		return
	}


	items, err := h.Store.ListFixItemsByFix(r.Context(), id)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to list fix items")
		return
	}

	type fixDetail struct {
		*models.Fix
		Items []models.FixItem `json:"items"`
	}

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{
		Data: fixDetail{Fix: fix, Items: items},
	})
}

// StreamFix handles GET /api/fixes/:id/stream — SSE stream for fix progress.
func StreamFix(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing user context")
		return
	}
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}

	id := chi.URLParam(r, "id")
	fix, err := h.Store.GetFixByID(r.Context(), id)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", fmt.Sprintf("fix %s not found", id))
		return
	}


	flusher, ok := w.(http.Flusher)
	if !ok {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	sendSSE(w, flusher, "fix_status", fmt.Sprintf(
		`{"fix_id":"%s","status":"%s","findings_attempted":%d,"findings_fixed":%d,"findings_failed":%d}`,
		fix.ID, fix.Status, fix.FindingsAttempted, fix.FindingsFixed, fix.FindingsFailed,
	))

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			fix, err = h.Store.GetFixByID(r.Context(), id)
			if err != nil {
				return
			}
			sendSSE(w, flusher, "fix_status", fmt.Sprintf(
				`{"fix_id":"%s","status":"%s","findings_attempted":%d,"findings_fixed":%d,"findings_failed":%d}`,
				fix.ID, fix.Status, fix.FindingsAttempted, fix.FindingsFixed, fix.FindingsFailed,
			))
			if fix.Status == models.FixStatusCompleted || fix.Status == models.FixStatusFailed || fix.Status == models.FixStatusCancelled {
				sendSSE(w, flusher, "fix_completed", fmt.Sprintf(
					`{"fix_id":"%s","fixed":%d,"failed":%d}`,
					fix.ID, fix.FindingsFixed, fix.FindingsFailed,
				))
				return
			}
		}
	}
}

// CancelFix handles DELETE /api/fixes/:id — cancel a running fix.
func CancelFix(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing user context")
		return
	}
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}

	id := chi.URLParam(r, "id")
	fix, err := h.Store.GetFixByID(r.Context(), id)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", fmt.Sprintf("fix %s not found", id))
		return
	}


	if fix.Status != models.FixStatusRunning && fix.Status != models.FixStatusPending {
		response.WriteError(w, http.StatusConflict, "conflict", "fix is not running or pending")
		return
	}

	now := time.Now()
	fix.Status = models.FixStatusCancelled
	fix.CompletedAt = &now
	fix.UpdatedAt = now

	if err := h.Store.UpdateFix(r.Context(), fix); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to cancel fix")
		return
	}

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: fix})
}

func sendSSE(w http.ResponseWriter, flusher http.Flusher, _ /* event */, data string) {
	// Don't emit "event:" field — EventSource.onmessage only fires for unnamed events.
	// The JSON data already contains a "type" key for routing on the client side.
	fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()
}

func toJSON(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func parseSeverities(s string) []models.Severity {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	var result []models.Severity
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, models.Severity(p))
		}
	}
	return result
}
