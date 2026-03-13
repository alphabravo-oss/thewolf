package routes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/api/sse"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/fix/engine"
	fixgit "github.com/alphabravocompany/thewolf/internal/fix/git"
	"github.com/alphabravocompany/thewolf/internal/fix/runner"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/wolflog"
)

var (
	activeFixesMu  sync.Mutex
	activeFixCtxs  = make(map[string]context.CancelFunc)
)

type createFixRequest struct {
	ScanID     string   `json:"scan_id"`
	FindingIDs []string `json:"finding_ids"`
	Severity   []string `json:"severity"`
	Mode       string   `json:"mode"`     // "interactive" or "wolfpack"
	Engine     string   `json:"engine"`   // engine name
	AutoInit   bool     `json:"auto_init"` // auto-initialize git if not a repo
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

	mode := models.FixModeInteractive
	if req.Mode == "wolfpack" {
		mode = models.FixModeWolfPack
	}

	engineName := req.Engine
	if engineName == "" {
		// Check settings for default engine
		if val, err := h.Store.GetSetting(r.Context(), "fix.engine"); err == nil && val != "" {
			engineName = val
		} else {
			engineName = "auto"
		}
	}

	// Check default mode from settings if not specified
	if req.Mode == "" {
		if val, err := h.Store.GetSetting(r.Context(), "fix.default_mode"); err == nil && val != "" {
			mode = models.FixMode(val)
		}
	}

	// Resolve repo path from scan or from finding IDs
	var repoPath string
	if req.ScanID != "" {
		scan, err := h.Store.GetScanByID(r.Context(), req.ScanID)
		if err != nil {
			response.WriteError(w, http.StatusBadRequest, "scan_not_found", fmt.Sprintf("scan %s not found", req.ScanID))
			return
		}
		repo, err := h.Store.GetRepoByID(r.Context(), scan.RepoID)
		if err != nil {
			response.WriteError(w, http.StatusBadRequest, "repo_not_found", "could not resolve repository for scan")
			return
		}
		repoPath = repo.SourcePath
	} else if len(req.FindingIDs) > 0 {
		// Derive repo path from the first finding
		f, err := h.Store.GetFindingByID(r.Context(), req.FindingIDs[0])
		if err != nil {
			response.WriteError(w, http.StatusBadRequest, "finding_not_found", fmt.Sprintf("finding %s not found", req.FindingIDs[0]))
			return
		}
		repo, err := h.Store.GetRepoByID(r.Context(), f.RepoID)
		if err != nil {
			response.WriteError(w, http.StatusBadRequest, "repo_not_found", "could not resolve repository for finding")
			return
		}
		repoPath = repo.SourcePath
	}

	if repoPath == "" {
		response.WriteError(w, http.StatusBadRequest, "no_repo_path", "could not determine repository path")
		return
	}

	// Handle auto-init for non-git repos
	if !fixgit.IsGitRepo(repoPath) {
		if req.AutoInit {
			if err := fixgit.InitRepo(repoPath); err != nil {
				response.WriteError(w, http.StatusBadRequest, "git_init_failed",
					fmt.Sprintf("failed to initialize git repository: %v", err))
				return
			}
		} else {
			response.WriteError(w, http.StatusBadRequest, "not_git_repo",
				"repository is not a git repo — enable auto_init or run 'git init' manually")
			return
		}
	}

	now := time.Now()
	fix := &models.Fix{
		ID:             uuid.New().String(),
		UserID:         claims.UserID,
		ScanID:         req.ScanID,
		Status:         models.FixStatusPending,
		Mode:           mode,
		Engine:         engineName,
		SeverityFilter: toJSON(req.Severity),
		WorktreePath:   repoPath,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	if err := h.Store.CreateFix(r.Context(), fix); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to create fix")
		return
	}

	// Launch the fix runner in the background
	go executeFix(h, fix.ID, claims.UserID, req.ScanID, repoPath, engineName, mode, req.Severity, req.FindingIDs)

	response.WriteJSON(w, http.StatusCreated, response.SuccessResponse{Data: fix})
}

