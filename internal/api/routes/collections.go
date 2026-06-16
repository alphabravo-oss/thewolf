package routes

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/artifacts"
	"github.com/alphabravocompany/thewolf/internal/auth"
	gitpkg "github.com/alphabravocompany/thewolf/internal/fix/git"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/scan/detector"
	"github.com/alphabravocompany/thewolf/internal/setup"
	"github.com/alphabravocompany/thewolf/internal/wolflog"
)

type createCollectionRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	ScanConfig  string `json:"scan_config,omitempty"`
}

type updateCollectionRequest struct {
	// Pointer fields so the client can distinguish "leave alone"
	// (omitted) from "set to empty" (explicit ""). Previously the
	// handler couldn't tell the difference, making it impossible to
	// clear a description from the UI.
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	ScanConfig  *string `json:"scan_config,omitempty"`
}

type addRepoToCollectionRequest struct {
	RepoID string `json:"repo_id"`
}

func ListCollections(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "not authenticated")
		return
	}
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}

	var (
		cols []models.Collection
		err  error
	)
	if fleetModeEnabled(r.Context(), h.Store) {
		cols, err = h.Store.ListAllCollections(r.Context())
	} else {
		cols, err = h.Store.ListCollectionsByUser(r.Context(), claims.UserID)
	}
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to list collections")
		return
	}

	response.WriteJSON(w, http.StatusOK, response.ListResponse{
		Data: cols,
		Meta: response.ListMeta{Total: len(cols), Page: 1, PerPage: len(cols)},
	})
}

func CreateCollection(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "not authenticated")
		return
	}
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}

	var req createCollectionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}

	if req.Name == "" {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "name is required")
		return
	}

	// Enforce per-user name uniqueness so users can't end up with two
	// collections called "Backend" that they can't tell apart in the
	// UI. The DB has no constraint for this (the migration would need
	// to be applied to existing rows), so we do a runtime check.
	if existing, _ := h.Store.ListCollectionsByUser(r.Context(), claims.UserID); existing != nil {
		for _, c := range existing {
			if strings.EqualFold(c.Name, req.Name) {
				response.WriteError(w, http.StatusConflict, "name_taken", "a collection with that name already exists")
				return
			}
		}
	}

	scanConfig := req.ScanConfig
	if scanConfig == "" {
		scanConfig = "{}"
	}

	now := time.Now()
	col := &models.Collection{
		ID:          uuid.New().String(),
		UserID:      claims.UserID,
		Name:        req.Name,
		Description: req.Description,
		ScanConfig:  scanConfig,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := h.Store.CreateCollection(r.Context(), col); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to create collection")
		return
	}

	response.WriteJSON(w, http.StatusCreated, response.SuccessResponse{Data: col})
}

func GetCollection(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "not authenticated")
		return
	}
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}

	id := chi.URLParam(r, "id")
	col, err := h.Store.GetCollectionByID(r.Context(), id)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", "collection not found")
		return
	}

	repos, _ := h.Store.ListReposInCollection(r.Context(), id)

	// Build repo lookup for populating scan.Repo.
	repoMap := make(map[string]*models.Repo, len(repos))
	for i := range repos {
		repoMap[repos[i].ID] = &repos[i]
	}

	// Fetch recent scans for this collection, filtered to currently linked repos.
	allScans, _ := h.Store.ListScansByCollection(r.Context(), id)
	recentScans := make([]models.Scan, 0, len(allScans))
	for i := range allScans {
		if _, linked := repoMap[allScans[i].RepoID]; linked {
			allScans[i].Repo = repoMap[allScans[i].RepoID]
			recentScans = append(recentScans, allScans[i])
		}
	}

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{
		Data: map[string]interface{}{
			"collection":   col,
			"repos":        repos,
			"scans":        recentScans,
			"recent_scans": recentScans, // backwards compat
		},
	})
}

