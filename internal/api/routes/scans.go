package routes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/ai"
	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/api/sse"
	"github.com/alphabravocompany/thewolf/internal/artifacts"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/wolflog"
	"github.com/alphabravocompany/thewolf/internal/scan/coverage"
	"github.com/alphabravocompany/thewolf/internal/scan/enricher"
	"github.com/alphabravocompany/thewolf/internal/scan/mapper"
	"github.com/alphabravocompany/thewolf/internal/scan/report"
	"github.com/alphabravocompany/thewolf/internal/scan/runner"
	promptpkg "github.com/alphabravocompany/thewolf/internal/prompt"
	"github.com/alphabravocompany/thewolf/internal/scan/scorer"
	"github.com/alphabravocompany/thewolf/internal/secrets"
)

// SSEBroker is the package-level SSE broker for scan streaming.
// Set this before starting the server.
var SSEBroker *sse.Broker

var (
	activeScansMu  sync.Mutex
	activeScanCtxs = make(map[string]context.CancelFunc)
)

// createScanRequest is the JSON body for POST /api/scans.
type createScanRequest struct {
	RepoID        string   `json:"repo_id"`
	CollectionID  *string  `json:"collection_id,omitempty"`
	Branch        string   `json:"branch,omitempty"`
	Tools         []string `json:"tools,omitempty"`
	DisabledTools []string `json:"disabled_tools,omitempty"`
	AIEnabled     bool     `json:"ai_enabled,omitempty"`
	AIEngine      string   `json:"ai_engine,omitempty"`
	AIModel       string   `json:"ai_model,omitempty"`
}

// CreateScan handles POST /api/scans — create a new scan.
func CreateScan(w http.ResponseWriter, r *http.Request) {
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

	var req createScanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}

	if req.RepoID == "" {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "repo_id is required")
		return
	}

	// Verify the repo exists. No RBAC yet — all authenticated users can scan any repo.
	repo, err := h.Store.GetRepoByID(r.Context(), req.RepoID)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", "repo not found")
		return
	}

	// Look up collection scan config and apply defaults where request doesn't override.
	var collectionConfig models.ScanConfig
	if req.CollectionID != nil && *req.CollectionID != "" {
		col, colErr := h.Store.GetCollectionByID(r.Context(), *req.CollectionID)
		if colErr == nil && col.ScanConfig != "" && col.ScanConfig != "{}" {
			_ = json.Unmarshal([]byte(col.ScanConfig), &collectionConfig)
		}
	}

	if len(req.DisabledTools) == 0 && len(collectionConfig.DisabledTools) > 0 {
		req.DisabledTools = collectionConfig.DisabledTools
	}
	if !req.AIEnabled && collectionConfig.AIEnabled {
		req.AIEnabled = collectionConfig.AIEnabled
	}
	// Global AI kill switch — override per-scan/collection AI if globally disabled.
	if req.AIEnabled {
		if globalAI, err := h.Store.GetSetting(r.Context(), "ai_enabled"); err == nil && globalAI == "false" {
			req.AIEnabled = false
		}
	}
	if req.AIEngine == "" && collectionConfig.AIEngine != "" {
		req.AIEngine = collectionConfig.AIEngine
	}
	if req.AIModel == "" && collectionConfig.AIModel != "" {
		req.AIModel = collectionConfig.AIModel
	}

	branch := req.Branch
	if branch == "" {
		branch = repo.DefaultBranch
	}

	toolsSelected, _ := json.Marshal(req.Tools)

	now := time.Now()
	scan := &models.Scan{
		ID:            uuid.New().String(),
		UserID:        claims.UserID,
		RepoID:        req.RepoID,
		CollectionID:  req.CollectionID,
		Branch:        branch,
		Status:        models.ScanStatusPending,
		ToolsSelected: string(toolsSelected),
		AIEnabled:     req.AIEnabled,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	if err := h.Store.CreateScan(r.Context(), scan); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to create scan")
		return
	}

	go executeScan(h, scan.ID, claims.UserID, repo.SourcePath, branch, req)

	response.WriteJSON(w, http.StatusCreated, response.SuccessResponse{Data: scan})
}