// executeFix runs the fix operation in the background.
// It resolves the engine, fetches findings, and runs the fix runner.
func executeFix(h *Handler, fixID, userID, scanID, repoPath, engineName string, mode models.FixMode, severities []string, findingIDs []string) {
	log := wolflog.Component("fix")
	log.Info().Str("fix_id", fixID).Str("engine", engineName).Str("mode", string(mode)).Msg("fix starting")

	ctx, cancel := context.WithCancel(context.Background())

	activeFixesMu.Lock()
	activeFixCtxs[fixID] = cancel
	activeFixesMu.Unlock()

	defer func() {
		activeFixesMu.Lock()
		delete(activeFixCtxs, fixID)
		activeFixesMu.Unlock()
		cancel()
	}()

	// Resolve engine
	eng, err := engine.NewEngine(engineName)
	if err != nil {
		log.Error().Err(err).Str("engine", engineName).Msg("failed to create engine")
		markFixFailed(ctx, h, fixID, fmt.Sprintf("engine error: %v", err))
		return
	}

	if !eng.Available() {
		log.Error().Str("engine", eng.Name()).Msg("engine not available")
		markFixFailed(ctx, h, fixID, fmt.Sprintf("engine %q not available — is the CLI installed?", eng.Name()))
		return
	}

	// Apply settings to engine (works for ClaudeCode, AutoEngine, and any SettingsApplier)
	if sa, ok := eng.(engine.SettingsApplier); ok {
		var maxBudget float64
		var maxTurns int
		if val, err := h.Store.GetSetting(ctx, "fix.max_budget_usd"); err == nil && val != "" {
			if f, err := strconv.ParseFloat(val, 64); err == nil {
				maxBudget = f
			}
		}
		if val, err := h.Store.GetSetting(ctx, "fix.max_turns"); err == nil && val != "" {
			if n, err := strconv.Atoi(val); err == nil {
				maxTurns = n
			}
		}
		if maxBudget > 0 || maxTurns > 0 {
			sa.ApplySettings(maxBudget, maxTurns)
		}
	}

	// Fetch findings
	var findings []models.Finding
	if len(findingIDs) > 0 {
		// Fetch specific findings by ID
		for _, fid := range findingIDs {
			f, err := h.Store.GetFindingByID(ctx, fid)
			if err != nil {
				log.Warn().Str("finding_id", fid).Err(err).Msg("finding not found, skipping")
				continue
			}
			findings = append(findings, *f)
		}
	} else if scanID != "" {
		// Fetch all findings from scan, optionally filtered by severity
		allFindings, err := h.Store.ListFindingsByScan(ctx, scanID)
		if err != nil {
			log.Error().Err(err).Msg("failed to list findings")
			markFixFailed(ctx, h, fixID, fmt.Sprintf("failed to fetch findings: %v", err))
			return
		}
		if len(severities) > 0 {
			findings = filterBySeverity(allFindings, severities)
		} else {
			findings = allFindings
		}
	}

	if len(findings) == 0 {
		markFixFailed(ctx, h, fixID, "no findings to fix")
		return
	}

	log.Info().Int("count", len(findings)).Str("fix_id", fixID).Msg("findings loaded, starting runner")

	// Create event callback for SSE broadcasting
	onEvent := func(eventType string, data map[string]interface{}) {
		if SSEBroker != nil {
			jsonData, _ := json.Marshal(data)
			SSEBroker.Publish("fix:"+fixID, sse.Event{
				Type: eventType,
				Data: string(jsonData),
			})
		}
	}

	// Create and run the fix runner
	r := runner.New(h.Store, eng, fixID, repoPath, mode, onEvent)
	if err := r.Run(ctx, findings); err != nil {
		log.Error().Err(err).Str("fix_id", fixID).Msg("fix runner failed")
		if ctx.Err() == nil {
			// Only mark as failed if not cancelled
			markFixFailed(ctx, h, fixID, fmt.Sprintf("runner error: %v", err))
		}
		return
	}

	log.Info().Str("fix_id", fixID).Msg("fix completed")
}