func UpdateCollection(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "not authenticated")
		return
	}
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}

	id := chi.URLParam(r, "id")
	col, err := h.Store.GetCollectionByID(r.Context(), id)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", "collection not found")
		return
	}
	if !canModifyOwned(claims, col.UserID) {
		response.WriteError(w, http.StatusForbidden, "forbidden", "you can only modify collections you created")
		return
	}

	var req updateCollectionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}

	if req.Name != nil && *req.Name != "" && *req.Name != col.Name {
		// Same per-user name-uniqueness rule as CreateCollection.
		if existing, _ := h.Store.ListCollectionsByUser(r.Context(), claims.UserID); existing != nil {
			for _, c := range existing {
				if c.ID == col.ID {
					continue
				}
				if strings.EqualFold(c.Name, *req.Name) {
					response.WriteError(w, http.StatusConflict, "name_taken", "a collection with that name already exists")
					return
				}
			}
		}
		col.Name = *req.Name
	}
	if req.Description != nil {
		col.Description = *req.Description
	}
	if req.ScanConfig != nil && *req.ScanConfig != "" {
		col.ScanConfig = *req.ScanConfig
	}
	col.UpdatedAt = time.Now()

	if err := h.Store.UpdateCollection(r.Context(), col); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to update collection")
		return
	}

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: col})
}

func DeleteCollection(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "not authenticated")
		return
	}
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}

	id := chi.URLParam(r, "id")
	col, err := h.Store.GetCollectionByID(r.Context(), id)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", "collection not found")
		return
	}
	if !canModifyOwned(claims, col.UserID) {
		response.WriteError(w, http.StatusForbidden, "forbidden", "you can only delete collections you created")
		return
	}

	// Folder model: move this collection's repos to the owner's Default
	// collection first, so deleting a collection never strands a repo as an
	// unreachable orphan. (Skipped when deleting the Default collection itself.)
	if defID, derr := ensureDefaultCollection(r.Context(), h.Store, col.UserID); derr == nil && defID != id {
		if repos, lerr := h.Store.ListReposInCollection(r.Context(), id); lerr == nil {
			for i := range repos {
				_ = h.Store.SetRepoCollection(r.Context(), repos[i].ID, defID)
			}
		}
	}

	scanIDs, err := h.Store.DeleteCollectionCascade(r.Context(), id)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to delete collection")
		return
	}

	// Clean up artifact files on disk.
	if len(scanIDs) > 0 {
		go artifacts.Global.DeleteScans(scanIDs)
	}

	wolflog.Info().Str("collection_id", id).Str("collection_name", col.Name).Int("scans_deleted", len(scanIDs)).Msg("collection deleted with cascade")

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: map[string]interface{}{
		"message":       "collection deleted",
		"scans_deleted": len(scanIDs),
	}})
}

func AddRepoToCollection(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "not authenticated")
		return
	}
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}

	collectionID := chi.URLParam(r, "id")
	col, err := h.Store.GetCollectionByID(r.Context(), collectionID)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", "collection not found")
		return
	}
	if !canModifyOwned(claims, col.UserID) {
		response.WriteError(w, http.StatusForbidden, "forbidden", "you can only modify collections you created")
		return
	}

	var req addRepoToCollectionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if req.RepoID == "" {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "repo_id is required")
		return
	}

	// Folder model: a repo belongs to exactly one collection, so adding it here
	// moves it out of whatever collection it was in (including Default).
	if err := h.Store.SetRepoCollection(r.Context(), req.RepoID, collectionID); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to add repo to collection")
		return
	}

	// Trigger detection if not already cached.
	repo, err := h.Store.GetRepoByID(r.Context(), req.RepoID)
	if err == nil && repo.DetectedAt == nil {
		go runDetection(h.Store, repo.ID, repo.SourcePath)
	}

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: map[string]string{"message": "repo added to collection"}})
}

func RemoveRepoFromCollection(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "not authenticated")
		return
	}
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}

	collectionID := chi.URLParam(r, "id")
	col, err := h.Store.GetCollectionByID(r.Context(), collectionID)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", "collection not found")
		return
	}
	if !canModifyOwned(claims, col.UserID) {
		response.WriteError(w, http.StatusForbidden, "forbidden", "you can only modify collections you created")
		return
	}

	repoID := chi.URLParam(r, "repoId")
	if err := h.Store.RemoveRepoFromCollection(r.Context(), collectionID, repoID); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to remove repo from collection")
		return
	}

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: map[string]string{"message": "repo removed from collection"}})
}