// executeScan runs the scan in a background goroutine.
func executeScan(h *Handler, scanID, userID, repoPath, branch string, req createScanRequest) {
	log := wolflog.Component("scan")
	log.Info().Str("scan_id", scanID).Str("repo", repoPath).Str("branch", branch).Msg("scan starting")

	ctx, cancel := context.WithCancel(context.Background())

	activeScansMu.Lock()
	activeScanCtxs[scanID] = cancel
	activeScansMu.Unlock()

	defer func() {
		activeScansMu.Lock()
		delete(activeScanCtxs, scanID)
		activeScansMu.Unlock()
		cancel()
	}()

	// Mark scan as running.
	now := time.Now()
	scan, err := h.Store.GetScanByID(ctx, scanID)
	if err != nil {
		log.Error().Str("scan_id", scanID).Err(err).Msg("failed to load scan record")
		return
	}
	scan.Status = models.ScanStatusRunning
	scan.StartedAt = &now
	scan.UpdatedAt = now
	if err := h.Store.UpdateScan(ctx, scan); err != nil {
		return
	}

	topic := "scan:" + scanID

	// Use the durable artifact store for per-tool log files (~/.wolf/artifacts/<scanID>/).
	var logDir string
	if artifacts.Global != nil {
		logDir = artifacts.Global.ScanDir(scanID)
	} else {
		logDir = filepath.Join(os.TempDir(), "wolf-scans", scanID)
	}
	os.MkdirAll(logDir, 0o755)

	// Map to hold open log file writers per tool.
	var logMu sync.Mutex
	toolLogs := make(map[string]*os.File)

	// Track incremental tool completion for DB persistence so polling
	// clients (and page refreshes) see correct per-tool status mid-scan.
	var toolStateMu sync.Mutex
	toolsCompleted := make([]string, 0)
	toolsFailed := make([]string, 0)

	cfg := runner.RunConfig{
		RepoPath:      repoPath,
		Branch:        branch,
		Registry:      h.Registry,
		Tools:         req.Tools,
		DisabledTools: req.DisabledTools,
		OnToolsSelected: func(toolNames []string) {
			log.Info().Str("scan_id", scanID).Int("count", len(toolNames)).Strs("tools", toolNames).Msg("tools selected")
			// Persist selected tools immediately so the live page can show all cards.
			selectedJSON, _ := json.Marshal(toolNames)
			if s, err := h.Store.GetScanByID(context.Background(), scanID); err == nil {
				s.ToolsSelected = string(selectedJSON)
				s.UpdatedAt = time.Now()
				h.Store.UpdateScan(context.Background(), s)
			}
			// Also broadcast via SSE so connected clients see the full list.
			if SSEBroker != nil {
				toolsJSON, _ := json.Marshal(toolNames)
				SSEBroker.Publish(topic, sse.Event{
					Type: "tools_selected",
					Data: fmt.Sprintf(`{"type":"tools_selected","scan_id":"%s","tools":%s}`, scanID, string(toolsJSON)),
				})
			}
		},
		OnToolStart: func(toolName string) {
			log.Debug().Str("scan_id", scanID).Str("tool", toolName).Msg("tool starting")
			if SSEBroker != nil {
				SSEBroker.Publish(topic, sse.Event{
					Type: "scan_progress",
					Data: fmt.Sprintf(`{"type":"scan_progress","scan_id":"%s","tool_name":"%s","status":"running","finding_count":0,"elapsed_ms":0,"progress_pct":0}`, scanID, toolName),
				})
			}
		},
		OnToolDone: func(toolName string, findingCount int, toolErr error) {
			status := "completed"
			errMsg := ""
			if toolErr != nil {
				status = "failed"
				errMsg = toolErr.Error()
				log.Warn().Str("scan_id", scanID).Str("tool", toolName).Err(toolErr).Msg("tool failed")
			} else {
				log.Info().Str("scan_id", scanID).Str("tool", toolName).Int("findings", findingCount).Msg("tool completed")
			}
			if SSEBroker != nil {
				escapedErr, _ := json.Marshal(errMsg)
				SSEBroker.Publish(topic, sse.Event{
					Type: "scan_progress",
					Data: fmt.Sprintf(`{"type":"scan_progress","scan_id":"%s","tool_name":"%s","status":"%s","finding_count":%d,"elapsed_ms":0,"progress_pct":100,"error":%s}`, scanID, toolName, status, findingCount, string(escapedErr)),
				})
			}

			// Persist incremental tool status to DB so polling clients
			// and page refreshes see correct per-tool state mid-scan.
			toolStateMu.Lock()
			if toolErr != nil {
				toolsFailed = append(toolsFailed, toolName)
			} else {
				toolsCompleted = append(toolsCompleted, toolName)
			}
			completedJSON, _ := json.Marshal(toolsCompleted)
			failedJSON, _ := json.Marshal(toolsFailed)
			toolStateMu.Unlock()

			if s, err := h.Store.GetScanByID(context.Background(), scanID); err == nil {
				s.ToolsCompleted = string(completedJSON)
				s.ToolsFailed = string(failedJSON)
				s.UpdatedAt = time.Now()
				h.Store.UpdateScan(context.Background(), s)
			}
		},
		OnToolOutput: func(toolName string, line string) {
			if SSEBroker != nil {
				escapedLine, _ := json.Marshal(line)
				SSEBroker.Publish(topic, sse.Event{
					Type: "tool_output",
					Data: fmt.Sprintf(`{"type":"tool_output","scan_id":"%s","tool_name":"%s","line":%s}`, scanID, toolName, string(escapedLine)),
				})
			}
			// Append line to per-tool log file.
			logMu.Lock()
			f, ok := toolLogs[toolName]
			if !ok {
				f, _ = os.Create(filepath.Join(logDir, toolName+".log"))
				toolLogs[toolName] = f
			}
			if f != nil {
				fmt.Fprintln(f, line)
			}
			logMu.Unlock()
		},
	}

	result, runErr := runner.Run(ctx, cfg)

	// Update scan with results.
	completedAt := time.Now()
	scan, err = h.Store.GetScanByID(context.Background(), scanID)
	if err != nil {
		log.Error().Str("scan_id", scanID).Err(err).Msg("failed to reload scan after run")
		return
	}

	if runErr != nil {
		scan.Status = models.ScanStatusFailed
		log.Error().Str("scan_id", scanID).Err(runErr).Msg("scan run failed")
	} else {
		scan.Status = models.ScanStatusCompleted
		log.Info().Str("scan_id", scanID).Str("status", string(scan.Status)).Msg("scan run finished")
	}
	scan.CompletedAt = &completedAt
	scan.UpdatedAt = completedAt

	if result != nil {
		scan.FindingCount = len(result.Findings)

		failedNames := make(map[string]bool, len(result.ToolsFailed))
		for name := range result.ToolsFailed {
			failedNames[name] = true
		}

		completedOnly := make([]string, 0, len(result.ToolsRun))
		for _, name := range result.ToolsRun {
			if !failedNames[name] {
				completedOnly = append(completedOnly, name)
			}
		}

		// Set tools_selected from what actually ran (if not set at creation time).
		if scan.ToolsSelected == "" || scan.ToolsSelected == "null" {
			selectedJSON, _ := json.Marshal(result.ToolsRun)
			scan.ToolsSelected = string(selectedJSON)
		}

		completedJSON, _ := json.Marshal(completedOnly)
		scan.ToolsCompleted = string(completedJSON)

		failedList := make([]string, 0, len(result.ToolsFailed))
		for name := range result.ToolsFailed {
			failedList = append(failedList, name)
		}
		failedJSON, _ := json.Marshal(failedList)
		scan.ToolsFailed = string(failedJSON)

		// Enrich findings with code context (best-effort, non-blocking).
		go func(findings []models.Finding, repoID, rPath, br string) {
			repo, repoErr := h.Store.GetRepoByID(context.Background(), repoID)
			if repoErr != nil {
				return
			}
			var langs map[string]int
			if repo.DetectedLanguages != "" {
				_ = json.Unmarshal([]byte(repo.DetectedLanguages), &langs)
			}
			// Try to get existing repo map from DB (fast path).
			var repoMap *mapper.RepoMap
			if dbMap, dbErr := h.Store.GetRepoMap(context.Background(), repoID, br); dbErr == nil && dbMap != nil && dbMap.StructuralData != "" {
				var rm mapper.RepoMap
				if json.Unmarshal([]byte(dbMap.StructuralData), &rm) == nil {
					repoMap = &rm
				}
			}
			// Fall back to building map in background.
			if repoMap == nil {
				built, mapErr := mapper.BuildMap(mapper.MapConfig{RepoPath: rPath})
				if mapErr != nil {
					wolflog.Warn().Err(mapErr).Msg("Enrichment: mapper failed, using partial enrichment")
				}
				if built != nil {
					repoMap = built
				}
			}

			if repoMap == nil {
				// Even without a repo map, we can still extract module names.
				repoMap = &mapper.RepoMap{}
			}

			enricher.Enrich(findings, enricher.EnrichConfig{
				RepoPath:  rPath,
				RepoMap:   repoMap,
				Languages: langs,
			})

			// Update findings in DB with enrichment data.
			for _, f := range findings {
				_ = h.Store.UpdateFinding(context.Background(), &f)
			}
			wolflog.Info().Int("findings", len(findings)).Msg("Enrichment: completed in background")
		}(result.Findings, scan.RepoID, repoPath, branch)

		// Score findings with default AI context score.
		scorer.ScoreFindings(result.Findings)

		// Persist findings.
		if len(result.Findings) > 0 {
			for i := range result.Findings {
				result.Findings[i].ID = uuid.New().String()
				result.Findings[i].ScanID = scanID
				result.Findings[i].RepoID = scan.RepoID
				result.Findings[i].CreatedAt = completedAt
				result.Findings[i].UpdatedAt = completedAt
			}
			_ = h.Store.CreateFindings(context.Background(), result.Findings)
		}

		// AI Assessment phase.
		if req.AIEnabled && len(result.Findings) > 0 {
			aiProvider := resolveAIProvider(h, req.AIEngine, req.AIModel, scan.UserID)
			wolflog.Info().Str("engine", req.AIEngine).Str("provider", aiProvider.Name()).Int("findings", len(result.Findings)).Msg("AI assessment: resolved provider")
			if aiProvider.Name() != "noop" {
				runAIAssessment(context.Background(), h, aiProvider, scan, result.Findings, topic)
			} else {
				wolflog.Warn().Str("engine", req.AIEngine).Msg("AI assessment: skipped (noop provider)")
			}
		}

		// Static test coverage analysis.
		covReport, covErr := coverage.Analyze(repoPath)
		if covErr == nil && covReport.TotalSourceFiles > 0 {
			covJSON, _ := json.Marshal(covReport)
			scan.CoverageSummary = string(covJSON)
		}
	}

	// Close and persist tool log artifacts (from OnToolOutput callback).
	for _, f := range toolLogs {
		f.Close()
		info, err := os.Stat(f.Name())
		if err == nil && info.Size() > 0 {
			artifact := &models.ScanArtifact{
				ID:           uuid.New().String(),
				ScanID:       scanID,
				ArtifactType: models.ArtifactLog,
				FilePath:     f.Name(),
				FileSize:     info.Size(),
				CreatedAt:    time.Now(),
				UpdatedAt:    time.Now(),
			}
			h.Store.CreateScanArtifact(context.Background(), artifact)
		}
	}

	// Save per-tool findings as JSON artifacts for raw results viewing.
	if result != nil && len(result.Findings) > 0 {
		toolFindings := make(map[string][]models.Finding)
		for _, f := range result.Findings {
			toolFindings[f.ToolName] = append(toolFindings[f.ToolName], f)
		}
		for toolName, findings := range toolFindings {
			data, err := json.MarshalIndent(findings, "", "  ")
			if err != nil {
				continue
			}
			fpath := filepath.Join(logDir, toolName+".json")
			if err := os.WriteFile(fpath, data, 0o644); err != nil {
				continue
			}
			artifact := &models.ScanArtifact{
				ID:           uuid.New().String(),
				ScanID:       scanID,
				ArtifactType: models.ArtifactJSON,
				FilePath:     fpath,
				FileSize:     int64(len(data)),
				CreatedAt:    time.Now(),
				UpdatedAt:    time.Now(),
			}
			h.Store.CreateScanArtifact(context.Background(), artifact)
		}
	}

	_ = h.Store.UpdateScan(context.Background(), scan)

	// Publish completion event.
	if SSEBroker != nil {
		SSEBroker.Publish(topic, sse.Event{
			Type: "scan_complete",
			Data: fmt.Sprintf(`{"type":"scan_complete","scan_id":"%s","status":"%s","finding_count":%d}`, scanID, scan.Status, scan.FindingCount),
		})
	}

	log.Info().
		Str("scan_id", scanID).
		Str("status", string(scan.Status)).
		Int("findings", scan.FindingCount).
		Msg("scan fully complete")
}

