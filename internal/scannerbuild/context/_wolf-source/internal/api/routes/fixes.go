package routes

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/artifacts"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/fix/fixstore"
	"github.com/alphabravocompany/thewolf/internal/fix/profile"
	"github.com/alphabravocompany/thewolf/internal/fix/workerstatus"
	"github.com/alphabravocompany/thewolf/internal/fix/workspace"
	"github.com/alphabravocompany/thewolf/internal/models"
)

// createFixRequest is the body of POST /fixes. It enqueues an autonomous fix
// job. dry_run retains a local review branch; push also publishes it and opens
// a GitHub PR. RepoID + (FindingIDs | ScanID) scopes the
// work; the rest tune the engine chain, severity floor, and attempt budget.
type createFixRequest struct {
	RepoID         string   `json:"repo_id"`
	ScanID         string   `json:"scan_id"`
	FindingIDs     []string `json:"finding_ids"`
	TargetBranch   string   `json:"target_branch"`
	Engine         string   `json:"engine"`
	Mode           string   `json:"mode"`
	SeverityFloor  string   `json:"severity_floor"`
	MaxAttempts    int      `json:"max_attempts"`
	MaxLoops       int      `json:"max_loops"`
	HumanInTheLoop bool     `json:"human_in_the_loop"`
	Model          string   `json:"model"`
	Effort         string   `json:"effort"`
	Variant        string   `json:"variant"`
	PlannedRuns    int      `json:"planned_runs"`
}

// fixArtifacts returns a fixstore rooted at the running artifacts directory, or
// nil when the artifact store isn't initialized (e.g. some test setups). The
// worker writes the log + diff there; the server reads them here.
func fixArtifacts() *fixstore.Store {
	if artifacts.Global == nil {
		return nil
	}
	return fixstore.New(artifacts.Global.Root())
}

