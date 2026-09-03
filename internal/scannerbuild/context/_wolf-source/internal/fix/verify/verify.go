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
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/models"
)

// ErrMissingImage means the targeted scanner container is not present.
// The finding is NOT cleared — the gate could not judge it.
var ErrMissingImage = errors.New("scanner image missing")

// IsMissingImage reports whether err is a missing-scanner-image failure.
func IsMissingImage(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrMissingImage) {
		return true
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "scanner image missing") ||
		strings.Contains(s, "is not present locally") ||
		strings.Contains(s, "no such image") ||
		strings.Contains(s, "unable to find image") ||
		strings.Contains(s, "image not present")
}

// BatchScanner optionally rescans many files in one tool invocation.
type BatchScanner interface {
	RescanFiles(ctx context.Context, repoPath string, files []string, tool, rule string) ([]models.Finding, error)
}

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

	// UnableToVerify means the targeted rescan could not run (missing
	// image, no scanner, transport error). The gate did not judge the
	// finding. The orchestrator should keep a build-clean edit.
	UnableToVerify bool `json:"unable_to_verify,omitempty"`

	// ChangedFiles is the set of repo-relative paths the engine touched.
	ChangedFiles []string `json:"changed_files,omitempty"`
}

// BuildFailed reports a hard compile/parse failure. Missing toolchains are
// skipped and do not count.
func (r *VerifyResult) BuildFailed() bool {
	if r == nil {
		return false
	}
	for _, s := range r.Stages {
		if s.Stage == StageBuild && !s.Skipped && !s.Passed {
			return true
		}
	}
	return false
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
	if strings.TrimSpace(finding.FilePath) == "" {
		res := &VerifyResult{}
		res.Stages = append(res.Stages, StageResult{
			Stage: StageFindingCleared, Passed: false, Skipped: false,
			Detail: "empty file_path — skipped verify",
		})
		return finalize(res), nil
	}
	batch, err := GateBatch(ctx, ws, []models.Finding{finding}, scanner, opts)
	if err != nil {
		return &VerifyResult{}, err
	}
	if r, ok := batch[finding.ID]; ok && r != nil {
		return r, nil
	}
	// Findings without an ID still need a result — take the sole entry.
	for _, r := range batch {
		return r, nil
	}
	return &VerifyResult{}, nil
}

// GateBatch verifies many findings from one scanner turn with one build and
// one targeted rescan of the unique files. Missing scanner images leave
// findings uncleared (Passed=false). Empty file_path findings are skipped.
func GateBatch(ctx context.Context, ws Workspace, findings []models.Finding, scanner Scanner, opts Options) (map[string]*VerifyResult, error) {
	out := make(map[string]*VerifyResult, len(findings))
	var toCheck []models.Finding
	for _, f := range findings {
		key := f.ID
		if key == "" {
			key = f.FilePath + ":" + f.RuleID
		}
		if strings.TrimSpace(f.FilePath) == "" {
			res := &VerifyResult{}
			res.Stages = append(res.Stages, StageResult{
				Stage: StageFindingCleared, Passed: false, Skipped: false,
				Detail: "empty file_path — skipped verify",
			})
			out[key] = finalize(res)
			continue
		}
		toCheck = append(toCheck, f)
	}
	if len(toCheck) == 0 {
		return out, nil
	}

	changed, err := ws.ChangedFiles(ctx)
	if err != nil {
		return out, fmt.Errorf("verify: list changed files: %w", err)
	}

	base := func() *VerifyResult {
		res := &VerifyResult{ChangedFiles: changed}
		if len(changed) == 0 {
			res.Stages = append(res.Stages, StageResult{Stage: StageFilesChanged, Passed: false, Detail: "engine made no changes"})
			return finalize(res)
		}
		res.FilesChanged = true
		res.Stages = append(res.Stages, StageResult{Stage: StageFilesChanged, Passed: true})
		return res
	}

	if len(changed) == 0 {
		for _, f := range toCheck {
			out[findingKey(f)] = base()
		}
		return out, nil
	}

	builds := map[string]StageResult{}
	if opts.SkipBuild {
		builds[""] = StageResult{Stage: StageBuild, Passed: true, Skipped: true, Detail: "build stage skipped"}
	} else {
		for _, f := range toCheck {
			lang := langForFile(f.FilePath)
			if lang == "" {
				lang = inferLanguage(f, changed)
			}
			if _, ok := builds[lang]; ok {
				continue
			}
			builds[lang] = buildStage(ctx, ws.Path(), f, changed)
		}
	}
	buildFor := func(f models.Finding) StageResult {
		if opts.SkipBuild {
			return builds[""]
		}
		lang := langForFile(f.FilePath)
		if lang == "" {
			lang = inferLanguage(f, changed)
		}
		if b, ok := builds[lang]; ok {
			return b
		}
		return StageResult{Stage: StageBuild, Passed: true, Skipped: true, Detail: "no build check"}
	}

	var rescanable []models.Finding
	for _, f := range toCheck {
		bld := buildFor(f)
		if !bld.Passed {
			res := base()
			res.Stages = append(res.Stages, bld)
			out[findingKey(f)] = finalize(res)
			continue
		}
		rescanable = append(rescanable, f)
	}
	if len(rescanable) == 0 {
		return out, nil
	}

	files := uniqueNonEmptyPaths(rescanable)
	tool := strings.TrimSpace(rescanable[0].ToolName)
	if scanner == nil {
		for _, f := range rescanable {
			res := base()
			res.Built = true
			res.UnableToVerify = true
			res.Stages = append(res.Stages, buildFor(f))
			res.Stages = append(res.Stages, StageResult{
				Stage: StageFindingCleared, Passed: false, Skipped: true,
				Detail: "no scanner backend configured for targeted rescan",
			})
			out[findingKey(f)] = finalize(res)
		}
		return out, nil
	}
	post, err := rescanFiles(ctx, scanner, ws.Path(), files, tool)
	if err != nil {
		if IsMissingImage(err) {
			for _, f := range rescanable {
				res := base()
				res.Built = true
				res.UnableToVerify = true
				res.Stages = append(res.Stages, buildFor(f))
				res.Stages = append(res.Stages, StageResult{
					Stage: StageFindingCleared, Passed: false, Skipped: false,
					Detail: "scanner image missing",
				})
				out[findingKey(f)] = finalize(res)
			}
			return out, fmt.Errorf("%w: %v", ErrMissingImage, err)
		}
		detail := "targeted rescan failed — keep edits, leave findings open"
		for _, f := range rescanable {
			res := base()
			res.Built = true
			res.UnableToVerify = true
			res.Stages = append(res.Stages, buildFor(f))
			res.Stages = append(res.Stages, StageResult{
				Stage: StageFindingCleared, Passed: false, Skipped: true,
				Detail: detail,
			})
			out[findingKey(f)] = finalize(res)
		}
		return out, nil
	}

	for _, f := range rescanable {
		res := base()
		res.Built = true
		res.Stages = append(res.Stages, buildFor(f))
		filePost := findingsForFile(post, f.FilePath)
		if findingStillPresent(f, filePost) {
			res.Stages = append(res.Stages, StageResult{Stage: StageFindingCleared, Passed: false, Detail: "finding still present after fix"})
			out[findingKey(f)] = finalize(res)
			continue
		}
		res.FindingCleared = true
		res.Stages = append(res.Stages, StageResult{Stage: StageFindingCleared, Passed: true})
		if newOnes := newFindings(f, filePost); len(newOnes) > 0 {
			res.NewFindings = true
			res.Stages = append(res.Stages, StageResult{
				Stage:  StageNoRegressions,
				Passed: false,
				Detail: fmt.Sprintf("fix introduced %d new finding(s) in %s", len(newOnes), f.FilePath),
			})
			out[findingKey(f)] = finalize(res)
			continue
		}
		res.Stages = append(res.Stages, StageResult{Stage: StageNoRegressions, Passed: true})
		if opts.Test.Name == "" {
			res.TestsPassed = true
			res.Stages = append(res.Stages, StageResult{Stage: StageTests, Passed: true, Skipped: true, Detail: "no test command configured"})
		} else {
			tout, terr := runCommand(ctx, ws.Path(), opts.Test.Name, opts.Test.Args...)
			if terr != nil {
				res.Stages = append(res.Stages, StageResult{Stage: StageTests, Passed: false, Detail: truncate(tout, 2000)})
				out[findingKey(f)] = finalize(res)
				continue
			}
			res.TestsPassed = true
			res.Stages = append(res.Stages, StageResult{Stage: StageTests, Passed: true})
		}
		out[findingKey(f)] = finalize(res)
	}
	return out, nil
}

