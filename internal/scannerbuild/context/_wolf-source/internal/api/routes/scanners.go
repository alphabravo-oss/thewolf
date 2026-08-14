package routes

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
	"github.com/alphabravocompany/thewolf/internal/scan/detector"
	"github.com/alphabravocompany/thewolf/internal/scan/planner"
	"github.com/alphabravocompany/thewolf/internal/scannertools/latest"
	"github.com/alphabravocompany/thewolf/internal/scannertools/manifest"
	scstatus "github.com/alphabravocompany/thewolf/internal/scannertools/status"
	"github.com/alphabravocompany/thewolf/internal/setup/scanners"
	"github.com/go-chi/chi/v5"
)

type scannerVersionStore interface {
	UpsertScannerVersionCheck(rctx context.Context, check *models.ScannerVersionCheck) error
	GetScannerVersionCheck(rctx context.Context, toolName string) (*models.ScannerVersionCheck, error)
	ListScannerVersionChecks(rctx context.Context) ([]models.ScannerVersionCheck, error)
}

const (
	scannerVersionCheckTimeout = 15 * time.Second
	scannerVersionSuccessTTL   = 24 * time.Hour
	scannerVersionFailureTTL   = time.Hour
)

// scannerSummary is the lightweight per-plugin payload returned by
// ScannersList for the UI's scanner-picker. Avoids the more elaborate
// shape of CollectionTools (which adds language detection and install
// hints — useful for collection setup but heavy for a multi-select).
type scannerSummary struct {
	Name      string   `json:"name"`
	Category  string   `json:"category"`
	Languages []string `json:"languages"`
}

type scannerPlanRequest struct {
	RepoID            string   `json:"repo_id,omitempty"`
	Languages         []string `json:"languages,omitempty"`
	Tools             []string `json:"tools,omitempty"`
	DisabledTools     []string `json:"disabled_tools,omitempty"`
	CheckAvailability bool     `json:"check_availability,omitempty"`
}

type scannerPlanResponse struct {
	planner.Result
	DetectionSource string   `json:"detection_source"`
	Languages       []string `json:"languages"`
}

// ScannersList returns every registered scanner plugin sorted by name.
// Used by the New-scan form to render a checkbox list so operators can
// pick exactly which tools to run.
//
// Route: GET /api/scanners/list
func ScannersList(w http.ResponseWriter, r *http.Request) {
	all := plugin.Global.GetAll()
	out := make([]scannerSummary, 0, len(all))
	for _, p := range all {
		langs := make([]string, 0, len(p.Languages()))
		for _, l := range p.Languages() {
			langs = append(langs, string(l))
		}
		out = append(out, scannerSummary{
			Name:      p.Name(),
			Category:  string(p.Category()),
			Languages: langs,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	response.WriteJSON(w, http.StatusOK, response.ListResponse{
		Data: out,
		Meta: response.ListMeta{Total: len(out), Page: 1, PerPage: len(out)},
	})
}

// ScannersPlan explains which scanners would run or skip for a requested scan.
//
// Route: POST /api/scanners/plan
func ScannersPlan(w http.ResponseWriter, r *http.Request) {
	h := DefaultHandler
	if h == nil {
		response.WriteError(w, http.StatusInternalServerError, "server_error", "handler not initialized")
		return
	}
	var req scannerPlanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.WriteError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}

	languages, source, err := scannerPlanLanguages(r.Context(), h, req)
	if err != nil {
		response.WriteError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	m, err := manifest.LoadDefault()
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "scanner_manifest_error", err.Error())
		return
	}
	reg := h.Registry
	if reg == nil {
		reg = plugin.Global
	}
	result := planner.Build(planner.Config{
		Registry:          reg,
		Manifest:          m,
		Languages:         languages,
		Tools:             req.Tools,
		DisabledTools:     req.DisabledTools,
		CheckAvailability: req.CheckAvailability,
	})
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: scannerPlanResponse{
		Result:          result,
		DetectionSource: source,
		Languages:       languageNames(languages),
	}})
}

