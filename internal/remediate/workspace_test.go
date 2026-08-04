package remediate

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/remediate/driver"
)

func TestBranchNameIsStableAndScoped(t *testing.T) {
	got := BranchName("rs-42")
	if got != "wolf/remediation-rs-42" {
		t.Fatalf("BranchName = %q, want %q", got, "wolf/remediation-rs-42")
	}
	if strings.Contains(got, " ") {
		t.Error("branch name contains a space")
	}
	if BranchName("rs-42") != got {
		t.Error("BranchName is not deterministic")
	}
}

// gitCommonDir resolves the real .git directory a worktree/repo at dir is
// using — the FILE a `git worktree add` checkout carries points here, so
// this is the thing that must not live inside the source repository.
func gitCommonDir(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "--git-common-dir")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git rev-parse --git-common-dir: %v", err)
	}
	p := strings.TrimSpace(string(out))
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", p, err)
	}
	return resolved
}

// isWithin reports whether path is root itself or nested under it, after
// resolving symlinks on both (macOS's /tmp is a symlink to /private/tmp, so
// a naive string prefix check on raw temp paths is unreliable).
func isWithin(t *testing.T, root, path string) bool {
	t.Helper()
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", root, err)
	}
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	return path == root || strings.HasPrefix(path, root+string(filepath.Separator))
}

// Constraint 1 (Task 4's review): a worktree taken directly off the source
// repo carries a .git FILE pointing back into the source repo's own object
// store, which the driver would then have to mount read-write into the
// container — an agent under --auto could write the user's real refs and
// objects. Cloning first must keep the workspace's git dir entirely outside
// the source repository, not merely outside its working-tree files.
func TestPrepareWorkspaceGitDirNotInsideSourceRepo(t *testing.T) {
	store := newTestStore(t)
	sess := seedSession(t, store, nil)
	sourceRepo, err := store.GetRepoByID(context.Background(), sess.RepoID)
	if err != nil {
		t.Fatalf("GetRepoByID: %v", err)
	}
	sourceRoot, err := filepath.EvalSymlinks(sourceRepo.SourcePath)
	if err != nil {
		t.Fatalf("EvalSymlinks(source): %v", err)
	}

	r := NewRunner(store, driver.NewFake(nil, fixturePlan()), Config{Enabled: true})
	ws, err := r.prepareWorkspace(context.Background(), sess)
	if err != nil {
		t.Fatalf("prepareWorkspace: %v", err)
	}
	defer ws.Cleanup(context.Background())

	gitDir := gitCommonDir(t, ws.Path())
	if isWithin(t, sourceRoot, gitDir) {
		t.Fatalf("workspace git dir %q is inside the source repository %q — local remediation must clone, not worktree, off the user's real repo", gitDir, sourceRoot)
	}
}

// Constraint 2 (Task 4's review): BranchName is deterministic, and
// `git worktree add -b <branch>` fails outright once a branch of that name
// already exists on the repo it runs against. A worktree taken directly off
// the source repo would carry the branch forward from the first attempt and
// die here on a retry. Cloning fixes this because every attempt clones fresh
// from the untouched source — this test is the guard: a future
// "optimization" that reuses a worktree instead of cloning would fail here.
func TestPrepareWorkspaceRetrySucceedsTwice(t *testing.T) {
	store := newTestStore(t)
	sess := seedSession(t, store, nil)
	r := NewRunner(store, driver.NewFake(nil, fixturePlan()), Config{Enabled: true})

	ws1, err := r.prepareWorkspace(context.Background(), sess)
	if err != nil {
		t.Fatalf("first prepareWorkspace: %v", err)
	}
	defer ws1.Cleanup(context.Background())

	ws2, err := r.prepareWorkspace(context.Background(), sess)
	if err != nil {
		t.Fatalf("second prepareWorkspace (retry) failed: %v", err)
	}
	defer ws2.Cleanup(context.Background())

	if ws1.Path() == ws2.Path() {
		t.Error("expected two independent workspaces, one per attempt")
	}
	if ws1.Branch() != ws2.Branch() {
		t.Errorf("branch changed across attempts: %q vs %q, want both %q", ws1.Branch(), ws2.Branch(), BranchName(sess.ID))
	}
}

// The source repo is never touched: only the scratch clone gets the new
// branch. A worktree-based implementation would leave BranchName checked out
// on the source repo itself.
func TestPrepareWorkspaceDoesNotBranchTheSourceRepo(t *testing.T) {
	store := newTestStore(t)
	sess := seedSession(t, store, nil)
	sourceRepo, err := store.GetRepoByID(context.Background(), sess.RepoID)
	if err != nil {
		t.Fatalf("GetRepoByID: %v", err)
	}

	r := NewRunner(store, driver.NewFake(nil, fixturePlan()), Config{Enabled: true})
	ws, err := r.prepareWorkspace(context.Background(), sess)
	if err != nil {
		t.Fatalf("prepareWorkspace: %v", err)
	}
	defer ws.Cleanup(context.Background())

	cmd := exec.Command("git", "branch", "--list", BranchName(sess.ID))
	cmd.Dir = sourceRepo.SourcePath
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git branch --list: %v", err)
	}
	if strings.TrimSpace(string(out)) != "" {
		t.Fatalf("source repo has the remediation branch checked out/created: %q", out)
	}
}