// ListScans handles GET /api/scans — list scans for the current user.
// Supports query params: ?repo_id=&status=&page=1&per_page=50
func ListScans(w http.ResponseWriter, r *http.Request) {
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

	scans, err := h.Store.ListAllScans(r.Context())
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to list scans")
		return
	}

	// Build repo lookup for populating scan.Repo.
	repoCache := make(map[string]*models.Repo)
	for i := range scans {
		rid := scans[i].RepoID
		if _, ok := repoCache[rid]; !ok {
			if repo, rerr := h.Store.GetRepoByID(r.Context(), rid); rerr == nil {
				repoCache[rid] = repo
			}
		}
		scans[i].Repo = repoCache[rid]
	}

	// Apply optional filters.
	repoID := r.URL.Query().Get("repo_id")
	status := r.URL.Query().Get("status")

	filtered := make([]models.Scan, 0, len(scans))
	for _, s := range scans {
		if repoID != "" && s.RepoID != repoID {
			continue
		}
		if status != "" && string(s.Status) != status {
			continue
		}
		filtered = append(filtered, s)
	}

	// Pagination.
	page, perPage := parsePagination(r)
	total := len(filtered)
	start, end := paginateSlice(total, page, perPage)
	paged := filtered[start:end]

	response.WriteJSON(w, http.StatusOK, response.ListResponse{
		Data: paged,
		Meta: response.ListMeta{Total: total, Page: page, PerPage: perPage},
	})
}

// GetScan handles GET /api/scans/:id — get scan details.
func GetScan(w http.ResponseWriter, r *http.Request) {
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
	scan, err := h.Store.GetScanByID(r.Context(), id)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", fmt.Sprintf("scan %s not found", id))
		return
	}


	// Populate repo.
	if repo, rerr := h.Store.GetRepoByID(r.Context(), scan.RepoID); rerr == nil {
		scan.Repo = repo
	}

	// Include artifacts in response.
	artifacts, _ := h.Store.ListScanArtifacts(r.Context(), id)
	if artifacts == nil {
		artifacts = []models.ScanArtifact{}
	}

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{
		Data: map[string]interface{}{
			"id":               scan.ID,
			"user_id":          scan.UserID,
			"repo_id":          scan.RepoID,
			"collection_id":    scan.CollectionID,
			"loop_id":          scan.LoopID,
			"iteration":        scan.Iteration,
			"branch":           scan.Branch,
			"status":           scan.Status,
			"tools_selected":   scan.ToolsSelected,
			"tools_completed":  scan.ToolsCompleted,
			"tools_failed":     scan.ToolsFailed,
			"finding_count":    scan.FindingCount,
			"coverage_summary": scan.CoverageSummary,
			"ai_summary":       scan.AISummary,
			"started_at":       scan.StartedAt,
			"completed_at":     scan.CompletedAt,
			"created_at":       scan.CreatedAt,
			"updated_at":       scan.UpdatedAt,
			"repo":             scan.Repo,
			"artifacts":        artifacts,
		},
	})
}

// GetScanFindings handles GET /api/scans/:id/findings — list findings for a scan.
// Supports: ?severity=critical,high&tool=semgrep&status=open&sort=composite_score&order=desc&page=1&per_page=50
func GetScanFindings(w http.ResponseWriter, r *http.Request) {
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

	scanID := chi.URLParam(r, "id")

	// Verify scan exists.
	_, err := h.Store.GetScanByID(r.Context(), scanID)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", fmt.Sprintf("scan %s not found", scanID))
		return
	}


	findings, err := h.Store.ListFindingsByScan(r.Context(), scanID)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to list findings")
		return
	}

	// Filter by severity (comma-separated).
	severityFilter := r.URL.Query().Get("severity")
	toolFilter := r.URL.Query().Get("tool")
	statusFilter := r.URL.Query().Get("status")

	var severities map[string]bool
	if severityFilter != "" {
		severities = make(map[string]bool)
		for _, s := range strings.Split(severityFilter, ",") {
			severities[strings.TrimSpace(s)] = true
		}
	}

	filtered := make([]models.Finding, 0, len(findings))
	for _, f := range findings {
		if severities != nil && !severities[string(f.Severity)] {
			continue
		}
		if toolFilter != "" && f.ToolName != toolFilter {
			continue
		}
		if statusFilter != "" && string(f.Status) != statusFilter {
			continue
		}
		filtered = append(filtered, f)
	}

	// Sort.
	sortField := r.URL.Query().Get("sort")
	order := r.URL.Query().Get("order")
	desc := strings.EqualFold(order, "desc")

	if sortField != "" {
		sort.Slice(filtered, func(i, j int) bool {
			var less bool
			switch sortField {
			case "composite_score":
				less = filtered[i].CompositeScore < filtered[j].CompositeScore
			case "severity":
				less = severityRank(filtered[i].Severity) < severityRank(filtered[j].Severity)
			case "created_at":
				less = filtered[i].CreatedAt.Before(filtered[j].CreatedAt)
			case "tool_name":
				less = filtered[i].ToolName < filtered[j].ToolName
			default:
				less = filtered[i].CompositeScore < filtered[j].CompositeScore
			}
			if desc {
				return !less
			}
			return less
		})
	}

	// Pagination.
	page, perPage := parsePagination(r)
	total := len(filtered)
	start, end := paginateSlice(total, page, perPage)
	paged := filtered[start:end]

	response.WriteJSON(w, http.StatusOK, response.ListResponse{
		Data: paged,
		Meta: response.ListMeta{Total: total, Page: page, PerPage: perPage},
	})
}

