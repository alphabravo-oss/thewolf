package report

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
)

// ScanDirName builds a human-readable, sortable, unique directory name for a
// scan run: "<repo-basename>_<YYYYMMDD-HHMMSS>_<shortID>". The basename is
// sanitized to filesystem-safe ASCII; shortID is the first 8 chars of scanID
// (or "anon" if empty). Use this for on-disk artifact layout so a host that
// scans many projects ends up with a self-explanatory ~/.wolf/artifacts/.
func ScanDirName(repoPath string, ts time.Time, scanID string) string {
	base := filepath.Base(strings.TrimRight(repoPath, string(filepath.Separator)))
	if base == "" || base == "." || base == "/" {
		base = "repo"
	}
	base = sanitizeDirComponent(base)
	stamp := ts.UTC().Format("20060102-150405")
	short := "anon"
	if len(scanID) >= 8 {
		short = scanID[:8]
	} else if scanID != "" {
		short = scanID
	}
	return base + "_" + stamp + "_" + short
}

// sanitizeDirComponent replaces anything outside [A-Za-z0-9_.-] with '-'.
// Empty result falls back to "x" so callers never get a zero-length name.
func sanitizeDirComponent(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '_', r == '.', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := b.String()
	out = strings.Trim(out, "-.")
	if out == "" {
		return "x"
	}
	if len(out) > 64 {
		out = out[:64]
	}
	return out
}

// Manifest is the per-scan metadata sidecar written to manifest.json next to
// findings.json / FIX-*.md. It captures the inputs to the scan (repo, commit,
// languages, scanners) and the headline outputs (counts) so a consumer can
// understand the artifact directory without rescanning.
type Manifest struct {
	ScanID      string            `json:"scan_id"`
	RepoName    string            `json:"repo_name,omitempty"`
	RepoPath    string            `json:"repo_path"`
	RepoCommit  string            `json:"repo_commit,omitempty"`
	Branch      string            `json:"branch,omitempty"`
	StartedAt   time.Time         `json:"started_at"`
	FinishedAt  time.Time         `json:"finished_at"`
	WolfVersion string            `json:"wolf_version,omitempty"`
	Detection   DetectionSummary  `json:"detection"`
	ScannersRun []string          `json:"scanners_run"`
	Skipped     []ScannerSkip     `json:"scanners_skipped,omitempty"`
	Failed      map[string]string `json:"scanners_failed,omitempty"`
	Counts      Counts            `json:"counts"`
}

// DetectionSummary captures the output of internal/scan/detector that drove
// scanner selection.
type DetectionSummary struct {
	Languages  []string `json:"languages"`
	Frameworks []string `json:"frameworks,omitempty"`
	TestFiles  int      `json:"test_files"`
	TotalFiles int      `json:"total_files"`
}

// ScannerSkip records why a scanner was not executed.
type ScannerSkip struct {
	Tool   string `json:"tool"`
	Reason string `json:"reason"`
}

// Counts is the cheap-to-derive numeric summary of findings at each stage of
// the pipeline. raw_findings is what plugins emitted before dedupe;
// after_dedupe is what remains in findings.json.
type Counts struct {
	RawFindings  int `json:"raw_findings"`
	AfterDedupe  int `json:"after_dedupe"`
	Suppressed   int `json:"suppressed"` // filtered by defaults + .wolfignore
	Visible      int `json:"visible"`    // after_dedupe - suppressed
	HighSeverity int `json:"high_severity"` // critical + high (visible only)
}

// CountFindings derives Counts from a raw/deduped pair. rawTotal can be 0
// when the caller doesn't know the pre-dedupe number; in that case
// raw_findings equals after_dedupe.
func CountFindings(rawTotal int, deduped []models.Finding) Counts {
	c := Counts{
		RawFindings: rawTotal,
		AfterDedupe: len(deduped),
	}
	if c.RawFindings == 0 {
		c.RawFindings = c.AfterDedupe
	}
	for _, f := range deduped {
		if f.Severity == models.SeverityCritical || f.Severity == models.SeverityHigh {
			c.HighSeverity++
		}
	}
	return c
}

// WriteManifest serializes m to <dir>/manifest.json with 2-space indent.
// Returns the absolute path written.
func WriteManifest(dir string, m Manifest) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return path, nil
}
