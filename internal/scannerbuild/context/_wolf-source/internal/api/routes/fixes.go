package routes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/artifacts"
	"github.com/alphabravocompany/thewolf/internal/auth"
	fixauth "github.com/alphabravocompany/thewolf/internal/fix/auth"
	fixengine "github.com/alphabravocompany/thewolf/internal/fix/engine"
	"github.com/alphabravocompany/thewolf/internal/fix/fixstore"
	"github.com/alphabravocompany/thewolf/internal/fix/lineage"
	"github.com/alphabravocompany/thewolf/internal/fix/profile"
	"github.com/alphabravocompany/thewolf/internal/fix/workspace"
	"github.com/alphabravocompany/thewolf/internal/models"
)

// createFixRequest is the body of POST /fixes. It enqueues an autonomous fix
// job (v1: dry-run, branch-only). RepoID + (FindingIDs | ScanID) scopes the
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
	switch mode {
	case "":
		mode = models.FixModeDryRun
	case models.FixModeDryRun, models.FixModePush:
	default:
		response.WriteError(w, http.StatusBadRequest, "validation_error", "mode must be dry_run or push")
		return
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

	scanID := strings.TrimSpace(req.ScanID)
	var clicked *models.Scan
	if scanID != "" && h.Store != nil {
		s, serr := h.Store.GetScanByID(r.Context(), scanID)
		if serr != nil || s == nil || (s.UserID != "" && s.UserID != claims.UserID) {
			response.WriteError(w, http.StatusNotFound, "not_found", "scan not found")
			return
		}
		clicked = s
	}

	findingIDs := req.FindingIDs
	if len(findingIDs) == 0 && scanID != "" && h.Store != nil {
		if findings, ferr := h.Store.ListFindingsByScan(r.Context(), scanID); ferr == nil {
			for _, f := range findings {
				findingIDs = append(findingIDs, f.ID)
			}
		}
	}

	now := time.Now().UTC()
	job := &models.FixJob{
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

	if err := attachRemediation(r.Context(), h, claims.UserID, clicked, job); err != nil {
		writeRemediationError(w, err)
		return
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
	if err != nil || job == nil || !ownsFixJob(job, claims) {
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
	if err != nil || job == nil || !ownsFixJob(job, claims) {
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
	if err != nil || job == nil || !ownsFixJob(job, claims) {
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
	if err != nil || job == nil || !ownsFixJob(job, claims) {
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
	f, err := os.Open(logPath) // #nosec G304 -- logPath is derived from a server-issued console UUID
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
	if err != nil || job == nil || !ownsFixJob(job, claims) {
		// Not-owned is reported as 404 (not 403) so a caller can't enumerate
		// which job IDs exist for other tenants.
		response.WriteError(w, http.StatusNotFound, "not_found", fmt.Sprintf("fix %s not found", id))
		return
	}

	if isTerminal(job.Status) {
		response.WriteError(w, http.StatusConflict, "conflict", "fix job is already finished")
		return
	}

	discardFixBranch(r.Context(), h, job)
	if rem := loadJobRemediation(r.Context(), h, job); rem != nil && rem.State == models.RemediationOpen {
		_ = lineage.Discard(r.Context(), rem, h.Store)
	}

	now := time.Now().UTC()
	job.Status = models.FixJobCancelled
	job.FinishedAt = &now
	job.UpdatedAt = now
	job.PauseReason = ""
	job.ResumeAction = ""
	job.ResultBranch = ""
	job.WorkspacePath = ""

	if err := h.Store.UpdateFixJob(r.Context(), job); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to cancel fix job")
		return
	}

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: job})
}

func discardFixBranch(ctx context.Context, h *Handler, job *models.FixJob) {
	if h == nil || job == nil {
		return
	}
	_ = workspace.DiscardPath(ctx, job.WorkspacePath, job.ResultBranch)
	if h.Store == nil || job.RepoID == "" {
		return
	}
	repo, err := h.Store.GetRepoByID(ctx, job.RepoID)
	if err != nil || repo == nil || repo.SourceType != models.SourceTypeLocal {
		return
	}
	_ = workspace.DiscardLocalBranch(ctx, repo.SourcePath, job.ResultBranch)
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

// ownsFixJob reports whether the job belongs to the caller. Jobs with an empty
// UserID (legacy/system-created) are treated as accessible, matching the scans
// ownership model (scan.UserID != "" && scan.UserID != claims.UserID).
func ownsFixJob(job *models.FixJob, claims *auth.Claims) bool {
	return job.UserID == "" || job.UserID == claims.UserID
}

// isTerminal reports whether a fix job has reached a final state.
func isTerminal(status string) bool {
	return models.FixJobTerminal(status)
}

type resumeFixRequest struct {
	Action string `json:"action"` // continue | push
}

// ResumeFix handles POST /api/fixes/{id}/resume — re-queue a paused job.
// action=continue runs the next loop; action=push publishes the fix branch.
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
	if err != nil || job == nil || !ownsFixJob(job, claims) {
		response.WriteError(w, http.StatusNotFound, "not_found", fmt.Sprintf("fix %s not found", id))
		return
	}
	legacyFailedPush := job.Status == models.FixJobFailed && job.ResultBranch != "" && !job.Pushed
	if !models.FixJobPaused(job.Status) && !legacyFailedPush {
		response.WriteError(w, http.StatusConflict, "conflict", "fix job is not waiting for review")
		return
	}
	var req resumeFixRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	action := strings.TrimSpace(req.Action)
	if action == "" {
		if job.Status == models.FixJobAwaitingPush || job.Status == models.FixJobPushFailed || legacyFailedPush {
			action = models.FixResumePush
		} else {
			action = models.FixResumeContinue
		}
	}
	if action != models.FixResumeContinue && action != models.FixResumePush {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "action must be continue or push")
		return
	}
	if (legacyFailedPush || job.Status == models.FixJobPushFailed) && action != models.FixResumePush {
		response.WriteError(w, http.StatusConflict, "conflict", "only push can be retried after a failed publish")
		return
	}
	job.ResumeAction = action
	job.Status = models.FixJobQueued
	job.ClaimedBy = ""
	job.PauseReason = ""
	job.Error = ""
	if err := h.Store.UpdateFixJob(r.Context(), job); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to resume fix job")
		return
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: job})
}