// CreateFix handles POST /api/fixes — enqueue an autonomous fix job. Gated by
// the autofix_enabled setting (403 autofix_disabled when off) and write:fixes.
// The job is durable; a `wolf fixer` worker claims and runs it. The default
// dry_run retains a review branch; explicit push also publishes it and opens a GitHub PR.
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

	// The master gate: with autofix off, the execute path is dark.
	if !autofixEnabled(r.Context(), h.Store) {
		response.WriteError(w, http.StatusForbidden, "autofix_disabled",
			"autonomous fixing is disabled; enable autofix_enabled in settings to use this endpoint")
		return
	}

	var req createFixRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}

	if decoder.Decode(new(any)) != io.EOF {
		response.WriteError(w, 400, "bad_request", "expected one fix request")
		return
	}
	req.RepoID, req.ScanID, req.TargetBranch = strings.TrimSpace(req.RepoID), strings.TrimSpace(req.ScanID), strings.TrimSpace(req.TargetBranch)
	if len(req.FindingIDs) > 5000 {
		response.WriteError(w, 400, "validation_error", "at most 5000 explicit finding IDs are allowed")
		return
	}
	for i := range req.FindingIDs {
		req.FindingIDs[i] = strings.TrimSpace(req.FindingIDs[i])
		if req.FindingIDs[i] == "" {
			response.WriteError(w, 400, "validation_error", "finding IDs must not be empty")
			return
		}
	}
	slices.Sort(req.FindingIDs)
	req.FindingIDs = slices.Compact(req.FindingIDs)
	if len(req.FindingIDs) == 0 {
		req.FindingIDs = nil
	}
	if req.TargetBranch != "" && (strings.HasPrefix(req.TargetBranch, "-") || plumbing.ReferenceName("refs/heads/"+req.TargetBranch).Validate() != nil) {
		response.WriteError(w, 400, "validation_error", "invalid target branch")
		return
	}

	if req.RepoID == "" {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "repo_id required")
		return
	}
	if req.ScanID == "" && len(req.FindingIDs) == 0 {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "scan_id or finding_ids required")
		return
	}

	mode := req.Mode
	switch mode {
	case "":
		mode = models.FixModeDryRun
	case models.FixModeDryRun, models.FixModePush:
	default:
		response.WriteError(w, http.StatusBadRequest, "validation_error", "mode must be dry_run or push")
		return
	}
	req.Mode = mode
	repo, err := h.Store.GetRepoByID(r.Context(), req.RepoID)
	if err != nil || repo == nil || !ownsFixResource(repo.UserID, claims) {
		response.WriteError(w, 404, "not_found", "repository not found")
		return
	}
	if req.TargetBranch != "" && (strings.EqualFold(req.TargetBranch, "main") || strings.EqualFold(req.TargetBranch, "master") || strings.EqualFold(req.TargetBranch, repo.DefaultBranch)) {
		response.WriteError(w, 400, "validation_error", "fixes require a separate review branch")
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if len(key) > 255 {
		response.WriteError(w, 400, "validation_error", "Idempotency-Key must be 255 characters or fewer")
		return
	}
	intent, _ := json.Marshal(req)
	digest := fmt.Sprintf("fix-create-v1:%x", sha256.Sum256(intent))
	if key != "" {
		existing, err := h.Store.FindFixJobByIdempotencyKey(r.Context(), claims.UserID, key)
		if err == nil {
			if existing.RequestDigest != digest {
				writeFixCreationError(w, db.ErrFixIdempotencyConflict)
				return
			}
			response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: existing})
			return
		}
		if !errors.Is(err, sql.ErrNoRows) {
			response.WriteError(w, 500, "server_error", "failed to check fix request key")
			return
		}
	}

	engine := req.Engine
	if engine == "" {
		engine = "auto"
	}
	maxLoops := req.MaxLoops
	if maxLoops <= 0 {
		maxLoops = 2
	}
	plannedRuns := req.PlannedRuns
	if plannedRuns <= 0 {
		plannedRuns = 1
	}
	if plannedRuns > 5 {
		plannedRuns = 5
	}
	if req.Engine == "" {
		if v, err := h.Store.GetSetting(r.Context(), "fixer_engine"); err == nil && strings.TrimSpace(v) != "" {
			engine = strings.TrimSpace(v)
		}
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		if v, err := h.Store.GetSetting(r.Context(), profile.SettingModel); err == nil {
			model = strings.TrimSpace(v)
		}
	}
	effort := profile.NormalizeEffort(req.Effort)
	if strings.TrimSpace(req.Effort) == "" {
		if v, err := h.Store.GetSetting(r.Context(), profile.SettingEffort); err == nil && strings.TrimSpace(v) != "" {
			effort = profile.NormalizeEffort(v)
		}
	}
	variant := strings.TrimSpace(req.Variant)
	if variant == "" {
		if v, err := h.Store.GetSetting(r.Context(), profile.SettingVariant); err == nil {
			variant = strings.TrimSpace(v)
		}
	}

	scanID := req.ScanID
	findingIDs := req.FindingIDs

	now := time.Now().UTC()
	job := &models.FixJob{
		IdempotencyKey: key,
		RequestDigest:  digest,
		ID:             uuid.New().String(),
		UserID:         claims.UserID,
		Type:           "fix",
		RepoID:         req.RepoID,
		ScanID:         scanID,
		FindingIDs:     toJSON(findingIDs),
		FindingIDList:  findingIDs,
		TargetBranch:   req.TargetBranch,
		Engine:         engine,
		Mode:           mode,
		SeverityFloor:  req.SeverityFloor,
		MaxAttempts:    req.MaxAttempts,
		MaxLoops:       maxLoops,
		PlannedRuns:    plannedRuns,
		RunIndex:       1,
		HumanInTheLoop: req.HumanInTheLoop,
		Model:          model,
		Effort:         effort,
		Variant:        variant,
		Status:         models.FixJobQueued,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	created, replayed, err := h.Store.CreateFixJob(r.Context(), job, len(req.FindingIDs) == 0)
	if err != nil {
		writeFixCreationError(w, err)
		return
	}
	status := http.StatusCreated
	if replayed {
		status = http.StatusOK
	}
	response.WriteJSON(w, status, response.SuccessResponse{Data: created})
}

