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
	if err == nil {
		t.Fatal("expected an error when the scanner backend fails")
	}
	if res.Passed {
		t.Error("a backend error must not be reported as a pass")
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
