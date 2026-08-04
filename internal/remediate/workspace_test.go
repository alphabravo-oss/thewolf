package remediate

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/remediate/driver"
	"github.com/alphabravocompany/thewolf/internal/remediate/meter"
	"github.com/alphabravocompany/thewolf/internal/remediate/plan"
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
	// CloneRoot is the scratch clone's own root — NOT ws.Path() (the
	// worktree, a different directory worktreed off the clone) — the
	// handle a later cleanup step needs since it isn't derivable from
	// WorktreePath alone (independent temp roots).
	if sess.CloneRoot == "" {
		t.Error("sess.CloneRoot is empty for a local-source session")
	}
	if sess.CloneRoot == sess.WorktreePath {
		t.Errorf("sess.CloneRoot must not equal WorktreePath: both %q", sess.CloneRoot)
	}
	if _, err := os.Stat(filepath.Join(sess.CloneRoot, ".git")); err != nil {
		t.Errorf("sess.CloneRoot %q does not look like a git clone: %v", sess.CloneRoot, err)
	}
}

// The in-memory struct isn't what the resume design actually rests on — a
// gated session pauses and resumes in a LATER Runner that only has the
// database row to go on. This asserts the field survives the round trip
// through a real Run() call, not just prepareWorkspace's return value.
func TestRunPersistsWorktreePath(t *testing.T) {
	store := newTestStore(t)
	sess := seedSession(t, store, func(s *models.RemediationSession) {
		s.PlanGateEnabled = true
		s.PatchGateEnabled = false
	})
	r := NewRunner(store, driver.NewFake([]meter.Event{{Type: "assistant"}}, fixturePlan()),
		Config{Enabled: true, MaxTurns: 10, AllowYolo: true})

	if err := r.Run(context.Background(), sess.ID); err != nil {
		t.Fatalf("Run: %v", err)
	}

	got, err := store.GetRemediationSession(context.Background(), sess.ID)
	if err != nil {
		t.Fatalf("GetRemediationSession: %v", err)
	}
	if got.WorktreePath == "" {
		t.Error("persisted WorktreePath is empty after Run")
	}
	if got.BranchName != BranchName(sess.ID) {
		t.Errorf("persisted BranchName = %q, want %q", got.BranchName, BranchName(sess.ID))
	}
	if got.CloneRoot == "" {
		t.Error("persisted CloneRoot is empty after Run for a local-source session")
	}
}

// midPlanAssertingDriver reads the session back from the store from INSIDE
// its own Plan method — i.e. while driver.Plan is "running", before it
// returns. This is the only shape that can catch a regression where
// prepareWorkspace's fields are set in memory but not persisted before the
// long-running driver call starts: a post-Run assertion (like
// TestRunPersistsWorktreePath above) can't distinguish "written before
// Plan" from "written by the post-plan transition after Plan returns" —
// both look identical once Run has already completed.
type midPlanAssertingDriver struct {
	t         *testing.T
	store     db.Store
	sessionID string
	plan      *plan.Plan
	checked   bool
}

func (d *midPlanAssertingDriver) Plan(ctx context.Context, req driver.PlanRequest) (*plan.Plan, meter.Usage, error) {
	d.t.Helper()
	d.checked = true
	got, err := d.store.GetRemediationSession(ctx, d.sessionID)
	if err != nil {
		d.t.Fatalf("GetRemediationSession mid-plan: %v", err)
	}
	if got.WorktreePath == "" {
		d.t.Error("WorktreePath not yet persisted when driver.Plan runs")
	}
	if got.CloneRoot == "" {
		d.t.Error("CloneRoot not yet persisted when driver.Plan runs")
	}
	if req.OnEvent != nil {
		req.OnEvent(meter.Event{Type: "step_finish"})
	}
	return d.plan, meter.Usage{Turns: 1}, nil
}

func (d *midPlanAssertingDriver) Execute(_ context.Context, req driver.ExecuteRequest) (*driver.PatchSeries, meter.Usage, error) {
	if req.OnEvent != nil {
		req.OnEvent(meter.Event{Type: "step_finish"})
	}
	return &driver.PatchSeries{}, meter.Usage{Turns: 1}, nil
}

// The regression this pins: moving prepareWorkspace after the CAS claim
// (to fix the concurrency leak) accidentally left WorktreePath/CloneRoot
// unpersisted for the whole plan phase, since prepareWorkspace only sets
// them in memory. If the process died during driver.Plan, RecoverOrphan-
// Sessions would mark the row failed with both fields still empty —
// reopening the exact leaked-clone-with-no-handle problem persisting
// CloneRoot was meant to close.
func TestRunPersistsWorktreePathBeforeDriverPlanRuns(t *testing.T) {
	store := newTestStore(t)
	sess := seedSession(t, store, func(s *models.RemediationSession) {
		s.PlanGateEnabled = true
		s.PatchGateEnabled = false
	})
	d := &midPlanAssertingDriver{t: t, store: store, sessionID: sess.ID, plan: fixturePlan()}
	r := NewRunner(store, d, Config{Enabled: true, MaxTurns: 10, AllowYolo: true})

	if err := r.Run(context.Background(), sess.ID); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !d.checked {
		t.Fatal("driver.Plan was never called; this test did not exercise the mid-plan window at all")
	}
}