// markFixFailed sets a fix to failed status with an error message.
func markFixFailed(ctx context.Context, h *Handler, fixID, errMsg string) {
	fix, err := h.Store.GetFixByID(ctx, fixID)
	if err != nil {
		wolflog.Error().Err(err).Str("fix_id", fixID).Msg("failed to load fix for marking failed")
		return
	}
	now := time.Now()
	fix.Status = models.FixStatusFailed
	fix.CompletedAt = &now
	fix.UpdatedAt = now
	h.Store.UpdateFix(ctx, fix)

	if SSEBroker != nil {
		data, _ := json.Marshal(map[string]string{
			"fix_id": fixID,
			"error":  errMsg,
		})
		SSEBroker.Publish("fix:"+fixID, sse.Event{
			Type: "fix_failed",
			Data: string(data),
		})
	}
}

// filterBySeverity returns findings matching any of the given severity levels.
func filterBySeverity(findings []models.Finding, severities []string) []models.Finding {
	allowed := make(map[string]bool, len(severities))
	for _, s := range severities {
		allowed[strings.ToLower(strings.TrimSpace(s))] = true
	}
	var result []models.Finding
	for _, f := range findings {
		if allowed[strings.ToLower(string(f.Severity))] {
			result = append(result, f)
		}
	}
	return result
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

	// Enrich items with finding details
	type enrichedItem struct {
		models.FixItem
		Finding *models.Finding `json:"finding,omitempty"`
	}
	enriched := make([]enrichedItem, len(items))
	for i, item := range items {
		enriched[i] = enrichedItem{FixItem: item}
		if f, err := h.Store.GetFindingByID(r.Context(), item.FindingID); err == nil {
			enriched[i].Finding = f
		}
	}

	type fixDetail struct {
		*models.Fix
		Items []enrichedItem `json:"items"`
	}

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{
		Data: fixDetail{Fix: fix, Items: enriched},
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

	// Send initial state with items
	sendFixState(w, flusher, fix, h, r.Context())

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
			sendFixState(w, flusher, fix, h, r.Context())
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

func sendFixState(w http.ResponseWriter, flusher http.Flusher, fix *models.Fix, h *Handler, ctx context.Context) {
	items, _ := h.Store.ListFixItemsByFix(ctx, fix.ID)

	proposed := 0
	approved := 0
	rejected := 0
	fixed := 0
	failed := 0
	inProgress := 0
	for _, it := range items {
		switch it.Status {
		case models.FixItemStatusProposed:
			proposed++
		case models.FixItemStatusApproved:
			approved++
		case models.FixItemStatusRejected:
			rejected++
		case models.FixItemStatusFixed:
			fixed++
		case models.FixItemStatusFailed:
			failed++
		case models.FixItemStatusInProgress:
			inProgress++
		}
	}

	sendSSE(w, flusher, "fix_status", fmt.Sprintf(
		`{"fix_id":"%s","status":"%s","mode":"%s","engine":"%s","findings_attempted":%d,"findings_fixed":%d,"findings_failed":%d,"proposed":%d,"approved":%d,"rejected":%d,"in_progress":%d,"total_items":%d}`,
		fix.ID, fix.Status, fix.Mode, fix.Engine, fix.FindingsAttempted, fix.FindingsFixed, fix.FindingsFailed,
		proposed, approved, rejected, inProgress, len(items),
	))
}

// ApproveFixItem handles POST /api/fixes/:id/items/:item_id/approve
func ApproveFixItem(w http.ResponseWriter, r *http.Request) {
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

	fixID := chi.URLParam(r, "id")
	itemID := chi.URLParam(r, "itemId")

	fix, err := h.Store.GetFixByID(r.Context(), fixID)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", "fix not found")
		return
	}

	repoPath := fix.WorktreePath
	if repoPath == "" {
		// Fall back to looking up the repo path from the scan
		scan, err := h.Store.GetScanByID(r.Context(), fix.ScanID)
		if err == nil {
			repo, err := h.Store.GetRepoByID(r.Context(), scan.RepoID)
			if err == nil {
				repoPath = repo.SourcePath
			}
		}
	}

	if err := runner.ApplyItem(r.Context(), h.Store, repoPath, itemID); err != nil {
		response.WriteError(w, http.StatusBadRequest, "apply_failed", err.Error())
		return
	}

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: map[string]string{
		"status": "applied",
		"item_id": itemID,
	}})
}