// ListFixes handles GET /api/fixes — list fix jobs. An optional repo_id query
// narrows to one repo.
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

	jobs, err := h.Store.ListFixJobsByUser(r.Context(), claims.UserID, r.URL.Query().Get("repo_id"))
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to list fix jobs")
		return
	}
	attachQueuedBehind(r.Context(), h.Store, jobs)

	response.WriteJSON(w, http.StatusOK, response.ListResponse{
		Data: jobs,
		Meta: response.ListMeta{Total: len(jobs), Page: 1, PerPage: len(jobs)},
	})
}

// GetFix handles GET /api/fixes/:id — the job plus its per-finding attempts
// (the audit trail).
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
	job, err := h.Store.GetFixJobByID(r.Context(), id)
	if err != nil || job == nil || !ownsFixResource(job.UserID, claims) {
		// Not-owned is reported as 404 (not 403) so a caller can't enumerate
		// which job IDs exist for other tenants.
		response.WriteError(w, http.StatusNotFound, "not_found", fmt.Sprintf("fix %s not found", id))
		return
	}
	if siblings, lerr := h.Store.ListFixJobsByUser(r.Context(), claims.UserID, ""); lerr == nil {
		attachQueuedBehind(r.Context(), h.Store, siblings)
		for i := range siblings {
			if siblings[i].ID == job.ID {
				job.QueuedBehind = siblings[i].QueuedBehind
				break
			}
		}
	}

	attempts, err := h.Store.ListFixAttempts(r.Context(), id)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to list fix attempts")
		return
	}

	var toolStats []ScannerToolStat
	if job.ScanID != "" {
		if all, ferr := h.Store.ListFindingsByScan(r.Context(), job.ScanID); ferr == nil {
			inJob := findingsInJob(all, job)
			annotateAttemptTools(attempts, inJob)
			toolStats = scannerToolStats(inJob, attempts)
		}
	}

	type fixDetail struct {
		*models.FixJob
		Attempts  []models.FixAttempt `json:"attempts"`
		ToolStats []ScannerToolStat   `json:"tool_stats,omitempty"`
	}

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{
		Data: fixDetail{FixJob: job, Attempts: attempts, ToolStats: toolStats},
	})
}

// GetFixDiff handles GET /api/fixes/:id/diff — the proposed unified diff the
// worker assembled on the fix branch (the v1 deliverable). Served as plain
// text so it drops straight into a diff viewer or `git apply`.
func GetFixDiff(w http.ResponseWriter, r *http.Request) {
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
	job, err := h.Store.GetFixJobByID(r.Context(), id)
	if err != nil || job == nil || !ownsFixResource(job.UserID, claims) {
		// Not-owned is reported as 404 (not 403) so a caller can't enumerate
		// which job IDs exist for other tenants.
		response.WriteError(w, http.StatusNotFound, "not_found", fmt.Sprintf("fix %s not found", id))
		return
	}

	var diff string
	if fs := fixArtifacts(); fs != nil {
		got, err := fs.ReadDiff(id)
		if err != nil {
			response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to read diff")
			return
		}
		diff = got
	}
	if strings.TrimSpace(diff) == "" {
		diff = liveWorkspaceDiff(job.WorkspacePath)
	}
	if files := r.URL.Query()["file"]; len(files) > 0 {
		diff = filterUnifiedDiff(diff, files)
	}
	if strings.TrimSpace(diff) == "" {
		response.WriteError(w, http.StatusNotFound, "not_found", "no diff available yet for this job")
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(diff))
}

// GetFixCommits handles GET /api/fixes/:id/commits — git log of the live
// workspace so the agent page can show a wolf-fix: timeline while the job runs.
func GetFixCommits(w http.ResponseWriter, r *http.Request) {
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
	job, err := h.Store.GetFixJobByID(r.Context(), id)
	if err != nil || job == nil || !ownsFixResource(job.UserID, claims) {
		response.WriteError(w, http.StatusNotFound, "not_found", fmt.Sprintf("fix %s not found", id))
		return
	}
	commits := listWorkspaceCommits(job.WorkspacePath)
	if commits == nil {
		commits = []fixCommit{}
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: commits})
}