// StreamScan handles GET /api/scans/:id/stream — SSE endpoint for scan progress.
func StreamScan(w http.ResponseWriter, r *http.Request) {
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

	scanID := chi.URLParam(r, "id")

	// Verify scan exists.
	_, err := h.Store.GetScanByID(r.Context(), scanID)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", fmt.Sprintf("scan %s not found", scanID))
		return
	}


	broker := SSEBroker
	if broker == nil {
		// Fallback: poll-based SSE if no broker is configured.
		streamScanPoll(w, r, h, scanID)
		return
	}

	topic := "scan:" + scanID
	clientID := uuid.New().String()
	client := broker.Subscribe(topic, clientID)
	defer broker.Unsubscribe(topic, clientID)

	sse.ServeHTTP(w, r, client)
}

// streamScanPoll is a fallback SSE implementation that polls the database.
func streamScanPoll(w http.ResponseWriter, r *http.Request, h *Handler, scanID string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Send initial status.
	scan, err := h.Store.GetScanByID(r.Context(), scanID)
	if err != nil {
		return
	}
	sendSSE(w, flusher, "scan_status", fmt.Sprintf(
		`{"type":"scan_status","scan_id":"%s","status":"%s","finding_count":%d}`,
		scan.ID, scan.Status, scan.FindingCount,
	))

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			scan, err = h.Store.GetScanByID(r.Context(), scanID)
			if err != nil {
				return
			}
			sendSSE(w, flusher, "scan_status", fmt.Sprintf(
				`{"type":"scan_status","scan_id":"%s","status":"%s","finding_count":%d}`,
				scan.ID, scan.Status, scan.FindingCount,
			))
			if scan.Status == models.ScanStatusCompleted || scan.Status == models.ScanStatusFailed || scan.Status == models.ScanStatusCancelled {
				sendSSE(w, flusher, "scan_complete", fmt.Sprintf(
					`{"type":"scan_complete","scan_id":"%s","status":"%s","finding_count":%d}`,
					scan.ID, scan.Status, scan.FindingCount,
				))
				return
			}
		}
	}
}

// GetScanReport handles GET /api/scans/:id/report — return markdown report artifact.
func GetScanReport(w http.ResponseWriter, r *http.Request) {
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

	scanID := chi.URLParam(r, "id")

	scan, err := h.Store.GetScanByID(r.Context(), scanID)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", fmt.Sprintf("scan %s not found", scanID))
		return
	}


	cfg, err := buildReportConfig(h, r, scan)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to build report data")
		return
	}

	md, err := report.GenerateMarkdown(cfg)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to generate report")
		return
	}

	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="scan-%s-report.md"`, scanID))
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(md))
}

// GetScanSARIF handles GET /api/scans/:id/sarif — generate and return SARIF report.
func GetScanSARIF(w http.ResponseWriter, r *http.Request) {
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

	scanID := chi.URLParam(r, "id")

	scan, err := h.Store.GetScanByID(r.Context(), scanID)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", fmt.Sprintf("scan %s not found", scanID))
		return
	}


	cfg, err := buildReportConfig(h, r, scan)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to build report data")
		return
	}

	sarif, err := report.GenerateSARIF(cfg)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to generate SARIF")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="scan-%s.sarif.json"`, scanID))
	w.WriteHeader(http.StatusOK)
	w.Write(sarif)
}

// CancelScan handles DELETE /api/scans/:id — cancel a running or pending scan.
func CancelScan(w http.ResponseWriter, r *http.Request) {
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
	scan, err := h.Store.GetScanByID(r.Context(), id)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", fmt.Sprintf("scan %s not found", id))
		return
	}


	if scan.Status != models.ScanStatusRunning && scan.Status != models.ScanStatusPending {
		response.WriteError(w, http.StatusConflict, "conflict", "scan is not running or pending")
		return
	}

	now := time.Now()
	scan.Status = models.ScanStatusCancelled
	scan.CompletedAt = &now
	scan.UpdatedAt = now

	if err := h.Store.UpdateScan(r.Context(), scan); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to cancel scan")
		return
	}

	activeScansMu.Lock()
	if cancelFn, ok := activeScanCtxs[id]; ok {
		cancelFn()
		delete(activeScanCtxs, id)
	}
	activeScansMu.Unlock()

	// Notify SSE subscribers if broker is available.
	if SSEBroker != nil {
		SSEBroker.Publish("scan:"+id, sse.Event{
			Type: "scan_cancelled",
			Data: fmt.Sprintf(`{"scan_id":"%s","status":"cancelled"}`, id),
		})
	}

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: scan})
}

// ComparisonResult holds the diff between two scans.
type ComparisonResult struct {
	Scan1           *models.Scan      `json:"scan1"`
	Scan2           *models.Scan      `json:"scan2"`
	NewFindings     []models.Finding  `json:"new_findings"`
	FixedFindings   []models.Finding  `json:"fixed_findings"`
	UnchangedCount  int               `json:"unchanged_count"`
	ChangedFindings []ChangedFinding  `json:"changed_findings"`
	Summary         ComparisonSummary `json:"summary"`
}

// ChangedFinding pairs a before/after finding with the same fingerprint but different severity/status.
type ChangedFinding struct {
	Before models.Finding `json:"before"`
	After  models.Finding `json:"after"`
}

// ComparisonSummary provides aggregate counts for the comparison.
type ComparisonSummary struct {
	Scan1Total     int     `json:"scan1_total"`
	Scan2Total     int     `json:"scan2_total"`
	NewCount       int     `json:"new_count"`
	FixedCount     int     `json:"fixed_count"`
	UnchangedCount int     `json:"unchanged_count"`
	ChangedCount   int     `json:"changed_count"`
	DeltaPercent   float64 `json:"delta_percent"`
}

