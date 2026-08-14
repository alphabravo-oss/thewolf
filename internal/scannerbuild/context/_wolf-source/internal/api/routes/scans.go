package routes

import (
	"context"
	"crypto/sha256"
	"database/sql"
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
	"github.com/alphabravocompany/thewolf/internal/fix/lineage"
	"github.com/alphabravocompany/thewolf/internal/models"
	scannercontainer "github.com/alphabravocompany/thewolf/internal/plugin/container"
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
	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
	"github.com/alphabravocompany/thewolf/internal/scannerruntime"
	kubernetesruntime "github.com/alphabravocompany/thewolf/internal/scannerruntime/kubernetes"
	scannermanifest "github.com/alphabravocompany/thewolf/internal/scannertools/manifest"
	"github.com/alphabravocompany/thewolf/internal/scantarget"
	"github.com/alphabravocompany/thewolf/internal/secrets"
	"github.com/alphabravocompany/thewolf/internal/wolflog"
)

// ExecuteQueuedScan runs a scan claimed by a scan-worker. The normalized
// request is loaded from the durable scan row, so the API process and worker
// never need to share process memory.
func ExecuteQueuedScan(ctx context.Context, h *Handler, scan *models.Scan) error {
	if h == nil || h.Store == nil {
		return errors.New("scan handler is not initialized")
	}
	if scan == nil {
		return errors.New("scan is required")
	}
	var req createScanRequest
	if scan.RequestJSON != "" && scan.RequestJSON != "{}" {
		if err := json.Unmarshal([]byte(scan.RequestJSON), &req); err != nil {
			return fmt.Errorf("decode durable scan request: %w", err)
		}
	}
	req.RepoID = scan.RepoID
	if req.Branch == "" {
		req.Branch = scan.Branch
	}
	executeScan(ctx, h, scan.ID, scan.UserID, scan.RepoID, req.Branch, req)
	return nil
}

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
	RepoID          string             `json:"repo_id"`
	Source          *scanSourceRequest `json:"source,omitempty"`
	CollectionID    *string            `json:"collection_id,omitempty"`
	Branch          string             `json:"branch,omitempty"`
	Profile         string             `json:"profile,omitempty"`
	Categories      []string           `json:"categories,omitempty"`
	Tools           []string           `json:"tools,omitempty"`
	DisabledTools   []string           `json:"disabled_tools,omitempty"`
	IncludePaths    []string           `json:"include_paths,omitempty"`
	ExcludePaths    []string           `json:"exclude_paths,omitempty"`
	ClientReference string             `json:"client_reference,omitempty"`
	AIEnabled       bool               `json:"ai_enabled,omitempty"`
	AIEngine        string             `json:"ai_engine,omitempty"`
	AIModel         string             `json:"ai_model,omitempty"`
}

func normalizedScanRequest(req createScanRequest, repoID, branch string) ([]byte, string, error) {
	req.RepoID = repoID
	req.Branch = branch
	normalized := req
	normalized.Tools = append([]string(nil), req.Tools...)
	normalized.DisabledTools = append([]string(nil), req.DisabledTools...)
	normalized.Categories = append([]string(nil), req.Categories...)
	normalized.IncludePaths = append([]string(nil), req.IncludePaths...)
	normalized.ExcludePaths = append([]string(nil), req.ExcludePaths...)
	sort.Strings(normalized.Tools)
	sort.Strings(normalized.DisabledTools)
	sort.Strings(normalized.Categories)
	sort.Strings(normalized.IncludePaths)
	sort.Strings(normalized.ExcludePaths)
	data, err := json.Marshal(normalized)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(data)
	return data, hex.EncodeToString(sum[:]), nil
}

func queuedScanExecution() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("WOLF_SCAN_EXECUTION_MODE"))) {
	case "queue", "worker", "workers":
		return true
	default:
		return false
	}
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

	if (req.RepoID == "") == (req.Source == nil) {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "exactly one of repo_id or source is required")
		return
	}
	if err := validateScanRequestSelectors(h, &req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	if req.Source != nil {
		if req.Source.Ref != "" && req.Branch != "" && req.Source.Ref != req.Branch {
			response.WriteError(w, http.StatusBadRequest, "validation_error", "source.ref conflicts with branch")
			return
		}
		sourceRepo, err := materializeScanSource(r.Context(), h, claims.UserID, req.Source)
		if err != nil {
			response.WriteError(w, http.StatusBadRequest, "source_invalid", err.Error())
			return
		}
		req.RepoID = sourceRepo.ID
		if req.Source.Ref != "" {
			req.Branch = req.Source.Ref
		}
	}

	repo, ok := loadRepoForCaller(w, r, h.Store, req.RepoID, claims)
	if !ok {
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
	categoriesJSON, _ := json.Marshal(req.Categories)
	includePathsJSON, _ := json.Marshal(req.IncludePaths)
	excludePathsJSON, _ := json.Marshal(req.ExcludePaths)
	requestJSON, requestDigest, err := normalizedScanRequest(req, repo.ID, branch)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to normalize scan request")
		return
	}
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if len(idempotencyKey) > 255 {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "Idempotency-Key must be 255 characters or fewer")
		return
	}
	if idempotencyKey != "" {
		if existing, findErr := h.Store.FindScanByIdempotencyKey(r.Context(), claims.UserID, idempotencyKey); findErr == nil {
			if existing.RequestDigest != requestDigest {
				response.WriteError(w, http.StatusConflict, "idempotency_conflict", "Idempotency-Key was already used for a different scan request")
				return
			}
			response.WriteJSON(w, http.StatusCreated, response.SuccessResponse{Data: existing})
			return
		} else if !errors.Is(findErr, sql.ErrNoRows) {
			response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to check idempotency key")
			return
		}
	}

	now := time.Now()
	scan := &models.Scan{
		ID:                uuid.New().String(),
		UserID:            claims.UserID,
		RepoID:            req.RepoID,
		CollectionID:      req.CollectionID,
		Branch:            branch,
		SourceType:        repo.SourceType,
		RemoteNodeID:      repo.RemoteNodeID,
		SourcePath:        repo.SourcePath,
		SourceFingerprint: repo.SourceFingerprint,
		Status:            models.ScanStatusPending,
		ToolsSelected:     string(toolsSelected),
		RequestJSON:       string(requestJSON),
		RequestDigest:     requestDigest,
		ClientReference:   strings.TrimSpace(req.ClientReference),
		IdempotencyKey:    idempotencyKey,
		Phase:             "queued",
		MaxAttempts:       2,
		Profile:           req.Profile,
		Categories:        string(categoriesJSON),
		IncludePaths:      string(includePathsJSON),
		ExcludePaths:      string(excludePathsJSON),
		AIEnabled:         req.AIEnabled,
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	if err := h.Store.CreateScan(r.Context(), scan); err != nil {
		if idempotencyKey != "" {
			if existing, findErr := h.Store.FindScanByIdempotencyKey(r.Context(), claims.UserID, idempotencyKey); findErr == nil {
				if existing.RequestDigest != requestDigest {
					response.WriteError(w, http.StatusConflict, "idempotency_conflict", "Idempotency-Key was already used for a different scan request")
					return
				}
				response.WriteJSON(w, http.StatusCreated, response.SuccessResponse{Data: existing})
				return
			}
		}
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to create scan")
		return
	}

	publishScanEvent(h, scan.ID, "scan_status", fmt.Sprintf(
		`{"type":"scan_status","scan_id":"%s","status":"pending","finding_count":0}`, scan.ID,
	))
	if !queuedScanExecution() {
		go executeScan(context.Background(), h, scan.ID, claims.UserID, repo.ID, branch, req)
	}

	response.WriteJSON(w, http.StatusCreated, response.SuccessResponse{Data: scan})
}

