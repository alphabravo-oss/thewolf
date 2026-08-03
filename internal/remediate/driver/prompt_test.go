package driver

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/remediate/plan"
)

func TestTriagePromptListsFindingsAndForbidsProseAroundTheLastLine(t *testing.T) {
	findings := []models.Finding{
		{ID: "f-1", Severity: "high", Category: "sql-injection", FilePath: "db/user.go", LineStart: 10, LineEnd: 12, Title: "SQLi via string concat"},
	}
	got := triagePrompt(findings)
	if !strings.Contains(got, "f-1") {
		t.Errorf("prompt missing finding id: %s", got)
	}
	if !strings.Contains(got, "LAST line") {
		t.Errorf("prompt does not warn that only the last line is parsed: %s", got)
	}
	if !strings.Contains(got, `"summary"`) || !strings.Contains(got, `"finding_id"`) {
		t.Errorf("prompt does not show the required plan.Plan shape: %s", got)
	}
}

func TestExecutePromptListsFixItemsAndTrailerInstructionOnly(t *testing.T) {
	p := &plan.Plan{
		Summary: "s",
		Items: []plan.Item{
			{FindingID: "f-1", Action: plan.ActionFix, Rationale: "sqli"},
			{FindingID: "f-2", Action: plan.ActionSkip, Rationale: "test fixture, not exploitable"},
		},
	}
	findings := []models.Finding{
		{ID: "f-1", Severity: "high", Category: "sql-injection", FilePath: "db/user.go", LineStart: 10, LineEnd: 12, Title: "SQLi"},
		{ID: "f-2", Severity: "low", Category: "test", FilePath: "db/user_test.go"},
	}
	got := executePrompt(p, findings)
	if !strings.Contains(got, "f-1") {
		t.Errorf("prompt missing fix item f-1: %s", got)
	}
	if !strings.Contains(got, "f-2") {
		t.Errorf("prompt does not list f-2 among the do-not-touch findings: %s", got)
	}
	if !strings.Contains(got, findingIDsTrailer+":") {
		t.Errorf("prompt does not instruct the %s trailer: %s", findingIDsTrailer, got)
	}
}

func TestCommitFindingIDsParsesCommaSeparatedTrailer(t *testing.T) {
	body := "Fix SQL injection\n\nRationale: parameterize the query.\n\nFinding-IDs: f-1, f-2\n"
	valid := map[string]struct{}{"f-1": {}, "f-2": {}}
	got := commitFindingIDs(body, valid)
	want := []string{"f-1", "f-2"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("commitFindingIDs(%q) = %v, want %v", body, got, want)
	}
}

func TestCommitFindingIDsAbsentTrailerReturnsNil(t *testing.T) {
	valid := map[string]struct{}{"f-1": {}}
	if got := commitFindingIDs("no trailer here\n", valid); got != nil {
		t.Errorf("commitFindingIDs = %v, want nil", got)
	}
}

func TestCommitFindingIDsDropsUnknownIDs(t *testing.T) {
	body := "Finding-IDs: f-1, f-999\n"
	valid := map[string]struct{}{"f-1": {}}
	got := commitFindingIDs(body, valid)
	if len(got) != 1 || got[0] != "f-1" {
		t.Errorf("commitFindingIDs = %v, want [f-1] (f-999 is not in the finding set)", got)
	}
}

func TestCommitFindingIDsDropsSpaceSeparatedMalformedTrailer(t *testing.T) {
	// "f-1 f-2" (space, not comma) parses as one bogus id "f-1 f-2", which
	// matches nothing in validIDs and must be dropped, not trusted.
	body := "Finding-IDs: f-1 f-2\n"
	valid := map[string]struct{}{"f-1": {}, "f-2": {}}
	if got := commitFindingIDs(body, valid); got != nil {
		t.Errorf("commitFindingIDs = %v, want nil for a malformed (space-separated) trailer", got)
	}
}

// runGitT is a t.Fatal-on-error test helper — collectPatches's real logic is
// git plumbing (log ranges, show, status), so it is tested against a real
// temp git repo rather than a stub, matching the convention already used by
// internal/fix/workspace's own tests.
func runGitT(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %s: %v", args, out, err)
	}
}

// gitOutputT is runGitT plus the trimmed stdout, for callers that need the
// result (e.g. `rev-parse HEAD`).
func gitOutputT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s: %v", args, out, err)
	}
	return strings.TrimSpace(string(out))
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newFixtureWorktree builds a base repo plus a `git worktree add -b branch`
// off it, the same shape internal/fix/workspace.Prepare produces for a local
// repo: a fresh branch carrying the FULL history of its origin, not a
// shallow one. It returns the worktree path and its HEAD at creation time —
// what execDriver.Execute captures as `base` before invoking the agent.
func newFixtureWorktree(t *testing.T, branch string) (worktreePath, base string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	wt := filepath.Join(dir, "wt")

	runGitT(t, "", "init", "-q", repo)
	runGitT(t, repo, "config", "user.email", "test@test.com")
	runGitT(t, repo, "config", "user.name", "test")
	writeTestFile(t, filepath.Join(repo, "base.txt"), "base\n")
	runGitT(t, repo, "add", "base.txt")
	runGitT(t, repo, "commit", "-q", "-m", "pre-existing repo history")
	runGitT(t, repo, "worktree", "add", "-q", wt, "-b", branch)
	base = gitOutputT(t, wt, "rev-parse", "HEAD")
	return wt, base
}