func scannerPlanLanguages(ctx context.Context, h *Handler, req scannerPlanRequest) ([]models.Language, string, error) {
	if len(req.Languages) > 0 {
		return parsePlanLanguages(req.Languages), "request", nil
	}
	if req.RepoID == "" {
		return nil, "none", nil
	}
	repo, err := h.Store.GetRepoByID(ctx, req.RepoID)
	if err != nil {
		return nil, "", err
	}
	if repo.DetectedLanguages != "" {
		var counts map[string]int
		if json.Unmarshal([]byte(repo.DetectedLanguages), &counts) == nil && len(counts) > 0 {
			return languagesFromCounts(counts), "repo_cache", nil
		}
	}
	if repo.SourceType == models.SourceTypeLocal && repo.SourcePath != "" {
		det, err := detector.Detect(repo.SourcePath)
		if err == nil {
			return languagesFromModelCounts(det.Languages), "local_detection", nil
		}
	}
	return nil, "none", nil
}

// ScannersTools returns every scanner tool with manifest-backed metadata,
// configured image routing, and reproducibility flags.
//
// Route: GET /api/scanners/tools
func ScannersTools(w http.ResponseWriter, r *http.Request) {
	m, err := manifest.LoadDefault()
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "scanner_manifest_error", err.Error())
		return
	}
	cfg := container.Default()
	rows := scstatus.BuildWithChecksAndImages(m, cfg, scannerVersionChecksByTool(r.Context()), localScannerImagePresence(cfg))
	response.WriteJSON(w, http.StatusOK, response.ListResponse{
		Data: rows,
		Meta: response.ListMeta{Total: len(rows), Page: 1, PerPage: len(rows)},
	})
}

// ScannersTool returns manifest-backed metadata for one scanner tool.
//
// Route: GET /api/scanners/tools/{name}
func ScannersTool(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	m, err := manifest.LoadDefault()
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "scanner_manifest_error", err.Error())
		return
	}
	cfg := container.Default()
	row, ok := scstatus.Find(scstatus.BuildWithChecksAndImages(m, cfg, scannerVersionChecksByTool(r.Context()), localScannerImagePresence(cfg)), name)
	if !ok {
		response.WriteError(w, http.StatusNotFound, "scanner_tool_not_found", "scanner tool not found")
		return
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: row})
}

// ScannersCheckUpdates refreshes cached latest-version metadata for every
// manifest tool. It never changes scanner pins or configured images.
//
// Route: POST /api/scanners/tools/check-updates
func ScannersCheckUpdates(w http.ResponseWriter, r *http.Request) {
	m, err := manifest.LoadDefault()
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "scanner_manifest_error", err.Error())
		return
	}
	store, ok := scannerVersionCache()
	if !ok {
		response.WriteError(w, http.StatusServiceUnavailable, "scanner_version_cache_unavailable", "scanner version cache is not available")
		return
	}

	checker := latest.Checker{}
	force := scannerUpdateForce(r)
	out := make([]models.ScannerVersionCheck, 0, len(m.Tools))
	for _, name := range m.Names() {
		tool := m.Tools[name]
		if !force {
			if cached, ok := freshScannerVersionCheck(r.Context(), store, name, tool); ok {
				out = append(out, *cached)
				continue
			}
		}
		checkCtx, cancel := context.WithTimeout(r.Context(), scannerVersionCheckTimeout)
		check := checker.Check(checkCtx, name, tool)
		cancel()
		if err := store.UpsertScannerVersionCheck(r.Context(), &check); err != nil {
			response.WriteError(w, http.StatusInternalServerError, "scanner_version_cache_error", err.Error())
			return
		}
		out = append(out, check)
	}
	response.WriteJSON(w, http.StatusOK, response.ListResponse{
		Data: out,
		Meta: response.ListMeta{Total: len(out), Page: 1, PerPage: len(out)},
	})
}

