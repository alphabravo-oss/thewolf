package routes

import (
	"encoding/json"
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
}

// ScanPreflight reports which of the scanners a scan WOULD run are missing
// their container image locally, so the UI can prompt the user to pull them
// before starting instead of discovering the failures mid-scan (the runner
// uses --pull never by design). It resolves the tool set exactly the way the
// runner does (runner.SelectTools), using the repo's cached language detection
// for the auto-detect case so no clone is required — this is a fast estimate.
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

	var languages []models.Language
	var langCounts map[string]int
	if repo.DetectedLanguages != "" {
		_ = json.Unmarshal([]byte(repo.DetectedLanguages), &langCounts)
	}
	for language := range langCounts {
		languages = append(languages, models.Language(language))
	}
	sort.Slice(languages, func(i, j int) bool { return languages[i] < languages[j] })

	selectedTools := req.Tools
	toolsExplicit := len(req.Tools) > 0
	if req.Profile == "full" || len(req.Categories) > 0 {
		selectedTools = toolsForProfile(h, createReq, languages)
		toolsExplicit = true
	}
	cfgRun := runner.RunConfig{
		Registry:      h.Registry,
		Tools:         selectedTools,
		ToolsExplicit: toolsExplicit,
		DisabledTools: req.DisabledTools,
	}
	// Auto-detect (no explicit tools, not all-scanners): use cached languages
	// so the estimate matches what the scan would select, without cloning.
	if !toolsExplicit && !req.AllScanners {
		cfgRun.Languages = languages
	}
	plugins := runner.SelectTools(cfgRun)

	cc := container.Default()
	if cc == nil {
		// No container backend: nothing to pre-pull, report empty.
		response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: scanPreflightResponse{
			Missing: []missingImage{}, ToolCount: len(plugins),
		}})
		return
	}

	// Map each selected tool to its image, then probe each UNIQUE image once
	// (many tools share the bundled wolf-scanners image) concurrently.
	toolImage := make(map[string]string, len(plugins))
	uniq := make(map[string]bool)
	for _, p := range plugins {
		img := cc.ImageFor(p.Name())
		if img == "" {
			continue
		}
		toolImage[p.Name()] = img
		uniq[img] = true
	}
	present := make(map[string]bool, len(uniq))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for img := range uniq {
		img := img
		wg.Add(1)
		go func() {
			defer wg.Done()
			// dockerImageDigest returns an error when the image is absent.
			_, derr := dockerImageDigest(img)
			mu.Lock()
			present[img] = derr == nil
			mu.Unlock()
		}()
	}
	wg.Wait()

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
		ToolCount:    len(plugins),
	}})
}
