// Package report — artifacts.go defines WriteAll, the single helper that
// persists every per-scan deterministic artifact: findings.json, RAW.md
// (legacy combined markdown), combined.sarif, and manifest.json. Both the
// CLI scan command and the API executeScan goroutine call this so the
// on-disk layout is identical regardless of entrypoint.
package report

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/alphabravocompany/thewolf/internal/scan/suppress"
)

// WriteAllResult captures the on-disk paths produced by WriteAll. Empty
// fields indicate the corresponding artifact was not written (e.g. SARIF
// generation failed). All paths are absolute.
type WriteAllResult struct {
	FindingsJSON  string
	RawMarkdown   string
	CombinedSARIF string
	Manifest      string
	FixHigh       string // FIX-HIGH.md (Phase 2)
	FixAll        string // FIX-ALL.md (Phase 2)
}

// WriteAll materializes every deterministic artifact for a scan into dir.
// It is intentionally tolerant: any single artifact failing to render or
// write logs the cause but does not abort the others — a partial set of
// artifacts is better than none.
//
// Caller is responsible for populating ReportConfig and Manifest with
// matching data (same findings, same scan ID, etc.). Suppression rules
// (defaults + repo-local .wolfignore) are applied here, in-place on the
// rcfg.Findings slice, so findings.json captures the Suppressed flag
// alongside everything else.
func WriteAll(dir string, rcfg ReportConfig, manifest Manifest) (WriteAllResult, error) {
	var res WriteAllResult
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return res, fmt.Errorf("create scan dir: %w", err)
	}

	// Apply suppression. The repo-local .wolfignore is opened relative to
	// the manifest's RepoPath when present. Default rules always apply
	// unless the env override is set (callers set this).
	wolfignoreRules, _ := suppress.ParseWolfIgnoreFile(filepath.Join(manifest.RepoPath, ".wolfignore"))
	ruleset := suppress.Combine(suppress.DefaultRules(), wolfignoreRules)
	var suppressedCount int
	rcfg.Findings, suppressedCount = suppress.Apply(rcfg.Findings, ruleset)
	// Layer the repo's gitignore on top of glob-based rules so files the
	// user explicitly excludes from version control don't surface in the
	// exported artifacts. Uses git check-ignore for canonical semantics.
	suppressedCount += suppress.ApplyGitignore(rcfg.Findings, manifest.RepoPath)
	manifest.Counts.Suppressed = suppressedCount
	manifest.Counts.Visible = len(rcfg.Findings) - suppressedCount
	// Recompute high-severity over visible findings only — that's the
	// number a reader actually cares about ("how much real signal?").
	manifest.Counts.HighSeverity = 0
	for _, f := range rcfg.Findings {
		if f.Suppressed {
			continue
		}
		if f.Severity == "critical" || f.Severity == "high" {
			manifest.Counts.HighSeverity++
		}
	}

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

	// RAW.md — legacy combined markdown. Best-effort.
	if md, err := GenerateMarkdown(rcfg); err == nil {
		path := filepath.Join(dir, "RAW.md")
		if werr := os.WriteFile(path, []byte(md), 0o600); werr == nil {
			res.RawMarkdown = path
		}
	}

	// combined.sarif — SARIF 2.1.0 aggregate. Best-effort.
	if sarifData, err := GenerateSARIF(rcfg); err == nil {
		path := filepath.Join(dir, "combined.sarif")
		if werr := os.WriteFile(path, sarifData, 0o600); werr == nil {
			res.CombinedSARIF = path
		}
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
		}
	}
	if md := RenderFixAll(fxCfg); md != "" {
		path := filepath.Join(dir, "FIX-ALL.md")
		if werr := os.WriteFile(path, []byte(md), 0o600); werr == nil {
			res.FixAll = path
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
