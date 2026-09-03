package verify

import (
	"context"
	"errors"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

// fakeWorkspace satisfies the verify.Workspace interface without a real git
// repo. The verify gate's only workspace inputs are the changed-file list and
// the path; build/scanner execution is stubbed via package vars.
type fakeWorkspace struct {
	path    string
	changed []string
	err     error
}

func (f *fakeWorkspace) Path() string                                   { return f.path }
func (f *fakeWorkspace) ChangedFiles(context.Context) ([]string, error) { return f.changed, f.err }
func (f *fakeWorkspace) Rollback(context.Context, string) error         { return nil }

// fakeScanner returns canned rescan results.
type fakeScanner struct {
	findings []models.Finding
	err      error
	calls    int
}

func (s *fakeScanner) RescanFile(_ context.Context, _, _, _, _ string) ([]models.Finding, error) {
	s.calls++
	return s.findings, s.err
}

// stubBuild forces the build stage to pass/fail without a real toolchain, and
// makes inferred languages report as available.
func stubBuild(t *testing.T, fail bool) {
	t.Helper()
	origCmd := runCommand
	origLook := lookPath
	lookPath = func(string) (string, error) { return "/usr/bin/go", nil }
	runCommand = func(_ context.Context, _, name string, _ ...string) (string, error) {
		if fail {
			return "build error: undefined: x", errors.New("exit 1")
		}
		return "ok", nil
	}
	t.Cleanup(func() { runCommand = origCmd; lookPath = origLook })
}

func goFinding() models.Finding {
	return models.Finding{
		ID:        "f1",
		ToolName:  "gosec",
		RuleID:    "G101",
		FilePath:  "main.go",
		LineStart: 10,
	}
}

func TestGate_CleanFix_Passes(t *testing.T) {
	stubBuild(t, false)
	ws := &fakeWorkspace{path: "/ws", changed: []string{"main.go"}}
	scanner := &fakeScanner{findings: nil} // finding gone, no regressions

	res, err := Gate(context.Background(), ws, goFinding(), scanner, Options{})
	if err != nil {
		t.Fatalf("Gate: %v", err)
	}
	if !res.Passed {
		t.Fatalf("expected pass, got %+v", res)
	}
	if !res.FilesChanged || !res.Built || !res.FindingCleared || res.NewFindings {
		t.Errorf("stage booleans wrong: %+v", res)
	}
	if scanner.calls != 1 {
		t.Errorf("expected exactly one targeted rescan, got %d", scanner.calls)
	}
}

func TestGate_NoChanges_Fails(t *testing.T) {
	ws := &fakeWorkspace{path: "/ws", changed: nil}
	scanner := &fakeScanner{}

	res, err := Gate(context.Background(), ws, goFinding(), scanner, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Passed {
		t.Fatal("expected fail when nothing changed")
	}
	if scanner.calls != 0 {
		t.Errorf("rescan should not run when files unchanged, calls=%d", scanner.calls)
	}
	if res.Stages[0].Stage != StageFilesChanged || res.Stages[0].Passed {
		t.Errorf("first stage should be a failed files_changed, got %+v", res.Stages[0])
	}
}

func TestGate_BuildFails_Fails(t *testing.T) {
	stubBuild(t, true)
	ws := &fakeWorkspace{path: "/ws", changed: []string{"main.go"}}
	scanner := &fakeScanner{}

	res, err := Gate(context.Background(), ws, goFinding(), scanner, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Passed || res.Built {
		t.Fatalf("expected build failure, got %+v", res)
	}
	if scanner.calls != 0 {
		t.Errorf("rescan should not run after a build failure, calls=%d", scanner.calls)
	}
}

func TestGate_FindingStillPresent_Fails(t *testing.T) {
	stubBuild(t, false)
	ws := &fakeWorkspace{path: "/ws", changed: []string{"main.go"}}
	// The rescan still reports the same finding (matched by rule+file+line).
	scanner := &fakeScanner{findings: []models.Finding{goFinding()}}

	res, err := Gate(context.Background(), ws, goFinding(), scanner, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Passed || res.FindingCleared {
		t.Fatalf("expected finding-still-present failure, got %+v", res)
	}
}

func TestGate_NewRegression_Fails(t *testing.T) {
	stubBuild(t, false)
	ws := &fakeWorkspace{path: "/ws", changed: []string{"main.go"}}
	// Original gone, but a DIFFERENT finding appeared in the same file.
	regression := models.Finding{ToolName: "gosec", RuleID: "G404", FilePath: "main.go", LineStart: 12}
	scanner := &fakeScanner{findings: []models.Finding{regression}}

	res, err := Gate(context.Background(), ws, goFinding(), scanner, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Passed {
		t.Fatalf("expected regression failure, got %+v", res)
	}
	if !res.FindingCleared {
		t.Error("finding should be cleared (the regression is a different rule)")
	}
	if !res.NewFindings {
		t.Error("NewFindings should be true")
	}
}

func TestGate_NoScannerConfigured_Fails(t *testing.T) {
	stubBuild(t, false)
	ws := &fakeWorkspace{path: "/ws", changed: []string{"main.go"}}

	res, err := Gate(context.Background(), ws, goFinding(), nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Passed {
		t.Fatal("expected fail with no scanner backend")
	}
}

func TestGate_ScannerError_PropagatesUnverified(t *testing.T) {
	stubBuild(t, false)
	ws := &fakeWorkspace{path: "/ws", changed: []string{"main.go"}}
	scanner := &fakeScanner{err: errors.New("docker down")}

	res, err := Gate(context.Background(), ws, goFinding(), scanner, Options{})
	if err != nil {
		t.Fatalf("scanner backend errors are unverified, not gate errors: %v", err)
	}
	if res.Passed || !res.UnableToVerify {
		t.Fatalf("backend error must be unable-to-verify, got %+v", res)
	}
}

func TestGate_SkipBuild(t *testing.T) {
	// No build stub: SkipBuild must avoid invoking any toolchain.
	ws := &fakeWorkspace{path: "/ws", changed: []string{"main.go"}}
	scanner := &fakeScanner{findings: nil}

	res, err := Gate(context.Background(), ws, goFinding(), scanner, Options{SkipBuild: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Passed {
		t.Fatalf("expected pass with build skipped, got %+v", res)
	}
	var buildStg *StageResult
	for i := range res.Stages {
		if res.Stages[i].Stage == StageBuild {
			buildStg = &res.Stages[i]
		}
	}
	if buildStg == nil || !buildStg.Skipped {
		t.Errorf("build stage should be marked skipped, got %+v", buildStg)
	}
}

func TestGate_OptionalTestPasses(t *testing.T) {
	origCmd := runCommand
	origLook := lookPath
	lookPath = func(string) (string, error) { return "/usr/bin/go", nil }
	calledTest := false
	runCommand = func(_ context.Context, _, name string, args ...string) (string, error) {
		if name == "make" {
			calledTest = true
		}
		return "ok", nil
	}
	t.Cleanup(func() { runCommand = origCmd; lookPath = origLook })

	ws := &fakeWorkspace{path: "/ws", changed: []string{"main.go"}}
	scanner := &fakeScanner{findings: nil}
	res, err := Gate(context.Background(), ws, goFinding(), scanner, Options{Test: TestCommand{Name: "make", Args: []string{"test"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Passed || !res.TestsPassed {
		t.Fatalf("expected pass with tests, got %+v", res)
	}
	if !calledTest {
		t.Error("the configured test command should have been invoked")
	}
}

func TestGate_OptionalTestFails(t *testing.T) {
	origCmd := runCommand
	origLook := lookPath
	lookPath = func(string) (string, error) { return "/usr/bin/go", nil }
	runCommand = func(_ context.Context, _, name string, _ ...string) (string, error) {
		if name == "make" {
			return "FAIL: TestFoo", errors.New("exit 1")
		}
		return "ok", nil // build passes
	}
	t.Cleanup(func() { runCommand = origCmd; lookPath = origLook })

	ws := &fakeWorkspace{path: "/ws", changed: []string{"main.go"}}
	scanner := &fakeScanner{findings: nil}
	res, err := Gate(context.Background(), ws, goFinding(), scanner, Options{Test: TestCommand{Name: "make", Args: []string{"test"}}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Passed || res.TestsPassed {
		t.Fatalf("expected test-stage failure, got %+v", res)
	}
}

func TestGate_ChangedFilesError(t *testing.T) {
	ws := &fakeWorkspace{path: "/ws", err: errors.New("git broke")}
	res, err := Gate(context.Background(), ws, goFinding(), &fakeScanner{}, Options{})
	if err == nil {
		t.Fatal("expected error when listing changed files fails")
	}
	if res.Passed {
		t.Error("must not pass on a changed-files error")
	}
}

func TestInferLanguage(t *testing.T) {
	cases := []struct {
		file string
		want string
	}{
		{"a.go", "go"},
		{"a.ts", "ts"},
		{"a.tsx", "ts"},
		{"a.js", "js"},
		{"a.py", "py"},
		{"a.rb", ""},
	}
	for _, c := range cases {
		got := inferLanguage(models.Finding{FilePath: c.file}, nil)
		if got != c.want {
			t.Errorf("inferLanguage(%q) = %q, want %q", c.file, got, c.want)
		}
	}
	// Falls back to changed-file extensions when the finding path is unknown.
	if got := inferLanguage(models.Finding{FilePath: "Makefile"}, []string{"x.py"}); got != "py" {
		t.Errorf("fallback inferLanguage = %q, want py", got)
	}
}

func TestIsNodeCheckable(t *testing.T) {
	if !isNodeCheckable("app.js") || !isNodeCheckable("mod.mjs") || !isNodeCheckable("c.cjs") {
		t.Fatal("js/mjs/cjs must be checkable")
	}
	for _, f := range []string{"a.ts", "a.tsx", "a.jsx", "a.go"} {
		if isNodeCheckable(f) {
			t.Fatalf("%s must not be node --check'd", f)
		}
	}
}

func TestGate_TsxDoesNotFailNodeCheck(t *testing.T) {
	var ran []string
	origCmd := runCommand
	origLook := lookPath
	lookPath = func(string) (string, error) { return "/usr/bin/node", nil }
	runCommand = func(_ context.Context, _, name string, args ...string) (string, error) {
		ran = append(ran, name+" "+args[0])
		return "ERR_UNKNOWN_FILE_EXTENSION", errors.New("exit 1")
	}
	t.Cleanup(func() { runCommand = origCmd; lookPath = origLook })

	ws := &fakeWorkspace{path: "/ws", changed: []string{"frontend/modal.tsx"}}
	f := models.Finding{ID: "t1", ToolName: "renovate", FilePath: "frontend/modal.tsx", LineStart: 12}
	res, err := Gate(context.Background(), ws, f, &fakeScanner{findings: nil}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range ran {
		if c == "node --check" || c == "node frontend/modal.tsx" {
			t.Fatalf("must not node --check tsx, ran %v", ran)
		}
	}
	// node --check is never invoked; args[0] would be --check
	for _, c := range ran {
		if len(c) >= 4 && c[:4] == "node" {
			t.Fatalf("node must not run for tsx-only changes: %v", ran)
		}
	}
	if !res.Passed {
		t.Fatalf("tsx-only edit must not fail build: %+v", res)
	}
}

type batchScanner struct {
	calls    int
	files    [][]string
	findings []models.Finding
	err      error
}

func (s *batchScanner) RescanFile(context.Context, string, string, string, string) ([]models.Finding, error) {
	t := "RescanFile should not be used when RescanFiles is implemented"
	panic(t)
}

func (s *batchScanner) RescanFiles(_ context.Context, _ string, files []string, _, _ string) ([]models.Finding, error) {
	s.calls++
	cp := append([]string(nil), files...)
	s.files = append(s.files, cp)
	return s.findings, s.err
}

func TestGateBatch_OneRescanForManyFindings(t *testing.T) {
	stubBuild(t, false)
	ws := &fakeWorkspace{path: "/ws", changed: []string{"a.go", "b.go"}}
	sc := &batchScanner{}
	var findings []models.Finding
	for i := 0; i < 10; i++ {
		file := "a.go"
		if i >= 5 {
			file = "b.go"
		}
		findings = append(findings, models.Finding{
			ID: "id-" + string(rune('a'+i)), ToolName: "gosec", RuleID: "G101", FilePath: file, LineStart: i + 1,
		})
	}
	out, err := GateBatch(context.Background(), ws, findings, sc, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if sc.calls != 1 {
		t.Fatalf("rescan calls = %d, want 1", sc.calls)
	}
	if len(out) != 10 {
		t.Fatalf("results = %d, want 10", len(out))
	}
	for _, r := range out {
		if !r.Passed || !r.FindingCleared {
			t.Fatalf("expected cleared, got %+v", r)
		}
	}
}

func TestGate_EmptyFilePathSkipped(t *testing.T) {
	ws := &fakeWorkspace{path: "/ws", changed: []string{"main.go"}}
	sc := &fakeScanner{}
	f := models.Finding{ID: "empty-1", ToolName: "scorecard", RuleID: "Branch-Protection"}
	res, err := Gate(context.Background(), ws, f, sc, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Passed || res.FindingCleared {
		t.Fatalf("empty path must not look cleared: %+v", res)
	}
	if sc.calls != 0 {
		t.Fatalf("empty path must not rescan, calls=%d", sc.calls)
	}
}

func TestGateBatch_TSSkipDoesNotFailGo(t *testing.T) {
	origCmd := runCommand
	origLook := lookPath
	lookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }
	runCommand = func(_ context.Context, _, name string, args ...string) (string, error) {
		if name == "node" && len(args) > 0 && args[0] == "--check" {
			return "ERR_UNKNOWN_FILE_EXTENSION", errors.New("exit 1")
		}
		if name == "go" {
			return "ok", nil
		}
		return "ok", nil
	}
	t.Cleanup(func() { runCommand = origCmd; lookPath = origLook })

	ws := &fakeWorkspace{path: "/ws", changed: []string{"a.ts", "main.go"}}
	sc := &batchScanner{}
	ts := models.Finding{ID: "ts-1", ToolName: "bearer", RuleID: "secret", FilePath: "a.ts", LineStart: 1}
	goF := goFinding()
	goF.ID = "go-1"
	out, err := GateBatch(context.Background(), ws, []models.Finding{ts, goF}, sc, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := out[ts.ID]; got == nil || got.BuildFailed() {
		t.Fatalf("ts must skip node --check, got %+v", got)
	}
	if got := out[goF.ID]; got == nil || !got.Built || !got.FindingCleared {
		t.Fatalf("go must still build, got %+v", got)
	}
}

func TestGateBatch_JSBuildFailDoesNotFailGo(t *testing.T) {
	origCmd := runCommand
	origLook := lookPath
	lookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }
	runCommand = func(_ context.Context, _, name string, args ...string) (string, error) {
		if name == "node" && len(args) > 0 && args[0] == "--check" {
			return "SyntaxError", errors.New("exit 1")
		}
		if name == "go" {
			return "ok", nil
		}
		return "ok", nil
	}
	t.Cleanup(func() { runCommand = origCmd; lookPath = origLook })

	ws := &fakeWorkspace{path: "/ws", changed: []string{"a.js", "main.go"}}
	sc := &batchScanner{}
	js := models.Finding{ID: "js-1", ToolName: "eslint", RuleID: "x", FilePath: "a.js", LineStart: 1}
	goF := goFinding()
	goF.ID = "go-1"
	out, err := GateBatch(context.Background(), ws, []models.Finding{js, goF}, sc, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := out[js.ID]; got == nil || got.Passed || !got.BuildFailed() {
		t.Fatalf("js should fail node --check, got %+v", got)
	}
	if got := out[goF.ID]; got == nil || !got.Built || !got.FindingCleared {
		t.Fatalf("go must not inherit the js build failure, got %+v", got)
	}
}

func TestGateBatch_MissingImageLeavesUncleared(t *testing.T) {
	stubBuild(t, false)
	ws := &fakeWorkspace{path: "/ws", changed: []string{"main.go"}}
	sc := &batchScanner{err: errors.New("scanner image missing: bearer/bearer:1.49.0")}
	f := goFinding()
	f.ID = "f-missing"
	out, err := GateBatch(context.Background(), ws, []models.Finding{f}, sc, Options{})
	if err == nil || !IsMissingImage(err) {
		t.Fatalf("missing image must fail verification, err=%v", err)
	}
	got := out[f.ID]
	if got == nil || got.Passed || got.FindingCleared || !got.UnableToVerify {
		t.Fatalf("missing image must leave uncleared/unverified, got %+v", got)
	}
	if got.BuildFailed() {
		t.Fatalf("missing image is not a build failure: %+v", got)
	}
}

func TestSameFinding_FingerprintPreferred(t *testing.T) {
	a := models.Finding{Fingerprint: "fp1", RuleID: "R1", FilePath: "x", LineStart: 1}
	b := models.Finding{Fingerprint: "fp1", RuleID: "R2", FilePath: "y", LineStart: 9}
	if !sameFinding(a, b) {
		t.Error("matching fingerprints should be the same finding regardless of line")
	}
	c := models.Finding{Fingerprint: "fp2", RuleID: "R1", FilePath: "x", LineStart: 1}
	if sameFinding(a, c) {
		t.Error("different fingerprints should not match")
	}
}