type releaseRescanRequest struct {
	ReleaseID string `json:"release_id"`
	Reason    string `json:"reason"`
}

// CreateReleaseRescan creates a distinct scan operation pinned to an explicitly
// selected immutable release. Worker lease retries continue to reuse the
// original scan row and therefore retain its original release assignment.
func CreateReleaseRescan(w http.ResponseWriter, r *http.Request) {
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
	source, ok := loadScanForCaller(w, r, h.Store, chi.URLParam(r, "id"), claims)
	if !ok {
		return
	}
	var req releaseRescanRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	req.ReleaseID = strings.TrimSpace(req.ReleaseID)
	req.Reason = strings.TrimSpace(req.Reason)
	if req.ReleaseID == "" || req.Reason == "" {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "release_id and reason are required")
		return
	}
	if req.ReleaseID == source.ScannerReleaseID {
		response.WriteError(w, http.StatusConflict, "release_unchanged", "selected release is already assigned to the source scan")
		return
	}
	inventory, err := h.Store.ScannerReleases().GetReleaseInventory(r.Context(), req.ReleaseID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			response.WriteError(w, http.StatusNotFound, "release_not_found", "scanner release not found")
		} else {
			response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to load scanner release")
		}
		return
	}
	if inventory.Release.Legacy {
		response.WriteError(w, http.StatusConflict, "legacy_release_unverifiable", "legacy release snapshots are historical evidence and cannot be selected for a managed re-scan")
		return
	}
	if inventory.Release.State == scannerrelease.ReleaseRevoked ||
		inventory.Release.State == scannerrelease.ReleaseDeprecated {
		response.WriteError(w, http.StatusConflict, "release_not_runnable", "selected scanner release is not runnable")
		return
	}
	selectedImages, imageReferences, err := selectRuntimeReleaseImages(r.Context(), h, inventory.Images)
	if err != nil {
		response.WriteError(w, http.StatusConflict, "release_not_runnable", err.Error())
		return
	}
	releaseSnapshot, err := scannerruntime.SnapshotFromInventory(
		inventory.Release, inventory.Tools, selectedImages, imageReferences,
	)
	if err != nil {
		response.WriteError(w, http.StatusConflict, "release_not_runnable", err.Error())
		return
	}
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if idempotencyKey == "" {
		response.WriteError(w, http.StatusPreconditionRequired, "idempotency_key_required", "Idempotency-Key is required")
		return
	}
	if len(idempotencyKey) > 255 {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "Idempotency-Key must be 255 characters or fewer")
		return
	}
	if existing, findErr := h.Store.FindScanByIdempotencyKey(r.Context(), claims.UserID, idempotencyKey); findErr == nil {
		if existing.RescanOfScanID != source.ID || existing.ScannerReleaseID != req.ReleaseID ||
			existing.ReleaseSelectionReason != req.Reason {
			response.WriteError(w, http.StatusConflict, "idempotency_conflict", "Idempotency-Key was already used for a different scan request")
			return
		}
		response.WriteJSON(w, http.StatusCreated, response.SuccessResponse{Data: existing})
		return
	} else if !errors.Is(findErr, sql.ErrNoRows) {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to check idempotency key")
		return
	}

	now := time.Now().UTC()
	rescan := &models.Scan{
		ID: uuid.NewString(), UserID: claims.UserID, RepoID: source.RepoID,
		CollectionID: source.CollectionID, Branch: source.Branch,
		SourceType: source.SourceType, RemoteNodeID: source.RemoteNodeID,
		SourcePath: source.SourcePath, SourceFingerprint: source.SourceFingerprint,
		RequestJSON: source.RequestJSON, RequestDigest: source.RequestDigest,
		ClientReference: source.ClientReference, IdempotencyKey: idempotencyKey,
		Phase: "queued", MaxAttempts: source.MaxAttempts,
		Profile: source.Profile, Categories: source.Categories,
		IncludePaths: source.IncludePaths, ExcludePaths: source.ExcludePaths,
		ToolsSelected: source.ToolsSelected, Status: models.ScanStatusPending,
		AIEnabled: source.AIEnabled, ScannerReleaseID: releaseSnapshot.ReleaseID,
		ReleaseManifestDigest: releaseSnapshot.ManifestDigest,
		RescanOfScanID:        source.ID, ReleaseSelectionReason: req.Reason,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := h.Store.CreateScan(r.Context(), rescan); err != nil {
		if existing, findErr := h.Store.FindScanByIdempotencyKey(r.Context(), claims.UserID, idempotencyKey); findErr == nil &&
			existing.RescanOfScanID == source.ID && existing.ScannerReleaseID == req.ReleaseID &&
			existing.ReleaseSelectionReason == req.Reason {
			response.WriteJSON(w, http.StatusCreated, response.SuccessResponse{Data: existing})
			return
		}
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to create release re-scan")
		return
	}
	publishScanEvent(h, rescan.ID, "scan_status", fmt.Sprintf(
		`{"type":"scan_status","scan_id":"%s","status":"pending","finding_count":0}`, rescan.ID,
	))
	if !queuedScanExecution() {
		var durableRequest createScanRequest
		if err := json.Unmarshal([]byte(rescan.RequestJSON), &durableRequest); err == nil {
			go executeScan(context.Background(), h, rescan.ID, rescan.UserID, rescan.RepoID, rescan.Branch, durableRequest)
		}
	}
	response.WriteJSON(w, http.StatusCreated, response.SuccessResponse{Data: rescan})
}

