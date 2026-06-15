// Package verify implements the autonomous fix engine's verification gate —
// the heart of the system (design §7). After an engine attempts a fix in a
// workspace, the gate INDEPENDENTLY judges the result by what landed on disk +
// a targeted rescan, never by the engine's self-report. A fix passes only when
// every applicable stage passes; the orchestrator (Phase 5) keeps a passing fix
// on the branch and rolls back a failing one.
//
// Stages, in order (short-circuiting on the first hard failure):
//
//  1. Files changed   — a non-empty diff. An engine that claims success while
//     touching nothing is an immediate failure.
//  2. Builds          — language-aware compile/parse check (go build, tsc
//     --noEmit, …); at minimum a parse check. A fix that breaks the build is
//     rejected.
//  3. Finding cleared — re-run ONLY the finding's scanner/rule against the
//     changed file (targeted rescan via the Scanner backend) and confirm the
//     finding is gone.
//  4. No regressions  — the targeted rescan introduced no NEW findings of the
//     same rule/file (the regression guard).
//  5. Tests (optional) — a configured test command, when present, must pass.
//
// The Scanner backend is reached through the Scanner interface so tests stub it
// (no real docker / scanners). Build + test commands run through the runCommand
// package var so tests stub them too; workspace inputs are real temp git repos.
package verify

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/models"
)

// Workspace is the slice of *workspace.Workspace the gate needs. Defining it as
// an interface (rather than importing the concrete type) keeps verify
// decoupled and lets tests supply a trivial fake without preparing a real
// worktree when they only exercise a later stage. The concrete
// *workspace.Workspace satisfies it.
type Workspace interface {
	Path() string
	ChangedFiles(ctx context.Context) ([]string, error)
	Rollback(ctx context.Context, file string) error
}

// Scanner is the targeted-rescan backend: re-run a single scanner/rule against
// a single file and return the findings it reports. The production
// implementation drives the container scanner backend
// (internal/setup/scanners + internal/plugin/container); tests stub it so no
// real docker / network is touched.
//
// Implementations MUST scope the rescan to the requested tool + rule against
// the given file only (NOT the whole suite) — that targeting is what keeps the
// gate cheap enough to run after every attempt (design §13).
type Scanner interface {
	// RescanFile runs the named tool's rule against repoPath/file and returns
	// every finding the tool reports for that file. tool is the finding's
	// ToolName; rule is its RuleID ("" = the tool's full ruleset for the file).
	RescanFile(ctx context.Context, repoPath, file, tool, rule string) ([]models.Finding, error)
}

// TestCommand is an optional test gate: a command + args run in the workspace.
// A zero TestCommand (empty Name) disables the stage.
type TestCommand struct {
	Name string
	Args []string
}

// Options configures a Gate run.
type Options struct {
	// Build, when false, skips the build stage (stage 2). Default true — a
	// non-building fix is rejected.
	SkipBuild bool
	// Test is an optional test command (stage 5). Disabled when Name is empty.
	Test TestCommand
}

// Stage names a single verification stage for the structured result.
type Stage string

const (
	StageFilesChanged   Stage = "files_changed"
	StageBuild          Stage = "build"
	StageFindingCleared Stage = "finding_cleared"
	StageNoRegressions  Stage = "no_regressions"
	StageTests          Stage = "tests"
)