// CompareScan handles GET /api/scans/{id}/compare/{compareId} — compare two scans.
func CompareScan(w http.ResponseWriter, r *http.Request) {
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

	id1 := chi.URLParam(r, "id")
	id2 := chi.URLParam(r, "compareId")

	scan1, err := h.Store.GetScanByID(r.Context(), id1)


	scan2, err := h.Store.GetScanByID(r.Context(), id2)


	findings1, err := h.Store.ListFindingsByScan(r.Context(), id1)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to list findings for scan1")
		return
	}
	findings2, err := h.Store.ListFindingsByScan(r.Context(), id2)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to list findings for scan2")
		return
	}

	// Index findings by fingerprint.
	map1 := make(map[string]models.Finding, len(findings1))
	for _, f := range findings1 {
		map1[f.Fingerprint] = f
	}
	map2 := make(map[string]models.Finding, len(findings2))
	for _, f := range findings2 {
		map2[f.Fingerprint] = f
	}

	var newFindings []models.Finding
	var fixedFindings []models.Finding
	var changedFindings []ChangedFinding
	unchangedCount := 0

	for fp, f2 := range map2 {
		f1, exists := map1[fp]
		if !exists {
			newFindings = append(newFindings, f2)
		} else if f1.Severity != f2.Severity || f1.Status != f2.Status {
			changedFindings = append(changedFindings, ChangedFinding{Before: f1, After: f2})
		} else {
			unchangedCount++
		}
	}

	for fp, f1 := range map1 {
		if _, exists := map2[fp]; !exists {
			fixedFindings = append(fixedFindings, f1)
		}
	}

	var deltaPercent float64
	if len(findings1) > 0 {
		deltaPercent = float64(len(findings2)-len(findings1)) / float64(len(findings1)) * 100
	} else if len(findings2) > 0 {
		deltaPercent = 100
	}

	result := ComparisonResult{
		Scan1:           scan1,
		Scan2:           scan2,
		NewFindings:     newFindings,
		FixedFindings:   fixedFindings,
		UnchangedCount:  unchangedCount,
		ChangedFindings: changedFindings,
		Summary: ComparisonSummary{
			Scan1Total:     len(findings1),
			Scan2Total:     len(findings2),
			NewCount:       len(newFindings),
			FixedCount:     len(fixedFindings),
			UnchangedCount: unchangedCount,
			ChangedCount:   len(changedFindings),
			DeltaPercent:   deltaPercent,
		},
	}

	if result.NewFindings == nil {
		result.NewFindings = []models.Finding{}
	}
	if result.FixedFindings == nil {
		result.FixedFindings = []models.Finding{}
	}
	if result.ChangedFindings == nil {
		result.ChangedFindings = []ChangedFinding{}
	}

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: result})
}

// GetScanCoverage handles GET /api/scans/:id/coverage — return coverage analysis.
func GetScanCoverage(w http.ResponseWriter, r *http.Request) {
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

	scanID := chi.URLParam(r, "id")

	scan, err := h.Store.GetScanByID(r.Context(), scanID)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", fmt.Sprintf("scan %s not found", scanID))
		return
	}

	if scan.CoverageSummary == "" {
		response.WriteError(w, http.StatusNotFound, "not_found", "coverage data not available for this scan")
		return
	}

	var coverageData json.RawMessage
	if err := json.Unmarshal([]byte(scan.CoverageSummary), &coverageData); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "invalid coverage data")
		return
	}

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: coverageData})
}

// --- AI assessment ---

// resolveAIProvider creates an AI provider with auto-fallback logic:
//  1. Explicit engine → use that engine directly
//  2. No engine specified → try Anthropic API key (user secret → env var), fall back to CLI
func resolveAIProvider(h *Handler, engine, model, userID string) ai.Provider {
	// Explicit CLI engine selection.
	if ai.IsCLIEngine(engine) {
		return ai.NewCLIProvider(engine)
	}

	// Explicit API engine selection.
	if engine == "anthropic" || engine == "openai" {
		apiKey := getAPIKey(h, engine, userID)
		if apiKey == "" {
			return ai.NewNoopProvider()
		}
		if engine == "anthropic" {
			return ai.NewAnthropicProvider(apiKey, model)
		}
		return ai.NewProvider(engine, apiKey)
	}

	// Auto-select: try Anthropic API key first, then CLI fallback.
	if engine == "" {
		apiKey := getAPIKey(h, "anthropic", userID)
		if apiKey != "" {
			return ai.NewAnthropicProvider(apiKey, model)
		}
		// Fall back to claude-code CLI if available.
		if ai.IsCLIEngine("claude-code") {
			return ai.NewCLIProvider("claude-code")
		}
		return ai.NewNoopProvider()
	}

	return ai.NewNoopProvider()
}

// getAPIKey looks up an API key from user secrets, then falls back to environment variables.
func getAPIKey(h *Handler, engine, userID string) string {
	var keyType models.KeyType
	var envVar string
	switch strings.ToLower(engine) {
	case "anthropic":
		keyType = models.KeyTypeAnthropicKey
		envVar = "ANTHROPIC_API_KEY"
	case "openai":
		keyType = models.KeyTypeOpenAIKey
		envVar = "OPENAI_API_KEY"
	default:
		return ""
	}

	// 1. Check user secrets.
	if userID != "" {
		userSecrets, err := h.Store.ListSecretsByUser(context.Background(), userID)
		if err == nil {
			for _, s := range userSecrets {
				if s.KeyType == keyType {
					if decrypted, decErr := secrets.Decrypt(s.EncryptedValue); decErr == nil {
						return decrypted
					}
				}
			}
		}
	}

	// 2. Fall back to environment variable.
	return os.Getenv(envVar)
}

// runAIAssessment assesses findings per-tool, then generates a final summary.
// Each tool's findings are sent in a separate AI call, with SSE progress after each.
// ListAILogs returns AI call logs for a scan.
func ListAILogs(w http.ResponseWriter, r *http.Request) {
	h := DefaultHandler
	scanID := chi.URLParam(r, "id")
	logs, err := h.Store.ListAILogsByScan(r.Context(), scanID)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to list AI logs")
		return
	}
	if logs == nil {
		logs = []models.AILog{}
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: logs})
}

