// Package report — artifacts.go defines WriteAll, the single helper that
// persists every per-scan deterministic artifact: findings.json, RAW.md
// (legacy combined markdown), combined.sarif, and manifest.json. Both the
// CLI scan command and the API executeScan goroutine call this so the
// on-disk layout is identical regardless of entrypoint.
package report

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/alphabravocompany/thewolf/internal/scan/suppress"
)

// WriteAllResult captures the on-disk paths produced by WriteAll. Empty
// fields indicate the corresponding artifact was not written before an error.
// All paths are absolute when dir is absolute.
type WriteAllResult struct {
	FindingsJSON  string
	RawMarkdown   string
	CombinedSARIF string
	Manifest      string
	FixHigh       string // FIX-HIGH.md (Phase 2)
	FixAll        string // FIX-ALL.md (Phase 2)
}

// WriteAll materializes every deterministic artifact for a scan into dir.
// A render or write failure returns the paths already written and an error;
// callers must not report a partial bundle as complete.
//
// Caller is responsible for populating ReportConfig and Manifest with
// matching data (same findings, same scan ID, etc.). Suppression rules
// (defaults + repo-local .wolfignore) are applied here, in-place on the
// rcfg.Findings slice, so findings.json captures the Suppressed flag
// alongside everything else.
func WriteAll(ctx context.Context, dir string, rcfg ReportConfig, manifest Manifest) (WriteAllResult, error) {
	var res WriteAllResult
	if err := ctx.Err(); err != nil {
		return res, err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return res, fmt.Errorf("create scan dir: %w", err)
	}

	var err error
	if manifest.IgnorePolicy == nil {
		snapshot, captureErr := suppress.Capture(ctx, rcfg.Findings, manifest.RepoPath)
		if captureErr != nil {
			return res, captureErr
		}
		manifest.IgnorePolicy = &snapshot
	}
	rcfg.Findings, _, err = manifest.IgnorePolicy.Apply(rcfg.Findings)
	if err != nil {
		return res, err
	}
	manifest.Counts = CountFindings(manifest.Counts.RawFindings, rcfg.Findings)

	// findings.json — primary canonical artifact. If this fails, the whole
	// scan-output story breaks, so we surface the error.
	if data, err := GenerateJSON(rcfg); err == nil {
		path := filepath.Join(dir, "findings.json")
		if werr := os.WriteFile(path, data, 0o600); werr == nil {
			res.FindingsJSON = path
		} else {
			return res, fmt.Errorf("write findings.json: %w", werr)
		}
	} else {
		return res, fmt.Errorf("generate findings.json: %w", err)
	}

	// RAW.md — legacy combined markdown.
	if md, err := GenerateMarkdown(rcfg); err == nil {
		path := filepath.Join(dir, "RAW.md")
		if werr := os.WriteFile(path, []byte(md), 0o600); werr == nil {
			res.RawMarkdown = path
		} else {
			return res, fmt.Errorf("write RAW.md: %w", werr)
		}
	} else {
		return res, fmt.Errorf("generate RAW.md: %w", err)
	}

	// combined.sarif — SARIF 2.1.0 aggregate.
	if sarifData, err := GenerateSARIF(rcfg); err == nil {
		path := filepath.Join(dir, "combined.sarif")
		if werr := os.WriteFile(path, sarifData, 0o600); werr == nil {
			res.CombinedSARIF = path
		} else {
			return res, fmt.Errorf("write combined.sarif: %w", werr)
		}
	} else {
		return res, fmt.Errorf("generate combined.sarif: %w", err)
	}

	// FIX-HIGH.md and FIX-ALL.md — the curated Phase-2 docs. Built from
	// the same findings already in rcfg, no extra inputs required.
	fxCfg := FixDocConfig{
		ScanID:      manifest.ScanID,
		RepoName:    manifest.RepoName,
		RepoPath:    manifest.RepoPath,
		Branch:      manifest.Branch,
		Commit:      manifest.RepoCommit,
		Languages:   manifest.Detection.Languages,
		ScannersRun: manifest.ScannersRun,
		GeneratedAt: manifest.FinishedAt,
		Findings:    rcfg.Findings,
		RawTotal:    manifest.Counts.RawFindings,
	}
	if md := RenderFixHigh(fxCfg); md != "" {
		path := filepath.Join(dir, "FIX-HIGH.md")
		if werr := os.WriteFile(path, []byte(md), 0o600); werr == nil {
			res.FixHigh = path
		} else {
			return res, fmt.Errorf("write FIX-HIGH.md: %w", werr)
		}
	}
	if md := RenderFixAll(fxCfg); md != "" {
		path := filepath.Join(dir, "FIX-ALL.md")
		if werr := os.WriteFile(path, []byte(md), 0o600); werr == nil {
			res.FixAll = path
		} else {
			return res, fmt.Errorf("write FIX-ALL.md: %w", werr)
		}
	}

	// manifest.json — write last so its on-disk presence indicates the
	// rest of the artifacts have been materialized.
	if mpath, err := WriteManifest(dir, manifest); err == nil {
		res.Manifest = mpath
	} else {
		return res, fmt.Errorf("write manifest: %w", err)
	}

	return res, nil
}

// MarshalManifest is a convenience for callers (e.g. tests) that want the
// manifest JSON without writing to disk.
func MarshalManifest(m Manifest) ([]byte, error) {
	return json.MarshalIndent(m, "", "  ")
}
