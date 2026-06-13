package routes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	findingdiff "github.com/alphabravocompany/thewolf/internal/finding/diff"
	findingsuppression "github.com/alphabravocompany/thewolf/internal/finding/suppression"
	"github.com/alphabravocompany/thewolf/internal/models"
	promptpkg "github.com/alphabravocompany/thewolf/internal/prompt"
	"github.com/alphabravocompany/thewolf/internal/scan/coverage"
	"github.com/alphabravocompany/thewolf/internal/scan/detector"
	"github.com/alphabravocompany/thewolf/internal/scan/enricher"
	"github.com/alphabravocompany/thewolf/internal/scan/mapper"
	"github.com/alphabravocompany/thewolf/internal/scan/planner"
	"github.com/alphabravocompany/thewolf/internal/scan/report"
	"github.com/alphabravocompany/thewolf/internal/scan/runner"
	"github.com/alphabravocompany/thewolf/internal/scan/scorer"
	"github.com/alphabravocompany/thewolf/internal/scan/suppress"
	scannermanifest "github.com/alphabravocompany/thewolf/internal/scannertools/manifest"
	"github.com/alphabravocompany/thewolf/internal/scantarget"
	"github.com/alphabravocompany/thewolf/internal/secrets"
	"github.com/alphabravocompany/thewolf/internal/wolflog"
)

// SSEBroker is the package-level SSE broker for scan streaming.
// Set this before starting the server.
var SSEBroker *sse.Broker

