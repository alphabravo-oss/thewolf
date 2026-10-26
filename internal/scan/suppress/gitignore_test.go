package suppress

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

// initRepo creates a tmp git repo with a .gitignore containing the
// given lines. Git must be installed for native semantic checks.
func initRepo(t *testing.T, gitignore string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatal("git is required for native ignore qualification")
	}
	dir := t.TempDir()
	mustRun := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		// Quiet the default branch warning on fresh git installs.
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v (%s)", args, err, out)
		}
	}
	mustRun("git", "init", "-q")
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(gitignore), 0o600); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}
	return dir
}

// TestApplyGitignore_BasicPatterns exercises the common cases: simple
// suffix patterns, directory patterns, root-anchored patterns, and
// negation — all of which git check-ignore handles natively so we
// don't have to reimplement them.
func TestApplyGitignore_BasicPatterns(t *testing.T) {
	repo := initRepo(t, `
# logs
*.log
# build outputs anywhere
build/
# root-anchored
/secret.env
# negation
!keep.log
`)

	findings := []models.Finding{
		{FilePath: "app.log"},                 // *.log → ignored
		{FilePath: "frontend/build/index.js"}, // build/ → ignored
		{FilePath: "src/main.go"},             // not matched
		{FilePath: "secret.env"},              // root-anchored → ignored
		{FilePath: "nested/secret.env"},       // root-anchored, not matched
		{FilePath: "keep.log"},                // negated → NOT ignored
	}

	n, err := ApplyGitignore(t.Context(), findings, repo)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("expected 3 suppressions, got %d", n)
	}

	want := map[string]bool{
		"app.log":                 true,
		"frontend/build/index.js": true,
		"src/main.go":             false,
		"secret.env":              true,
		"nested/secret.env":       false,
		"keep.log":                false,
	}
	for _, f := range findings {
		if f.Suppressed != want[f.FilePath] {
			t.Errorf("%s: suppressed=%v want=%v", f.FilePath, f.Suppressed, want[f.FilePath])
		}
		if f.Suppressed && f.SuppressedReason != gitignoreReason {
			t.Errorf("%s: reason=%q want=%q", f.FilePath, f.SuppressedReason, gitignoreReason)
		}
	}
}

// TestApplyGitignore_RespectsExistingSuppression locks down that an
// earlier suppress.Apply pass keeps its reason — gitignore is layered
// on top, not authoritative over the built-in defaults.
func TestApplyGitignore_RespectsExistingSuppression(t *testing.T) {
	repo := initRepo(t, "*.log\n")

	findings := []models.Finding{
		{FilePath: "app.log", Suppressed: true, SuppressedReason: "default:testdata"},
	}
	n, err := ApplyGitignore(t.Context(), findings, repo)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("should have skipped pre-suppressed finding, got n=%d", n)
	}
	if findings[0].SuppressedReason != "default:testdata" {
		t.Errorf("reason got overwritten: %q", findings[0].SuppressedReason)
	}
}

// TestApplyGitignore_NotAGitRepo confirms the graceful-degrade path:
// pointing at a plain directory must not error, just return 0.
func TestApplyGitignore_NotAGitRepo(t *testing.T) {
	dir := t.TempDir() // no `git init`
	findings := []models.Finding{{FilePath: "app.log"}}
	n, err := ApplyGitignore(t.Context(), findings, dir)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("non-git dir should be a no-op, got n=%d", n)
	}
	if findings[0].Suppressed {
		t.Errorf("finding should not be suppressed against a non-git dir")
	}
}

// TestApplyGitignore_BadPathsDontPoisonGoodOnes locks down the bug where
// a single absolute-looking finding path (e.g. "/deploy/...") aborted git
// check-ignore with exit 128 mid-stream, dropping results for the good
// paths that came before it. The fix filters such inputs upfront.
func TestApplyGitignore_BadPathsDontPoisonGoodOnes(t *testing.T) {
	repo := initRepo(t, "*.log\n")

	findings := []models.Finding{
		{FilePath: "app.log"},              // should be suppressed
		{FilePath: "/deploy/foo.yml"},      // absolute — git would barf
		{FilePath: "../escape/secret.log"}, // traversal — git would barf
		{FilePath: "subdir/other.log"},     // should be suppressed
	}
	n, err := ApplyGitignore(t.Context(), findings, repo)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("expected 2 suppressions (good paths only), got %d", n)
	}
	if !findings[0].Suppressed {
		t.Errorf("app.log should be suppressed")
	}
	if findings[1].Suppressed {
		t.Errorf("absolute path should NOT be suppressed (skipped)")
	}
	if findings[2].Suppressed {
		t.Errorf("traversal path should NOT be suppressed (skipped)")
	}
	if !findings[3].Suppressed {
		t.Errorf("subdir/other.log should be suppressed")
	}
}

// TestApplyGitignore_EmptyInputs covers the trivial nil/empty branches.
func TestApplyGitignore_EmptyInputs(t *testing.T) {
	if n, err := ApplyGitignore(t.Context(), nil, "/tmp"); n != 0 || err != nil {
		t.Errorf("nil findings: n=%d", n)
	}
	if n, err := ApplyGitignore(t.Context(), []models.Finding{{FilePath: "x"}}, ""); n != 0 || err != nil {
		t.Errorf("empty repoPath: n=%d", n)
	}
}

func TestGitignoreLiteralFilenames(t *testing.T) {
	repo := initRepo(t, "*.log\n")
	findings := []models.Finding{{FilePath: "a\nb.log"}, {FilePath: "quote\".log"}, {FilePath: "space name.log"}, {FilePath: "café.log"}, {FilePath: "name..log"}, {FilePath: "tab\t.log"}, {FilePath: "safe.go\nphantom.log"}, {FilePath: "safe.go"}}
	n, err := ApplyGitignore(t.Context(), findings, repo)
	if err != nil {
		t.Fatal(err)
	}
	if n != 7 {
		t.Fatalf("literal paths matched %d, want 7: %+v", n, findings)
	}
	for i := 0; i < 7; i++ {
		if !findings[i].Suppressed {
			t.Errorf("literal path not matched: %q", findings[i].FilePath)
		}
	}
	if findings[7].Suppressed {
		t.Fatal("newline input injected a separate finding path")
	}
}