// StreamFix handles GET /api/fixes/:id/stream — relays the worker's log over
// SSE. The worker runs out-of-process and appends to a durable log artifact;
// the server tails that file and forwards new lines plus the job's status.
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
	job, err := h.Store.GetFixJobByID(r.Context(), id)
	if err != nil || job == nil || !ownsFixResource(job.UserID, claims) {
		// Not-owned is reported as 404 (not 403) so a caller can't enumerate
		// which job IDs exist for other tenants.
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

	logPath := ""
	if fs := fixArtifacts(); fs != nil {
		logPath = fs.LogPath(id)
	}

	sendSSE(w, flusher, "fix_status", fixStatusJSON(job))

	var offset int64
	// Drain any log already on disk before live-tailing.
	offset = relayLogLines(w, flusher, logPath, offset)

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			offset = relayLogLines(w, flusher, logPath, offset)
			job, err = h.Store.GetFixJobByID(r.Context(), id)
			if err != nil || job == nil {
				return
			}
			sendSSE(w, flusher, "fix_status", fixStatusJSON(job))
			if isTerminal(job.Status) || models.FixJobPaused(job.Status) {
				_ = relayLogLines(w, flusher, logPath, offset)
				sendSSE(w, flusher, "fix_completed", fixStatusJSON(job))
				return
			}
		}
	}
}

// relayLogLines forwards any log bytes appended past offset as SSE log events,
// returning the new offset. A missing/empty file is a no-op.
func relayLogLines(w http.ResponseWriter, flusher http.Flusher, logPath string, offset int64) int64 {
	return relayTypedLogLines(w, flusher, logPath, offset, "fix_log")
}

func relayTypedLogLines(w http.ResponseWriter, flusher http.Flusher, logPath string, offset int64, typ string) int64 {
	if logPath == "" {
		return offset
	}
	if typ == "" {
		typ = "fix_log"
	}
	f, err := artifacts.Open(logPath)
	if err != nil {
		return offset
	}
	defer f.Close()
	if _, err := f.Seek(offset, 0); err != nil {
		return offset
	}
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	for {
		n, rerr := f.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			offset += int64(n)
		}
		if rerr != nil {
			break
		}
	}
	if len(buf) == 0 {
		return offset
	}
	for _, line := range splitLines(string(buf)) {
		if line == "" {
			continue
		}
		payload, _ := json.Marshal(map[string]string{"type": typ, "line": line})
		sendSSE(w, flusher, typ, string(payload))
	}
	return offset
}

// relayConsoleData sends newly appended PTY bytes as one console_data event
// so the browser terminal can replay CSI/CR instead of treating each
// carriage return as a log line.
func relayConsoleData(w http.ResponseWriter, flusher http.Flusher, logPath string, offset int64) int64 {
	if logPath == "" {
		return offset
	}
	f, err := artifacts.Open(logPath)
	if err != nil {
		return offset
	}
	defer f.Close()
	if _, err := f.Seek(offset, 0); err != nil {
		return offset
	}
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	for {
		n, rerr := f.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			offset += int64(n)
		}
		if rerr != nil {
			break
		}
	}
	if len(buf) == 0 {
		return offset
	}
	payload, err := json.Marshal(map[string]string{"type": "console_data", "data": string(buf)})
	if err != nil {
		return offset
	}
	sendSSE(w, flusher, "console_data", string(payload))
	return offset
}