// ScannersCheckUpdate refreshes cached latest-version metadata for one tool.
//
// Route: POST /api/scanners/tools/{name}/check-update
func ScannersCheckUpdate(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	m, err := manifest.LoadDefault()
	if err != nil {
		response.WriteError(w, http.StatusInternalServerError, "scanner_manifest_error", err.Error())
		return
	}
	tool, ok := m.Tools[name]
	if !ok {
		response.WriteError(w, http.StatusNotFound, "scanner_tool_not_found", "scanner tool not found")
		return
	}
	store, ok := scannerVersionCache()
	if !ok {
		response.WriteError(w, http.StatusServiceUnavailable, "scanner_version_cache_unavailable", "scanner version cache is not available")
		return
	}
	checkCtx, cancel := context.WithTimeout(r.Context(), scannerVersionCheckTimeout)
	check := latest.Checker{}.Check(checkCtx, name, tool)
	cancel()
	if err := store.UpsertScannerVersionCheck(r.Context(), &check); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "scanner_version_cache_error", err.Error())
		return
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: check})
}

func scannerVersionCache() (scannerVersionStore, bool) {
	if DefaultHandler == nil || DefaultHandler.Store == nil {
		return nil, false
	}
	store, ok := DefaultHandler.Store.(scannerVersionStore)
	return store, ok
}

func parsePlanLanguages(values []string) []models.Language {
	langs := make([]models.Language, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			langs = append(langs, models.Language(value))
		}
	}
	sort.Slice(langs, func(i, j int) bool { return langs[i] < langs[j] })
	return langs
}

func languagesFromCounts(counts map[string]int) []models.Language {
	langs := make([]models.Language, 0, len(counts))
	for value := range counts {
		if strings.TrimSpace(value) != "" {
			langs = append(langs, models.Language(value))
		}
	}
	sort.Slice(langs, func(i, j int) bool { return langs[i] < langs[j] })
	return langs
}

func languagesFromModelCounts(counts map[models.Language]int) []models.Language {
	langs := make([]models.Language, 0, len(counts))
	for value := range counts {
		if value != "" {
			langs = append(langs, value)
		}
	}
	sort.Slice(langs, func(i, j int) bool { return langs[i] < langs[j] })
	return langs
}

func languageNames(langs []models.Language) []string {
	names := make([]string, 0, len(langs))
	for _, lang := range langs {
		names = append(names, string(lang))
	}
	sort.Strings(names)
	return names
}