func runAIAssessment(ctx context.Context, h *Handler, provider ai.Provider, scan *models.Scan, findings []models.Finding, topic string) {
	// Set up AI call logging with phase/tool tracking.
	var currentPhase, currentTool string
	ai.SetLogCallback(provider, func(entry ai.AICallLog) {
		logEntry := &models.AILog{
			ID:             uuid.New().String(),
			ScanID:         scan.ID,
			Provider:       entry.Provider,
			Model:          entry.Model,
			Phase:          currentPhase,
			ToolName:       currentTool,
			Prompt:         entry.Prompt,
			Response:       entry.Response,
			Error:          entry.Error,
			PromptTokens:   entry.PromptTokens,
			ResponseTokens: entry.ResponseTokens,
			DurationMs:     entry.DurationMs,
		}
		if err := h.Store.CreateAILog(context.Background(), logEntry); err != nil {
			wolflog.Error().Err(err).Msg("Failed to persist AI log")
		}
		if SSEBroker != nil {
			SSEBroker.Publish(topic, sse.Event{
				Type: "ai_log",
				Data: fmt.Sprintf(`{"type":"ai_log","scan_id":"%s","provider":"%s","model":"%s","phase":"%s","tool":"%s","duration_ms":%d,"error":"%s"}`,
					scan.ID, entry.Provider, entry.Model, currentPhase, currentTool, entry.DurationMs, strings.ReplaceAll(entry.Error, `"`, `\"`)),
			})
		}
	})

	// Resolve repo context.
	repoName := "unknown"
	var languages map[string]int
	var frameworks []string
	if repo, err := h.Store.GetRepoByID(ctx, scan.RepoID); err == nil {
		repoName = repo.Name
		if repo.DetectedLanguages != "" {
			_ = json.Unmarshal([]byte(repo.DetectedLanguages), &languages)
		}
		if repo.DetectedFrameworks != "" {
			_ = json.Unmarshal([]byte(repo.DetectedFrameworks), &frameworks)
		}
	}

	// Resolve collection ID for prompt template lookups.
	collectionID := ""
	if scan.CollectionID != nil {
		collectionID = *scan.CollectionID
	}

	// Resolve prompt template sections.
	toolSysCtx := promptpkg.Resolve(ctx, h.Store, promptpkg.TypeToolAssess, promptpkg.SectionSystemCtx, collectionID)
	toolScoring := promptpkg.Resolve(ctx, h.Store, promptpkg.TypeToolAssess, promptpkg.SectionScoring, collectionID)
	toolOutput := promptpkg.Resolve(ctx, h.Store, promptpkg.TypeToolAssess, promptpkg.SectionOutputInstr, collectionID)
	execSysCtx := promptpkg.Resolve(ctx, h.Store, promptpkg.TypeExecSummary, promptpkg.SectionSystemCtx, collectionID)
	execScoring := promptpkg.Resolve(ctx, h.Store, promptpkg.TypeExecSummary, promptpkg.SectionScoring, collectionID)
	execOutput := promptpkg.Resolve(ctx, h.Store, promptpkg.TypeExecSummary, promptpkg.SectionOutputInstr, collectionID)

	// Build severity/tool counts.
	bySeverity := make(map[string]int)
	byToolCount := make(map[string]int)
	for _, f := range findings {
		bySeverity[string(f.Severity)]++
		byToolCount[f.ToolName]++
	}

	// Two-phase pipeline: per-tool assessment + executive summary.

	// Group findings by tool.
	byTool := make(map[string][]int)
	for i, f := range findings {
		byTool[f.ToolName] = append(byTool[f.ToolName], i)
	}

	toolNames := make([]string, 0, len(byTool))
	for t := range byTool {
		toolNames = append(toolNames, t)
	}
	sort.Strings(toolNames)

	totalSteps := len(toolNames) + 1
	toolSummaries := make(map[string]string)
	var allCriticalIssues []ai.AssessedIssue

	wolflog.Info().Int("findings", len(findings)).Int("tools", len(toolNames)).Str("provider", provider.Name()).Msg("AI assessment: starting per-tool assessment")

	// Phase 1: Assess each tool's findings.
	for step, toolName := range toolNames {
		currentPhase = "tool_assess"
		currentTool = toolName
		indices := byTool[toolName]
		pct := ((step + 1) * 80) / totalSteps

		if SSEBroker != nil {
			SSEBroker.Publish(topic, sse.Event{
				Type: "ai_assessment",
				Data: fmt.Sprintf(`{"type":"ai_assessment","scan_id":"%s","phase":"assessing","tool":"%s","step":%d,"total_steps":%d,"progress_pct":%d}`,
					scan.ID, toolName, step+1, totalSteps, pct),
			})
		}

		// Build finding data for prompt package.
		findingData := make([]promptpkg.FindingData, len(indices))
		for j, idx := range indices {
			f := findings[idx]
			findingData[j] = promptpkg.FindingData{
				Index:        j,
				Severity:     string(f.Severity),
				Title:        f.Title,
				Description:  f.Description,
				FilePath:     f.FilePath,
				LineStart:    f.LineStart,
				ModuleName:   f.ModuleName,
				FunctionName: f.FunctionName,
				FilePurpose:  f.FilePurpose,
				Dependents:   len(parseDependents(f.DependentsJSON)),
			}
		}

		p := promptpkg.BuildToolAssess(toolSysCtx, toolScoring, toolOutput, promptpkg.ToolAssessData{
			ToolName:   toolName,
			RepoName:   repoName,
			Languages:  languages,
			Frameworks: frameworks,
			Findings:   findingData,
		})

		wolflog.Info().Str("tool", toolName).Int("findings", len(findingData)).Int("prompt_len", len(p)).Msg("AI assessment: assessing tool")

		body, err := provider.Complete(ctx, p)
		if err != nil {
			wolflog.Error().Err(err).Str("tool", toolName).Msg("AI assessment: tool assessment failed")
			continue
		}

		wolflog.Debug().Str("tool", toolName).Int("body_len", len(body)).Msg("AI assessment: raw response")

		var toolResp ai.ToolAssessResponse
		if err := extractJSON(body, &toolResp); err != nil {
			wolflog.Error().Err(err).Str("tool", toolName).Msg("AI assessment: failed to parse tool response")
			continue
		}

		toolSummaries[toolName] = toolResp.ToolSummary

		// Persist tool summary.
		sevCounts := make(map[string]int)
		for _, idx := range indices {
			sevCounts[string(findings[idx].Severity)]++
		}
		sevCountsJSON, _ := json.Marshal(sevCounts)
		critJSON, _ := json.Marshal(toolResp.CriticalIssues)
		ts := &models.ToolSummary{
			ID:             uuid.New().String(),
			ScanID:         scan.ID,
			ToolName:       toolName,
			SummaryText:    toolResp.ToolSummary,
			FindingCount:   len(indices),
			SeverityCounts: string(sevCountsJSON),
			CriticalIssues: string(critJSON),
		}
		_ = h.Store.CreateToolSummary(context.Background(), ts)

		// Apply scores back to findings.
		for _, fs := range toolResp.FindingScores {
			if fs.Index >= 0 && fs.Index < len(indices) {
				globalIdx := indices[fs.Index]
				findings[globalIdx].AIContextScore = fs.ContextScore
			}
		}

		// Apply fix suggestions from critical issues.
		for _, ci := range toolResp.CriticalIssues {
			if ci.FindingIndex >= 0 && ci.FindingIndex < len(indices) {
				globalIdx := indices[ci.FindingIndex]
				findings[globalIdx].AIFixSuggestion = ci.FixSuggestion
				if ci.ContextScore > 0 {
					findings[globalIdx].AIContextScore = ci.ContextScore
				}
			}
			allCriticalIssues = append(allCriticalIssues, ci)
		}

		wolflog.Info().Str("tool", toolName).Int("scores", len(toolResp.FindingScores)).Int("critical", len(toolResp.CriticalIssues)).Msg("AI assessment: tool complete")
	}

	// Re-score with AI context scores.
	scorer.ScoreFindings(findings)

	// Phase 2: Executive summary across all tools.
	currentPhase = "summary"
	currentTool = ""
	if SSEBroker != nil {
		SSEBroker.Publish(topic, sse.Event{
			Type: "ai_assessment",
			Data: fmt.Sprintf(`{"type":"ai_assessment","scan_id":"%s","phase":"summarizing","step":%d,"total_steps":%d,"progress_pct":85}`,
				scan.ID, totalSteps, totalSteps),
		})
	}

	// Sort critical issues by score desc, take top 10.
	sort.Slice(allCriticalIssues, func(i, j int) bool {
		return allCriticalIssues[i].ContextScore > allCriticalIssues[j].ContextScore
	})
	topIssues := allCriticalIssues
	if len(topIssues) > 10 {
		topIssues = topIssues[:10]
	}

	// Convert top issues to prompt package format.
	promptTopIssues := make([]promptpkg.TopIssue, len(topIssues))
	for i, ci := range topIssues {
		promptTopIssues[i] = promptpkg.TopIssue{
			Severity:     ci.Severity,
			ContextScore: ci.ContextScore,
			Title:        ci.Title,
			Impact:       ci.Impact,
		}
	}

	summaryPrompt := promptpkg.BuildExecSummary(execSysCtx, execScoring, execOutput, promptpkg.ExecSummaryData{
		RepoName:      repoName,
		Languages:     languages,
		Frameworks:    frameworks,
		TotalFindings: len(findings),
		BySeverity:    bySeverity,
		ByTool:        byToolCount,
		ToolSummaries: toolSummaries,
		TopIssues:     promptTopIssues,
	})

	wolflog.Info().Int("prompt_len", len(summaryPrompt)).Msg("AI assessment: generating executive summary")

	summaryBody, err := provider.Complete(ctx, summaryPrompt)
	if err != nil {
		wolflog.Error().Err(err).Msg("AI assessment: executive summary failed")
		var fallback strings.Builder
		for _, t := range toolNames {
			if s, ok := toolSummaries[t]; ok {
				fmt.Fprintf(&fallback, "**%s**: %s\n\n", t, s)
			}
		}
		scan.AISummary = fallback.String()
	} else {
		wolflog.Debug().Int("body_len", len(summaryBody)).Msg("AI assessment: summary raw response")
		var finalResp ai.FinalSummaryResponse
		if err := extractJSON(summaryBody, &finalResp); err != nil {
			wolflog.Warn().Err(err).Msg("AI assessment: summary parse failed, using raw prose")
			scan.AISummary = summaryBody
		} else {
			scan.AISummary = finalResp.Summary
			if len(finalResp.Recommendations) > 0 {
				scan.AISummary += "\n\n## Recommendations\n"
				for i, rec := range finalResp.Recommendations {
					scan.AISummary += fmt.Sprintf("%d. %s\n", i+1, rec)
				}
			}
			if len(finalResp.StructuredRecs) > 0 {
				for _, sr := range finalResp.StructuredRecs {
					affectedJSON, _ := json.Marshal(sr.AffectedTools)
					rec := &models.ScanRecommendation{
						ID:             uuid.New().String(),
						ScanID:         scan.ID,
						Priority:       sr.Priority,
						Category:       sr.Category,
						Title:          sr.Title,
						Description:    sr.Description,
						AffectedTools:  string(affectedJSON),
						EffortEstimate: sr.EffortEstimate,
					}
					_ = h.Store.CreateScanRecommendation(context.Background(), rec)
				}
			}
		}
	}

	// Update findings and scan in DB.
	for _, f := range findings {
		_ = h.Store.UpdateFinding(context.Background(), &f)
	}
	_ = h.Store.UpdateScan(context.Background(), scan)

	wolflog.Info().Int("summary_len", len(scan.AISummary)).Msg("AI assessment complete")

	if SSEBroker != nil {
		SSEBroker.Publish(topic, sse.Event{
			Type: "ai_assessment",
			Data: fmt.Sprintf(`{"type":"ai_assessment","scan_id":"%s","phase":"complete","progress_pct":100}`, scan.ID),
		})
	}
}

