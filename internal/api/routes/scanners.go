package routes

import (
	"bytes"
	"net/http"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
	"github.com/alphabravocompany/thewolf/internal/setup/scanners"
)

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