// CancelFix handles DELETE /api/fixes/:id — cancel a queued, running, or
// paused job. The worker observes cancelled and winds down. Branch work is
// discarded so the job does not linger as awaiting_push.
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
	job, err := h.Store.GetFixJobByID(r.Context(), id)
	if err != nil || job == nil || !ownsFixResource(job.UserID, claims) {
		// Not-owned is reported as 404 (not 403) so a caller can't enumerate
		// which job IDs exist for other tenants.
		response.WriteError(w, http.StatusNotFound, "not_found", fmt.Sprintf("fix %s not found", id))
		return
	}

	if isTerminal(job.Status) && job.Status != models.FixJobCancelled {
		response.WriteError(w, http.StatusConflict, "conflict", "fix job is already finished")
		return
	}
	if job.Status != models.FixJobCancelled {
		active := job.Status == models.FixJobClaimed || job.Status == models.FixJobRunning
		now := time.Now().UTC()
		job.Status = models.FixJobCancelled
		job.FinishedAt = &now
		job.PauseReason = models.FixPauseCancellationCleanup
		job.ResumeAction = ""
		if !active {
			job.ClaimedBy = ""
		}
		if err := h.Store.UpdateFixJob(r.Context(), job); err != nil {
			if errors.Is(err, db.ErrFixJobConflict) || errors.Is(err, db.ErrFixBusy) {
				response.WriteError(w, http.StatusConflict, "conflict", err.Error())
			} else {
				response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to cancel fix job")
			}
			return
		}
	}
	// An active worker acknowledges cleanup only after its engine returns.
	// A repeated DELETE retries failed cleanup, never the fix itself.
	if job.PauseReason == models.FixPauseCancellationCleanup && job.ClaimedBy == "" {
		if err := h.Store.CompleteFixCancellation(r.Context(), job, workspace.DiscardFixJob); err != nil {
			job.Error = "cancellation cleanup pending: " + err.Error()
			_ = h.Store.UpdateFixJob(context.WithoutCancel(r.Context()), job)
			status := http.StatusInternalServerError
			if errors.Is(err, db.ErrFixJobConflict) || errors.Is(err, db.ErrFixBusy) {
				status = http.StatusConflict
			}
			response.WriteError(w, status, "cleanup_pending", "fix cancelled; cleanup is pending and the recovery path is retained; retry cancellation after resolving the cleanup error")
			return
		}
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: job})
}

// fixStatusJSON renders the SSE status payload for a job.
func fixStatusJSON(job *models.FixJob) string {
	payload, _ := json.Marshal(map[string]any{
		"type":             "fix_status",
		"fix_id":           job.ID,
		"status":           job.Status,
		"result_branch":    job.ResultBranch,
		"diff_artifact_id": job.DiffArtifactID,
	})
	return string(payload)
}

// ownsFixResource keeps private fix resources owner-only, including between
// administrators. Ownerless legacy resources require an administrator session.
func ownsFixResource(ownerUserID string, claims *auth.Claims) bool {
	return canModifyOwned(claims, ownerUserID) && (ownerUserID == "" || ownerUserID == claims.UserID)
}

// isTerminal reports whether a fix job has reached a final state.
func isTerminal(status string) bool {
	return models.FixJobTerminal(status)
}

type resumeFixRequest struct {
	Action string `json:"action"` // continue | push
}

// ResumeFix handles POST /api/fixes/{id}/resume — re-queue a paused job.
// action=continue runs the next loop; action=push publishes the reviewed branch
// and opens or recovers its GitHub PR.
func ResumeFix(w http.ResponseWriter, r *http.Request) {
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
	if !autofixEnabled(r.Context(), h.Store) {
		response.WriteError(w, http.StatusForbidden, "autofix_disabled",
			"autonomous fixing is disabled")
		return
	}
	id := chi.URLParam(r, "id")
	job, err := h.Store.GetFixJobByID(r.Context(), id)
	if err != nil || job == nil || !ownsFixResource(job.UserID, claims) {
		response.WriteError(w, http.StatusNotFound, "not_found", fmt.Sprintf("fix %s not found", id))
		return
	}
	if !models.FixJobPaused(job.Status) {
		response.WriteError(w, http.StatusConflict, "conflict", "fix job is not waiting for review")
		return
	}
	var req resumeFixRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil && err != io.EOF {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "invalid resume request")
		return
	}
	if decoder.Decode(new(any)) != io.EOF {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "expected one resume request")
		return
	}
	action := strings.TrimSpace(req.Action)
	if action == "" {
		if job.Status == models.FixJobAwaitingPush || job.Status == models.FixJobPushFailed {
			action = models.FixResumePush
		} else {
			action = models.FixResumeContinue
		}
	}
	if action != models.FixResumeContinue && action != models.FixResumePush {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "action must be continue or push")
		return
	}
	if job.Status == models.FixJobPushFailed && action != models.FixResumePush {
		response.WriteError(w, http.StatusConflict, "conflict", "only push can be retried after a failed publish")
		return
	}
	job.ResumeAction = action
	job.Status = models.FixJobQueued
	job.ClaimedBy = ""
	job.PauseReason = ""
	job.Error = ""
	if err := h.Store.UpdateFixJob(r.Context(), job); err != nil {
		if errors.Is(err, db.ErrFixJobConflict) || errors.Is(err, db.ErrFixBusy) {
			response.WriteError(w, http.StatusConflict, "conflict", err.Error())
		} else {
			response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to resume fix job")
		}
		return
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: job})
}