type remediationConflict struct {
	code    string
	message string
}

func (e *remediationConflict) Error() string { return e.message }

func writeRemediationError(w http.ResponseWriter, err error) {
	if err == nil {
		return
	}
	if c, ok := err.(*remediationConflict); ok {
		response.WriteError(w, http.StatusConflict, c.code, c.message)
		return
	}
	response.WriteError(w, http.StatusInternalServerError, "server_error", err.Error())
}

func attachRemediation(ctx context.Context, h *Handler, userID string, clicked *models.Scan, job *models.FixJob) error {
	if h == nil || h.Store == nil || job == nil || clicked == nil {
		return nil
	}
	originID := lineage.OriginID(clicked)
	latest, err := h.Store.GetLatestRemediationByOrigin(ctx, originID)
	if err != nil {
		return err
	}
	if latest != nil && latest.State == models.RemediationFrozen {
		return &remediationConflict{
			code:    "remediation_frozen",
			message: fmt.Sprintf("This scan's branch was handed off. Scan %s again to keep fixing.", latest.Branch),
		}
	}
	if latest != nil && latest.State == models.RemediationOpen {
		jobs, jerr := h.Store.ListFixJobsByRemediation(ctx, latest.ID)
		if jerr != nil {
			return jerr
		}
		for _, existing := range jobs {
			if models.RemediationBusy(existing.Status) {
				return &remediationConflict{
					code:    "remediation_busy",
					message: "An agent is already running on this scan's fix branch",
				}
			}
		}
		if clicked.ID == originID {
			if kids, kerr := h.Store.ListScansByOrigin(ctx, originID); kerr == nil {
				for i := len(kids) - 1; i >= 0; i-- {
					if kids[i].ID != originID && kids[i].OriginScanID == originID {
						if findings, ferr := h.Store.ListFindingsByScan(ctx, kids[i].ID); ferr == nil && len(findings) > 0 {
							ids := make([]string, 0, len(findings))
							for _, f := range findings {
								ids = append(ids, f.ID)
							}
							job.ScanID = kids[i].ID
							job.FindingIDList = ids
							job.FindingIDs = toJSON(ids)
						}
						break
					}
				}
			}
		}
		job.RemediationID = latest.ID
		job.TargetBranch = latest.Branch
		job.WorkspacePath = latest.WorkspacePath
		_ = lineage.SupersedeSiblings(ctx, h.Store, job)
		return nil
	}

	branch := lineage.BranchName(originID)
	rem := &models.Remediation{
		ID:           uuid.NewString(),
		UserID:       userID,
		RepoID:       job.RepoID,
		OriginScanID: originID,
		Branch:       branch,
		State:        models.RemediationOpen,
	}
	if err := h.Store.CreateRemediation(ctx, rem); err != nil {
		return err
	}
	job.RemediationID = rem.ID
	job.TargetBranch = branch
	return nil
}