func TestCollectPatchesBoundsToCommitsMadeAfterBase(t *testing.T) {
	wt, base := newFixtureWorktree(t, "wolf/remediation-test")

	writeTestFile(t, filepath.Join(wt, "fix1.txt"), "fix1\n")
	runGitT(t, wt, "add", "fix1.txt")
	runGitT(t, wt, "commit", "-q", "-m", "Fix one\n\nFinding-IDs: f-1")

	writeTestFile(t, filepath.Join(wt, "fix2.txt"), "fix2\n")
	runGitT(t, wt, "add", "fix2.txt")
	runGitT(t, wt, "commit", "-q", "-m", "Fix two\n\nFinding-IDs: f-2, f-3")

	findings := []models.Finding{{ID: "f-1"}, {ID: "f-2"}, {ID: "f-3"}}
	series, err := collectPatches(context.Background(), wt, base, findings)
	if err != nil {
		t.Fatalf("collectPatches: %v", err)
	}
	if len(series.Patches) != 2 {
		t.Fatalf("got %d patches, want 2 (pre-existing repo history must not be included): %+v", len(series.Patches), series.Patches)
	}
	if series.Patches[0].Message != "Fix one" || series.Patches[1].Message != "Fix two" {
		t.Errorf("patches out of order or wrong messages: %+v", series.Patches)
	}
	if got := series.Patches[0].FindingIDs; len(got) != 1 || got[0] != "f-1" {
		t.Errorf("patch 0 FindingIDs = %v, want [f-1]", got)
	}
	if got := series.Patches[1].FindingIDs; len(got) != 2 || got[0] != "f-2" || got[1] != "f-3" {
		t.Errorf("patch 1 FindingIDs = %v, want [f-2 f-3]", got)
	}
	if got := series.Patches[0].FilesChanged; len(got) != 1 || got[0] != "fix1.txt" {
		t.Errorf("patch 0 FilesChanged = %v, want [fix1.txt]", got)
	}
}

func TestCollectPatchesNoNewCommitsReturnsEmptySeries(t *testing.T) {
	wt, base := newFixtureWorktree(t, "wolf/remediation-empty")

	series, err := collectPatches(context.Background(), wt, base, nil)
	if err != nil {
		t.Fatalf("collectPatches: %v", err)
	}
	if len(series.Patches) != 0 {
		t.Errorf("got %d patches, want 0 when the agent made no commits", len(series.Patches))
	}
}

func TestCollectPatchesRejectsUncommittedWork(t *testing.T) {
	wt, base := newFixtureWorktree(t, "wolf/remediation-dirty")
	writeTestFile(t, filepath.Join(wt, "fix1.txt"), "fix1\n")
	runGitT(t, wt, "add", "fix1.txt")
	runGitT(t, wt, "commit", "-q", "-m", "Fix one\n\nFinding-IDs: f-1")
	// Leave an uncommitted edit behind, simulating an agent interrupted
	// mid-fix.
	writeTestFile(t, filepath.Join(wt, "fix2.txt"), "half-done\n")

	_, err := collectPatches(context.Background(), wt, base, []models.Finding{{ID: "f-1"}})
	if err == nil {
		t.Fatal("collectPatches succeeded with an uncommitted file present, want an error")
	}
}

func TestCollectPatchesRejectsCommitWithNoAttributableFindingIDs(t *testing.T) {
	wt, base := newFixtureWorktree(t, "wolf/remediation-bad-trailer")
	writeTestFile(t, filepath.Join(wt, "fix1.txt"), "fix1\n")
	runGitT(t, wt, "add", "fix1.txt")
	// Malformed trailer (space, not comma) — must not be trusted.
	runGitT(t, wt, "commit", "-q", "-m", "Fix one\n\nFinding-IDs: f-1 f-2")

	_, err := collectPatches(context.Background(), wt, base, []models.Finding{{ID: "f-1"}, {ID: "f-2"}})
	if err == nil {
		t.Fatal("collectPatches succeeded for a commit with an unparsable trailer, want an error")
	}
}

func TestCollectPatchesDropsUnknownFindingID(t *testing.T) {
	wt, base := newFixtureWorktree(t, "wolf/remediation-unknown-id")
	writeTestFile(t, filepath.Join(wt, "fix1.txt"), "fix1\n")
	runGitT(t, wt, "add", "fix1.txt")
	runGitT(t, wt, "commit", "-q", "-m", "Fix one\n\nFinding-IDs: f-1, f-999")

	series, err := collectPatches(context.Background(), wt, base, []models.Finding{{ID: "f-1"}})
	if err != nil {
		t.Fatalf("collectPatches: %v", err)
	}
	got := series.Patches[0].FindingIDs
	if len(got) != 1 || got[0] != "f-1" {
		t.Errorf("FindingIDs = %v, want [f-1] (f-999 is not in the finding set and must be dropped)", got)
	}
}
