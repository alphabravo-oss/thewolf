package routes

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/artifacts"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/fix/fixstore"
	"github.com/alphabravocompany/thewolf/internal/models"
)

// createFixRequest is the body of POST /fixes. It enqueues an autonomous fix
// job (v1: dry-run, branch-only). RepoID + (FindingIDs | ScanID) scopes the
// work; the rest tune the engine chain, severity floor, and attempt budget.
type createFixRequest struct {
	RepoID        string   `json:"repo_id"`
	ScanID        string   `json:"scan_id"`
	FindingIDs    []string `json:"finding_ids"`
	TargetBranch  string   `json:"target_branch"`
	Engine        string   `json:"engine"`
	Mode          string   `json:"mode"`
	SeverityFloor string   `json:"severity_floor"`
	MaxAttempts   int      `json:"max_attempts"`
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
// The job is durable; a `wolf fixer` worker claims and runs it. v1 mode is
// dry_run (branch + diff for review, no push/PR).
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "invalid request body")
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
	if mode == "" {
		mode = models.FixModeDryRun
	}
	engine := req.Engine
	if engine == "" {
		engine = "auto"
	}

	now := time.Now().UTC()
	job := &models.FixJob{
		ID:            uuid.New().String(),
		UserID:        claims.UserID,
		Type:          "fix",
		RepoID:        req.RepoID,
		ScanID:        req.ScanID,
		FindingIDs:    toJSON(req.FindingIDs),
		FindingIDList: req.FindingIDs,
		TargetBranch:  req.TargetBranch,
		Engine:        engine,
		Mode:          mode,
		SeverityFloor: req.SeverityFloor,
		MaxAttempts:   req.MaxAttempts,
		Status:        models.FixJobQueued,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	if err := h.Store.EnqueueFixJob(r.Context(), job); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to enqueue fix job")
		return
	}

	response.WriteJSON(w, http.StatusCreated, response.SuccessResponse{Data: job})
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

	jobs, err := h.Store.ListFixJobs(r.Context(), r.URL.Query().Get("repo_id"))
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to list fix jobs")
		return
	}

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
	if err != nil || job == nil {
		response.WriteError(w, http.StatusNotFound, "not_found", fmt.Sprintf("fix %s not found", id))
		return
	}

	attempts, err := h.Store.ListFixAttempts(r.Context(), id)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to list fix attempts")
		return
	}

	type fixDetail struct {
		*models.FixJob
		Attempts []models.FixAttempt `json:"attempts"`
	}

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{
		Data: fixDetail{FixJob: job, Attempts: attempts},
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
	if err != nil || job == nil {
		response.WriteError(w, http.StatusNotFound, "not_found", fmt.Sprintf("fix %s not found", id))
		return
	}

	fs := fixArtifacts()
	if fs == nil {
		response.WriteError(w, http.StatusServiceUnavailable, "unavailable", "artifact store not initialized")
		return
	}
	diff, err := fs.ReadDiff(id)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to read diff")
		return
	}
	if diff == "" {
		response.WriteError(w, http.StatusNotFound, "not_found", "no diff available yet for this job")
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(diff))
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
	if err != nil || job == nil {
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
			if isTerminal(job.Status) {
				offset = relayLogLines(w, flusher, logPath, offset)
				sendSSE(w, flusher, "fix_completed", fixStatusJSON(job))
				return
			}
		}
	}
}

// relayLogLines forwards any log bytes appended past offset as SSE log events,
// returning the new offset. A missing/empty file is a no-op.
func relayLogLines(w http.ResponseWriter, flusher http.Flusher, logPath string, offset int64) int64 {
	if logPath == "" {
		return offset
	}
	f, err := os.Open(logPath) // #nosec G304 -- logPath is derived from a server-issued job UUID under the artifacts root
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
		payload, _ := json.Marshal(map[string]string{"type": "fix_log", "line": line})
		sendSSE(w, flusher, "fix_log", string(payload))
	}
	return offset
}

// CancelFix handles DELETE /api/fixes/:id — cancel a queued or running job. The
// worker observes the cancelled status and winds down.
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
	if err != nil || job == nil {
		response.WriteError(w, http.StatusNotFound, "not_found", fmt.Sprintf("fix %s not found", id))
		return
	}

	if isTerminal(job.Status) {
		response.WriteError(w, http.StatusConflict, "conflict", "fix job is already finished")
		return
	}

	now := time.Now().UTC()
	job.Status = models.FixJobCancelled
	job.FinishedAt = &now
	job.UpdatedAt = now

	if err := h.Store.UpdateFixJob(r.Context(), job); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to cancel fix job")
		return
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

// isTerminal reports whether a fix job has reached a final state.
func isTerminal(status string) bool {
	switch status {
	case models.FixJobSucceeded, models.FixJobFailed, models.FixJobCancelled:
		return true
	default:
		return false
	}
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
