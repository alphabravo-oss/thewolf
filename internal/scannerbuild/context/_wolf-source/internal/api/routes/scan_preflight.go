package routes

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
	"github.com/alphabravocompany/thewolf/internal/scan/runner"
)

// missingImageMarker is the stable substring the container runner emits when a
// tool's image isn't present locally (container.CommandContext). executeScan
// reclassifies such a "failure" as a skip — the tool didn't run because its
// image needs pulling, which is actionable, not a real tool error.
const missingImageMarker = "is not present locally"

// isMissingImageErr reports whether a tool's error is just "image not pulled".
func isMissingImageErr(msg string) bool {
	return strings.Contains(msg, missingImageMarker)
}

// scanPreflightRequest mirrors the parts of a scan-create request that
// determine which scanners run, so the preflight resolves the same tool set.
type scanPreflightRequest struct {
	RepoID        string   `json:"repo_id"`
	Profile       string   `json:"profile,omitempty"`
	Categories    []string `json:"categories,omitempty"`
	Tools         []string `json:"tools,omitempty"`
	AllScanners   bool     `json:"all_scanners,omitempty"`
	DisabledTools []string `json:"disabled_tools,omitempty"`
	IncludePaths  []string `json:"include_paths,omitempty"`
	ExcludePaths  []string `json:"exclude_paths,omitempty"`
}

// missingImage is one scanner whose container image is not present locally.
type missingImage struct {
	Tool  string `json:"tool"`
	Image string `json:"image"`
}

type scanPreflightResponse struct {
	Missing      []missingImage `json:"missing"`
	MissingCount int            `json:"missing_count"`
	ToolCount    int            `json:"tool_count"`
	Tools        []string       `json:"tools"`
}

// ScanPreflight reports which of the scanners a scan WOULD run are missing
// their container image locally, so the UI can prompt the user to pull them
// before starting instead of discovering the failures mid-scan (the runner
// uses --pull never by design). It resolves the tool set exactly the way the
// runner does (runner.SelectTools), detecting the current local tree before
// falling back to cached languages for sources that require preparation.
//
// Route: POST /api/v1/scans/preflight
func ScanPreflight(w http.ResponseWriter, r *http.Request) {
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

	var req scanPreflightRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if req.RepoID == "" {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "repo_id is required")
		return
	}
	createReq := createScanRequest{
		RepoID:        req.RepoID,
		Profile:       req.Profile,
		Categories:    req.Categories,
		Tools:         req.Tools,
		DisabledTools: req.DisabledTools,
		IncludePaths:  req.IncludePaths,
		ExcludePaths:  req.ExcludePaths,
	}
	if err := validateScanRequestSelectors(h, &createReq); err != nil {
		response.WriteError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	repo, ok := loadRepoForCaller(w, r, h.Store, req.RepoID, claims)
	if !ok {
		return
	}
	applyScanProfile(h, &createReq, repo.ID)

	languages, _, err := scannerPlanLanguages(r.Context(), h, scannerPlanRequest{RepoID: repo.ID})
	if err != nil {
		response.WriteError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	repoPath := ""
	if repo.SourceType == models.SourceTypeLocal {
		repoPath = repo.SourcePath
	}
	plugins := runner.SelectTools(scanSelectionConfig(h, createReq, languages, repoPath))
	tools := make([]string, 0, len(plugins))
	for _, p := range plugins {
		// Execution checks availability before publishing its selected list.
		if p.CheckAvailable() {
			tools = append(tools, p.Name())
		}
	}
	sort.Strings(tools)

	cc := container.Default()
	if cc == nil {
		// No container backend: nothing to pre-pull, report empty.
		response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: scanPreflightResponse{
			Missing: []missingImage{}, ToolCount: len(tools), Tools: tools,
		}})
		return
	}

	// Map each selected tool to its image, then probe each UNIQUE image once
	// (many tools share the bundled wolf-scanners image) concurrently.
	toolImage := make(map[string]string, len(plugins))
	uniq := make(map[string]bool)
	for _, tool := range tools {
		img := cc.ImageFor(tool)
		if img == "" {
			continue
		}
		toolImage[tool] = img
		uniq[img] = true
	}
	present := make(map[string]bool, len(uniq))
	var probeErr error
	var mu sync.Mutex
	var wg sync.WaitGroup
	for img := range uniq {
		img := img
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Probe the same Docker endpoint/configuration used for execution.
			_, derr := container.LocalImageDigest(r.Context(), cc, img)
			mu.Lock()
			present[img] = derr == nil
			if derr != nil && !errors.Is(derr, container.ErrImageMissing) {
				probeErr = derr
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	if probeErr != nil {
		response.WriteError(w, http.StatusServiceUnavailable, "readiness_unavailable", "scanner image readiness could not be checked; verify Docker connectivity")
		return
	}

	missing := make([]missingImage, 0)
	for tool, img := range toolImage {
		if !present[img] {
			missing = append(missing, missingImage{Tool: tool, Image: img})
		}
	}
	sort.Slice(missing, func(i, j int) bool { return missing[i].Tool < missing[j].Tool })

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: scanPreflightResponse{
		Missing:      missing,
		MissingCount: len(missing),
		ToolCount:    len(tools),
		Tools:        tools,
	}})
}

// scanSelectionConfig is shared by readiness and execution. Source detection
// and profile policy feed the same runner selector, including explicit empty
// category selections and repository-specific applicability checks.
func scanSelectionConfig(h *Handler, req createScanRequest, languages []models.Language, repoPath string) runner.RunConfig {
	return runner.RunConfig{
		Registry:      h.Registry,
		RepoPath:      repoPath,
		Tools:         toolsForProfile(h, req, languages),
		ToolsExplicit: req.Profile == "full" || len(req.Categories) > 0 || len(req.Tools) > 0,
		Languages:     languages,
		DisabledTools: req.DisabledTools,
	}
}