// StageResult is the outcome of one stage.
type StageResult struct {
	Stage   Stage  `json:"stage"`
	Passed  bool   `json:"passed"`
	Skipped bool   `json:"skipped,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

// VerifyResult is the structured verdict the gate returns. Passed is the
// conjunction of every non-skipped stage. The orchestrator persists this onto
// the FixAttempt (built / finding_cleared / new_findings / tests).
type VerifyResult struct {
	Passed bool          `json:"passed"`
	Stages []StageResult `json:"stages"`

	// Convenience booleans mirroring the FixAttempt.verify shape (design §5).
	FilesChanged   bool `json:"files_changed"`
	Built          bool `json:"built"`
	FindingCleared bool `json:"finding_cleared"`
	NewFindings    bool `json:"new_findings"`
	TestsPassed    bool `json:"tests_passed"`

	// ChangedFiles is the set of repo-relative paths the engine touched.
	ChangedFiles []string `json:"changed_files,omitempty"`
}

// runCommand runs name+args in dir and returns combined output. Package var so
// tests stub build / test execution; the default shells out.
var runCommand = func(ctx context.Context, dir, name string, args ...string) (string, error) {
	// #nosec G204 -- name is a fixed toolchain command (go / tsc / node / a
	// configured test runner); args are internal, not raw user input.
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// Gate runs the verification stages against ws for finding, using scanner for
// the targeted rescan. It returns a structured VerifyResult. A nil error means
// "the gate ran"; inspect result.Passed for the verdict. A non-nil error means
// the gate itself couldn't run a stage (e.g. the scanner backend errored), in
// which case the fix is treated as unverified (Passed=false) by the caller.
func Gate(ctx context.Context, ws Workspace, finding models.Finding, scanner Scanner, opts Options) (*VerifyResult, error) {
	res := &VerifyResult{}

	// --- Stage 1: files changed? ---
	changed, err := ws.ChangedFiles(ctx)
	if err != nil {
		return res, fmt.Errorf("verify: list changed files: %w", err)
	}
	res.ChangedFiles = changed
	if len(changed) == 0 {
		res.Stages = append(res.Stages, StageResult{Stage: StageFilesChanged, Passed: false, Detail: "engine made no changes"})
		return finalize(res), nil
	}
	res.FilesChanged = true
	res.Stages = append(res.Stages, StageResult{Stage: StageFilesChanged, Passed: true})

	// --- Stage 2: still builds? (language-aware, parse-min) ---
	if opts.SkipBuild {
		res.Built = true
		res.Stages = append(res.Stages, StageResult{Stage: StageBuild, Passed: true, Skipped: true, Detail: "build stage skipped"})
	} else {
		bld := buildStage(ctx, ws.Path(), finding, changed)
		res.Stages = append(res.Stages, bld)
		if !bld.Passed {
			return finalize(res), nil
		}
		res.Built = true
	}

	// --- Stage 3: finding actually gone? (targeted rescan) ---
	if scanner == nil {
		res.Stages = append(res.Stages, StageResult{Stage: StageFindingCleared, Passed: false, Detail: "no scanner backend configured for targeted rescan"})
		return finalize(res), nil
	}
	post, err := scanner.RescanFile(ctx, ws.Path(), finding.FilePath, finding.ToolName, finding.RuleID)
	if err != nil {
		return res, fmt.Errorf("verify: targeted rescan: %w", err)
	}
	if findingStillPresent(finding, post) {
		res.Stages = append(res.Stages, StageResult{Stage: StageFindingCleared, Passed: false, Detail: "finding still present after fix"})
		return finalize(res), nil
	}
	res.FindingCleared = true
	res.Stages = append(res.Stages, StageResult{Stage: StageFindingCleared, Passed: true})

	// --- Stage 4: no new findings? (regression guard over the rescan) ---
	if newOnes := newFindings(finding, post); len(newOnes) > 0 {
		res.NewFindings = true
		res.Stages = append(res.Stages, StageResult{
			Stage:  StageNoRegressions,
			Passed: false,
			Detail: fmt.Sprintf("fix introduced %d new finding(s) in %s", len(newOnes), finding.FilePath),
		})
		return finalize(res), nil
	}
	res.Stages = append(res.Stages, StageResult{Stage: StageNoRegressions, Passed: true})

	// --- Stage 5: optional tests ---
	if opts.Test.Name == "" {
		res.TestsPassed = true
		res.Stages = append(res.Stages, StageResult{Stage: StageTests, Passed: true, Skipped: true, Detail: "no test command configured"})
	} else {
		out, terr := runCommand(ctx, ws.Path(), opts.Test.Name, opts.Test.Args...)
		if terr != nil {
			res.Stages = append(res.Stages, StageResult{Stage: StageTests, Passed: false, Detail: truncate(out, 2000)})
			return finalize(res), nil
		}
		res.TestsPassed = true
		res.Stages = append(res.Stages, StageResult{Stage: StageTests, Passed: true})
	}

	return finalize(res), nil
}

// finalize sets Passed to the conjunction of every non-skipped stage.
func finalize(res *VerifyResult) *VerifyResult {
	res.Passed = true
	for _, s := range res.Stages {
		if s.Skipped {
			continue
		}
		if !s.Passed {
			res.Passed = false
			break
		}
	}
	return res
}

// buildStage runs a language-aware build/parse check rooted at the workspace.
// Language is inferred from the changed files' extensions; the minimum is a
// parse check. An inferred-but-uninstalled toolchain is NOT a fix failure — we
// can't penalise a fix for the environment lacking a compiler — so it is
// reported as skipped/passed.
func buildStage(ctx context.Context, repoPath string, finding models.Finding, changed []string) StageResult {
	lang := inferLanguage(finding, changed)
	switch lang {
	case "go":
		if !commandAvailable("go") {
			return StageResult{Stage: StageBuild, Passed: true, Skipped: true, Detail: "go toolchain not available; build skipped"}
		}
		if out, err := runCommand(ctx, repoPath, "go", "build", "./..."); err != nil {
			return StageResult{Stage: StageBuild, Passed: false, Detail: truncate(out, 2000)}
		}
		return StageResult{Stage: StageBuild, Passed: true, Detail: "go build ./... ok"}

	case "ts":
		if commandAvailable("tsc") {
			if out, err := runCommand(ctx, repoPath, "tsc", "--noEmit"); err != nil {
				return StageResult{Stage: StageBuild, Passed: false, Detail: truncate(out, 2000)}
			}
			return StageResult{Stage: StageBuild, Passed: true, Detail: "tsc --noEmit ok"}
		}
		if commandAvailable("node") {
			// Parse-min fallback: node --check on each changed JS/TS file.
			return nodeParseCheck(ctx, repoPath, changed)
		}
		return StageResult{Stage: StageBuild, Passed: true, Skipped: true, Detail: "no ts/js toolchain available; build skipped"}

	case "js":
		if commandAvailable("node") {
			return nodeParseCheck(ctx, repoPath, changed)
		}
		return StageResult{Stage: StageBuild, Passed: true, Skipped: true, Detail: "node not available; build skipped"}

	case "py":
		if commandAvailable("python3") {
			return pyParseCheck(ctx, repoPath, "python3", changed)
		}
		if commandAvailable("python") {
			return pyParseCheck(ctx, repoPath, "python", changed)
		}
		return StageResult{Stage: StageBuild, Passed: true, Skipped: true, Detail: "python not available; build skipped"}

	default:
		// No language-specific check we can run; the parse-min requirement is
		// satisfied vacuously. Don't reject a fix for an unknown language.
		return StageResult{Stage: StageBuild, Passed: true, Skipped: true, Detail: fmt.Sprintf("no build check for language %q; skipped", lang)}
	}
}

// nodeParseCheck runs `node --check` on each changed JS/TS file as a parse-only
// build minimum.
func nodeParseCheck(ctx context.Context, repoPath string, changed []string) StageResult {
	checked := 0
	for _, f := range changed {
		if !isJSLike(f) {
			continue
		}
		if out, err := runCommand(ctx, repoPath, "node", "--check", f); err != nil {
			return StageResult{Stage: StageBuild, Passed: false, Detail: truncate(f+": "+out, 2000)}
		}
		checked++
	}
	if checked == 0 {
		return StageResult{Stage: StageBuild, Passed: true, Skipped: true, Detail: "no js/ts files to parse-check"}
	}
	return StageResult{Stage: StageBuild, Passed: true, Detail: fmt.Sprintf("node --check passed on %d file(s)", checked)}
}

// pyParseCheck runs `python -m py_compile` on each changed .py file.
func pyParseCheck(ctx context.Context, repoPath, py string, changed []string) StageResult {
	checked := 0
	for _, f := range changed {
		if !strings.HasSuffix(f, ".py") {
			continue
		}
		if out, err := runCommand(ctx, repoPath, py, "-m", "py_compile", f); err != nil {
			return StageResult{Stage: StageBuild, Passed: false, Detail: truncate(f+": "+out, 2000)}
		}
		checked++
	}
	if checked == 0 {
		return StageResult{Stage: StageBuild, Passed: true, Skipped: true, Detail: "no python files to parse-check"}
	}
	return StageResult{Stage: StageBuild, Passed: true, Detail: fmt.Sprintf("py_compile passed on %d file(s)", checked)}
}

// inferLanguage picks a language for the build check from the finding's file
// then the changed-file set. Returns "go" | "ts" | "js" | "py" | "" (unknown).
func inferLanguage(finding models.Finding, changed []string) string {
	if l := langForFile(finding.FilePath); l != "" {
		return l
	}
	for _, f := range changed {
		if l := langForFile(f); l != "" {
			return l
		}
	}
	return ""
}

func langForFile(f string) string {
	switch strings.ToLower(filepath.Ext(f)) {
	case ".go":
		return "go"
	case ".ts", ".tsx":
		return "ts"
	case ".js", ".jsx", ".mjs", ".cjs":
		return "js"
	case ".py":
		return "py"
	default:
		return ""
	}
}

func isJSLike(f string) bool {
	l := langForFile(f)
	return l == "js" || l == "ts"
}

// findingStillPresent reports whether the original finding is still reported in
// the post-fix rescan results. Identity is matched by rule + file + line so a
// fix that merely shifts the line slightly is still recognised as the same
// finding when fingerprints aren't carried by the rescan.
func findingStillPresent(orig models.Finding, post []models.Finding) bool {
	for _, p := range post {
		if sameFinding(orig, p) {
			return true
		}
	}
	return false
}

// newFindings returns rescan findings that are NOT the original finding —
// regressions the fix introduced in the targeted file. Because the rescan is
// scoped to the one file + rule, any extra finding here is a new problem.
func newFindings(orig models.Finding, post []models.Finding) []models.Finding {
	var out []models.Finding
	for _, p := range post {
		if sameFinding(orig, p) {
			continue
		}
		out = append(out, p)
	}
	return out
}

// sameFinding matches two findings as "the same issue". Prefer a stable
// fingerprint when both carry one; otherwise fall back to (rule, file, line).
func sameFinding(a, b models.Finding) bool {
	if a.Fingerprint != "" && b.Fingerprint != "" {
		return a.Fingerprint == b.Fingerprint
	}
	if a.StableFingerprint != "" && b.StableFingerprint != "" {
		return a.StableFingerprint == b.StableFingerprint
	}
	return a.RuleID == b.RuleID &&
		a.ToolName == b.ToolName &&
		a.FilePath == b.FilePath &&
		a.LineStart == b.LineStart
}

// commandAvailable reports whether cmd is on PATH. Package-overridable via the
// lookPath var for tests.
func commandAvailable(cmd string) bool {
	_, err := lookPath(cmd)
	return err == nil
}

var lookPath = exec.LookPath

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