var (
	activeScansMu  sync.Mutex
	activeScanCtxs = make(map[string]context.CancelFunc)
	activeAICtxs   = make(map[string]context.CancelFunc)
	// activeToolCtxs holds per-tool cancel funcs nested under each scan:
	// activeToolCtxs[scanID][toolName] = cancel. Set by the runner
	// callback OnToolCancelable, fired by CancelScanTool (the handler
	// for DELETE /api/scans/{id}/tools/{name}), cleared on tool done.
	activeToolCtxs = make(map[string]map[string]context.CancelFunc)
	// cancelledTools tracks which tools were explicitly cancelled by the
	// user so the OnToolDone bookkeeping can mark them with the right
	// error message instead of whatever transient ctx.Err string the
	// scanner exited with. Keyed by (scanID, toolName).
	cancelledTools = make(map[string]map[string]bool)
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
	// Default to AI enabled if the global setting is on and no explicit choice was made.
	if !req.AIEnabled {
		if globalAI, err := h.Store.GetSetting(r.Context(), "ai_enabled"); err == nil && globalAI == "true" {
			req.AIEnabled = true
		}
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
		SourceType:    repo.SourceType,
		RemoteNodeID:  repo.RemoteNodeID,
		SourcePath:    repo.SourcePath,
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

	go executeScan(h, scan.ID, claims.UserID, repo.ID, branch, req)

	response.WriteJSON(w, http.StatusCreated, response.SuccessResponse{Data: scan})
}

// executeScan runs the scan in a background goroutine.
func executeScan(h *Handler, scanID, userID, repoID, branch string, req createScanRequest) {
	log := wolflog.Component("scan")
	log.Info().Str("scan_id", scanID).Str("repo_id", repoID).Str("branch", branch).Msg("scan starting")

	ctx, cancel := context.WithCancel(context.Background())

	activeScansMu.Lock()
	activeScanCtxs[scanID] = cancel
	activeToolCtxs[scanID] = make(map[string]context.CancelFunc)
	cancelledTools[scanID] = make(map[string]bool)
	activeScansMu.Unlock()

	defer func() {
		activeScansMu.Lock()
		delete(activeScanCtxs, scanID)
		delete(activeToolCtxs, scanID)
		delete(cancelledTools, scanID)
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

	repo, err := h.Store.GetRepoByID(ctx, repoID)
	if err != nil {
		failPreparedScan(h, scan, topic, fmt.Errorf("load repo: %w", err))
		return
	}
	prepared, err := (scantarget.Resolver{Store: h.Store}).Prepare(ctx, repo, branch)
	if err != nil {
		failPreparedScan(h, scan, topic, err)
		return
	}
	repoPath := prepared.Path
	cleanupPrepared := prepared.Cleanup
	if cleanupPrepared == nil {
		cleanupPrepared = func() {}
	}
	cleanupAtEnd := true
	cleanupRelease := make(chan struct{})
	defer close(cleanupRelease)
	defer func() {
		if cleanupAtEnd {
			cleanupPrepared()
		}
	}()
	scan.SourceType = prepared.SourceType
	scan.RemoteNodeID = prepared.RemoteNodeID
	scan.SourcePath = prepared.SourcePath
	scan.CommitSHA = prepared.CommitSHA
	scan.DirtyState = prepared.DirtyState
	scan.PreparedWorkspace = prepared.PreparedWorkspace
	_ = h.Store.UpdateScan(ctx, scan)
	if repo.SourceType == models.SourceTypeSSH {
		repo.LastCommitSHA = prepared.CommitSHA
		repo.LastDirtyState = prepared.DirtyState
		_ = h.Store.UpdateRepo(ctx, repo)
	}

	// Refresh language + framework detection at scan start. The original
	// runDetection-on-repo-create call only fires once; deps drift, repos
	// grow new frameworks, and we want the repo detail page to show the
	// current truth. Cheap (one tree walk + a few file reads), runs
	// inline so the scan picks up the same tool selection the UI shows.
	var detectedLanguages []models.Language
	if detResult, derr := detector.Detect(repoPath); derr == nil {
		detectedLanguages = languagesFromModelCounts(detResult.Languages)
		langs := make(map[string]int, len(detResult.Languages))
		for l, n := range detResult.Languages {
			langs[string(l)] = n
		}
		langsJSON, _ := json.Marshal(langs)
		fwJSON, _ := json.Marshal(detResult.Frameworks)
		if uerr := h.Store.UpdateRepoDetection(ctx, scan.RepoID, string(langsJSON), string(fwJSON)); uerr != nil {
			log.Warn().Err(uerr).Str("repo_id", scan.RepoID).Msg("scan: failed to persist detection refresh")
		}
	} else {
		log.Warn().Err(derr).Str("scan_id", scanID).Msg("scan: language detection failed; runner will use fallback selection")
	}

	// Per-tool log files live under a project- and time-stamped directory so
	// a host scanning many repos stays inspectable on disk. The DB still
	// keys by scanID; the on-disk directory just embeds the repo basename,
	// UTC timestamp, and the first 8 chars of scanID for legibility.
	scanDirName := report.ScanDirName(repoPath, now.UTC(), scanID)
	var logDir string
	if artifacts.Global != nil {
		logDir = filepath.Join(artifacts.Global.Root(), scanDirName)
	} else {
		logDir = filepath.Join(os.TempDir(), "wolf-scans", scanDirName)
	}
	os.MkdirAll(logDir, 0o750) // #nosec G104 -- intentional: response/log write errors are not actionable here

	// Raw pre-parse tool output lives in <logDir>/raw/. Plugins that opt
	// into plugin.SaveRaw produce a <tool>.<ext> file here so the original
	// tool output is preserved alongside the parsed findings.
	rawDir := filepath.Join(logDir, "raw")
	os.MkdirAll(rawDir, 0o750) // #nosec G104 -- intentional: response/log write errors are not actionable here

	// Map to hold open log file writers per tool.
	var logMu sync.Mutex
	toolLogs := make(map[string]*os.File)

	// Track incremental tool completion for DB persistence so polling
	// clients (and page refreshes) see correct per-tool status mid-scan.
	var toolStateMu sync.Mutex
	toolsCompleted := make([]string, 0)
	toolsFailed := make([]string, 0)
	// toolsErrors maps toolName → error message. Persisted so the UI
	// can render "why" beside the failure indicator.
	toolsErrors := make(map[string]string)
	// toolStartTimes captures when each tool entered the running state
	// so OnToolDone can broadcast a real elapsed_ms (without changing
	// the runner's callback signature). Without this the live page
	// always shows "0s" because the SSE payload hardcoded elapsed.
	toolStartTimes := make(map[string]time.Time)
	cumulativeFindingCount := 0
	activeSuppressions, suppressionErr := h.Store.ListFindingSuppressions(context.Background(), scan.RepoID, false)
	if suppressionErr != nil {
		log.Warn().Str("scan_id", scanID).Err(suppressionErr).Msg("failed to load durable suppressions")
		activeSuppressions = nil
	}
	scanBranches := map[string]string{scanID: branch}

	// Read scan concurrency from settings (default: 8).
	scanConcurrency := 8
	if val, err := h.Store.GetSetting(context.Background(), "scan_concurrency"); err == nil && val != "" {
		if n, parseErr := strconv.Atoi(val); parseErr == nil && n > 0 {
			scanConcurrency = n
		}
	}
	heavyScannerConcurrency := 1
	if val, err := h.Store.GetSetting(context.Background(), "heavy_scanner_concurrency"); err == nil && val != "" {
		if n, parseErr := strconv.Atoi(val); parseErr == nil && n > 0 {
			heavyScannerConcurrency = n
		}
	}
	networkScannerConcurrency := 2
	if val, err := h.Store.GetSetting(context.Background(), "network_scanner_concurrency"); err == nil && val != "" {
		if n, parseErr := strconv.Atoi(val); parseErr == nil && n > 0 {
			networkScannerConcurrency = n
		}
	}
	log.Info().
		Str("scan_id", scanID).
		Int("concurrency", scanConcurrency).
		Int("heavy_concurrency", heavyScannerConcurrency).
		Int("network_concurrency", networkScannerConcurrency).
		Msg("scan concurrency configured")

	var scannerPlan *report.ScannerPlan
	cfg := runner.RunConfig{
		RepoPath:           repoPath,
		Branch:             branch,
		Registry:           h.Registry,
		Tools:              req.Tools,
		DisabledTools:      req.DisabledTools,
		Concurrency:        scanConcurrency,
		HeavyConcurrency:   heavyScannerConcurrency,
		NetworkConcurrency: networkScannerConcurrency,
		RawOutputDir:       rawDir,
		OnToolsSelected: func(toolNames []string) {
			log.Info().Str("scan_id", scanID).Int("count", len(toolNames)).Strs("tools", toolNames).Msg("tools selected")
			// Persist selected tools immediately so the live page can show all cards. // #nosec G104 -- intentional: response/log write errors are not actionable here
			selectedJSON, _ := json.Marshal(toolNames)
			if s, err := h.Store.GetScanByID(context.Background(), scanID); err == nil {
				s.ToolsSelected = string(selectedJSON)
				s.UpdatedAt = time.Now()
				h.Store.UpdateScan(context.Background(), s) // #nosec G104 -- intentional: HTTP write / log errors aren't actionable in this branch
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
		OnToolCancelable: func(toolName string, cancel context.CancelFunc) {
			// Register so DELETE /api/scans/{id}/tools/{name} can fire it.
			activeScansMu.Lock()
			if m, ok := activeToolCtxs[scanID]; ok {
				m[toolName] = cancel
			}
			activeScansMu.Unlock()
		},
		OnToolStart: func(toolName string) {
			log.Debug().Str("scan_id", scanID).Str("tool", toolName).Msg("tool starting")
			startedAt := time.Now().UTC()
			toolStateMu.Lock()
			toolStartTimes[toolName] = startedAt
			toolStateMu.Unlock()
			upsertScannerRunRecord(context.Background(), h, scannerRunRecordStart(scanID, toolName, scannerPlan, startedAt))
			if SSEBroker != nil {
				SSEBroker.Publish(topic, sse.Event{
					Type: "scan_progress",
					Data: fmt.Sprintf(`{"type":"scan_progress","scan_id":"%s","tool_name":"%s","status":"running","finding_count":0,"elapsed_ms":0,"progress_pct":0}`, scanID, toolName),
				})
			}
		},
		OnToolDone: func(toolName string, toolFindings []models.Finding, toolErr error) {
			findingCount := len(toolFindings)
			status := "completed"
			errMsg := ""
			// Was this tool explicitly cancelled by the user? Replace the
			// raw ctx.Err() ("context canceled") with a clear message and
			// clear the registry entry. The runner's defer-toolCancel
			// also fires, but we may get here first when the cancel
			// triggered the tool's pre-start check.
			activeScansMu.Lock()
			wasCancelled := cancelledTools[scanID] != nil && cancelledTools[scanID][toolName]
			if m, ok := activeToolCtxs[scanID]; ok {
				delete(m, toolName)
			}
			activeScansMu.Unlock()

			if toolErr != nil {
				status = "failed"
				if wasCancelled {
					errMsg = "cancelled by user"
				} else {
					errMsg = toolErr.Error()
				}
				log.Warn().Str("scan_id", scanID).Str("tool", toolName).Err(toolErr).Bool("user_cancelled", wasCancelled).Msg("tool failed")
			} else {
				log.Info().Str("scan_id", scanID).Str("tool", toolName).Int("findings", findingCount).Msg("tool completed")
			}

			// Persist findings immediately so they appear in the UI in real time.
			if findingCount > 0 {
				persistAt := time.Now()
				for i := range toolFindings {
					toolFindings[i].ID = uuid.New().String()
					toolFindings[i].ScanID = scanID
					toolFindings[i].RepoID = scan.RepoID
					applyScanSourceToFinding(&toolFindings[i], scan, branch)
					toolFindings[i].CreatedAt = persistAt
					toolFindings[i].UpdatedAt = persistAt
				}
				toolFindings, _ = findingsuppression.Apply(toolFindings, activeSuppressions, scanBranches, persistAt)
				if createErr := h.Store.CreateFindings(context.Background(), toolFindings); createErr != nil {
					log.Error().Str("scan_id", scanID).Str("tool", toolName).Err(createErr).Msg("failed to persist tool findings")
				}
			}

			// Update tool status and cumulative finding count atomically.
			toolStateMu.Lock()
			if toolErr != nil {
				toolsFailed = append(toolsFailed, toolName)
				// Trim the error to a UI-friendly size; full traces
				// remain in the per-tool .log artifact for deep dives.
				e := errMsg
				if e == "" {
					e = toolErr.Error()
				}
				if len(e) > 500 {
					e = e[:500] + "…"
				}
				toolsErrors[toolName] = e
			} else {
				toolsCompleted = append(toolsCompleted, toolName)
			}
			cumulativeFindingCount += findingCount
			currentTotal := cumulativeFindingCount
			completedJSON, _ := json.Marshal(toolsCompleted)
			failedJSON, _ := json.Marshal(toolsFailed)
			errorsJSON, _ := json.Marshal(toolsErrors)
			toolStateMu.Unlock()
			// #nosec G104 -- intentional: response/log write errors are not actionable here
			// Update scan record with incremental state.
			if s, err := h.Store.GetScanByID(context.Background(), scanID); err == nil {
				s.ToolsCompleted = string(completedJSON)
				s.ToolsFailed = string(failedJSON)
				s.ToolsErrors = string(errorsJSON)
				s.FindingCount = currentTotal
				s.UpdatedAt = time.Now()
				h.Store.UpdateScan(context.Background(), s)
			}

			// Compute real elapsed_ms so the live page can render a non-
			// zero duration on the tool card. Without this every tool
			// shows 0s forever; both OnToolStart and OnToolDone were
			// hardcoding elapsed_ms=0 in the SSE payload.
			finishedAt := time.Now().UTC()
			elapsedMs := int64(0)
			var recordStartedAt *time.Time
			toolStateMu.Lock()
			if startedAt, ok := toolStartTimes[toolName]; ok {
				elapsedMs = finishedAt.Sub(startedAt).Milliseconds()
				recordStartedAt = &startedAt
				delete(toolStartTimes, toolName)
			}
			toolStateMu.Unlock()
			recordStatus := status
			if wasCancelled {
				recordStatus = "cancelled"
			}
			upsertScannerRunRecord(context.Background(), h, scannerRunRecordDone(scanID, toolName, scannerPlan, recordStatus, findingCount, errMsg, elapsedMs, recordStartedAt, finishedAt))

			// Broadcast SSE with per-tool count and cumulative total.
			if SSEBroker != nil {
				escapedErr, _ := json.Marshal(errMsg)
				SSEBroker.Publish(topic, sse.Event{
					Type: "scan_progress",
					Data: fmt.Sprintf(`{"type":"scan_progress","scan_id":"%s","tool_name":"%s","status":"%s","finding_count":%d,"total_findings":%d,"elapsed_ms":%d,"progress_pct":100,"error":%s}`, scanID, toolName, status, findingCount, currentTotal, elapsedMs, string(escapedErr)),
				})
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
				// #nosec G304 -- path is scanRoot/<scanID>/<artifact>; scanRoot is validated and scanID comes from chi URL param
				f, _ = os.Create(filepath.Join(logDir, toolName+".log"))
				toolLogs[toolName] = f
			}
			if f != nil {
				fmt.Fprintln(f, line)
			}
			logMu.Unlock()
		},
	}
	if len(req.Tools) == 0 {
		cfg.Languages = detectedLanguages
	}

	if toolManifest, merr := scannermanifest.LoadDefault(); merr == nil {
		cfg.ToolResources = runner.ResourceSpecsFromManifest(toolManifest)
		scannerPlan = planner.ToReportPlan(planner.Build(planner.Config{
			Registry:      h.Registry,
			Manifest:      toolManifest,
			Languages:     detectedLanguages,
			Tools:         req.Tools,
			DisabledTools: req.DisabledTools,
		}))
	} else {
		log.Warn().Err(merr).Str("scan_id", scanID).Msg("scanner tool manifest unavailable; manifest scanner plan omitted")
	}

	result, runErr := runner.Run(ctx, cfg)
	if result != nil {
		for _, toolName := range result.ToolsSkipped {
			upsertScannerRunRecord(context.Background(), h, scannerRunRecordSkipped(scanID, toolName, scannerPlan, "tool unavailable"))
		}
	}

	// Update scan with results.
	completedAt := time.Now()
	scan, err = h.Store.GetScanByID(context.Background(), scanID)
	if err != nil {
		log.Error().Str("scan_id", scanID).Err(err).Msg("failed to reload scan after run")
		return
	}

	// Cancel is "sticky": if the user (or orphan-recovery) marked the scan
	// cancelled while runner.Run was still finishing its tail, don't
	// flip it back to completed/failed when we land here. Findings
	// already persisted during the run are preserved either way.
	if scan.Status == models.ScanStatusCancelled {
		log.Info().Str("scan_id", scanID).Msg("scan run finished but was already cancelled; preserving status")
	} else if runErr != nil {
		scan.Status = models.ScanStatusFailed
		log.Error().Str("scan_id", scanID).Err(runErr).Msg("scan run failed")
	} else {
		scan.Status = models.ScanStatusCompleted
		log.Info().Str("scan_id", scanID).Str("status", string(scan.Status)).Msg("scan run finished")
	}
	scan.CompletedAt = &completedAt
	scan.UpdatedAt = completedAt

	if result != nil {
		applyScanSourceToFindings(result.Findings, scan, branch)
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
		enrichCleanup := cleanupPrepared
		cleanupAtEnd = false
		go func(findings []models.Finding, repoID, rPath, br string) {
			defer func() {
				<-cleanupRelease
				enrichCleanup()
			}()
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

		// Findings were persisted incrementally in OnToolDone (raw, per-
		// tool). The headline finding_count must reconcile with what the
		// UI actually shows on the findings table, which means counting
		// *visible* findings (post-suppression). Source from the DB so
		// the number can never drift away from what the user sees.
		if dbFindings, ferr := h.Store.ListFindingsByScan(context.Background(), scanID); ferr == nil {
			dbFindings, _ = suppress.Apply(dbFindings, suppress.DefaultRules())
			suppress.ApplyGitignore(dbFindings, repoPath)
			visible := 0
			for _, f := range dbFindings {
				if !f.Suppressed {
					visible++ // #nosec G104 -- intentional: response/log write errors are not actionable here
				}
			}
			scan.FindingCount = visible
		} else {
			// Fall back to the runner's post-dedup count if the DB read
			// fails — better than reporting zero.
			scan.FindingCount = len(result.Findings)
		}

		// Static test coverage analysis.
		covReport, covErr := coverage.Analyze(repoPath)
		if covErr == nil && covReport.TotalSourceFiles > 0 { // #nosec G104 -- intentional: response/log write errors are not actionable here
			covJSON, _ := json.Marshal(covReport)
			scan.CoverageSummary = string(covJSON)
		}
	}

	// Close and persist tool log artifacts (from OnToolOutput callback).
	for _, f := range toolLogs {
		f.Close() // #nosec G104 -- intentional: HTTP write / log errors aren't actionable in this branch
		info, err := os.Stat(f.Name())
		if err == nil && info.Size() > 0 {
			recordScanArtifact(context.Background(), h, scanID, models.ArtifactLog, f.Name())
		}
	}

	// Save per-tool findings as JSON artifacts for raw results viewing.
	if result != nil && len(result.Findings) > 0 {
		toolFindings := make(map[string][]models.Finding)
		for _, f := range result.Findings {
			toolFindings[f.ToolName] = append(toolFindings[f.ToolName], f) // #nosec G104 -- intentional: response/log write errors are not actionable here
		}
		for toolName, findings := range toolFindings {
			data, err := json.MarshalIndent(findings, "", "  ")
			if err != nil {
				continue
			}
			fpath := filepath.Join(logDir, toolName+".json")
			if err := os.WriteFile(fpath, data, 0o600); err != nil {
				continue
			}
			recordScanArtifact(context.Background(), h, scanID, models.ArtifactJSON, fpath)
		}
	}

	// Materialize the deterministic artifact bundle (findings.json, RAW.md,
	// combined.sarif, manifest.json) so consumers can read scan output from
	// disk without going through the API. Best-effort: failures are logged
	// but don't fail the scan.
	if result != nil {
		rcfg := report.ReportConfig{
			ScanID:      scanID,
			RepoName:    scan.RepoID,
			Branch:      branch,
			Findings:    result.Findings,
			ToolsRun:    result.ToolsRun,
			ToolsFailed: result.ToolsFailed,
			Duration:    result.Duration,
		}
		mfst := report.Manifest{
			ScanID:      scanID,
			Source:      scanSourceProvenance(scan),
			RepoName:    filepath.Base(repoPath),
			RepoPath:    repoPath,
			Branch:      branch,
			StartedAt:   now,
			FinishedAt:  completedAt,
			ScannersRun: result.ToolsRun,
			ScannerPlan: scannerPlan,
			Failed: func() map[string]string {
				if len(result.ToolsFailed) == 0 {
					return nil
				}
				m := make(map[string]string, len(result.ToolsFailed))
				for k, v := range result.ToolsFailed {
					m[k] = v.Error()
				}
				return m
			}(),
			Counts: report.CountFindings(0, result.Findings),
		}
		if w, werr := report.WriteAll(logDir, rcfg, mfst); werr != nil {
			log.Warn().Str("scan_id", scanID).Err(werr).Msg("artifact bundle write failed")
		} else {
			recordScanArtifact(context.Background(), h, scanID, models.ArtifactJSON, w.FindingsJSON)
			recordScanArtifact(context.Background(), h, scanID, models.ArtifactMarkdown, w.RawMarkdown)
			recordScanArtifact(context.Background(), h, scanID, models.ArtifactSARIF, w.CombinedSARIF)
			recordScanArtifact(context.Background(), h, scanID, models.ArtifactManifest, w.Manifest)
			recordScanArtifact(context.Background(), h, scanID, models.ArtifactMarkdown, w.FixHigh)
			recordScanArtifact(context.Background(), h, scanID, models.ArtifactMarkdown, w.FixAll)
			log.Info().Str("scan_id", scanID).
				Str("findings_json", w.FindingsJSON).
				Str("manifest", w.Manifest).
				Msg("artifact bundle written")
		}
	}

	// Mark scan complete NOW — tools are done. AI assessment runs in background.
	_ = h.Store.UpdateScan(context.Background(), scan)
	if gateResult, eval, policy, findingCount, gateErr := evaluateAndPersistGateContext(context.Background(), h, scanID, userID); gateErr != nil {
		log.Warn().Str("scan_id", scanID).Err(gateErr).Msg("quality gate evaluation failed")
	} else {
		_ = writeGateResultArtifact(context.Background(), h, scan, gateResult, eval, policy, findingCount, logDir)
		log.Info().Str("scan_id", scanID).Str("gate_status", eval.Status).Msg("quality gate evaluated")
	}

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
		Msg("scan complete — all tools finished")

	// AI Assessment phase — runs asynchronously after scan is already marked complete.
	if result != nil && req.AIEnabled && len(result.Findings) > 0 {
		aiProvider := resolveAIProvider(h, req.AIEngine, req.AIModel, scan.UserID)
		wolflog.Info().Str("engine", req.AIEngine).Str("provider", aiProvider.Name()).Int("findings", len(result.Findings)).Msg("AI assessment: resolved provider")
		if aiProvider.Name() != "noop" {
			aiCtx, aiCancel := context.WithCancel(context.Background())
			activeScansMu.Lock()
			activeAICtxs[scan.ID] = aiCancel
			activeScansMu.Unlock()
			go func() {
				defer func() {
					activeScansMu.Lock()
					delete(activeAICtxs, scan.ID)
					activeScansMu.Unlock()
					aiCancel()
				}()
				runAIAssessment(aiCtx, h, aiProvider, scan, result.Findings, topic)
			}()
		} else {
			wolflog.Warn().Str("engine", req.AIEngine).Msg("AI assessment: skipped (noop provider)")
		}
	}
}

func failPreparedScan(h *Handler, scan *models.Scan, topic string, err error) {
	now := time.Now().UTC()
	scan.Status = models.ScanStatusFailed
	scan.CompletedAt = &now
	scan.UpdatedAt = now
	errMsg := err.Error()
	if len(errMsg) > 500 {
		errMsg = errMsg[:500] + "…"
	}
	errorsJSON, _ := json.Marshal(map[string]string{"prepare": errMsg})
	scan.ToolsErrors = string(errorsJSON)
	_ = h.Store.UpdateScan(context.Background(), scan)
	if SSEBroker != nil {
		escapedErr, _ := json.Marshal(errMsg)
		SSEBroker.Publish(topic, sse.Event{
			Type: "scan_complete",
			Data: fmt.Sprintf(`{"type":"scan_complete","scan_id":"%s","status":"failed","finding_count":0,"error":%s}`, scan.ID, string(escapedErr)),
		})
	}
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
			"id":                 scan.ID,
			"user_id":            scan.UserID,
			"repo_id":            scan.RepoID,
			"collection_id":      scan.CollectionID,
			"loop_id":            scan.LoopID,
			"iteration":          scan.Iteration,
			"branch":             scan.Branch,
			"source_type":        scan.SourceType,
			"remote_node_id":     scan.RemoteNodeID,
			"source_path":        scan.SourcePath,
			"commit_sha":         scan.CommitSHA,
			"dirty_state":        scan.DirtyState,
			"prepared_workspace": scan.PreparedWorkspace,
			"status":             scan.Status,
			"tools_selected":     scan.ToolsSelected,
			"tools_completed":    scan.ToolsCompleted,
			"tools_failed":       scan.ToolsFailed,
			"tools_errors":       scan.ToolsErrors,
			"finding_count":      scan.FindingCount,
			"coverage_summary":   scan.CoverageSummary,
			"ai_summary":         scan.AISummary,
			"started_at":         scan.StartedAt,
			"completed_at":       scan.CompletedAt,
			"created_at":         scan.CreatedAt,
			"updated_at":         scan.UpdatedAt,
			"repo":               scan.Repo,
			"artifacts":          artifacts,
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
	scan, err := h.Store.GetScanByID(r.Context(), scanID)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", fmt.Sprintf("scan %s not found", scanID))
		return
	}
	if !ensureScanOwner(w, scan, claims) {
		return
	}

	findings, err := h.Store.ListFindingsByScan(r.Context(), scanID)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to list findings")
		return
	}

	// Apply path-suppression rules (testdata/, vendor/, node_modules/,
	// generated, lockfiles). The Phase-3 suppress package marks each
	// finding's Suppressed flag in-place; we then drop suppressed rows
	// unless the client asked for them via ?include_suppressed=true.
	// This is intentionally a query-time filter (rather than persisting
	// the flag at scan time) so a default-rules change applies to all
	// historical scans without a backfill.
	findings, _ = suppress.Apply(findings, suppress.DefaultRules())
	// Layer the repo's gitignore on top — files the user has explicitly
	// chosen to exclude from version control are noise by definition.
	applyGitignoreByRepoID(r.Context(), h, scan.RepoID, findings)
	includeSuppressed := r.URL.Query().Get("include_suppressed") == "true"
	suppressedCount := 0
	if !includeSuppressed {
		kept := findings[:0]
		for _, f := range findings {
			if f.Suppressed {
				suppressedCount++
				continue
			}
			kept = append(kept, f)
		}
		findings = kept
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
		Meta: response.ListMeta{
			Total:      total,
			Page:       page,
			PerPage:    perPage,
			Suppressed: suppressedCount,
		},
	})
}

// GetScanFindingStats handles GET /api/scans/:id/findings/stats — severity and tool counts.
// Returns aggregate counts without transferring all findings.
func GetScanFindingStats(w http.ResponseWriter, r *http.Request) {
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
	if !ensureScanOwner(w, scan, claims) {
		return
	}
	findings, err := h.Store.ListFindingsByScan(r.Context(), scanID)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", fmt.Sprintf("scan %s not found", scanID))
		return
	}

	bySeverity := map[string]int{"critical": 0, "high": 0, "medium": 0, "low": 0, "info": 0}
	byTool := make(map[string]int)
	for _, f := range findings {
		bySeverity[string(f.Severity)]++
		byTool[f.ToolName]++
	}

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{
		Data: map[string]interface{}{
			"total":       len(findings),
			"by_severity": bySeverity,
			"by_tool":     byTool,
		},
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
		response.WriteError(w, http.StatusNotFound, "not_found", fmt.Sprintf("scan %s not found", scanID)) // #nosec G104 -- intentional: response/log write errors are not actionable here
		return
	}
	if !ensureScanOwner(w, scan, claims) {
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
	w.Write([]byte(md)) // nosemgrep: go.lang.security.audit.xss.no-direct-write-to-responsewriter.no-direct-write-to-responsewriter // #nosec G104 -- intentional: HTTP write / log errors aren't actionable in this branch
}

// GetScanManifest handles GET /api/scans/:id/manifest.
func GetScanManifest(w http.ResponseWriter, r *http.Request) {
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
	if scan.UserID != "" && scan.UserID != claims.UserID {
		response.WriteError(w, http.StatusForbidden, "forbidden", "scan does not belong to current user")
		return
	}

	if artifact, ok := findScanArtifactByType(r.Context(), h, scanID, models.ArtifactManifest); ok {
		content, err := os.ReadFile(artifact.FilePath)
		if err == nil {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="scan-%s-manifest.json"`, scanID))
			w.WriteHeader(http.StatusOK)
			w.Write(content) // nosemgrep: go.lang.security.audit.xss.no-direct-write-to-responsewriter.no-direct-write-to-responsewriter // #nosec G104 -- intentional: HTTP write / log errors aren't actionable in this branch
			return
		}
	}

	mfst, err := buildFallbackManifest(h, r, scan)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to build manifest")
		return
	}
	data, err := report.MarshalManifest(mfst)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to marshal manifest")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="scan-%s-manifest.json"`, scanID))
	w.WriteHeader(http.StatusOK)
	w.Write(data) // nosemgrep: go.lang.security.audit.xss.no-direct-write-to-responsewriter.no-direct-write-to-responsewriter // #nosec G104 -- intentional: HTTP write / log errors aren't actionable in this branch
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
		response.WriteError(w, http.StatusNotFound, "not_found", fmt.Sprintf("scan %s not found", scanID)) // #nosec G104 -- intentional: response/log write errors are not actionable here
		return
	}
	if !ensureScanOwner(w, scan, claims) {
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
	w.Write(sarif) // nosemgrep: go.lang.security.audit.xss.no-direct-write-to-responsewriter.no-direct-write-to-responsewriter // #nosec G104 -- intentional: HTTP write / log errors aren't actionable in this branch
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

	// Check if there's an active AI assessment for this scan.
	activeScansMu.Lock()
	_, hasActiveScan := activeScanCtxs[id]
	_, hasActiveAI := activeAICtxs[id]
	activeScansMu.Unlock()

	if scan.Status != models.ScanStatusRunning && scan.Status != models.ScanStatusPending && !hasActiveAI {
		response.WriteError(w, http.StatusConflict, "conflict", "scan is not running or pending")
		return
	}

	now := time.Now()
	if scan.Status == models.ScanStatusRunning || scan.Status == models.ScanStatusPending {
		scan.Status = models.ScanStatusCancelled
		scan.CompletedAt = &now
	}
	scan.UpdatedAt = now

	if err := h.Store.UpdateScan(r.Context(), scan); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to cancel scan")
		return
	}

	activeScansMu.Lock()
	if hasActiveScan {
		if cancelFn, ok := activeScanCtxs[id]; ok {
			cancelFn()
			delete(activeScanCtxs, id)
		}
	}
	if hasActiveAI {
		if cancelFn, ok := activeAICtxs[id]; ok {
			cancelFn()
			delete(activeAICtxs, id)
		}
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

// CancelScanTool handles DELETE /api/scans/:id/tools/:toolName — cancel a
// single tool that's currently running OR sitting queued behind a slot
// limit. The rest of the scan keeps going. Findings already persisted
// from completed tools are preserved.
func CancelScanTool(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing user context")
		return
	}
	scanID := chi.URLParam(r, "id")
	toolName := chi.URLParam(r, "toolName")
	if scanID == "" || toolName == "" {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "scanId and toolName required")
		return
	}

	activeScansMu.Lock()
	toolCtxs, scanRegistered := activeToolCtxs[scanID]
	if !scanRegistered {
		activeScansMu.Unlock()
		response.WriteError(w, http.StatusConflict, "conflict", "scan is not currently running")
		return
	}
	cancel, ok := toolCtxs[toolName]
	if !ok {
		activeScansMu.Unlock()
		response.WriteError(w, http.StatusNotFound, "not_found", fmt.Sprintf("tool %q is not active in scan %s (already finished or never queued)", toolName, scanID))
		return
	}
	// Mark intent so OnToolDone replaces the raw "context canceled"
	// error string with "cancelled by user" for the UI.
	if cancelledTools[scanID] == nil {
		cancelledTools[scanID] = make(map[string]bool)
	}
	cancelledTools[scanID][toolName] = true
	activeScansMu.Unlock()

	cancel()

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{
		Data: map[string]string{
			"scan_id":   scanID,
			"tool_name": toolName,
			"status":    "cancelled",
		},
	})
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

type compareScanRequest struct {
	BaselineScanID string `json:"baseline_scan_id"`
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

	result, scan2, err := compareScanPair(r.Context(), h, claims.UserID, id1, id2)
	if err != nil {
		writeCompareError(w, err)
		return
	}
	_, _ = writeJSONScanArtifact(r.Context(), h, scan2.ID, artifactDirForScan(r.Context(), h, scan2), "diff.json", result)
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: result})
}