func findingKey(f models.Finding) string {
	if f.ID != "" {
		return f.ID
	}
	return f.FilePath + ":" + f.RuleID
}

func uniqueNonEmptyPaths(findings []models.Finding) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range findings {
		p := strings.TrimSpace(f.FilePath)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

func findingsForFile(all []models.Finding, file string) []models.Finding {
	var out []models.Finding
	for _, f := range all {
		if f.FilePath == file || strings.HasSuffix(f.FilePath, "/"+file) || strings.HasSuffix(file, "/"+f.FilePath) {
			out = append(out, f)
		}
	}
	return out
}

func rescanFiles(ctx context.Context, scanner Scanner, repoPath string, files []string, tool string) ([]models.Finding, error) {
	if bs, ok := scanner.(BatchScanner); ok {
		return bs.RescanFiles(ctx, repoPath, files, tool, "")
	}
	var all []models.Finding
	for _, file := range files {
		got, err := scanner.RescanFile(ctx, repoPath, file, tool, "")
		if err != nil {
			return all, err
		}
		all = append(all, got...)
	}
	return all, nil
}

// finalize sets Passed to the conjunction of every non-skipped stage.
// UnableToVerify is never a pass — the gate did not judge the finding.
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
	if res.UnableToVerify {
		res.Passed = false
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
		// Do not run `node --check` on .ts/.tsx — Node rejects the
		// extension (ERR_UNKNOWN_FILE_EXTENSION) and we used to roll
		// back valid TSX edits. `tsc --noEmit` at the repo root is also
		// unsafe on mixed Go+TS trees. Parse-check only real JS files
		// in the same batch; a TS-only turn skips this stage.
		if commandAvailable("node") {
			return nodeParseCheck(ctx, repoPath, changed)
		}
		return StageResult{Stage: StageBuild, Passed: true, Skipped: true, Detail: "no js parse-check for TypeScript; build skipped"}

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

// nodeParseCheck runs `node --check` on changed JS files only. TypeScript
// and JSX are skipped: Node cannot parse those extensions.
func nodeParseCheck(ctx context.Context, repoPath string, changed []string) StageResult {
	checked := 0
	for _, f := range changed {
		if !isNodeCheckable(f) {
			continue
		}
		if out, err := runCommand(ctx, repoPath, "node", "--check", f); err != nil {
			return StageResult{Stage: StageBuild, Passed: false, Detail: truncate(f+": "+out, 2000)}
		}
		checked++
	}
	if checked == 0 {
		return StageResult{Stage: StageBuild, Passed: true, Skipped: true, Detail: "no .js/.mjs/.cjs files to parse-check"}
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

func isNodeCheckable(f string) bool {
	switch strings.ToLower(filepath.Ext(f)) {
	case ".js", ".mjs", ".cjs":
		return true
	default:
		return false
	}
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