// SECURITY: `git clone --local` hardlinks object files by default — the
// clone's objects are the SAME inode as the source's. The driver mounts
// this clone's .git directory read-write, and the execute permission
// document allows make/go test/npm run/pytest (repo-supplied code
// execution), so a hardlinked object store lets a write through the clone
// reach — and corrupt — the user's real repository. --no-hardlinks must
// force a real copy so the clone's objects are genuinely independent.
func TestPrepareWorkspaceObjectsAreNotHardlinkedToSource(t *testing.T) {
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

	if sess.CloneRoot == "" {
		t.Fatal("sess.CloneRoot is empty; cannot check object isolation")
	}
	if sameInodeAsAnyObject(t, sourceRepo.SourcePath, sess.CloneRoot) {
		t.Fatal("clone shares an object-file inode with the source repo — a write through the clone (e.g. a driver-run build step) could corrupt the user's real objects")
	}
}

// sameInodeAsAnyObject reports whether any object file under clonePath's
// .git/objects is the SAME file (os.SameFile: same inode on Unix, same
// volume+file-index on Windows) as the same-named object file under
// sourcePath's .git/objects — i.e. hardlinked to it, not a real copy.
// os.SameFile is used rather than poking at syscall.Stat_t directly so this
// stays portable rather than failing to compile on non-Unix platforms.
//
// Fails loudly if it never found a same-named pair to compare: with zero
// comparisons this would otherwise report "not hardlinked" having tested
// nothing (e.g. if a future git version packs objects, or the objects
// directory structure changes), which is exactly the silent-pass failure
// mode this whole review round was about closing.
func sameInodeAsAnyObject(t *testing.T, sourcePath, clonePath string) bool {
	t.Helper()
	cloneObjDir := filepath.Join(clonePath, ".git", "objects")
	found := false
	compared := 0
	err := filepath.WalkDir(cloneObjDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, rerr := filepath.Rel(cloneObjDir, path)
		if rerr != nil {
			return rerr
		}
		srcObj := filepath.Join(sourcePath, ".git", "objects", rel)
		srcInfo, statErr := os.Stat(srcObj)
		if statErr != nil {
			return nil // this object doesn't exist in the source; nothing to compare
		}
		cloneInfo, err := os.Stat(path)
		if err != nil {
			return err
		}
		compared++
		if os.SameFile(srcInfo, cloneInfo) {
			found = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk clone objects: %v", err)
	}
	if compared == 0 {
		t.Fatal("compared zero object-file pairs — this test proved nothing; the fixture or the walk logic needs fixing")
	}
	return found
}

// The strip-failure ordering fix: if StripAgentConfig fails AFTER
// workspace.Prepare succeeded, prepared.Cleanup(ctx) deletes the worktree —
// so the session must not end up with WorktreePath/BranchName/CloneRoot
// persisted pointing at directories that no longer exist. Uses the
// stripAgentConfig package var (a test seam) because a permission-based
// filesystem trick doesn't survive git's own checkout, which resets
// directory modes regardless of what the source repo's permissions were.
func TestPrepareWorkspaceClearsWorktreePathWhenStripFails(t *testing.T) {
	store := newTestStore(t)
	sess := seedSession(t, store, nil)
	r := NewRunner(store, driver.NewFake(nil, fixturePlan()), Config{Enabled: true})

	orig := stripAgentConfig
	stripAgentConfig = func(string) ([]string, error) { return nil, errors.New("boom") }
	defer func() { stripAgentConfig = orig }()

	if _, err := r.prepareWorkspace(context.Background(), sess); err == nil {
		t.Fatal("prepareWorkspace succeeded despite a forced strip failure, want error")
	}

	got, err := store.GetRemediationSession(context.Background(), sess.ID)
	if err != nil {
		t.Fatalf("GetRemediationSession: %v", err)
	}
	if got.WorktreePath != "" {
		t.Errorf("WorktreePath = %q, want empty — prepared.Cleanup already deleted it", got.WorktreePath)
	}
	if got.BranchName != "" {
		t.Errorf("BranchName = %q, want empty", got.BranchName)
	}
	if got.CloneRoot != "" {
		t.Errorf("CloneRoot = %q, want empty", got.CloneRoot)
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

// SECURITY (CWE-88, argument injection): Repo.SourcePath traces back to
// user-supplied API input (createRepoRequest.SourcePath in
// internal/api/routes/repos.go) validated only for emptiness — nothing
// there rejects a leading '-'. Handed straight to `git clone` as a
// positional argument, a value like "--upload-pack=<cmd>" is parsed by git
// as a FLAG instead of a path. cloneLocalForRemediation must reject this
// itself rather than relying solely on the "--" separator downstream: the
// error text below (not just "an error") pins that OUR check is what fired,
// not some incidental failure from git.
func TestCloneLocalForRemediationRejectsLeadingDash(t *testing.T) {
	_, cleanup, err := cloneLocalForRemediation(context.Background(), "--upload-pack=touch /tmp/wolf-pwned")
	if cleanup != nil {
		defer cleanup()
	}
	if err == nil {
		t.Fatal("cloneLocalForRemediation succeeded with a leading-dash source path, want error")
	}
	if !strings.Contains(err.Error(), "must not start with '-'") {
		t.Fatalf("err = %v, want it to name the leading-dash rejection explicitly", err)
	}
}

// A relative source path is already a bug at the caller (Repo.SourcePath
// for a registered local repo should always be absolute); rejecting it
// closes off resolving against whatever the wolf process's CWD happens to
// be at clone time.
func TestCloneLocalForRemediationRejectsRelativePath(t *testing.T) {
	_, cleanup, err := cloneLocalForRemediation(context.Background(), "relative/path/to/repo")
	if cleanup != nil {
		defer cleanup()
	}
	if err == nil {
		t.Fatal("cloneLocalForRemediation succeeded with a relative source path, want error")
	}
	if !strings.Contains(err.Error(), "must be absolute") {
		t.Fatalf("err = %v, want it to name the relative-path rejection explicitly", err)
	}
}
