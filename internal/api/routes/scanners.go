package routes

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os/exec"
	"sort"
	"strings"
	"sync"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
	"github.com/alphabravocompany/thewolf/internal/setup/scanners"
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
//	{"image": "alphabravodevops/wolf-scanners:latest"}
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
// digest for ref's tag. Doesn't pull — `docker manifest inspect` only
// fetches the small JSON. Errors are surfaced upstream so the UI can
// distinguish "registry unreachable" from "image not on disk".
func dockerManifestDigest(ref string) (string, error) {
	if _, err := exec.LookPath("docker"); err != nil {
		return "", err
	}
	out, err := exec.Command("docker", "manifest", "inspect", "--verbose", ref).Output()
	if err != nil {
		// Fallback: some daemons need experimental manifest enabled.
		// Try the non-verbose form which buildkit supports natively.
		out, err = exec.Command("docker", "manifest", "inspect", ref).Output()
		if err != nil {
			return "", err
		}
	}
	// Two shapes:
	//   - non-verbose: {"manifests":[...], "mediaType":"...", ...}
	//   - --verbose:   [{"Ref":"...", "Descriptor":{"digest":"sha256:..."}}, ...]
	var asArray []map[string]any
	if json.Unmarshal(out, &asArray) == nil && len(asArray) > 0 {
		if desc, ok := asArray[0]["Descriptor"].(map[string]any); ok {
			if d, ok := desc["digest"].(string); ok {
				return d, nil
			}
		}
	}
	var asObj map[string]any
	if json.Unmarshal(out, &asObj) == nil {
		// Top-level digest lives at .config.digest for image manifests,
		// or .digest on the list. Try both.
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