// RejectFixItem handles POST /api/fixes/:id/items/:item_id/reject
func RejectFixItem(w http.ResponseWriter, r *http.Request) {
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

	itemID := chi.URLParam(r, "itemId")

	if err := runner.RejectItem(r.Context(), h.Store, itemID); err != nil {
		response.WriteError(w, http.StatusBadRequest, "reject_failed", err.Error())
		return
	}

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: map[string]string{
		"status":  "rejected",
		"item_id": itemID,
	}})
}

// ApproveAllFixItems handles POST /api/fixes/:id/approve-all
func ApproveAllFixItems(w http.ResponseWriter, r *http.Request) {
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

	fixID := chi.URLParam(r, "id")
	fix, err := h.Store.GetFixByID(r.Context(), fixID)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", "fix not found")
		return
	}

	repoPath := fix.WorktreePath
	if repoPath == "" {
		scan, err := h.Store.GetScanByID(r.Context(), fix.ScanID)
		if err == nil {
			repo, err := h.Store.GetRepoByID(r.Context(), scan.RepoID)
			if err == nil {
				repoPath = repo.SourcePath
			}
		}
	}

	items, err := h.Store.ListFixItemsByFix(r.Context(), fixID)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to list items")
		return
	}

	applied := 0
	var errors []string
	for _, item := range items {
		if item.Status == models.FixItemStatusProposed {
			if err := runner.ApplyItem(r.Context(), h.Store, repoPath, item.ID); err != nil {
				errors = append(errors, fmt.Sprintf("%s: %v", item.ID, err))
			} else {
				applied++
			}
		}
	}

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: map[string]interface{}{
		"applied": applied,
		"errors":  errors,
	}})
}

// RejectAllFixItems handles POST /api/fixes/:id/reject-all
func RejectAllFixItems(w http.ResponseWriter, r *http.Request) {
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

	fixID := chi.URLParam(r, "id")
	items, err := h.Store.ListFixItemsByFix(r.Context(), fixID)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to list items")
		return
	}

	rejected := 0
	for _, item := range items {
		if item.Status == models.FixItemStatusProposed {
			if err := runner.RejectItem(r.Context(), h.Store, item.ID); err == nil {
				rejected++
			}
		}
	}

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: map[string]interface{}{
		"rejected": rejected,
	}})
}

// ListFixEngines handles GET /api/fix-engines — list available fix engines.
func ListFixEngines(w http.ResponseWriter, r *http.Request) {
	engines := engine.ListAvailableEngines()
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: engines})
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

	// Cancel the running goroutine
	activeFixesMu.Lock()
	if cancelFn, ok := activeFixCtxs[id]; ok {
		cancelFn()
		delete(activeFixCtxs, id)
	}
	activeFixesMu.Unlock()

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: fix})
}

func sendSSE(w http.ResponseWriter, flusher http.Flusher, event, data string) {
	if event != "" {
		fmt.Fprintf(w, "event: %s\n", event)
	}
	fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()
}

func toJSON(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}