// CompareScanToBaseline handles POST /api/scans/{id}/compare.
func CompareScanToBaseline(w http.ResponseWriter, r *http.Request) {
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
	var req compareScanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if req.BaselineScanID == "" {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "baseline_scan_id is required")
		return
	}
	currentID := chi.URLParam(r, "id")
	result, scan, err := compareScanPair(r.Context(), h, claims.UserID, req.BaselineScanID, currentID)
	if err != nil {
		writeCompareError(w, err)
		return
	}
	_, _ = writeJSONScanArtifact(r.Context(), h, scan.ID, artifactDirForScan(r.Context(), h, scan), "diff.json", result)
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: result})
}

// GetScanDiff handles GET /api/scans/{id}/diff?baseline_scan_id=...
func GetScanDiff(w http.ResponseWriter, r *http.Request) {
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
	baselineID := r.URL.Query().Get("baseline_scan_id")
	if baselineID == "" {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "baseline_scan_id is required")
		return
	}
	currentID := chi.URLParam(r, "id")
	result, scan, err := compareScanPair(r.Context(), h, claims.UserID, baselineID, currentID)
	if err != nil {
		writeCompareError(w, err)
		return
	}
	_, _ = writeJSONScanArtifact(r.Context(), h, scan.ID, artifactDirForScan(r.Context(), h, scan), "diff.json", result)
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: result})
}