// ToolInfo describes a tool auto-detected for a collection.
type ToolInfo struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Category    string   `json:"category"`
	Languages   []string `json:"languages"`
	Available   bool     `json:"available"`
	Installable bool     `json:"installable"`
	InstallHint string   `json:"install_hint,omitempty"`
	Enabled     bool     `json:"enabled"`
	Recommended bool     `json:"recommended"`
}

// RepoDetection summarises the detection results for a single repository.
type RepoDetection struct {
	RepoID        string         `json:"repo_id"`
	RepoName      string         `json:"repo_name"`
	Languages     map[string]int `json:"languages"`
	Frameworks    []string       `json:"frameworks"`
	TotalFiles    int            `json:"total_files"`
	SourceFiles   int            `json:"source_files"`
	TestFiles     int            `json:"test_files"`
	Branches      []string       `json:"branches"`
	DefaultBranch string         `json:"default_branch"`
	CurrentBranch string         `json:"current_branch"`
}

// CollectionToolsResponse wraps tools together with per-repo detection data.
type CollectionToolsResponse struct {
	Tools       []ToolInfo      `json:"tools"`
	RepoSummary []RepoDetection `json:"repo_summary"`
}

// CollectionTools handles GET /api/collections/{id}/tools — returns auto-detected tools for a collection.
func CollectionTools(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "not authenticated")
		return
	}
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}

	collectionID := chi.URLParam(r, "id")
	col, err := h.Store.GetCollectionByID(r.Context(), collectionID)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", "collection not found")
		return
	}

	// Parse collection's scan config to get disabled_tools.
	var scanConfig models.ScanConfig
	if col.ScanConfig != "" && col.ScanConfig != "{}" {
		_ = json.Unmarshal([]byte(col.ScanConfig), &scanConfig)
	}
	disabledSet := make(map[string]bool, len(scanConfig.DisabledTools))
	for _, name := range scanConfig.DisabledTools {
		disabledSet[name] = true
	}

	// Always run live detection so tool recommendations reflect the current repo state.
	repos, _ := h.Store.ListReposInCollection(r.Context(), collectionID)
	langSet := make(map[models.Language]bool)
	var repoSummary []RepoDetection
	for _, repo := range repos {
		result, err := detector.Detect(repo.SourcePath)
		if err != nil {
			repoSummary = append(repoSummary, RepoDetection{
				RepoID:        repo.ID,
				RepoName:      repo.Name,
				Branches:      []string{},
				DefaultBranch: repo.DefaultBranch,
			})
			continue
		}
		for lang := range result.Languages {
			langSet[lang] = true
		}
		langs := make(map[string]int, len(result.Languages))
		for lang, count := range result.Languages {
			langs[string(lang)] = count
		}
		frameworks := result.Frameworks
		if frameworks == nil {
			frameworks = []string{}
		}
		// List available branches for branch selection in the scan dialog.
		branches, _ := gitpkg.ListBranches(repo.SourcePath)
		if branches == nil {
			branches = []string{}
		}
		sort.Strings(branches)
		currentBranch, _ := gitpkg.CurrentBranch(repo.SourcePath)

		repoSummary = append(repoSummary, RepoDetection{
			RepoID:        repo.ID,
			RepoName:      repo.Name,
			Languages:     langs,
			Frameworks:    frameworks,
			TotalFiles:    result.TotalFiles,
			SourceFiles:   len(result.SourceFiles),
			TestFiles:     len(result.TestFiles),
			Branches:      branches,
			DefaultBranch: repo.DefaultBranch,
			CurrentBranch: currentBranch,
		})
		// Update the cache in the background so other views stay current.
		go runDetection(h.Store, repo.ID, repo.SourcePath)
	}

	// Detect available package managers once for install hints.
	prereqs := setup.DetectPrereqs()

	// buildToolInfo constructs a ToolInfo for a given plugin.
	buildToolInfo := func(p models.Plugin, recommended bool) ToolInfo {
		langs := make([]string, len(p.Languages()))
		for i, l := range p.Languages() {
			langs[i] = string(l)
		}
		installable := false
		installHint := ""
		description := ""
		if td, ok := setup.GetTool(p.Name()); ok {
			description = td.Description
			if len(td.InstallMethods) > 0 {
				_, canInstall := setup.BestMethod(td, prereqs)
				installable = canInstall
				if !canInstall {
					// Has install methods but prerequisites are missing — tell the user what to install.
					needed := make(map[string]bool)
					for _, m := range td.InstallMethods {
						if m.Requires != "" {
							needed[m.Requires] = true
						}
					}
					if len(needed) > 0 {
						names := make([]string, 0, len(needed))
						for n := range needed {
							names = append(names, n)
						}
						sort.Strings(names)
						installHint = "Requires: " + strings.Join(names, " or ")
					}
				}
			}
		}
		return ToolInfo{
			Name:        p.Name(),
			Description: description,
			Category:    string(p.Category()),
			Languages:   langs,
			Available:   p.CheckAvailable(),
			Installable: installable,
			InstallHint: installHint,
			Enabled:     !disabledSet[p.Name()],
			Recommended: recommended,
		}
	}

	// Collect plugins matching detected languages (deduped) — these are "recommended".
	seen := make(map[string]bool)
	var tools []ToolInfo
	for lang := range langSet {
		for _, p := range h.Registry.GetByLanguage(lang) {
			if seen[p.Name()] {
				continue
			}
			seen[p.Name()] = true
			tools = append(tools, buildToolInfo(p, true))
		}
	}

	// Add remaining plugins as "additional" (not recommended).
	for _, p := range h.Registry.GetAll() {
		if seen[p.Name()] {
			continue
		}
		seen[p.Name()] = true
		tools = append(tools, buildToolInfo(p, false))
	}

	// Sort by category then name.
	sort.Slice(tools, func(i, j int) bool {
		if tools[i].Category != tools[j].Category {
			return tools[i].Category < tools[j].Category
		}
		return tools[i].Name < tools[j].Name
	})

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{
		Data: CollectionToolsResponse{
			Tools:       tools,
			RepoSummary: repoSummary,
		},
	})
}