func scannerUpdateForce(r *http.Request) bool {
	if r.URL.Query().Get("force") == "true" || r.URL.Query().Get("force") == "1" {
		return true
	}
	if r.Body == nil {
		return false
	}
	var req struct {
		Force bool `json:"force"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return false
	}
	return req.Force
}

func freshScannerVersionCheck(ctx context.Context, store scannerVersionStore, name string, tool manifest.Tool) (*models.ScannerVersionCheck, bool) {
	check, err := store.GetScannerVersionCheck(ctx, name)
	if err != nil {
		return nil, false
	}
	if check == nil || check.PinnedVersion != tool.PinnedVersion || check.SourceType != tool.UpdateSource.Type {
		return nil, false
	}
	if scannerVersionCheckFresh(*check, time.Now().UTC()) {
		return check, true
	}
	return nil, false
}

func scannerVersionCheckFresh(check models.ScannerVersionCheck, now time.Time) bool {
	if check.CheckedAt.IsZero() {
		return false
	}
	ttl := scannerVersionSuccessTTL
	if check.Status == models.ScannerVersionCheckFailed {
		ttl = scannerVersionFailureTTL
	}
	return now.Sub(check.CheckedAt.UTC()) < ttl
}

func scannerVersionChecksByTool(ctx context.Context) map[string]models.ScannerVersionCheck {
	store, ok := scannerVersionCache()
	if !ok {
		return nil
	}
	checks, err := store.ListScannerVersionChecks(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return nil
	}
	out := make(map[string]models.ScannerVersionCheck, len(checks))
	for _, check := range checks {
		out[check.ToolName] = check
	}
	return out
}

func localScannerImagePresence(cfg *container.Config) map[string]bool {
	if cfg == nil {
		return nil
	}
	images := cfg.AllImages()
	out := make(map[string]bool, len(images))
	for _, image := range images {
		_, err := dockerImageDigest(image)
		out[image] = err == nil
	}
	return out
}

// ScannersConfig returns the live container.Config the wolf-slim process is
// using. Read-only — operators edit the config via wolf.yaml + env and
// restart wolf-slim.
//
// Route: GET /api/scanners/config
func ScannersConfig(w http.ResponseWriter, r *http.Request) {
	cfg := container.Default()
	if cfg == nil {
		response.WriteError(w, http.StatusServiceUnavailable, "scanners_not_configured",
			"container backend not initialized (was scanners.LoadAndInstall called at startup?)")
		return
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{
		Data: map[string]interface{}{
			"image":                   cfg.Image,
			"image_overrides":         cfg.ImageOverrides,
			"pull_policy":             cfg.PullPolicy.String(),
			"network":                 cfg.Network,
			"memory":                  cfg.Memory,
			"cpus":                    cfg.CPUs,
			"db_volume":               cfg.DBVolume,
			"host_repos_root":         cfg.HostReposRoot,
			"in_container_repos_root": cfg.InContainerReposRoot,
			"uid":                     cfg.UID,
			"gid":                     cfg.GID,
		},
	})
}

// ScannersDoctor runs the diagnostic checklist (scanners.Doctor) and
// returns a structured report. Maps the human-readable output to JSON
// suitable for the /scanners UI page.
//
// Route: POST /api/scanners/doctor
func ScannersDoctor(w http.ResponseWriter, r *http.Request) {
	var buf bytes.Buffer
	err := scanners.Doctor(r.Context(), &buf)

	// Re-parse the report. We capture the textual report and split it into
	// per-line entries — Doctor() writes lines like "OK <label>" or
	// "FAIL <label> <detail>".
	checks := parseDoctorReport(buf.String())

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{
		Data: map[string]interface{}{
			"overall_ok": err == nil,
			"checks":     checks,
		},
	})
}

type doctorCheck struct {
	Label  string `json:"label"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// parseDoctorReport extracts structured check rows from the human-readable
// report written by scanners.Doctor.
func parseDoctorReport(report string) []doctorCheck {
	var out []doctorCheck
	for _, line := range splitLines(report) {
		if line == "" {
			continue
		}
		if hasPrefix(line, "OK  ") || hasPrefix(line, "OK    ") {
			out = append(out, doctorCheck{Label: trimLeft(line[2:]), OK: true})
			continue
		}
		if hasPrefix(line, "FAIL  ") || hasPrefix(line, "FAIL    ") {
			rest := trimLeft(line[len("FAIL"):])
			label, detail := splitFirstWS(rest)
			out = append(out, doctorCheck{Label: label, OK: false, Detail: detail})
			continue
		}
		// Trailing summary lines like "doctor: all checks passed" — skip.
	}
	return out
}

// ScannersPull pre-pulls every image in the configured set
// (default + per-tool overrides).
//
// Route: POST /api/scanners/pull
func ScannersPull(w http.ResponseWriter, r *http.Request) {
	cfg := container.Default()
	if cfg == nil {
		response.WriteError(w, http.StatusServiceUnavailable, "scanners_not_configured",
			"container backend not initialized")
		return
	}

	imgs := cfg.AllImages()
	var pulled []string
	type pullErr struct {
		Image string `json:"image"`
		Error string `json:"error"`
	}
	var errs []pullErr

	for _, img := range imgs {
		sub := *cfg
		sub.Image = img
		sub.ImageOverrides = nil
		// Operator clicked 'Set up scanners' — bypass the configured
		// PullPolicy. The policy gates scan-time auto-pulls; an explicit
		// operator-initiated pull should always actually pull (or
		// surface a real error). Without this override, deployments
		// running PullPolicy=Never have a button that's always
		// rejected by the policy it's supposed to bootstrap past.
		sub.PullPolicy = container.PullAlways
		if err := container.EnsureImage(r.Context(), &sub); err != nil {
			errs = append(errs, pullErr{Image: img, Error: err.Error()})
			continue
		}
		pulled = append(pulled, img)
	}

	resp := map[string]interface{}{
		"pulled": pulled,
	}
	if len(errs) > 0 {
		resp["errors"] = errs
	}
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: resp})
}

// --- string helpers (kept inline to avoid pulling in strings just for prefix
// checks; the report format is tiny) ---

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func hasPrefix(s, p string) bool {
	return len(s) >= len(p) && s[:len(p)] == p
}

func trimLeft(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	return s
}