// prepareWorkspace must set sess.WorktreePath/BranchName from the prepared
// workspace so later phases (and a session resumed from the store after a
// gate pause) can find the same worktree.
func TestPrepareWorkspaceSetsSessionFields(t *testing.T) {
	store := newTestStore(t)
	sess := seedSession(t, store, nil)
	r := NewRunner(store, driver.NewFake(nil, fixturePlan()), Config{Enabled: true})

	ws, err := r.prepareWorkspace(context.Background(), sess)
	if err != nil {
		t.Fatalf("prepareWorkspace: %v", err)
	}
	defer ws.Cleanup(context.Background())

	if sess.WorktreePath != ws.Path() {
		t.Errorf("sess.WorktreePath = %q, want %q", sess.WorktreePath, ws.Path())
	}
	if sess.BranchName != BranchName(sess.ID) {
		t.Errorf("sess.BranchName = %q, want %q", sess.BranchName, BranchName(sess.ID))
	}
}

// A repo-supplied opencode.json overrides Wolf's injected permission
// document (the Task 4a spike finding), so prepareWorkspace must strip it
// from the ephemeral worktree before any driver call — and record the strip
// as a session event so it's visible in the audit trail.
func TestPrepareWorkspaceStripsAgentConfigAndRecordsEvent(t *testing.T) {
	store := newTestStore(t)
	sess := seedSession(t, store, nil)
	sourceRepo, err := store.GetRepoByID(context.Background(), sess.RepoID)
	if err != nil {
		t.Fatalf("GetRepoByID: %v", err)
	}
	// Commit a repo-supplied opencode.json into the source so it survives
	// the clone (an untracked file would not).
	if err := os.WriteFile(filepath.Join(sourceRepo.SourcePath, "opencode.json"), []byte(`{"permission":{"bash":"allow"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	commit := exec.Command("git", "commit", "-am", "add opencode.json")
	commit.Dir = sourceRepo.SourcePath
	commit.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com")
	add := exec.Command("git", "add", "-A")
	add.Dir = sourceRepo.SourcePath
	if out, err := add.CombinedOutput(); err != nil {
		t.Fatalf("git add: %s: %v", out, err)
	}
	if out, err := commit.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %s: %v", out, err)
	}

	r := NewRunner(store, driver.NewFake(nil, fixturePlan()), Config{Enabled: true})
	ws, err := r.prepareWorkspace(context.Background(), sess)
	if err != nil {
		t.Fatalf("prepareWorkspace: %v", err)
	}
	defer ws.Cleanup(context.Background())

	if _, err := os.Stat(filepath.Join(ws.Path(), "opencode.json")); !os.IsNotExist(err) {
		t.Fatalf("opencode.json was not stripped from the worktree (stat err = %v)", err)
	}

	events, err := store.ListRemediationEvents(context.Background(), sess.ID, 0)
	if err != nil {
		t.Fatalf("ListRemediationEvents: %v", err)
	}
	found := false
	for _, e := range events {
		if e.Type == "worktree.config_stripped" && strings.Contains(e.PayloadJSON, "opencode.json") {
			found = true
		}
	}
	if !found {
		t.Errorf("no worktree.config_stripped event recorded for opencode.json; events = %+v", events)
	}
}

// A missing repo must fail the session (not leave it stuck in "pending")
// rather than just bubbling a bare error up to an uninformed caller —
// prepareWorkspace follows the same named-return-plus-defer shape as
// runPlanPhase/runExecutePhase for exactly this reason.
func TestPrepareWorkspaceMissingRepoFailsSession(t *testing.T) {
	store := newTestStore(t)
	sess := seedSession(t, store, func(s *models.RemediationSession) {
		s.RepoID = "does-not-exist"
	})
	r := NewRunner(store, driver.NewFake(nil, fixturePlan()), Config{Enabled: true})

	if _, err := r.prepareWorkspace(context.Background(), sess); err == nil {
		t.Fatal("prepareWorkspace succeeded with a nonexistent repo, want error")
	}

	got, err := store.GetRemediationSession(context.Background(), sess.ID)
	if err != nil {
		t.Fatalf("GetRemediationSession: %v", err)
	}
	if got.Status != models.RemediationFailed {
		t.Fatalf("Status = %q, want %q", got.Status, models.RemediationFailed)
	}
}