// ---------------------------------------------------------------------------
// Collection Metrics
// ---------------------------------------------------------------------------

// metricsSnapshot holds the current-state aggregation of a collection.
type metricsSnapshot struct {
	TotalFindings   int                 `json:"total_findings"`
	BySeverity      map[string]int      `json:"by_severity"`
	ByStatus        map[string]int      `json:"by_status"`
	ReposScanned    int                 `json:"repos_scanned"`
	BranchesScanned int                 `json:"branches_scanned"`
	LatestScans     []latestScanSummary `json:"latest_scans"`
}

type latestScanSummary struct {
	RepoID       string         `json:"repo_id"`
	RepoName     string         `json:"repo_name"`
	Branch       string         `json:"branch"`
	ScanID       string         `json:"scan_id"`
	FindingCount int            `json:"finding_count"`
	CompletedAt  string         `json:"completed_at"`
	BySeverity   map[string]int `json:"by_severity"`
}

type resolutionRate struct {
	TotalUniqueFingerprints int     `json:"total_unique_fingerprints"`
	Resolved                int     `json:"resolved"`
	Open                    int     `json:"open"`
	Triaged                 int     `json:"triaged"`
	Suppressed              int     `json:"suppressed"`
	Rate                    float64 `json:"rate"`
}

type collectionMetricsResponse struct {
	Snapshot       metricsSnapshot `json:"snapshot"`
	Trends         []trendEntry    `json:"trends"`
	ResolutionRate resolutionRate  `json:"resolution_rate"`
	Branches       []string        `json:"branches"`
}