func splitFirstWS(s string) (head, rest string) {
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' || s[i] == '\t' {
			// Collapse subsequent whitespace.
			j := i
			for j < len(s) && (s[j] == ' ' || s[j] == '\t') {
				j++
			}
			return s[:i], s[j:]
		}
	}
	return s, ""
}

// imageStatus is one row in the scanner-images table the UI renders.
// digests are the OCI sha256:... values straight from the docker
// daemon and registry; truncate in the UI when displaying. updates_
// available is true iff both digests are known AND differ.
type imageStatus struct {
	Image            string `json:"image"`
	LocalDigest      string `json:"local_digest,omitempty"`
	RemoteDigest     string `json:"remote_digest,omitempty"`
	UpdatesAvailable bool   `json:"updates_available"`
	LocalError       string `json:"local_error,omitempty"`
	RemoteError      string `json:"remote_error,omitempty"`
}

// ScannersImages enumerates every configured scanner image and probes
// its local + remote digest in parallel. The UI uses this to surface
// "Update available" badges and a per-image pull button.
//
// Route: GET /api/scanners/images
func ScannersImages(w http.ResponseWriter, r *http.Request) {
	cfg := container.Default()
	if cfg == nil {
		response.WriteError(w, http.StatusServiceUnavailable, "scanners_not_configured",
			"container backend not initialized")
		return
	}
	imgs := cfg.AllImages()
	sort.Strings(imgs)

	// Probe each image's local + remote digest concurrently. Each probe
	// shells out to docker once; the inner timeouts are baked into the
	// helpers so a hung registry doesn't stall the whole response.
	results := make([]imageStatus, len(imgs))
	var wg sync.WaitGroup
	for i, img := range imgs {
		i, img := i, img
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := imageStatus{Image: img}
			// `docker image inspect` exits 1 when the image isn't present
			// locally — that's the 'not pulled yet' state, not an error.
			// Leave LocalDigest empty in that case; the UI renders an
			// 'not pulled' pill rather than 'error'.
			if d, err := dockerImageDigest(img); err == nil {
				s.LocalDigest = d
			}
			if d, err := dockerManifestDigest(img); err != nil {
				s.RemoteError = err.Error()
			} else {
				s.RemoteDigest = d
			}
			if s.LocalDigest != "" && s.RemoteDigest != "" && s.LocalDigest != s.RemoteDigest {
				s.UpdatesAvailable = true
			}
			results[i] = s
		}()
	}
	wg.Wait()

	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{Data: results})
}