// executeScan runs the scan in a background goroutine.
func executeScan(parent context.Context, h *Handler, scanID, userID, repoID, branch string, req createScanRequest) {
	log := wolflog.Component("scan")
	log.Info().Str("scan_id", scanID).Str("repo_id", repoID).Str("branch", branch).Msg("scan starting")

	ctx, cancel := context.WithCancel(parent)

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
	stopCancellationWatch := watchDurableScanCancellation(ctx, h, scanID, cancel)
	defer stopCancellationWatch()

	// Mark scan as running.
	now := time.Now()
	scan, err := h.Store.GetScanByID(ctx, scanID)
	if err != nil {
		log.Error().Str("scan_id", scanID).Err(err).Msg("failed to load scan record")
		return
	}
	executionLeaseToken := scan.LeaseToken
	started, err := h.Store.StartScanExecution(ctx, scanID, executionLeaseToken, now)
	if err != nil {
		log.Error().Str("scan_id", scanID).Err(err).Msg("failed to start scan execution")
		return
	}
	if !started {
		log.Info().Str("scan_id", scanID).Msg("scan claim was cancelled or lost before execution")
		return
	}
	scan, err = h.Store.GetScanByID(ctx, scanID)
	if err != nil {
		return
	}
	releaseSnapshot, releaseRuntimeConfig, err := resolveScanRelease(ctx, h, scan)
	if err != nil {
		failPreparedScan(h, scan, executionLeaseToken, fmt.Errorf("resolve scanner release: %w", err))
		return
	}
	var resumeSelected, resumeCompleted []string
	_ = json.Unmarshal([]byte(scan.ToolsSelected), &resumeSelected)
	_ = json.Unmarshal([]byte(scan.ToolsCompleted), &resumeCompleted)
	if scan.Attempt > 1 {
		runRecords, _ := h.Store.ListScannerRunRecords(ctx, scanID)
		completedSet := make(map[string]bool)
		for _, run := range runRecords {
			if run.Status == "completed" {
				completedSet[run.ToolName] = true
				continue
			}
			_ = h.Store.DeleteFindingsByScanTool(ctx, scanID, run.ToolName)
		}
		resumeCompleted = resumeCompleted[:0]
		for toolName := range completedSet {
			resumeCompleted = append(resumeCompleted, toolName)
			req.DisabledTools = appendUniqueString(req.DisabledTools, toolName)
		}
		sort.Strings(resumeCompleted)
		completedJSON, _ := json.Marshal(resumeCompleted)
		scan.ToolsCompleted = string(completedJSON)
		scan.ToolsFailed = "[]"
		scan.ToolsErrors = "{}"
		if retained, listErr := h.Store.ListFindingsByScan(ctx, scanID); listErr == nil {
			scan.FindingCount = len(retained)
		}
		_ = h.Store.UpdateScan(ctx, scan)
	}
	publishScanEventForLease(h, scanID, executionLeaseToken, "scan_status", fmt.Sprintf(
		`{"type":"scan_status","scan_id":"%s","status":"running","finding_count":%d}`, scan.ID, scan.FindingCount,
	))

	topic := "scan:" + scanID

	repo, err := h.Store.GetRepoByID(ctx, repoID)
	if err != nil {
		failPreparedScan(h, scan, executionLeaseToken, fmt.Errorf("load repo: %w", err))
		return
	}
	var prepared scantarget.Prepared
	var errPrep error
	if existing := strings.TrimSpace(scan.PreparedWorkspace); existing != "" {
		if st, serr := os.Stat(existing); serr == nil && st.IsDir() {
			prepared, errPrep = scantarget.PrepareExisting(existing, scan.SourceType)
		}
	}
	if prepared.Path == "" {
		prepared, errPrep = (scantarget.Resolver{Store: h.Store}).Prepare(ctx, repo, branch)
	}
	if errPrep != nil {
		failPreparedScan(h, scan, executionLeaseToken, errPrep)
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
	scan.TreeDigest = prepared.TreeDigest
	scan.DirtyState = prepared.DirtyState
	scan.PreparedWorkspace = prepared.PreparedWorkspace
	_ = h.Store.UpdateScan(ctx, scan)
	if prepared.CommitSHA != "" || prepared.DirtyState != "" {
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
	toolsCompleted := append([]string(nil), resumeCompleted...)
	toolsFailed := make([]string, 0)
	// toolsErrors maps toolName → error message. Persisted so the UI
	// can render "why" beside the failure indicator.
	toolsErrors := make(map[string]string)
	// toolStartTimes captures when each tool entered the running state
	// so OnToolDone can broadcast a real elapsed_ms (without changing
	// the runner's callback signature). Without this the live page
	// always shows "0s" because the SSE payload hardcoded elapsed.
	toolStartTimes := make(map[string]time.Time)
	cumulativeFindingCount := scan.FindingCount
	activeSuppressions, suppressionErr := h.Store.ListFindingSuppressions(context.Background(), scan.RepoID, false)
	if suppressionErr != nil {
		log.Warn().Str("scan_id", scanID).Err(suppressionErr).Msg("failed to load durable suppressions")
		activeSuppressions = nil
	}
	scanBranches := map[string]string{scanID: branch}

	// Read scan concurrency from settings (default: 2 — conservative for a
	// typical host; raise it in Settings → General for beefier machines).
	scanConcurrency := 2
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
	executionTools := toolsForProfile(h, req, detectedLanguages)
	cfg := runner.RunConfig{
		RepoPath:           repoPath,
		ScanID:             scanID,
		UserID:             scan.UserID,
		LeaseToken:         executionLeaseToken,
		Attempt:            scan.Attempt,
		Branch:             branch,
		Registry:           h.Registry,
		Tools:              executionTools,
		ToolsExplicit:      req.Profile == "full" || len(req.Categories) > 0 || len(req.Tools) > 0,
		DisabledTools:      req.DisabledTools,
		IncludePaths:       req.IncludePaths,
		ExcludePaths:       req.ExcludePaths,
		Concurrency:        scanConcurrency,
		HeavyConcurrency:   heavyScannerConcurrency,
		NetworkConcurrency: networkScannerConcurrency,
		ContainerCfg:       releaseRuntimeConfig,
		RawOutputDir:       rawDir,
		OnToolsSelected: func(toolNames []string) {
			if !scanExecutionOwned(h, scanID, executionLeaseToken) {
				return
			}
			log.Info().Str("scan_id", scanID).Int("count", len(toolNames)).Strs("tools", toolNames).Msg("tools selected")
			// Persist selected tools immediately so the live page can show all cards. // #nosec G104 -- intentional: response/log write errors are not actionable here
			allSelected := append([]string(nil), resumeSelected...)
			for _, toolName := range toolNames {
				allSelected = appendUniqueString(allSelected, toolName)
			}
			selectedJSON, _ := json.Marshal(allSelected)
			if s, err := h.Store.GetScanByID(context.Background(), scanID); err == nil {
				s.ToolsSelected = string(selectedJSON)
				s.LeaseToken = executionLeaseToken
				s.UpdatedAt = time.Now()
				_ = h.Store.UpdateScan(context.Background(), s) // #nosec G104 -- intentional: HTTP write / log errors aren't actionable in this branch
			}
			existingRuns, _ := h.Store.ListScannerRunRecords(context.Background(), scanID)
			existingByTool := make(map[string]models.ScannerRunRecord, len(existingRuns))
			for _, record := range existingRuns {
				existingByTool[record.ToolName] = record
			}
			for _, toolName := range toolNames {
				if existing, ok := existingByTool[toolName]; ok && existing.CancelRequestedAt != nil {
					continue
				}
				upsertScannerRunRecord(context.Background(), h, applyScannerRunScope(scannerRunRecordQueued(scanID, toolName, scannerPlan), req, scan, scannerPlan))
			}
			// Also broadcast via SSE so connected clients see the full list.
			toolsJSON, _ := json.Marshal(allSelected)
			publishScanEventForLease(h, scanID, executionLeaseToken, "tools_selected",
				fmt.Sprintf(`{"type":"tools_selected","scan_id":"%s","tools":%s}`, scanID, string(toolsJSON)))
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
			if !scanExecutionOwned(h, scanID, executionLeaseToken) {
				return
			}
			log.Debug().Str("scan_id", scanID).Str("tool", toolName).Msg("tool starting")
			startedAt := time.Now().UTC()
			toolStateMu.Lock()
			toolStartTimes[toolName] = startedAt
			toolStateMu.Unlock()
			upsertScannerRunRecord(context.Background(), h, applyScannerRunScope(scannerRunRecordStart(scanID, toolName, scannerPlan, startedAt), req, scan, scannerPlan))
			publishScanEventForLease(h, scanID, executionLeaseToken, "scan_progress",
				fmt.Sprintf(`{"type":"scan_progress","scan_id":"%s","tool_name":"%s","status":"running","finding_count":0,"elapsed_ms":0,"progress_pct":0}`, scanID, toolName))
		},
		OnToolDone: func(toolName string, toolFindings []models.Finding, toolErr error) {
			if !scanExecutionOwned(h, scanID, executionLeaseToken) {
				return
			}
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
				var createErr error
				persisted := true
				if executionLeaseToken != "" {
					persisted, createErr = h.Store.CreateFindingsForScanLease(
						context.Background(), toolFindings, scanID, executionLeaseToken,
					)
				} else {
					createErr = h.Store.CreateFindings(context.Background(), toolFindings)
				}
				if createErr != nil {
					log.Error().Str("scan_id", scanID).Str("tool", toolName).Err(createErr).Msg("failed to persist tool findings")
				} else if !persisted {
					log.Warn().Str("scan_id", scanID).Str("tool", toolName).
						Msg("scan lease changed; rejected stale tool findings")
					return
				}
			}

			// Update tool status and cumulative finding count atomically.
			toolStateMu.Lock()
			if toolErr != nil {
				// Trim the error to a UI-friendly size; full traces
				// remain in the per-tool .log artifact for deep dives.
				e := errMsg
				if e == "" {
					e = toolErr.Error()
				}
				if len(e) > 500 {
					e = e[:500] + "…"
				}
				if isMissingImageErr(e) {
					// Not a real failure: the tool's image just isn't pulled.
					// Record it as a skip (actionable: pull the image, rescan)
					// instead of a scary red failure.
					upsertScannerRunRecord(context.Background(), h, applyScannerRunScope(scannerRunRecordSkipped(scanID, toolName, scannerPlan, "image not pulled"), req, scan, scannerPlan))
				} else {
					toolsFailed = append(toolsFailed, toolName)
					toolsErrors[toolName] = e
				}
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
				s.LeaseToken = executionLeaseToken
				s.UpdatedAt = time.Now()
				_ = h.Store.UpdateScan(context.Background(), s)
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
			upsertScannerRunRecord(context.Background(), h, applyScannerRunScope(scannerRunRecordDone(scanID, toolName, scannerPlan, recordStatus, findingCount, errMsg, elapsedMs, recordStartedAt, finishedAt), req, scan, scannerPlan))

			// Broadcast SSE with per-tool count and cumulative total.
			escapedErr, _ := json.Marshal(errMsg)
			publishScanEventForLease(h, scanID, executionLeaseToken, "scan_progress",
				fmt.Sprintf(`{"type":"scan_progress","scan_id":"%s","tool_name":"%s","status":"%s","finding_count":%d,"total_findings":%d,"elapsed_ms":%d,"progress_pct":100,"error":%s}`, scanID, toolName, status, findingCount, currentTotal, elapsedMs, string(escapedErr)))
		},
		OnToolOutput: func(toolName string, line string) {
			if !scanExecutionOwned(h, scanID, executionLeaseToken) {
				return
			}
			escapedLine, _ := json.Marshal(line)
			publishScanEventForLease(h, scanID, executionLeaseToken, "tool_output",
				fmt.Sprintf(`{"type":"tool_output","scan_id":"%s","tool_name":"%s","line":%s}`, scanID, toolName, string(escapedLine)))
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
	if len(executionTools) == 0 {
		cfg.Languages = detectedLanguages
	}

	if toolManifest, merr := scannermanifest.LoadDefault(); merr == nil {
		cfg.ToolResources = runner.ResourceSpecsFromManifest(toolManifest)
		scannerPlan = planner.ToReportPlan(planner.Build(planner.Config{
			Registry:      h.Registry,
			Manifest:      toolManifest,
			Languages:     detectedLanguages,
			Tools:         executionTools,
			DisabledTools: req.DisabledTools,
		}))
		if releaseSnapshot != nil {
			applyReleaseToScannerPlan(scannerPlan, releaseSnapshot)
		} else {
			// Compatibility/read-only deployments still need truthful scanner
			// image provenance. The runner falls back to the process-wide
			// container config when no managed release is assigned, so bind the
			// report plan to that exact same config before scanner-run rows are
			// created. This records digest-pinned operator configuration without
			// inventing a managed release identity.
			applyContainerImagesToScannerPlan(scannerPlan, scannercontainer.Default())
		}
	} else {
		log.Warn().Err(merr).Str("scan_id", scanID).Msg("scanner tool manifest unavailable; manifest scanner plan omitted")
	}

	result, runErr := runner.Run(ctx, cfg)
	if result != nil {
		for _, toolName := range result.ToolsSkipped {
			upsertScannerRunRecord(context.Background(), h, applyScannerRunScope(scannerRunRecordSkipped(scanID, toolName, scannerPlan, "tool unavailable"), req, scan, scannerPlan))
		}
		// A reclaimed scan only reruns incomplete tools. Rebuild the
		// aggregate from durable findings so reports and AI assessment
		// contain successful tools from earlier attempts as well.
		if len(resumeCompleted) > 0 {
			if retained, listErr := h.Store.ListFindingsByScan(context.Background(), scanID); listErr == nil {
				result.Findings = retained
			}
			for _, toolName := range resumeCompleted {
				result.ToolsRun = appendUniqueString(result.ToolsRun, toolName)
			}
			sort.Strings(result.ToolsRun)
		}
	}

	// Update scan with results.
	completedAt := time.Now()
	scan, err = h.Store.GetScanByID(context.Background(), scanID)
	if err != nil {
		log.Error().Str("scan_id", scanID).Err(err).Msg("failed to reload scan after run")
		return
	}
	if executionLeaseToken != "" && scan.LeaseToken != executionLeaseToken {
		log.Warn().Str("scan_id", scanID).Msg("scan lease changed; stale executor will not finalize")
		return
	}

	// Cancel is "sticky": if the user (or orphan-recovery) marked the scan
	// cancelled while runner.Run was still finishing its tail, don't
	// flip it back to completed/failed when we land here. Findings
	// already persisted during the run are preserved either way.
	if scan.Status == models.ScanStatusCancelled {
		scan.Phase = "cancelled"
		log.Info().Str("scan_id", scanID).Msg("scan run finished but was already cancelled; preserving status")
	} else if runErr != nil {
		scan.Status = models.ScanStatusFailed
		scan.Phase = "failed"
		scan.FailureCode = "scan_failed"
		scan.FailureMessage = truncateScanError(runErr.Error())
		log.Error().Str("scan_id", scanID).Err(runErr).Msg("scan run failed")
	} else {
		scan.Status = models.ScanStatusCompleted
		scan.Phase = "completed"
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

		completedOnly := append([]string(nil), resumeCompleted...)
		for _, name := range result.ToolsRun {
			if !failedNames[name] {
				completedOnly = appendUniqueString(completedOnly, name)
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
		for name, terr := range result.ToolsFailed {
			// A tool whose image isn't pulled is a skip, not a failure — keep
			// it out of tools_failed (it's recorded as a skipped run record).
			if terr != nil && isMissingImageErr(terr.Error()) {
				continue
			}
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

	// Mark scan complete NOW — tools are done. Queue workers must still own
	// the lease at the instant of this write; the earlier read is not enough
	// because a reclaim or cancellation can race finalization.
	if executionLeaseToken != "" {
		finalized, finalizeErr := h.Store.FinalizeScan(context.Background(), scan, executionLeaseToken)
		if finalizeErr != nil {
			log.Error().Str("scan_id", scanID).Err(finalizeErr).Msg("failed to finalize leased scan")
			return
		}
		if !finalized {
			log.Warn().Str("scan_id", scanID).Msg("scan lease or status changed; stale executor finalization rejected")
			return
		}
		scan.ClaimedBy = ""
		scan.LeaseToken = ""
		scan.LeaseExpiresAt = nil
		scan.HeartbeatAt = nil
	} else if updateErr := h.Store.UpdateScan(context.Background(), scan); updateErr != nil {
		log.Error().Str("scan_id", scanID).Err(updateErr).Msg("failed to finalize inline scan")
		return
	}
	if gateResult, eval, policy, findingCount, gateErr := evaluateAndPersistGateContext(context.Background(), h, scanID, userID); gateErr != nil {
		log.Warn().Str("scan_id", scanID).Err(gateErr).Msg("quality gate evaluation failed")
	} else {
		_ = writeGateResultArtifact(context.Background(), h, scan, gateResult, eval, policy, findingCount, logDir)
		log.Info().Str("scan_id", scanID).Str("gate_status", eval.Status).Msg("quality gate evaluated")
	}

	publishScanEvent(h, scanID, "scan_complete",
		fmt.Sprintf(`{"type":"scan_complete","scan_id":"%s","status":"%s","finding_count":%d}`, scanID, scan.Status, scan.FindingCount))

	if scan.Status == models.ScanStatusCompleted && scan.FixJobID != "" {
		if next, nerr := lineage.MaybeEnqueueNextRun(context.Background(), h.Store, scan); nerr != nil {
			log.Warn().Str("scan_id", scanID).Err(nerr).Msg("next agent run enqueue failed")
		} else if next != nil {
			log.Info().Str("scan_id", scanID).Str("job", next.ID).Int("run_index", next.RunIndex).
				Msg("queued next sequential agent run")
		}
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

func scanExecutionOwned(h *Handler, scanID, leaseToken string) bool {
	if leaseToken == "" {
		return true
	}
	if h == nil || h.Store == nil {
		return false
	}
	current, err := h.Store.GetScanByID(context.Background(), scanID)
	return err == nil &&
		current.Status == models.ScanStatusRunning &&
		current.LeaseToken == leaseToken &&
		current.CancelRequestedAt == nil
}

func failPreparedScan(h *Handler, scan *models.Scan, leaseToken string, err error) {
	if leaseToken != "" {
		current, getErr := h.Store.GetScanByID(context.Background(), scan.ID)
		if getErr != nil || current.LeaseToken != leaseToken {
			return
		}
		scan = current
	}
	now := time.Now().UTC()
	scan.Status = models.ScanStatusFailed
	scan.Phase = "failed"
	scan.FailureCode = "source_prepare_failed"
	scan.CompletedAt = &now
	scan.UpdatedAt = now
	errMsg := err.Error()
	if len(errMsg) > 500 {
		errMsg = errMsg[:500] + "…"
	}
	scan.FailureMessage = errMsg
	errorsJSON, _ := json.Marshal(map[string]string{"prepare": errMsg})
	scan.ToolsErrors = string(errorsJSON)
	if leaseToken != "" {
		finalized, finalizeErr := h.Store.FinalizeScan(context.Background(), scan, leaseToken)
		if finalizeErr != nil || !finalized {
			return
		}
	} else if updateErr := h.Store.UpdateScan(context.Background(), scan); updateErr != nil {
		return
	}
	escapedErr, _ := json.Marshal(errMsg)
	publishScanEvent(h, scan.ID, "scan_complete",
		fmt.Sprintf(`{"type":"scan_complete","scan_id":"%s","status":"failed","finding_count":0,"error":%s}`, scan.ID, string(escapedErr)))
}

func truncateScanError(message string) string {
	if len(message) <= 500 {
		return message
	}
	return message[:500] + "…"
}

func appendUniqueString(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
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

	var (
		scans []models.Scan
		err   error
	)
	if fleetVisible(r.Context(), h.Store, claims.UserID) {
		scans, err = h.Store.ListAllScans(r.Context())
	} else {
		scans, err = h.Store.ListScansByUser(r.Context(), claims.UserID)
	}
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
	rootsOnly := strings.EqualFold(r.URL.Query().Get("roots"), "1") ||
		strings.EqualFold(r.URL.Query().Get("roots"), "true")

	filtered := make([]models.Scan, 0, len(scans))
	for _, s := range scans {
		if repoID != "" && s.RepoID != repoID {
			continue
		}
		if status != "" && string(s.Status) != status {
			continue
		}
		if rootsOnly && strings.TrimSpace(s.OriginScanID) != "" {
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
	scan, ok := loadScanForCaller(w, r, h.Store, id, claims)
	if !ok {
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
			"iteration":          scan.Iteration,
			"branch":             scan.Branch,
			"source_type":        scan.SourceType,
			"remote_node_id":     scan.RemoteNodeID,
			"source_path":        scan.SourcePath,
			"commit_sha":         scan.CommitSHA,
			"tree_digest":        scan.TreeDigest,
			"dirty_state":        scan.DirtyState,
			"prepared_workspace": scan.PreparedWorkspace,
			"status":             scan.Status,
			"phase":              scan.Phase,
			"attempt":            scan.Attempt,
			"failure_code":       scan.FailureCode,
			"failure_message":    scan.FailureMessage,
			"execution_backend":  scan.ExecutionBackend,
			"source_fingerprint": scan.SourceFingerprint,
			"profile":            scan.Profile,
			"categories":         scan.Categories,
			"include_paths":      scan.IncludePaths,
			"exclude_paths":      scan.ExcludePaths,
			"client_reference":   scan.ClientReference,
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
			"origin_scan_id":     scan.OriginScanID,
			"previous_scan_id":   scan.PreviousScanID,
			"remediation_id":     scan.RemediationID,
			"fix_job_id":         scan.FixJobID,
		},
	})
}

// GetScanLineage handles GET /api/scans/{id}/lineage — origin, children, agents.
func GetScanLineage(w http.ResponseWriter, r *http.Request) {
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
	scan, ok := loadScanForCaller(w, r, h.Store, id, claims)
	if !ok {
		return
	}
	originID := scan.ID
	if strings.TrimSpace(scan.OriginScanID) != "" {
		originID = scan.OriginScanID
	}
	origin, err := h.Store.GetScanByID(r.Context(), originID)
	if err != nil || origin == nil {
		origin = scan
		originID = scan.ID
	}
	kids, _ := h.Store.ListScansByOrigin(r.Context(), originID)
	var children []models.Scan
	for _, s := range kids {
		if s.ID == originID {
			continue
		}
		children = append(children, s)
	}
	if children == nil {
		children = []models.Scan{}
	}
	latest, _ := h.Store.GetLatestRemediationByOrigin(r.Context(), originID)
	scanIDs := map[string]bool{originID: true}
	for _, s := range children {
		scanIDs[s.ID] = true
	}
	var agents []models.FixJob
	seen := map[string]bool{}
	if latest != nil {
		if remJobs, err := h.Store.ListFixJobsByRemediation(r.Context(), latest.ID); err == nil {
			for _, j := range remJobs {
				if !seen[j.ID] {
					seen[j.ID] = true
					agents = append(agents, j)
				}
			}
		}
	}
	if origin.RepoID != "" {
		if repoJobs, err := h.Store.ListFixJobsByUser(r.Context(), claims.UserID, origin.RepoID); err == nil {
			for _, j := range repoJobs {
				if seen[j.ID] {
					continue
				}
				if scanIDs[j.ScanID] || (latest != nil && j.RemediationID == latest.ID) {
					seen[j.ID] = true
					agents = append(agents, j)
				}
			}
		}
	}
	if agents == nil {
		agents = []models.FixJob{}
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{
		Data: map[string]any{
			"origin":      origin,
			"children":    children,
			"remediation": latest,
			"agents":      agents,
			"runs":        lineage.BuildRuns(origin, children, agents),
		},
	})
}

// GetScanResult returns a compact, automation-oriented summary. Large finding
// payloads remain paginated behind /findings and report artifacts remain
// downloadable through their existing endpoints.
func GetScanResult(w http.ResponseWriter, r *http.Request) {
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
	scan, ok := loadScanForCaller(w, r, h.Store, scanID, claims)
	if !ok {
		return
	}
	findings, _ := h.Store.ListFindingsByScan(r.Context(), scanID)
	findings, _ = suppress.Apply(findings, suppress.DefaultRules())
	applyGitignoreByRepoID(r.Context(), h, scan.RepoID, findings)
	severityTotals := map[string]int{"critical": 0, "high": 0, "medium": 0, "low": 0, "info": 0}
	toolTotals := make(map[string]int)
	total := 0
	for _, finding := range findings {
		if finding.Suppressed {
			continue
		}
		total++
		severityTotals[string(finding.Severity)]++
		toolTotals[finding.ToolName]++
	}
	runs, _ := h.Store.ListScannerRunRecords(r.Context(), scanID)
	scopes := make([]map[string]interface{}, 0, len(runs))
	for _, run := range runs {
		scopes = append(scopes, map[string]interface{}{
			"tool_name":       run.ToolName,
			"status":          run.Status,
			"requested_scope": json.RawMessage(defaultString(run.RequestedScope, "{}")),
			"effective_scope": json.RawMessage(defaultString(run.EffectiveScope, "{}")),
			"scope_message":   run.ScopeMessage,
		})
	}
	gates, _ := h.Store.ListQualityGateResults(r.Context(), scanID)
	var gate interface{}
	if len(gates) > 0 {
		gate = gates[0]
	}
	base := "/api/v1/scans/" + scanID
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: map[string]interface{}{
		"id":               scan.ID,
		"status":           scan.Status,
		"phase":            scan.Phase,
		"attempt":          scan.Attempt,
		"failure_code":     scan.FailureCode,
		"failure_message":  scan.FailureMessage,
		"client_reference": scan.ClientReference,
		"provenance": map[string]interface{}{
			"repo_id": scan.RepoID, "source_type": scan.SourceType,
			"source_path": scan.SourcePath, "source_fingerprint": scan.SourceFingerprint,
			"branch": scan.Branch, "commit_sha": scan.CommitSHA,
			"tree_digest": scan.TreeDigest, "dirty_state": scan.DirtyState,
		},
		"totals": map[string]interface{}{
			"findings": total, "by_severity": severityTotals, "by_tool": toolTotals,
		},
		"scanner_scopes": scopes,
		"quality_gate":   gate,
		"links": map[string]string{
			"self": base, "findings": base + "/findings", "sarif": base + "/sarif",
			"manifest": base + "/manifest", "report": base + "/report",
			"scanner_runs": base + "/scanner-runs", "artifacts": base,
		},
		"started_at":   scan.StartedAt,
		"completed_at": scan.CompletedAt,
	}})
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
	if _, ok := loadScanForCaller(w, r, h.Store, scanID, claims); !ok {
		return
	}
	if queuedScanExecution() {
		streamDurableScanEvents(w, r, h, scanID, claims.UserID)
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
	if !ensureScanOwner(w, scan, auth.GetUserFromContext(r.Context())) {
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
			if !canModifyOwned(auth.GetUserFromContext(r.Context()), scan.UserID) {
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
	_, _ = w.Write([]byte(md)) // nosemgrep: go.lang.security.audit.xss.no-direct-write-to-responsewriter.no-direct-write-to-responsewriter // #nosec G104 -- intentional: HTTP write / log errors aren't actionable in this branch
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
	if !canModifyOwned(claims, scan.UserID) {
		response.WriteError(w, http.StatusForbidden, "forbidden", "scan does not belong to current user")
		return
	}

	if artifact, ok := findScanArtifactByType(r.Context(), h, scanID, models.ArtifactManifest); ok {
		content, err := os.ReadFile(artifact.FilePath)
		if err == nil {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="scan-%s-manifest.json"`, scanID))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(content) // nosemgrep: go.lang.security.audit.xss.no-direct-write-to-responsewriter.no-direct-write-to-responsewriter // #nosec G104 -- intentional: HTTP write / log errors aren't actionable in this branch
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
	_, _ = w.Write(data) // nosemgrep: go.lang.security.audit.xss.no-direct-write-to-responsewriter.no-direct-write-to-responsewriter // #nosec G104 -- intentional: HTTP write / log errors aren't actionable in this branch
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
	_, _ = w.Write(sarif) // nosemgrep: go.lang.security.audit.xss.no-direct-write-to-responsewriter.no-direct-write-to-responsewriter // #nosec G104 -- intentional: HTTP write / log errors aren't actionable in this branch
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
	scan, ok := loadScanForCaller(w, r, h.Store, id, claims)
	if !ok {
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
		if err := h.Store.RequestScanCancellation(r.Context(), id, now); err != nil {
			response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to cancel scan")
			return
		}
		if refreshed, err := h.Store.GetScanByID(r.Context(), id); err == nil {
			scan = refreshed
		}
		scan.Status = models.ScanStatusCancelled
		scan.CompletedAt = &now
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

	publishScanEvent(h, id, "scan_cancelled",
		fmt.Sprintf(`{"scan_id":"%s","status":"cancelled"}`, id))

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
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}
	if _, ok := loadScanForCaller(w, r, h.Store, scanID, claims); !ok {
		return
	}

	activeScansMu.Lock()
	toolCtxs, scanRegistered := activeToolCtxs[scanID]
	if !scanRegistered {
		activeScansMu.Unlock()
		if !queueToolCancellation(r.Context(), h, scanID, toolName) {
			response.WriteError(w, http.StatusNotFound, "not_found", fmt.Sprintf("tool %q is not active or queued in scan %s", toolName, scanID))
			return
		}
		response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: map[string]string{
			"scan_id": scanID, "tool_name": toolName, "status": "cancelled",
		}})
		return
	}
	cancel, ok := toolCtxs[toolName]
	if !ok {
		activeScansMu.Unlock()
		if !queueToolCancellation(r.Context(), h, scanID, toolName) {
			response.WriteError(w, http.StatusNotFound, "not_found", fmt.Sprintf("tool %q is not active in scan %s (already finished or never queued)", toolName, scanID))
			return
		}
		response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: map[string]string{
			"scan_id": scanID, "tool_name": toolName, "status": "cancelled",
		}})
		return
	}
	// Mark intent so OnToolDone replaces the raw "context canceled"
	// error string with "cancelled by user" for the UI.
	if cancelledTools[scanID] == nil {
		cancelledTools[scanID] = make(map[string]bool)
	}
	cancelledTools[scanID][toolName] = true
	activeScansMu.Unlock()

	_ = h.Store.RequestScannerRunCancellation(r.Context(), scanID, toolName, time.Now().UTC())
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
	if !ensureScanOwner(w, scan, claims) {
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
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}
	scanID := chi.URLParam(r, "id")
	if _, ok := loadScanForCaller(w, r, h.Store, scanID, auth.GetUserFromContext(r.Context())); !ok {
		return
	}
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
		publishScanEvent(h, scan.ID, "ai_log",
			fmt.Sprintf(`{"type":"ai_log","scan_id":"%s","provider":"%s","model":"%s","phase":"%s","tool":"%s","duration_ms":%d,"error":"%s"}`,
				scan.ID, entry.Provider, entry.Model, currentPhase, currentTool, entry.DurationMs, strings.ReplaceAll(entry.Error, `"`, `\"`)))
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
			publishScanEvent(h, scan.ID, "ai_assessment",
				fmt.Sprintf(`{"type":"ai_assessment","scan_id":"%s","phase":"cancelled","progress_pct":0}`, scan.ID))
			return
		}
		currentPhase = "tool_assess"
		currentTool = toolName
		indices := byTool[toolName]
		pct := ((step + 1) * 80) / totalSteps

		publishScanEvent(h, scan.ID, "ai_assessment",
			fmt.Sprintf(`{"type":"ai_assessment","scan_id":"%s","phase":"assessing","tool":"%s","step":%d,"total_steps":%d,"progress_pct":%d}`,
				scan.ID, toolName, step+1, totalSteps, pct))

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
		publishScanEvent(h, scan.ID, "ai_assessment",
			fmt.Sprintf(`{"type":"ai_assessment","scan_id":"%s","phase":"cancelled","progress_pct":0}`, scan.ID))
		return
	}

	// Phase 2: Executive summary across all tools.
	currentPhase = "summary"
	currentTool = ""
	publishScanEvent(h, scan.ID, "ai_assessment",
		fmt.Sprintf(`{"type":"ai_assessment","scan_id":"%s","phase":"summarizing","step":%d,"total_steps":%d,"progress_pct":85}`,
			scan.ID, totalSteps, totalSteps))

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

	publishScanEvent(h, scan.ID, "ai_assessment",
		fmt.Sprintf(`{"type":"ai_assessment","scan_id":"%s","phase":"complete","progress_pct":100}`, scan.ID))
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
	_, _ = w.Write(content) // nosemgrep: go.lang.security.audit.xss.no-direct-write-to-responsewriter.no-direct-write-to-responsewriter // #nosec G104 -- intentional: HTTP write / log errors aren't actionable in this branch
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
	storageKey := filepath.ToSlash(filepath.Join(scanID, filepath.Base(path)))
	if artifacts.Global != nil {
		if key, keyErr := artifacts.Global.Key(path); keyErr == nil {
			storageKey = key
		}
	}
	now := time.Now()
	_ = h.Store.CreateScanArtifact(ctx, &models.ScanArtifact{
		ID:             uuid.New().String(),
		ScanID:         scanID,
		ArtifactType:   artifactType,
		FilePath:       path,
		StorageKey:     storageKey,
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

func resolveScanRelease(
	ctx context.Context,
	h *Handler,
	scan *models.Scan,
) (*scannerruntime.ReleaseSnapshot, *scannercontainer.Config, error) {
	if h == nil || h.Store == nil || scan == nil {
		return nil, nil, errors.New("scan release resolver is unavailable")
	}
	releaseID := strings.TrimSpace(scan.ScannerReleaseID)
	if releaseID == "" {
		configured, err := h.Store.GetSetting(ctx, "desired_scanner_release_id")
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, nil, fmt.Errorf("read desired scanner release: %w", err)
		}
		releaseID = strings.TrimSpace(configured)
	}
	if releaseID == "" {
		// Compatibility mode: deployments that have not selected a managed
		// release continue using their existing process-wide scanner config.
		return nil, nil, nil
	}
	inventory, err := h.Store.ScannerReleases().GetReleaseInventory(ctx, releaseID)
	if err != nil {
		return nil, nil, fmt.Errorf("load release %q: %w", releaseID, err)
	}
	if scan.ReleaseManifestDigest != "" &&
		scan.ReleaseManifestDigest != inventory.Release.ManifestDigest {
		return nil, nil, errors.New("assigned release manifest digest changed")
	}
	selectedImages, references, err := selectRuntimeReleaseImages(ctx, h, inventory.Images)
	if err != nil {
		return nil, nil, err
	}
	snapshot, err := scannerruntime.SnapshotFromInventory(
		inventory.Release, inventory.Tools, selectedImages, references,
	)
	if err != nil {
		return nil, nil, err
	}
	base := scannercontainer.Default()
	if base == nil {
		return nil, nil, errors.New("scanner container runtime configuration is unavailable")
	}
	runtimeConfig, err := snapshot.Apply(base)
	if err != nil {
		return nil, nil, err
	}
	if scan.ScannerReleaseID == "" || scan.ReleaseManifestDigest == "" {
		scan.ScannerReleaseID = snapshot.ReleaseID
		scan.ReleaseManifestDigest = snapshot.ManifestDigest
		if err := h.Store.UpdateScan(ctx, scan); err != nil {
			return nil, nil, fmt.Errorf("persist scan release assignment: %w", err)
		}
	}
	return snapshot, runtimeConfig, nil
}

func selectRuntimeReleaseImages(
	ctx context.Context,
	h *Handler,
	images []scannerrelease.ReleaseImage,
) ([]scannerrelease.ReleaseImage, map[string]string, error) {
	if len(images) == 0 {
		return nil, nil, errors.New("scanner release inventory has no images")
	}
	targets, err := h.Store.ScannerReleases().ListRegistryTargets(ctx, true)
	if err != nil {
		return nil, nil, fmt.Errorf("list scanner registries: %w", err)
	}
	targetByID := make(map[string]scannerrelease.RegistryTarget, len(targets))
	for _, target := range targets {
		targetByID[target.ID] = target
	}
	preferred, prefErr := h.Store.GetSetting(ctx, "scanner_registry_target_id")
	if prefErr != nil && !errors.Is(prefErr, sql.ErrNoRows) {
		return nil, nil, fmt.Errorf("read scanner registry target: %w", prefErr)
	}
	preferred = strings.TrimSpace(preferred)
	if preferred == "" {
		preferred = completeRuntimeRegistryTarget(images, targets)
	}
	target, exists := targetByID[preferred]
	if !exists {
		return nil, nil, fmt.Errorf("scanner registry target %q is unavailable or disabled", preferred)
	}
	selected := make([]scannerrelease.ReleaseImage, 0)
	references := make(map[string]string)
	for _, image := range images {
		if !scannerrelease.IsRuntimeScannerImage(image) {
			continue
		}
		if image.RegistryTargetID != target.ID {
			continue
		}
		if _, duplicate := references[image.ImageKey]; duplicate {
			return nil, nil, fmt.Errorf("release has duplicate image %q for registry target %q", image.ImageKey, target.ID)
		}
		repository := strings.Trim(image.Repository, "/")
		namespace := strings.Trim(target.Namespace, "/")
		if namespace != "" && !strings.HasPrefix(repository, namespace+"/") {
			repository = namespace + "/" + repository
		}
		host := strings.TrimSpace(target.Host)
		if strings.Contains(host, "://") {
			host = strings.SplitN(host, "://", 2)[1]
		}
		references[image.ImageKey] = host + "/" + repository + "@" + image.Digest
		selected = append(selected, image)
	}
	requiredKeys := make(map[string]struct{})
	for _, image := range images {
		if !scannerrelease.IsRuntimeScannerImage(image) {
			continue
		}
		requiredKeys[image.ImageKey] = struct{}{}
	}
	if len(references) != len(requiredKeys) {
		return nil, nil, fmt.Errorf(
			"scanner registry target %q covers %d of %d release images",
			target.ID, len(references), len(requiredKeys),
		)
	}
	return selected, references, nil
}

func completeRuntimeRegistryTarget(
	images []scannerrelease.ReleaseImage,
	targets []scannerrelease.RegistryTarget,
) string {
	required := make(map[string]struct{})
	coverage := make(map[string]map[string]struct{})
	for _, image := range images {
		if !scannerrelease.IsRuntimeScannerImage(image) {
			continue
		}
		required[image.ImageKey] = struct{}{}
		if coverage[image.RegistryTargetID] == nil {
			coverage[image.RegistryTargetID] = make(map[string]struct{})
		}
		coverage[image.RegistryTargetID][image.ImageKey] = struct{}{}
	}
	for _, registryType := range []scannerrelease.RegistryType{
		scannerrelease.RegistryManaged, scannerrelease.RegistryPrivate,
		scannerrelease.RegistryAirGap, scannerrelease.RegistryMirror,
	} {
		for _, target := range targets {
			if target.Type == registryType && len(coverage[target.ID]) == len(required) {
				return target.ID
			}
		}
	}
	return ""
}

func applyReleaseToScannerPlan(plan *report.ScannerPlan, snapshot *scannerruntime.ReleaseSnapshot) {
	if plan == nil || snapshot == nil {
		return
	}
	plan.ScannerReleaseID = snapshot.ReleaseID
	plan.ReleaseManifestDigest = snapshot.ManifestDigest
	for index := range plan.Run {
		plan.Run[index].Image = snapshot.ImageFor(plan.Run[index].Tool)
	}
	for index := range plan.Skip {
		plan.Skip[index].Image = snapshot.ImageFor(plan.Skip[index].Tool)
	}
}

func applyContainerImagesToScannerPlan(plan *report.ScannerPlan, config *scannercontainer.Config) {
	if plan == nil || config == nil {
		return
	}
	for index := range plan.Run {
		plan.Run[index].Image = config.ImageFor(plan.Run[index].Tool)
	}
	for index := range plan.Skip {
		plan.Skip[index].Image = config.ImageFor(plan.Skip[index].Tool)
	}
}

func scannerRunRecordStart(scanID, toolName string, plan *report.ScannerPlan, startedAt time.Time) *models.ScannerRunRecord {
	meta := scannerRunPlanDecision(plan, toolName)
	return &models.ScannerRunRecord{
		ID:                    uuid.New().String(),
		ScanID:                scanID,
		ToolName:              toolName,
		Status:                "running",
		Category:              meta.Category,
		Image:                 meta.Image,
		ImageDigest:           imageDigestFromRef(meta.Image),
		ScannerReleaseID:      scannerPlanReleaseID(plan),
		ReleaseManifestDigest: scannerPlanManifestDigest(plan),
		CommandJSON:           "{}",
		ParserStatus:          "pending",
		StartedAt:             &startedAt,
	}
}

func scannerRunRecordQueued(scanID, toolName string, plan *report.ScannerPlan) *models.ScannerRunRecord {
	meta := scannerRunPlanDecision(plan, toolName)
	return &models.ScannerRunRecord{
		ID:                    uuid.New().String(),
		ScanID:                scanID,
		ToolName:              toolName,
		Status:                "queued",
		Category:              meta.Category,
		Image:                 meta.Image,
		ImageDigest:           imageDigestFromRef(meta.Image),
		ScannerReleaseID:      scannerPlanReleaseID(plan),
		ReleaseManifestDigest: scannerPlanManifestDigest(plan),
		CommandJSON:           "{}",
		ParserStatus:          "pending",
		RequestedScope:        "{}",
		EffectiveScope:        "{}",
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
		ID:                    uuid.New().String(),
		ScanID:                scanID,
		ToolName:              toolName,
		Status:                status,
		Category:              meta.Category,
		Image:                 meta.Image,
		ImageDigest:           imageDigestFromRef(meta.Image),
		ScannerReleaseID:      scannerPlanReleaseID(plan),
		ReleaseManifestDigest: scannerPlanManifestDigest(plan),
		CommandJSON:           "{}",
		ExitCode:              exitCode,
		DurationMS:            durationMS,
		FindingCount:          findingCount,
		ErrorMessage:          errMsg,
		ParserStatus:          parserStatus,
		ParserMessage:         errMsg,
		StartedAt:             startedAt,
		FinishedAt:            &finishedAt,
	}
}

func scannerRunRecordSkipped(scanID, toolName string, plan *report.ScannerPlan, reason string) *models.ScannerRunRecord {
	meta := scannerRunPlanDecision(plan, toolName)
	return &models.ScannerRunRecord{
		ID:                    uuid.New().String(),
		ScanID:                scanID,
		ToolName:              toolName,
		Status:                "skipped",
		Category:              meta.Category,
		Image:                 meta.Image,
		ImageDigest:           imageDigestFromRef(meta.Image),
		ScannerReleaseID:      scannerPlanReleaseID(plan),
		ReleaseManifestDigest: scannerPlanManifestDigest(plan),
		CommandJSON:           "{}",
		ErrorMessage:          reason,
		ParserStatus:          "not_run",
		ParserMessage:         reason,
	}
}

func applyScannerRunScope(record *models.ScannerRunRecord, req createScanRequest, scan *models.Scan, plan *report.ScannerPlan) *models.ScannerRunRecord {
	if record == nil {
		return nil
	}
	record.RequestedScope = requestedScopeJSON(req)
	effective := req
	meta := scannerRunPlanDecision(plan, record.ToolName)
	if len(req.IncludePaths) > 0 || len(req.ExcludePaths) > 0 {
		if meta.PathScope == "file_globs" {
			record.ScopeMessage = "scanner honors include_paths and exclude_paths"
		} else {
			effective.IncludePaths = nil
			effective.ExcludePaths = nil
			record.ScopeMessage = "scanner is repository-scoped; path selectors could not be enforced, so the full source snapshot was scanned"
		}
	}
	record.EffectiveScope = requestedScopeJSON(effective)
	if scan != nil {
		record.Attempt = scan.Attempt
		record.RuntimeBackend = scan.ExecutionBackend
		record.LeaseToken = scan.LeaseToken
		if scan.ExecutionBackend == "kubernetes" {
			record.RuntimeRef = kubernetesruntime.RuntimeRef(
				scan.ID, record.ToolName, scan.Attempt, scan.LeaseToken,
			)
		}
	}
	return record
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

func scannerPlanReleaseID(plan *report.ScannerPlan) string {
	if plan == nil {
		return ""
	}
	return plan.ScannerReleaseID
}

func scannerPlanManifestDigest(plan *report.ScannerPlan) string {
	if plan == nil {
		return ""
	}
	return plan.ReleaseManifestDigest
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
		TreeDigest: scan.TreeDigest,
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
	if !canModifyOwned(claims, scan.UserID) {
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
			_, _ = w.Write(content) // nosemgrep: go.lang.security.audit.xss.no-direct-write-to-responsewriter.no-direct-write-to-responsewriter // #nosec G104 -- intentional: HTTP write / log errors aren't actionable in this branch
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
			_, _ = w.Write(content) // nosemgrep: go.lang.security.audit.xss.no-direct-write-to-responsewriter.no-direct-write-to-responsewriter // #nosec G104 -- intentional: HTTP write / log errors aren't actionable in this branch
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
	if _, ok := loadRepoForCaller(w, r, h.Store, repoID, claims); !ok {
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
	_ = json.Unmarshal([]byte(scan.ToolsSelected), &selected)   // #nosec G104 -- intentional: HTTP write / log errors aren't actionable in this branch
	_ = json.Unmarshal([]byte(scan.ToolsCompleted), &completed) // #nosec G104 -- intentional: HTTP write / log errors aren't actionable in this branch
	_ = json.Unmarshal([]byte(scan.ToolsFailed), &failed)       // #nosec G104 -- intentional: HTTP write / log errors aren't actionable in this branch

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
		case runByTool[name].Status == "running":
			status = "running"
		case runByTool[name].Status == "cancelled":
			status = "cancelled"
		case runByTool[name].Status == "failed":
			status = "failed"
		case runByTool[name].Status == "completed":
			status = "completed"
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
			if errs[name] == "" && record.ErrorMessage != "" {
				errs[name] = record.ErrorMessage
			}
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
	if _, ok := loadScanForCaller(w, r, h.Store, scanID, auth.GetUserFromContext(r.Context())); !ok {
		return
	}
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
	if _, ok := loadScanForCaller(w, r, h.Store, scanID, auth.GetUserFromContext(r.Context())); !ok {
		return
	}
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