// CollectionMetrics handles GET /api/collections/{id}/metrics.
func CollectionMetrics(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserFromContext(r.Context())
	if claims == nil {
		response.WriteError(w, http.StatusUnauthorized, "unauthorized", "not authenticated")
		return
	}
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}

	collectionID := chi.URLParam(r, "id")
	_, err := h.Store.GetCollectionByID(r.Context(), collectionID)
	if err != nil {
		response.WriteError(w, http.StatusNotFound, "not_found", "collection not found")
		return
	}

	ctx := r.Context()
	branchFilter := r.URL.Query().Get("branch")

	// Build repo lookup for names — used to filter scans to currently linked repos only.
	repos, _ := h.Store.ListReposInCollection(ctx, collectionID)
	repoNames := make(map[string]string, len(repos))
	linkedRepoIDs := make(map[string]bool, len(repos))
	for _, repo := range repos {
		repoNames[repo.ID] = repo.Name
		linkedRepoIDs[repo.ID] = true
	}

	// 1. Load all scans for this collection, filter to completed, currently-linked repos,
	// and optionally by branch. Scans from repos that were unlinked are excluded.
	allScans, err := h.Store.ListScansByCollection(ctx, collectionID)
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "failed to load scans")
		return
	}

	var completedScans []models.Scan
	for _, s := range allScans {
		if !linkedRepoIDs[s.RepoID] {
			continue // skip scans from repos no longer linked to this collection
		}
		if s.Status != models.ScanStatusCompleted {
			continue
		}
		if branchFilter != "" && s.Branch != branchFilter {
			continue
		}
		completedScans = append(completedScans, s)
	}

	// Collect all unique branches from linked-repo scans (before branch filter) for the branch picker.
	branchSet := make(map[string]bool)
	for _, s := range allScans {
		if linkedRepoIDs[s.RepoID] && s.Branch != "" {
			branchSet[s.Branch] = true
		}
	}
	allBranches := make([]string, 0, len(branchSet))
	for b := range branchSet {
		allBranches = append(allBranches, b)
	}
	sort.Strings(allBranches)

	// 2. Build latest scan per (repo_id, branch).
	type repoBranch struct{ repoID, branch string }
	latestMap := make(map[repoBranch]*models.Scan)
	for i := range completedScans {
		s := &completedScans[i]
		key := repoBranch{s.RepoID, s.Branch}
		existing, ok := latestMap[key]
		if !ok || (s.CompletedAt != nil && (existing.CompletedAt == nil || s.CompletedAt.After(*existing.CompletedAt))) {
			latestMap[key] = s
		}
	}

	// 3. Load findings for each latest scan, aggregate snapshot.
	bySeverity := map[string]int{"critical": 0, "high": 0, "medium": 0, "low": 0, "info": 0}
	byStatus := map[string]int{"open": 0, "fixed": 0, "wont_fix": 0, "false_positive": 0}
	totalFindings := 0
	repoSet := make(map[string]bool)
	var latestScanSummaries []latestScanSummary

	// Track all fingerprints in latest scans for resolution rate.
	latestFingerprints := make(map[string]models.Status)

	for key, scan := range latestMap {
		findings, err := h.Store.ListFindingsByScan(ctx, scan.ID)
		if err != nil {
			continue
		}

		repoSet[key.repoID] = true
		scanSev := map[string]int{"critical": 0, "high": 0, "medium": 0, "low": 0, "info": 0}

		for _, f := range findings {
			totalFindings++
			sev := string(f.Severity)
			bySeverity[sev]++
			scanSev[sev]++
			st := string(f.Status)
			byStatus[st]++
			latestFingerprints[f.Fingerprint] = f.Status
		}

		completedAt := ""
		if scan.CompletedAt != nil {
			completedAt = scan.CompletedAt.Format(time.RFC3339)
		}

		latestScanSummaries = append(latestScanSummaries, latestScanSummary{
			RepoID:       key.repoID,
			RepoName:     repoNames[key.repoID],
			Branch:       key.branch,
			ScanID:       scan.ID,
			FindingCount: len(findings),
			CompletedAt:  completedAt,
			BySeverity:   scanSev,
		})
	}

	// Sort latest scan summaries by repo name then branch.
	sort.Slice(latestScanSummaries, func(i, j int) bool {
		if latestScanSummaries[i].RepoName != latestScanSummaries[j].RepoName {
			return latestScanSummaries[i].RepoName < latestScanSummaries[j].RepoName
		}
		return latestScanSummaries[i].Branch < latestScanSummaries[j].Branch
	})

	snapshot := metricsSnapshot{
		TotalFindings:   totalFindings,
		BySeverity:      bySeverity,
		ByStatus:        byStatus,
		ReposScanned:    len(repoSet),
		BranchesScanned: len(latestMap),
		LatestScans:     latestScanSummaries,
	}
	if snapshot.LatestScans == nil {
		snapshot.LatestScans = []latestScanSummary{}
	}

	// 4. Compute resolution rate from unique fingerprints.
	resolved := 0
	open := 0
	triaged := 0
	suppressed := 0
	for _, status := range latestFingerprints {
		switch status {
		case models.StatusFixed:
			resolved++
		case models.StatusOpen:
			open++
		case models.StatusWontFix:
			triaged++
		case models.StatusFalsePositive:
			suppressed++
		}
	}
	totalUnique := len(latestFingerprints)
	rate := 0.0
	if totalUnique > 0 {
		rate = float64(resolved) / float64(totalUnique)
	}

	resolution := resolutionRate{
		TotalUniqueFingerprints: totalUnique,
		Resolved:                resolved,
		Open:                    open,
		Triaged:                 triaged,
		Suppressed:              suppressed,
		Rate:                    rate,
	}

	// 5. Compute branch-aware trends: replay "latest per (repo,branch)" at each date.
	trends := computeBranchAwareTrends(ctx, h, completedScans)

	resp := collectionMetricsResponse{
		Snapshot:       snapshot,
		Trends:         trends,
		ResolutionRate: resolution,
		Branches:       allBranches,
	}

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: resp})
}