func writeFixCreationError(w http.ResponseWriter, err error) {
	status, code, message := http.StatusInternalServerError, "server_error", "failed to create fix job"
	switch {
	case errors.Is(err, db.ErrFixScope):
		status, code, message = http.StatusNotFound, "not_found", "repository, scan or finding not found"
	case errors.Is(err, db.ErrFixIdempotencyConflict):
		status, code, message = http.StatusConflict, "idempotency_conflict", err.Error()
	case errors.Is(err, db.ErrFixBusy):
		status, code, message = http.StatusConflict, "remediation_busy", err.Error()
	case errors.Is(err, db.ErrFixFrozen):
		status, code, message = http.StatusConflict, "remediation_frozen", err.Error()
	case errors.Is(err, db.ErrFixBranchConflict), errors.Is(err, db.ErrFixSourceBranchConflict):
		status, code, message = http.StatusConflict, "branch_conflict", err.Error()
	}
	response.WriteError(w, status, code, message)
}

// AcceptRemediation handles POST /api/v1/remediations/{id}/accept — local
// hand-off or published-workspace cleanup retry. The persisted publication or
// retained local branch must survive releasing the checkout.
func AcceptRemediation(w http.ResponseWriter, r *http.Request) {
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
	rem, err := h.Store.GetRemediationByID(r.Context(), id)
	if err != nil || rem == nil || !ownsFixResource(rem.UserID, claims) {
		response.WriteError(w, http.StatusNotFound, "not_found", "remediation not found")
		return
	}
	if err := h.Store.AcceptFixRemediation(r.Context(), rem, workspace.AcceptRemediation); err != nil {
		status, code := http.StatusInternalServerError, "handoff_failed"
		if errors.Is(err, db.ErrFixBusy) || errors.Is(err, db.ErrFixFrozen) || errors.Is(err, db.ErrFixScope) {
			status, code = http.StatusConflict, "remediation_busy"
		}
		if errors.Is(err, workspace.ErrNoHandoff) {
			status, code = http.StatusConflict, "nothing_to_handoff"
		}
		response.WriteError(w, status, code, "branch handoff was not completed: "+err.Error())
		return
	}

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: rem})
}