// ScannersPullOne pulls a single image. Used by the per-image
// "Update" button in Settings → Scanners. The whole-set Pull
// (POST /api/scanners/pull) remains the "set up everything" path.
//
// Route: POST /api/scanners/images/pull
//
//	{"image": "alphabravodevops/wolf-scanners:2.0.0"}
func ScannersPullOne(w http.ResponseWriter, r *http.Request) {
	cfg := container.Default()
	if cfg == nil {
		response.WriteError(w, http.StatusServiceUnavailable, "scanners_not_configured",
			"container backend not initialized")
		return
	}
	var body struct {
		Image string `json:"image"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Image == "" {
		response.WriteError(w, http.StatusBadRequest, "validation_error", "image is required")
		return
	}
	// Sanity-check that the requested image is one we know about — refuse
	// to act as a generic 'docker pull' proxy. The user can configure new
	// images via wolf.yaml / env vars; the API only operates on the
	// resolved set.
	known := false
	for _, k := range cfg.AllImages() {
		if k == body.Image {
			known = true
			break
		}
	}
	if !known {
		response.WriteError(w, http.StatusBadRequest, "validation_error",
			"image is not in the configured scanner set")
		return
	}
	sub := *cfg
	sub.Image = body.Image
	sub.ImageOverrides = nil
	// Same rationale as ScannersPull: explicit operator-initiated pull
	// bypasses the configured PullPolicy. Without this, an operator on
	// a Never-policy deployment can't use the per-image Update button.
	sub.PullPolicy = container.PullAlways
	if err := container.EnsureImage(r.Context(), &sub); err != nil {
		response.WriteError(w, http.StatusInternalServerError, "pull_failed", err.Error())
		return
	}
	// Re-probe the new local digest so the UI can refresh without
	// firing another round-trip.
	d, _ := dockerImageDigest(body.Image)
	response.WriteJSON(w, http.StatusOK, response.SuccessResponse{
		Data: map[string]string{"image": body.Image, "local_digest": d},
	})
}

// dockerImageDigest returns the local content-addressable digest of a
// loaded docker image (the sha256: matching the multi-arch manifest
// index entry that we actually pulled for our platform). Returns "" if
// the image isn't present locally.
func dockerImageDigest(ref string) (string, error) {
	if _, err := exec.LookPath("docker"); err != nil {
		return "", err
	}
	// RepoDigests is the upstream registry digest stored at pull time.
	// Prefer this over Id (the local layer hash) because it's what the
	// registry advertises and what we compare against.
	out, err := exec.Command("docker", "image", "inspect", "--format", "{{range .RepoDigests}}{{.}}{{println}}{{end}}", ref).Output()
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if i := strings.Index(line, "@"); i >= 0 {
			return strings.TrimSpace(line[i+1:]), nil
		}
	}
	return "", nil
}

// dockerManifestDigest queries the registry for the current manifest
// digest for ref's tag and returns the digest of the manifest entry
// matching THIS host's platform (runtime.GOARCH). Without the platform
// filter, multi-arch images would always look like "update available"
// because the top-level manifest-list digest never matches any single
// per-platform digest. Doesn't pull — `docker manifest inspect` only
// fetches the small JSON.
func dockerManifestDigest(ref string) (string, error) {
	if _, err := exec.LookPath("docker"); err != nil {
		return "", err
	}
	out, err := exec.Command("docker", "manifest", "inspect", "--verbose", ref).Output()
	if err != nil {
		// Single-arch fallback. Some images have only one manifest
		// (no manifest-list); --verbose returns an empty array, but
		// the non-verbose call returns the image manifest directly.
		out, err = exec.Command("docker", "manifest", "inspect", ref).Output()
		if err != nil {
			return "", err
		}
		// Non-verbose, single manifest: top-level .config.digest is
		// the image layer-config sha. Use that as the comparison
		// anchor since we can't get a multi-arch index here.
		var asObj map[string]any
		if json.Unmarshal(out, &asObj) == nil {
			if d, ok := asObj["digest"].(string); ok {
				return d, nil
			}
			if cfg, ok := asObj["config"].(map[string]any); ok {
				if d, ok := cfg["digest"].(string); ok {
					return d, nil
				}
			}
		}
		return "", nil
	}
	// --verbose shape: array of
	//   { "Ref": "...", "Descriptor": {"digest": "sha256:...",
	//     "platform": {"architecture": "...", "os": "..."}}, ... }
	var entries []map[string]any
	if jerr := json.Unmarshal(out, &entries); jerr != nil || len(entries) == 0 {
		// Some daemons return a SINGLE object when the image isn't
		// multi-arch (no manifest-list). Fall through to the digest
		// at .Descriptor.digest.
		var single map[string]any
		if json.Unmarshal(out, &single) == nil {
			if desc, ok := single["Descriptor"].(map[string]any); ok {
				if d, ok := desc["digest"].(string); ok {
					return d, nil
				}
			}
		}
		return "", nil
	}
	// Match the current host's platform first. RepoDigests on the
	// local image is the per-arch digest, so we need to compare apples
	// to apples here.
	for _, e := range entries {
		desc, ok := e["Descriptor"].(map[string]any)
		if !ok {
			continue
		}
		plat, ok := desc["platform"].(map[string]any)
		if !ok {
			continue
		}
		archStr, _ := plat["architecture"].(string)
		osStr, _ := plat["os"].(string)
		if osStr == "linux" && archStr == runtime.GOARCH {
			if d, ok := desc["digest"].(string); ok {
				return d, nil
			}
		}
	}
	// No platform match — image probably single-arch or doesn't
	// support this arch. Return the first descriptor's digest as a
	// last-ditch comparison anchor.
	if desc, ok := entries[0]["Descriptor"].(map[string]any); ok {
		if d, ok := desc["digest"].(string); ok {
			return d, nil
		}
	}
	return "", nil
}