func loadJobRemediation(ctx context.Context, h *Handler, job *models.FixJob) *models.Remediation {
	if h == nil || h.Store == nil || job == nil || job.RemediationID == "" {
		return nil
	}
	rem, err := h.Store.GetRemediationByID(ctx, job.RemediationID)
	if err != nil {
		return nil
	}
	return rem
}

// AcceptRemediation handles POST /api/v1/remediations/{id}/accept — local
// hand-off: freeze the origin, remove the worktree, keep the wolf-fix branch.
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
	if err != nil || rem == nil || (rem.UserID != "" && rem.UserID != claims.UserID) {
		response.WriteError(w, http.StatusNotFound, "not_found", "remediation not found")
		return
	}
	if rem.State != models.RemediationOpen {
		response.WriteError(w, http.StatusConflict, "conflict", "remediation is not open")
		return
	}
	jobs, _ := h.Store.ListFixJobsByRemediation(r.Context(), rem.ID)
	for _, j := range jobs {
		if models.RemediationBusy(j.Status) {
			response.WriteError(w, http.StatusConflict, "remediation_busy", "an agent is still running")
			return
		}
	}
	if rem.WorkspacePath != "" {
		if ws, oerr := workspace.Open(rem.WorkspacePath, ""); oerr == nil && ws != nil {
			_ = ws.Cleanup(r.Context())
		}
	}
	sha := rem.PublishedSHA
	for i := len(jobs) - 1; i >= 0; i-- {
		if jobs[i].PushSHA != "" {
			sha = jobs[i].PushSHA
			break
		}
	}
	if err := lineage.Freeze(r.Context(), rem, sha, h.Store); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to accept remediation")
		return
	}
	rem.WorkspacePath = ""
	_ = h.Store.UpdateRemediation(r.Context(), rem)
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
	var worker []fixauth.EngineStatus
	if store, ok := artifacts.Global.(*artifacts.Store); ok && store != nil {
		worker, _ = fixauth.ReadStatus(store.Root())
	}
	creds := fixauth.Credentials{}
	keys := map[string]bool{}
	defaults := map[string]string{}
	if h := DefaultHandler; h != nil && h.Store != nil {
		creds = fixauth.Resolve(r.Context(), h.Store, claims.UserID)
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
			fixengine.SettingPromptInitial, fixengine.SettingPromptFollowup,
		} {
			if v, err := h.Store.GetSetting(r.Context(), key); err == nil && v != "" {
				defaults[key] = v
			}
		}
	}
	if len(worker) == 0 {
		// Same-host install: the API process can see the user's OAuth files.
		worker = fixauth.ProbeAll(r.Context(), creds)
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
		"oauth_hint":    "Login with Claude, Codex, or OpenCode in the worker console. Grok uses an xAI API key under Account → Secrets. Sessions live in the fixer worker HOME.",
		"prompts": map[string]any{
			"initial": map[string]string{
				"key":     fixengine.SettingPromptInitial,
				"value":   firstNonEmpty(defaults[fixengine.SettingPromptInitial], fixengine.DefaultInitialInstructions),
				"default": fixengine.DefaultInitialInstructions,
			},
			"followup": map[string]string{
				"key":     fixengine.SettingPromptFollowup,
				"value":   firstNonEmpty(defaults[fixengine.SettingPromptFollowup], fixengine.DefaultFollowupInstructions),
				"default": fixengine.DefaultFollowupInstructions,
			},
			"placeholder": fixengine.FindingsFilePlaceholder,
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