func compareScanPair(ctx context.Context, h *Handler, userID, baselineScanID, currentScanID string) (ComparisonResult, *models.Scan, error) {
	scan1, err := h.Store.GetScanByID(ctx, baselineScanID)
	if err != nil {
		return ComparisonResult{}, nil, fmt.Errorf("baseline scan %s not found", baselineScanID)
	}
	scan2, err := h.Store.GetScanByID(ctx, currentScanID)
	if err != nil {
		return ComparisonResult{}, nil, fmt.Errorf("current scan %s not found", currentScanID)
	}
	if scan1.UserID != userID || scan2.UserID != userID {
		return ComparisonResult{}, nil, errForbidden()
	}
	if err := validateScanSourceCompatibility(scan1, scan2); err != nil {
		return ComparisonResult{}, nil, err
	}
	findings1, err := h.Store.ListFindingsByScan(ctx, baselineScanID)
	if err != nil {
		return ComparisonResult{}, nil, fmt.Errorf("failed to list findings for baseline scan: %w", err)
	}
	findings2, err := h.Store.ListFindingsByScan(ctx, currentScanID)
	if err != nil {
		return ComparisonResult{}, nil, fmt.Errorf("failed to list findings for current scan: %w", err)
	}

	diffResult := findingdiff.Compare(findings1, findings2)
	newFindings := diffResult.New
	fixedFindings := diffResult.Fixed
	var changedFindings []ChangedFinding
	unchangedCount := len(diffResult.Existing)

	baselineByStable := make(map[string]models.Finding, len(findings1))
	for _, f := range findings1 {
		key := f.StableFingerprint
		if key == "" {
			key = f.Fingerprint
		}
		if key != "" {
			baselineByStable[key] = f
		}
	}
	for _, f2 := range diffResult.Existing {
		key := f2.StableFingerprint
		if key == "" {
			key = f2.Fingerprint
		}
		if f1, exists := baselineByStable[key]; exists && (f1.Severity != f2.Severity || f1.Status != f2.Status) {
			changedFindings = append(changedFindings, ChangedFinding{Before: f1, After: f2})
			unchangedCount--
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

	if summaryJSON, err := json.Marshal(result.Summary); err == nil {
		_ = h.Store.UpsertScanComparison(ctx, &models.ScanComparison{
			ID:             uuid.New().String(),
			RepoID:         scan2.RepoID,
			BaselineScanID: scan1.ID,
			CurrentScanID:  scan2.ID,
			SummaryJSON:    string(summaryJSON),
		})
	}

	return result, scan2, nil
}

func writeCompareError(w http.ResponseWriter, err error) {
	var forbidden forbiddenError
	if errors.As(err, &forbidden) {
		response.WriteError(w, http.StatusForbidden, "forbidden", "scan does not belong to current user")
		return
	}
	var validation validationError
	if errors.As(err, &validation) {
		response.WriteError(w, http.StatusBadRequest, "validation_error", validation.Error())
		return
	}
	if strings.Contains(err.Error(), "not found") {
		response.WriteError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	response.WriteError(w, http.StatusInternalServerError, "server_error", err.Error())
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
			CostUSD:        entry.CostUSD,
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
		if ctx.Err() != nil {
			wolflog.Info().Str("scan_id", scan.ID).Msg("AI assessment: cancelled")
			if SSEBroker != nil {
				SSEBroker.Publish(topic, sse.Event{
					Type: "ai_assessment",
					Data: fmt.Sprintf(`{"type":"ai_assessment","scan_id":"%s","phase":"cancelled","progress_pct":0}`, scan.ID),
				})
			}
			return
		}
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

	// Check cancellation before executive summary.
	if ctx.Err() != nil {
		wolflog.Info().Str("scan_id", scan.ID).Msg("AI assessment: cancelled before summary")
		if SSEBroker != nil {
			SSEBroker.Publish(topic, sse.Event{
				Type: "ai_assessment",
				Data: fmt.Sprintf(`{"type":"ai_assessment","scan_id":"%s","phase":"cancelled","progress_pct":0}`, scan.ID),
			})
		}
		return
	}

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

	scan, err := h.Store.GetScanByID(r.Context(), scanID)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", "scan not found")
		return
	}
	if !ensureScanOwner(w, scan, claims) {
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
	if err != nil { // #nosec G104 -- intentional: response/log write errors are not actionable here
		response.WriteError(w, http.StatusNotFound, "not_found", "artifact file not found on disk")
		return
	}

	ct := "application/octet-stream"
	switch target.ArtifactType {
	case models.ArtifactJSON, models.ArtifactSARIF, models.ArtifactManifest:
		ct = "application/json"
	case models.ArtifactMarkdown:
		ct = "text/markdown; charset=utf-8"
	case models.ArtifactLog:
		ct = "text/plain; charset=utf-8"
	}

	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filepath.Base(target.FilePath)))
	w.WriteHeader(http.StatusOK)
	w.Write(content) // nosemgrep: go.lang.security.audit.xss.no-direct-write-to-responsewriter.no-direct-write-to-responsewriter // #nosec G104 -- intentional: HTTP write / log errors aren't actionable in this branch
}

// --- helpers ---

func recordScanArtifact(ctx context.Context, h *Handler, scanID string, artifactType models.ArtifactType, path string) {
	if h == nil || h.Store == nil || path == "" {
		return
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return
	}
	checksum, _ := fileSHA256(path)
	now := time.Now()
	_ = h.Store.CreateScanArtifact(ctx, &models.ScanArtifact{
		ID:             uuid.New().String(),
		ScanID:         scanID,
		ArtifactType:   artifactType,
		FilePath:       path,
		FileSize:       info.Size(),
		ChecksumSHA256: checksum,
		RedactionLevel: artifactRedactionLevel(artifactType, path),
		CreatedAt:      now,
		UpdatedAt:      now,
	})
}

func writeJSONScanArtifact(ctx context.Context, h *Handler, scanID, dir, filename string, payload any) (string, error) {
	if dir == "" {
		return "", fmt.Errorf("artifact directory is empty")
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", err
	}
	recordScanArtifact(ctx, h, scanID, models.ArtifactJSON, path)
	return path, nil
}

func artifactDirForScan(ctx context.Context, h *Handler, scan *models.Scan) string {
	if h == nil || h.Store == nil || scan == nil {
		return ""
	}
	if existing, err := h.Store.ListScanArtifacts(ctx, scan.ID); err == nil {
		for _, artifact := range existing {
			if artifact.FilePath != "" {
				return filepath.Dir(artifact.FilePath)
			}
		}
	}
	ts := scan.CreatedAt
	if scan.StartedAt != nil {
		ts = *scan.StartedAt
	}
	repoPath := scan.SourcePath
	if repoPath == "" && scan.RepoID != "" {
		if repo, err := h.Store.GetRepoByID(ctx, scan.RepoID); err == nil && repo != nil {
			repoPath = repo.SourcePath
		}
	}
	if repoPath == "" {
		repoPath = scan.ID
	}
	if artifacts.Global != nil {
		return filepath.Join(artifacts.Global.Root(), report.ScanDirName(repoPath, ts.UTC(), scan.ID))
	}
	return filepath.Join(os.TempDir(), "wolf-scans", report.ScanDirName(repoPath, ts.UTC(), scan.ID))
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close() // #nosec G104 -- checksum best effort
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func artifactRedactionLevel(artifactType models.ArtifactType, path string) string {
	switch artifactType {
	case models.ArtifactLog:
		return "raw_evidence"
	case models.ArtifactJSON:
		switch filepath.Base(path) {
		case "findings.json", "diff.json", "gate-result.json":
			return "internal_report"
		default:
			return "raw_evidence"
		}
	case models.ArtifactSARIF, models.ArtifactMarkdown, models.ArtifactManifest, models.ArtifactCoverage:
		return "internal_report"
	default:
		return "internal_report"
	}
}

func scannerRunRecordStart(scanID, toolName string, plan *report.ScannerPlan, startedAt time.Time) *models.ScannerRunRecord {
	meta := scannerRunPlanDecision(plan, toolName)
	return &models.ScannerRunRecord{
		ID:           uuid.New().String(),
		ScanID:       scanID,
		ToolName:     toolName,
		Status:       "running",
		Category:     meta.Category,
		Image:        meta.Image,
		ImageDigest:  imageDigestFromRef(meta.Image),
		CommandJSON:  "{}",
		ParserStatus: "pending",
		StartedAt:    &startedAt,
	}
}

func scannerRunRecordDone(scanID, toolName string, plan *report.ScannerPlan, status string, findingCount int, errMsg string, durationMS int64, startedAt *time.Time, finishedAt time.Time) *models.ScannerRunRecord {
	meta := scannerRunPlanDecision(plan, toolName)
	exitCode := 0
	parserStatus := "parsed"
	if status == "failed" || status == "cancelled" {
		exitCode = 1
		parserStatus = "failed"
	}
	return &models.ScannerRunRecord{
		ID:            uuid.New().String(),
		ScanID:        scanID,
		ToolName:      toolName,
		Status:        status,
		Category:      meta.Category,
		Image:         meta.Image,
		ImageDigest:   imageDigestFromRef(meta.Image),
		CommandJSON:   "{}",
		ExitCode:      exitCode,
		DurationMS:    durationMS,
		FindingCount:  findingCount,
		ErrorMessage:  errMsg,
		ParserStatus:  parserStatus,
		ParserMessage: errMsg,
		StartedAt:     startedAt,
		FinishedAt:    &finishedAt,
	}
}

func scannerRunRecordSkipped(scanID, toolName string, plan *report.ScannerPlan, reason string) *models.ScannerRunRecord {
	meta := scannerRunPlanDecision(plan, toolName)
	return &models.ScannerRunRecord{
		ID:            uuid.New().String(),
		ScanID:        scanID,
		ToolName:      toolName,
		Status:        "skipped",
		Category:      meta.Category,
		Image:         meta.Image,
		ImageDigest:   imageDigestFromRef(meta.Image),
		CommandJSON:   "{}",
		ErrorMessage:  reason,
		ParserStatus:  "not_run",
		ParserMessage: reason,
	}
}

func upsertScannerRunRecord(ctx context.Context, h *Handler, record *models.ScannerRunRecord) {
	if h == nil || h.Store == nil || record == nil {
		return
	}
	_ = h.Store.UpsertScannerRunRecord(ctx, record)
}

func scannerRunPlanDecision(plan *report.ScannerPlan, toolName string) report.ScannerPlanDecision {
	if plan == nil {
		return report.ScannerPlanDecision{}
	}
	for _, decision := range plan.Run {
		if decision.Tool == toolName {
			return decision
		}
	}
	for _, decision := range plan.Skip {
		if decision.Tool == toolName {
			return decision
		}
	}
	return report.ScannerPlanDecision{}
}

func imageDigestFromRef(ref string) string {
	if _, digest, ok := strings.Cut(ref, "@"); ok {
		return digest
	}
	return ""
}

func findScanArtifactByType(ctx context.Context, h *Handler, scanID string, artifactType models.ArtifactType) (models.ScanArtifact, bool) {
	if h == nil || h.Store == nil {
		return models.ScanArtifact{}, false
	}
	artifacts, err := h.Store.ListScanArtifacts(ctx, scanID)
	if err != nil {
		return models.ScanArtifact{}, false
	}
	for _, artifact := range artifacts {
		if artifact.ArtifactType == artifactType {
			return artifact, true
		}
	}
	return models.ScanArtifact{}, false
}

func buildFallbackManifest(h *Handler, r *http.Request, scan *models.Scan) (report.Manifest, error) {
	cfg, err := buildReportConfig(h, r, scan)
	if err != nil {
		return report.Manifest{}, err
	}
	startedAt := scan.CreatedAt
	if scan.StartedAt != nil {
		startedAt = *scan.StartedAt
	}
	finishedAt := time.Now()
	if scan.CompletedAt != nil {
		finishedAt = *scan.CompletedAt
	}
	langs := make([]string, 0, len(cfg.Languages))
	for lang := range cfg.Languages {
		langs = append(langs, lang)
	}
	sort.Strings(langs)
	failed := make(map[string]string, len(cfg.ToolsFailed))
	for tool, err := range cfg.ToolsFailed {
		failed[tool] = err.Error()
	}
	if len(failed) == 0 {
		failed = nil
	}
	return report.Manifest{
		ScanID:     scan.ID,
		Source:     scanSourceProvenance(scan),
		RepoName:   cfg.RepoName,
		RepoPath:   scan.SourcePath,
		RepoCommit: scan.CommitSHA,
		Branch:     scan.Branch,
		StartedAt:  startedAt,
		FinishedAt: finishedAt,
		Detection: report.DetectionSummary{
			Languages:  langs,
			Frameworks: cfg.Frameworks,
		},
		ScannersRun: cfg.ToolsRun,
		Failed:      failed,
		Counts:      report.CountFindings(0, cfg.Findings),
	}, nil
}

type validationError struct {
	msg string
}

func (e validationError) Error() string { return e.msg }

func validateScanSourceCompatibility(a, b *models.Scan) error {
	if a.RepoID != b.RepoID {
		return validationError{msg: "cannot compare scans from different repositories"}
	}
	aKind := scanSourceKind(a)
	bKind := scanSourceKind(b)
	if aKind != "" && bKind != "" && aKind != bKind {
		return validationError{msg: fmt.Sprintf("cannot compare scans from incompatible source kinds: %s vs %s", aKind, bKind)}
	}
	if aKind == "ssh_path" || bKind == "ssh_path" {
		aNode := scanRemoteNodeID(a)
		bNode := scanRemoteNodeID(b)
		if aNode != "" && bNode != "" && aNode != bNode {
			return validationError{msg: "cannot compare SSH scans from different remote nodes"}
		}
	}
	return nil
}

func scanSourceProvenance(scan *models.Scan) *report.SourceProvenance {
	if scan == nil {
		return nil
	}
	source := &report.SourceProvenance{
		Kind:       scanSourceKind(scan),
		RepoID:     scan.RepoID,
		RepoPath:   scan.SourcePath,
		Branch:     scan.Branch,
		CommitSHA:  scan.CommitSHA,
		DirtyState: scan.DirtyState,
	}
	source.RemoteNodeID = scanRemoteNodeID(scan)
	switch source.Kind {
	case "ssh_path":
		source.SnapshotStrategy = "remote_archive"
	case "local_path":
		source.SnapshotStrategy = "working_tree"
	case "github", "git_clone":
		source.SnapshotStrategy = "git_checkout"
	}
	return source
}

func applyScanSourceToFindings(findings []models.Finding, scan *models.Scan, branch string) {
	for i := range findings {
		applyScanSourceToFinding(&findings[i], scan, branch)
	}
}

func applyScanSourceToFinding(f *models.Finding, scan *models.Scan, branch string) {
	if f == nil || scan == nil {
		return
	}
	if f.SourceKind == "" {
		f.SourceKind = scanSourceKind(scan)
	}
	if f.SourceRef == "" {
		f.SourceRef = scanSourceRef(scan, branch)
	}
}

func scanSourceKind(scan *models.Scan) string {
	if scan == nil {
		return ""
	}
	switch scan.SourceType {
	case models.SourceTypeLocal, "":
		return "local_path"
	case models.SourceTypeSSH:
		return "ssh_path"
	case models.SourceTypeGitHub:
		return "github"
	case models.SourceTypeGit, models.SourceTypeGitLab:
		return "git_clone"
	default:
		return string(scan.SourceType)
	}
}

func scanSourceRef(scan *models.Scan, branch string) string {
	if scan == nil {
		return branch
	}
	if branch == "" {
		branch = scan.Branch
	}
	if scan.CommitSHA != "" {
		if branch != "" {
			return branch + "@" + scan.CommitSHA
		}
		return scan.CommitSHA
	}
	return branch
}

func scanRemoteNodeID(scan *models.Scan) string {
	if scan == nil || scan.RemoteNodeID == nil {
		return ""
	}
	return *scan.RemoteNodeID
}

func ensureScanOwner(w http.ResponseWriter, scan *models.Scan, claims *auth.Claims) bool {
	if scan == nil || claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "missing user context")
		return false
	}
	if scan.UserID != "" && scan.UserID != claims.UserID {
		response.WriteError(w, http.StatusForbidden, "forbidden", "scan does not belong to current user")
		return false
	}
	return true
}

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
// parsePagination extracts page and per_page from query params with defaults.
func parsePagination(r *http.Request) (page, perPage int) {
	page = 1
	perPage = 50

	if v := r.URL.Query().Get("page"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 {
			page = p
		}
	}
	// Accept legacy `limit` as an alias for `per_page` — earlier UI code
	// used limit=, and there's no good reason to error or silently use
	// the default when the client clearly means the same thing.
	if v := r.URL.Query().Get("per_page"); v != "" {
		if pp, err := strconv.Atoi(v); err == nil && pp > 0 {
			if pp > 50000 {
				pp = 50000
			}
			perPage = pp
		}
	} else if v := r.URL.Query().Get("limit"); v != "" {
		if pp, err := strconv.Atoi(v); err == nil && pp > 0 {
			if pp > 50000 {
				pp = 50000
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

// applyGitignoreByRepoID applies the repo's gitignore as an additional
// suppression pass. Looks up the repo by ID, resolves its on-disk path,
// then defers to suppress.ApplyGitignore which uses `git check-ignore`
// for canonical semantics (negations, nested .gitignore, etc.). Silent
// no-op when the repo can't be found or the path isn't a git repo — the
// underlying call already degrades gracefully.
func applyGitignoreByRepoID(ctx context.Context, h *Handler, repoID string, findings []models.Finding) {
	if h == nil || repoID == "" || len(findings) == 0 {
		return
	}
	repo, err := h.Store.GetRepoByID(ctx, repoID)
	if err != nil || repo.SourcePath == "" {
		return
	}
	suppress.ApplyGitignore(findings, repo.SourcePath)
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

	scan, err := h.Store.GetScanByID(r.Context(), scanID)
	if err != nil { // #nosec G104 -- intentional: response/log write errors are not actionable here
		response.WriteError(w, http.StatusNotFound, "not_found", fmt.Sprintf("scan %s not found", scanID))
		return
	}
	if !ensureScanOwner(w, scan, claims) {
		return
	}

	artifacts, err := h.Store.ListScanArtifacts(r.Context(), scanID)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to list artifacts")
		return
	}

	// Try log artifact first (stderr output), then JSON (raw findings).
	for _, a := range artifacts {
		if a.ArtifactType == models.ArtifactLog && strings.HasSuffix(a.FilePath, toolName+".log") { // #nosec G104 -- intentional: response/log write errors are not actionable here
			content, err := os.ReadFile(a.FilePath)
			if err != nil {
				continue
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			w.Write(content) // nosemgrep: go.lang.security.audit.xss.no-direct-write-to-responsewriter.no-direct-write-to-responsewriter // #nosec G104 -- intentional: HTTP write / log errors aren't actionable in this branch
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
			w.Write(content) // nosemgrep: go.lang.security.audit.xss.no-direct-write-to-responsewriter.no-direct-write-to-responsewriter // #nosec G104 -- intentional: HTTP write / log errors aren't actionable in this branch
			return
		}
	}

	response.WriteError(w, http.StatusNotFound, "not_found", "tool output not found")
}

// scanTrendEntry is one row per past scan in the trends timeline. Severity
// counts come from the persisted findings for that scan (post-suppression,
// to match the visible-count semantics the UI uses everywhere else). // #nosec G104 -- intentional: response/log write errors are not actionable here
type scanTrendEntry struct { // #nosec G104 -- intentional: response/log write errors are not actionable here
	ScanID      string `json:"scan_id"` // #nosec G104 -- intentional: response/log write errors are not actionable here
	Branch      string `json:"branch"`
	Status      string `json:"status"`
	CompletedAt string `json:"completed_at"`
	Total       int    `json:"total"`
	Critical    int    `json:"critical"`
	High        int    `json:"high"`
	Medium      int    `json:"medium"`
	Low         int    `json:"low"`
	Info        int    `json:"info"`
}

// ScansTrends handles GET /api/scans/trends?repo_id=&branch=&limit=
// One row per past scan against the repo (+ branch if provided), ordered
// by completed_at ascending so the chart's x-axis reads left-to-right
// from oldest to newest. Default limit is 30 — enough to spot a trend
// without hammering the DB on a noisy repo. Skipped: scans in
// pending/running state (no findings yet to summarize).
func ScansTrends(w http.ResponseWriter, r *http.Request) {
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

	repoID := r.URL.Query().Get("repo_id")
	if repoID == "" {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "repo_id is required")
		return
	}
	branch := r.URL.Query().Get("branch")
	limit := 30
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}

	scans, err := h.Store.ListScansByRepo(r.Context(), repoID)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to list scans")
		return
	}

	// Filter: branch match (if specified), terminal status only.
	completed := make([]models.Scan, 0, len(scans))
	for _, s := range scans {
		if branch != "" && s.Branch != branch {
			continue
		}
		if s.Status != models.ScanStatusCompleted && s.Status != models.ScanStatusCancelled && s.Status != models.ScanStatusFailed {
			continue
		}
		completed = append(completed, s)
	}

	// Sort newest-first to apply the limit, then reverse for ascending x-axis.
	sort.Slice(completed, func(i, j int) bool {
		// Prefer completed_at when present; fall back to created_at.
		ti := completed[i].CreatedAt
		if completed[i].CompletedAt != nil {
			ti = *completed[i].CompletedAt
		}
		tj := completed[j].CreatedAt
		if completed[j].CompletedAt != nil {
			tj = *completed[j].CompletedAt
		}
		return ti.After(tj)
	})
	if len(completed) > limit {
		completed = completed[:limit]
	}
	// Reverse for asc.
	for i, j := 0, len(completed)-1; i < j; i, j = i+1, j-1 {
		completed[i], completed[j] = completed[j], completed[i]
	}

	out := make([]scanTrendEntry, 0, len(completed))
	for _, s := range completed {
		// Apply suppression to mirror the visible-count the rest of the UI
		// shows. Cheap on this volume — we re-walk per scan, but the result
		// is a single integer per severity.
		findings, ferr := h.Store.ListFindingsByScan(r.Context(), s.ID)
		if ferr != nil {
			continue
		}
		findings, _ = suppress.Apply(findings, suppress.DefaultRules())
		applyGitignoreByRepoID(r.Context(), h, s.RepoID, findings)

		entry := scanTrendEntry{
			ScanID: s.ID,
			Branch: s.Branch,
			Status: string(s.Status),
		}
		if s.CompletedAt != nil {
			entry.CompletedAt = s.CompletedAt.Format(time.RFC3339)
		} else {
			entry.CompletedAt = s.CreatedAt.Format(time.RFC3339)
		}
		for _, f := range findings {
			if f.Suppressed {
				continue
			}
			entry.Total++
			switch f.Severity {
			case models.SeverityCritical:
				entry.Critical++
			case models.SeverityHigh:
				entry.High++
			case models.SeverityMedium:
				entry.Medium++
			case models.SeverityLow:
				entry.Low++
			case models.SeverityInfo:
				entry.Info++
			}
		}
		out = append(out, entry)
	}

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: out})
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
	if !ensureScanOwner(w, scan, claims) {
		return
	}

	// Parse tool arrays.
	var selected, completed, failed []string
	json.Unmarshal([]byte(scan.ToolsSelected), &selected)   // #nosec G104 -- intentional: HTTP write / log errors aren't actionable in this branch
	json.Unmarshal([]byte(scan.ToolsCompleted), &completed) // #nosec G104 -- intentional: HTTP write / log errors aren't actionable in this branch
	json.Unmarshal([]byte(scan.ToolsFailed), &failed)       // #nosec G104 -- intentional: HTTP write / log errors aren't actionable in this branch

	// Parse the per-tool error map (added in migration 009).
	errs := make(map[string]string)
	if scan.ToolsErrors != "" && scan.ToolsErrors != "{}" {
		_ = json.Unmarshal([]byte(scan.ToolsErrors), &errs)
	}

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

	// Get finding counts per tool — applies the same suppression filter
	// the /findings endpoint does so per-tool numbers match the visible
	// table.
	findings, _ := h.Store.ListFindingsByScan(r.Context(), scanID)
	findings, _ = suppress.Apply(findings, suppress.DefaultRules())
	applyGitignoreByRepoID(r.Context(), h, scan.RepoID, findings)
	visibleCounts := make(map[string]int)
	totalCounts := make(map[string]int)
	for _, f := range findings {
		totalCounts[f.ToolName]++
		if !f.Suppressed {
			visibleCounts[f.ToolName]++
		}
	}

	type toolStatus struct {
		Name         string                   `json:"name"`
		Status       string                   `json:"status"`
		FindingCount int                      `json:"finding_count"`       // visible (post-suppression)
		RawCount     int                      `json:"raw_count,omitempty"` // pre-suppression
		HasOutput    bool                     `json:"has_output"`
		Error        string                   `json:"error,omitempty"`
		Run          *models.ScannerRunRecord `json:"run,omitempty"`
	}

	// Distinguish "running" from "queued" by peeking at the live tool
	// registry — a tool with a registered cancel func is actively
	// executing (or about to). Without this, every not-yet-done tool
	// would show as "running" and the user can't tell which one is
	// actually hogging the slot.
	activeScansMu.Lock()
	liveTools := map[string]bool{}
	if m, ok := activeToolCtxs[scanID]; ok {
		for n := range m {
			liveTools[n] = true
		}
	}
	activeScansMu.Unlock()

	runRecords, _ := h.Store.ListScannerRunRecords(r.Context(), scanID)
	runByTool := make(map[string]models.ScannerRunRecord, len(runRecords))
	for _, record := range runRecords {
		runByTool[record.ToolName] = record
	}

	var tools []toolStatus
	selectedSet := make(map[string]bool, len(selected))
	for _, name := range selected {
		selectedSet[name] = true
		var status string
		switch {
		case completedSet[name]:
			status = "completed"
		case failedSet[name]:
			// Distinguish user-cancelled from natural failure so the
			// UI can render them differently (calmer color + a Retry
			// hint instead of an error pill).
			if errs[name] == "cancelled by user" {
				status = "cancelled"
			} else {
				status = "failed"
			}
		case scan.Status == models.ScanStatusCancelled:
			// Scan was cancelled before this tool ever finished; it's
			// neither completed nor failed — flag as cancelled so the
			// UI doesn't show a stale "running" pill forever.
			status = "cancelled"
		case liveTools[name]:
			status = "running"
		default:
			status = "queued"
		}
		var run *models.ScannerRunRecord
		if record, ok := runByTool[name]; ok {
			run = &record
		}
		tools = append(tools, toolStatus{
			Name:         name,
			Status:       status,
			FindingCount: visibleCounts[name],
			RawCount:     totalCounts[name],
			HasOutput:    hasOutput[name],
			Error:        errs[name],
			Run:          run,
		})
	}
	for _, record := range runRecords {
		if selectedSet[record.ToolName] {
			continue
		}
		record := record
		tools = append(tools, toolStatus{
			Name:         record.ToolName,
			Status:       record.Status,
			FindingCount: visibleCounts[record.ToolName],
			RawCount:     totalCounts[record.ToolName],
			HasOutput:    hasOutput[record.ToolName],
			Error:        record.ErrorMessage,
			Run:          &record,
		})
	}

	response.WriteJSON(w, http.StatusOK, response.ListResponse{
		Data: tools,
		Meta: response.ListMeta{Total: len(tools), Page: 1, PerPage: len(tools)},
	})
}

// GetScannerRunRecords handles GET /api/scans/{id}/scanner-runs.
func GetScannerRunRecords(w http.ResponseWriter, r *http.Request) {
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
	if !ensureScanOwner(w, scan, claims) {
		return
	}
	records, err := h.Store.ListScannerRunRecords(r.Context(), scanID)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to list scanner run records")
		return
	}
	if records == nil {
		records = []models.ScannerRunRecord{}
	}
	response.WriteJSON(w, http.StatusOK, response.ListResponse{
		Data: records,
		Meta: response.ListMeta{Total: len(records), Page: 1, PerPage: len(records)},
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