// computeBranchAwareTrends sorts completed scans chronologically, maintains a running
// latest-per-(repo,branch) map, and emits aggregate severity counts at each date boundary.
// Capped at 90 days of history.
func computeBranchAwareTrends(ctx context.Context, h *Handler, completedScans []models.Scan) []trendEntry {
	if len(completedScans) == 0 {
		return []trendEntry{}
	}

	cutoff := time.Now().AddDate(0, 0, -90)

	// Sort scans chronologically by completed_at.
	sorted := make([]models.Scan, len(completedScans))
	copy(sorted, completedScans)
	sort.Slice(sorted, func(i, j int) bool {
		ti := sorted[i].CompletedAt
		tj := sorted[j].CompletedAt
		if ti == nil && tj == nil {
			return false
		}
		if ti == nil {
			return true
		}
		if tj == nil {
			return false
		}
		return ti.Before(*tj)
	})

	// Preload findings for all scans within the cutoff window.
	scanFindings := make(map[string][]models.Finding, len(sorted))
	for _, s := range sorted {
		if s.CompletedAt == nil || s.CompletedAt.Before(cutoff) {
			continue
		}
		findings, err := h.Store.ListFindingsByScan(ctx, s.ID)
		if err != nil {
			continue
		}
		scanFindings[s.ID] = findings
	}

	type repoBranchKey struct{ repoID, branch string }

	// Replay scans chronologically. At each date boundary, snapshot the aggregate.
	runningLatest := make(map[repoBranchKey]string) // key -> scan ID
	dateEntries := make(map[string]*trendSeverityCounts)

	for _, s := range sorted {
		if s.CompletedAt == nil || s.CompletedAt.Before(cutoff) {
			continue
		}

		key := repoBranchKey{s.RepoID, s.Branch}
		runningLatest[key] = s.ID
		date := s.CompletedAt.Format("2006-01-02")

		// Re-aggregate from all current "latest" scans for this date.
		counts := &trendSeverityCounts{}
		for _, scanID := range runningLatest {
			for _, f := range scanFindings[scanID] {
				counts.Total++
				switch f.Severity {
				case models.SeverityCritical:
					counts.Critical++
				case models.SeverityHigh:
					counts.High++
				case models.SeverityMedium:
					counts.Medium++
				case models.SeverityLow:
					counts.Low++
				case models.SeverityInfo:
					counts.Info++
				}
			}
		}
		dateEntries[date] = counts
	}

	trends := make([]trendEntry, 0, len(dateEntries))
	for date, counts := range dateEntries {
		trends = append(trends, trendEntry{Date: date, Counts: *counts})
	}
	sort.Slice(trends, func(i, j int) bool {
		return trends[i].Date < trends[j].Date
	})

	return trends
}