// extractJSON parses JSON from AI response text, handling markdown fences.
func extractJSON[T any](text string, dest *T) error {
	text = strings.TrimSpace(text)

	if err := json.Unmarshal([]byte(text), dest); err == nil {
		return nil
	}

	// Strip markdown code fences if present.
	cleaned := text
	if idx := strings.Index(cleaned, "```json"); idx != -1 {
		cleaned = cleaned[idx+7:]
	} else if idx := strings.Index(cleaned, "```"); idx != -1 {
		cleaned = cleaned[idx+3:]
	}
	if idx := strings.LastIndex(cleaned, "```"); idx != -1 {
		cleaned = cleaned[:idx]
	}
	cleaned = strings.TrimSpace(cleaned)

	if err := json.Unmarshal([]byte(cleaned), dest); err != nil {
		return fmt.Errorf("failed to parse JSON: %w\nraw: %.500s", err, text)
	}
	return nil
}

// DownloadArtifact handles GET /api/scans/:id/artifacts/:artifactId/download.
func DownloadArtifact(w http.ResponseWriter, r *http.Request) {
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

	scanID := chi.URLParam(r, "id")
	artifactID := chi.URLParam(r, "artifactId")

	_, err := h.Store.GetScanByID(r.Context(), scanID)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", "scan not found")
		return
	}


	artifacts, err := h.Store.ListScanArtifacts(r.Context(), scanID)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to list artifacts")
		return
	}

	var target *models.ScanArtifact
	for _, a := range artifacts {
		if a.ID == artifactID {
			target = &a
			break
		}
	}
	if target == nil {
		response.WriteError(w, http.StatusNotFound, "not_found", "artifact not found")
		return
	}

	content, err := os.ReadFile(target.FilePath)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", "artifact file not found on disk")
		return
	}

	ct := "application/octet-stream"
	switch target.ArtifactType {
	case models.ArtifactJSON, models.ArtifactSARIF:
		ct = "application/json"
	case models.ArtifactMarkdown:
		ct = "text/markdown; charset=utf-8"
	case models.ArtifactLog:
		ct = "text/plain; charset=utf-8"
	}

	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filepath.Base(target.FilePath)))
	w.WriteHeader(http.StatusOK)
	w.Write(content)
}

// --- helpers ---

// buildReportConfig assembles a ReportConfig from DB data for on-demand report generation.
func buildReportConfig(h *Handler, r *http.Request, scan *models.Scan) (report.ReportConfig, error) {
	findings, err := h.Store.ListFindingsByScan(r.Context(), scan.ID)
	if err != nil {
		return report.ReportConfig{}, err
	}

	repo, _ := h.Store.GetRepoByID(r.Context(), scan.RepoID)
	repoName := scan.RepoID
	if repo != nil {
		repoName = repo.Name
	}

	// Parse tool arrays from JSON strings.
	var toolsSelected, toolsFailed []string
	_ = json.Unmarshal([]byte(scan.ToolsSelected), &toolsSelected)
	_ = json.Unmarshal([]byte(scan.ToolsFailed), &toolsFailed)

	failedMap := make(map[string]error, len(toolsFailed))
	for _, t := range toolsFailed {
		failedMap[t] = fmt.Errorf("tool failed during scan")
	}

	// Compute duration.
	var duration time.Duration
	if scan.StartedAt != nil {
		end := time.Now()
		if scan.CompletedAt != nil {
			end = *scan.CompletedAt
		}
		duration = end.Sub(*scan.StartedAt)
	}

	// Get language/framework info from repo detection cache if available.
	languages := map[string]int{}
	var frameworks []string
	if repo != nil && repo.DetectedAt != nil {
		_ = json.Unmarshal([]byte(repo.DetectedLanguages), &languages)
		_ = json.Unmarshal([]byte(repo.DetectedFrameworks), &frameworks)
	}

	return report.ReportConfig{
		ScanID:      scan.ID,
		RepoName:    repoName,
		Branch:      scan.Branch,
		Findings:    findings,
		Languages:   languages,
		Frameworks:  frameworks,
		ToolsRun:    toolsSelected,
		ToolsFailed: failedMap,
		Duration:    duration,
		AISummary:   scan.AISummary,
	}, nil
}