// ListFixEngines handles GET /api/fixes/engines — worker-reported CLI OAuth
// state plus whether this user has API keys stored.
func ListFixEngines(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing user context")
		return
	}
	var status workerstatus.Status
	if store, ok := artifacts.Global.(*artifacts.Store); ok && store != nil {
		status, _ = workerstatus.Read(store.Root())
	}
	worker := status.Engines
	if h := DefaultHandler; h != nil && !autofixEnabled(r.Context(), h.Store) {
		configured := false
		for _, status := range worker {
			configured = configured || status.Auth == "oauth" || status.Auth == "api_key"
		}
		secrets, err := h.Store.ListSecretsByUser(r.Context(), claims.UserID)
		if err != nil {
			response.WriteError(w, 503, "setup_unavailable", "unable to read fixer configuration")
			return
		}
		for _, secret := range secrets {
			switch secret.KeyType {
			case models.KeyTypeAnthropicKey, models.KeyTypeOpenAIKey, models.KeyTypeXAIKey:
				configured = true
			}
		}
		response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: map[string]bool{"configured": configured}})
		return
	}
	keys := map[string]bool{}
	defaults := map[string]string{}
	if h := DefaultHandler; h != nil && h.Store != nil {
		if list, err := h.Store.ListSecretsByUser(r.Context(), claims.UserID); err == nil {
			for _, s := range list {
				switch s.KeyType {
				case models.KeyTypeAnthropicKey:
					keys["anthropic"] = true
				case models.KeyTypeOpenAIKey:
					keys["openai"] = true
				case models.KeyTypeXAIKey:
					keys["xai"] = true
				}
			}
		}
		for _, key := range []string{
			"fixer_engine", profile.SettingModel, profile.SettingEffort, profile.SettingVariant,
			profile.SettingPromptInitial, profile.SettingPromptFollowup,
		} {
			if v, err := h.Store.GetSetting(r.Context(), key); err == nil && v != "" {
				defaults[key] = v
			}
		}
	}
	home, _ := os.UserHomeDir()
	live := map[string][]profile.Model{}
	for _, row := range worker {
		if len(row.Models) > 0 {
			live[row.Name] = row.Models
			if row.Command != "" {
				live[row.Command] = row.Models
			}
		}
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: map[string]any{
		"worker":   worker,
		"catalog":  profile.OverlayLive(profile.Catalog(), live),
		"defaults": defaults,
		"api_keys": map[string]bool{
			"anthropic": keys["anthropic"],
			"openai":    keys["openai"],
			"xai":       keys["xai"],
		},
		"home":          home,
		"console_shell": fixerConsoleShellEnabled(r, DefaultHandler),
		"oauth_hint":    "Log in with Claude, Codex, OpenCode, or Grok Build in the worker console, or add provider API keys under Account → Secrets. Sessions live in the fixer worker HOME.",
		"prompts": map[string]any{
			// Defaults come from the worker; empty until a worker has run.
			"initial": map[string]string{
				"key":     profile.SettingPromptInitial,
				"value":   firstNonEmpty(defaults[profile.SettingPromptInitial], status.Prompts[profile.SettingPromptInitial]),
				"default": status.Prompts[profile.SettingPromptInitial],
			},
			"followup": map[string]string{
				"key":     profile.SettingPromptFollowup,
				"value":   firstNonEmpty(defaults[profile.SettingPromptFollowup], status.Prompts[profile.SettingPromptFollowup]),
				"default": status.Prompts[profile.SettingPromptFollowup],
			},
			"placeholder": status.Placeholder,
		},
	}})
}

type queuedBehindStore interface {
	ListActiveFixerConsoles(ctx context.Context) ([]models.FixerConsole, error)
}

func attachQueuedBehind(ctx context.Context, store queuedBehindStore, jobs []models.FixJob) {
	if len(jobs) == 0 {
		return
	}
	pred := predecessorForQueue(jobs, nil)
	if store != nil {
		if consoles, err := store.ListActiveFixerConsoles(ctx); err == nil && len(consoles) > 0 {
			pred = predecessorForQueue(jobs, consoles)
		}
	}
	if pred == nil {
		return
	}
	for i := range jobs {
		if jobs[i].Status == models.FixJobQueued {
			cp := *pred
			jobs[i].QueuedBehind = &cp
		}
	}
}

func predecessorForQueue(jobs []models.FixJob, consoles []models.FixerConsole) *models.QueuedBehind {
	for i := range consoles {
		c := consoles[i]
		if c.Status == models.FixerConsoleClaimed || c.Status == models.FixerConsoleRunning {
			return &models.QueuedBehind{ID: c.ID, Kind: "console", StartedAt: c.StartedAt}
		}
	}
	var best *models.FixJob
	for i := range jobs {
		j := &jobs[i]
		if j.Status != models.FixJobClaimed && j.Status != models.FixJobRunning {
			continue
		}
		if best == nil || j.CreatedAt.Before(best.CreatedAt) {
			best = j
		}
	}
	if best == nil {
		return nil
	}
	return &models.QueuedBehind{ID: best.ID, Kind: "job", RepoID: best.RepoID, StartedAt: best.StartedAt}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func sendSSE(w http.ResponseWriter, flusher http.Flusher, _ /* event */ string, data string) {
	// Don't emit "event:" field — EventSource.onmessage only fires for unnamed events.
	// The JSON data already contains a "type" key for routing on the client side.
	fmt.Fprintf(w, "data: %s\n\n", data) // nosemgrep: go.lang.security.audit.xss.no-fprintf-to-responsewriter.no-fprintf-to-responsewriter
	flusher.Flush()
}

func toJSON(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}