// findArtifact looks up a scan artifact by type.
func findArtifact(h *Handler, r *http.Request, scanID string, artifactType models.ArtifactType) (*models.ScanArtifact, error) {
	artifacts, err := h.Store.ListScanArtifacts(r.Context(), scanID)
	if err != nil {
		return nil, err
	}
	for _, a := range artifacts {
		if a.ArtifactType == artifactType {
			return &a, nil
		}
	}
	return nil, fmt.Errorf("artifact of type %s not found", artifactType)
}

// parsePagination extracts page and per_page from query params with defaults.
func parsePagination(r *http.Request) (page, perPage int) {
	page = 1
	perPage = 50

	if v := r.URL.Query().Get("page"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 {
			page = p
		}
	}
	if v := r.URL.Query().Get("per_page"); v != "" {
		if pp, err := strconv.Atoi(v); err == nil && pp > 0 {
			if pp > 200 {
				pp = 200
			}
			perPage = pp
		}
	}
	return
}

// paginateSlice returns start and end indices for a slice given pagination params.
func paginateSlice(total, page, perPage int) (start, end int) {
	start = (page - 1) * perPage
	if start > total {
		start = total
	}
	end = start + perPage
	if end > total {
		end = total
	}
	return
}

// severityRank returns a numeric rank for sorting severities (higher = more severe).
func severityRank(s models.Severity) int {
	switch s {
	case models.SeverityCritical:
		return 5
	case models.SeverityHigh:
		return 4
	case models.SeverityMedium:
		return 3
	case models.SeverityLow:
		return 2
	case models.SeverityInfo:
		return 1
	default:
		return 0
	}
}

// GetToolOutput handles GET /api/scans/{id}/tools/{toolName}/output — return raw tool output.
func GetToolOutput(w http.ResponseWriter, r *http.Request) {
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

	scanID := chi.URLParam(r, "id")
	toolName := chi.URLParam(r, "toolName")

	_, err := h.Store.GetScanByID(r.Context(), scanID)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", fmt.Sprintf("scan %s not found", scanID))
		return
	}

	artifacts, err := h.Store.ListScanArtifacts(r.Context(), scanID)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to list artifacts")
		return
	}

	// Try log artifact first (stderr output), then JSON (raw findings).
	for _, a := range artifacts {
		if a.ArtifactType == models.ArtifactLog && strings.HasSuffix(a.FilePath, toolName+".log") {
			content, err := os.ReadFile(a.FilePath)
			if err != nil {
				continue
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			w.Write(content)
			return
		}
	}
	for _, a := range artifacts {
		if a.ArtifactType == models.ArtifactJSON && strings.HasSuffix(a.FilePath, toolName+".json") {
			content, err := os.ReadFile(a.FilePath)
			if err != nil {
				response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to read output file")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write(content)
			return
		}
	}

	response.WriteError(w, http.StatusNotFound, "not_found", "tool output not found")
}

// GetScanTools handles GET /api/scans/{id}/tools — list tools that ran in a scan.
func GetScanTools(w http.ResponseWriter, r *http.Request) {
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

	scanID := chi.URLParam(r, "id")
	scan, err := h.Store.GetScanByID(r.Context(), scanID)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", fmt.Sprintf("scan %s not found", scanID))
		return
	}

	// Parse tool arrays.
	var selected, completed, failed []string
	json.Unmarshal([]byte(scan.ToolsSelected), &selected)
	json.Unmarshal([]byte(scan.ToolsCompleted), &completed)
	json.Unmarshal([]byte(scan.ToolsFailed), &failed)

	completedSet := make(map[string]bool)
	for _, t := range completed {
		completedSet[t] = true
	}
	failedSet := make(map[string]bool)
	for _, t := range failed {
		failedSet[t] = true
	}

	// Check which tools have log or JSON artifacts.
	artifacts, _ := h.Store.ListScanArtifacts(r.Context(), scanID)
	hasOutput := make(map[string]bool)
	for _, a := range artifacts {
		if a.ArtifactType == models.ArtifactLog || a.ArtifactType == models.ArtifactJSON {
			base := filepath.Base(a.FilePath)
			name := strings.TrimSuffix(strings.TrimSuffix(base, ".log"), ".json")
			hasOutput[name] = true
		}
	}

	// Get finding counts per tool.
	findings, _ := h.Store.ListFindingsByScan(r.Context(), scanID)
	findingCounts := make(map[string]int)
	for _, f := range findings {
		findingCounts[f.ToolName]++
	}

	type toolStatus struct {
		Name         string `json:"name"`
		Status       string `json:"status"`
		FindingCount int    `json:"finding_count"`
		HasOutput    bool   `json:"has_output"`
	}

	var tools []toolStatus
	for _, name := range selected {
		status := "running"
		if completedSet[name] {
			status = "completed"
		}
		if failedSet[name] {
			status = "failed"
		}
		tools = append(tools, toolStatus{
			Name:         name,
			Status:       status,
			FindingCount: findingCounts[name],
			HasOutput:    hasOutput[name],
		})
	}

	response.WriteJSON(w, http.StatusOK, response.ListResponse{
		Data: tools,
		Meta: response.ListMeta{Total: len(tools), Page: 1, PerPage: len(tools)},
	})
}

// parseDependents parses a JSON-encoded array of dependent strings.
func parseDependents(raw string) []string {
	if raw == "" {
		return nil
	}
	var deps []string
	if err := json.Unmarshal([]byte(raw), &deps); err != nil {
		return nil
	}
	return deps
}

// GetToolSummaries handles GET /api/scans/{id}/tool-summaries.
func GetToolSummaries(w http.ResponseWriter, r *http.Request) {
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}
	scanID := chi.URLParam(r, "id")
	summaries, err := h.Store.ListToolSummariesByScan(r.Context(), scanID)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to list tool summaries")
		return
	}
	if summaries == nil {
		summaries = []models.ToolSummary{}
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: summaries})
}

// GetScanRecommendations handles GET /api/scans/{id}/recommendations.
func GetScanRecommendations(w http.ResponseWriter, r *http.Request) {
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}
	scanID := chi.URLParam(r, "id")
	recs, err := h.Store.ListScanRecommendations(r.Context(), scanID)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to list recommendations")
		return
	}
	if recs == nil {
		recs = []models.ScanRecommendation{}
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: recs})
}
