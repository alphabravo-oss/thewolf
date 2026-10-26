package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

// initRepo creates a real temp git repo with one committed file and returns its
// path. Real temp git repos are cheap and exercise the actual worktree path
// (per the phase guidance), so workspace tests do NOT stub git.
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %s: %v", strings.Join(args, " "), out, err)
		}
	}
	run("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-m", "initial")
	return dir
}

func localRepo(path string) *models.Repo {
	return &models.Repo{SourceType: models.SourceTypeLocal, SourcePath: path}
}

func TestPrepareLocal_CreatesWorktreeOnNewBranch(t *testing.T) {
	repo := initRepo(t)
	ctx := context.Background()

	ws, err := Prepare(ctx, Options{Repo: localRepo(repo), Branch: "wolf-fix/test/sql"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	defer ws.Cleanup(ctx)

	if ws.Kind() != KindWorktree {
		t.Errorf("kind = %q, want worktree", ws.Kind())
	}
	if ws.Branch() != "wolf-fix/test/sql" {
		t.Errorf("branch = %q", ws.Branch())
	}
	// The worktree path exists, is separate from the origin, and has the seed file.
	if ws.Path() == repo {
		t.Fatal("worktree path must differ from the origin repo")
	}
	if _, err := os.Stat(filepath.Join(ws.Path(), "main.go")); err != nil {
		t.Fatalf("seed file missing in worktree: %v", err)
	}
	// The new branch is checked out in the worktree.
	out, err := runGit(ctx, ws.Path(), "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out); got != "wolf-fix/test/sql" {
		t.Errorf("worktree HEAD = %q, want wolf-fix/test/sql", got)
	}
}

func TestChangedFiles_TrackedAndUntracked(t *testing.T) {
	repo := initRepo(t)
	ctx := context.Background()
	ws, err := Prepare(ctx, Options{Repo: localRepo(repo), Branch: "wolf-fix/test/x"})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Cleanup(ctx)

	// Modify a tracked file and add an untracked one.
	if err := os.WriteFile(filepath.Join(ws.Path(), "main.go"), []byte("package main\n\nfunc main() { _ = 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws.Path(), "new.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := ws.ChangedFiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, f := range files {
		got[f] = true
	}
	if !got["main.go"] {
		t.Errorf("expected main.go in changed files, got %v", files)
	}
	if !got["new.go"] {
		t.Errorf("expected untracked new.go in changed files, got %v", files)
	}
}

func TestDiff_IncludesUntracked(t *testing.T) {
	repo := initRepo(t)
	ctx := context.Background()
	ws, err := Prepare(ctx, Options{Repo: localRepo(repo), Branch: "wolf-fix/test/d"})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Cleanup(ctx)
	// A review diff must not execute a helper installed by a fix engine.
	if _, err := runGit(ctx, ws.Path(), "config", "core.fsmonitor", "sh -c 'echo invoked > helper-executed'"); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, ws.Path(), "config", "diff.fixture.textconv", "sh -c 'echo invoked > helper-executed'"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws.Path(), ".gitattributes"), []byte("*.go diff=fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(ws.Path(), "added.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	diff, err := ws.Diff(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff, "added.go") {
		t.Errorf("diff should reference the new untracked file, got:\n%s", diff)
	}
	if _, err := os.Stat(filepath.Join(ws.Path(), "helper-executed")); !os.IsNotExist(err) {
		t.Fatalf("textconv helper executed: %v", err)
	}
}

func TestRollback_TrackedFileRestoredFromHEAD(t *testing.T) {
	repo := initRepo(t)
	ctx := context.Background()
	ws, err := Prepare(ctx, Options{Repo: localRepo(repo), Branch: "wolf-fix/test/r"})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Cleanup(ctx)

	mainPath := filepath.Join(ws.Path(), "main.go")
	orig, _ := os.ReadFile(mainPath)
	if err := os.WriteFile(mainPath, []byte("package main\n// broken\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ws.Rollback(ctx, "main.go"); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	after, _ := os.ReadFile(mainPath)
	if string(after) != string(orig) {
		t.Errorf("rollback did not restore main.go: got %q want %q", after, orig)
	}
	files, _ := ws.ChangedFiles(ctx)
	if len(files) != 0 {
		t.Errorf("expected no changed files after rollback, got %v", files)
	}
}

func TestRollback_UntrackedFileRemoved(t *testing.T) {
	repo := initRepo(t)
	ctx := context.Background()
	ws, err := Prepare(ctx, Options{Repo: localRepo(repo), Branch: "wolf-fix/test/ru"})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Cleanup(ctx)

	created := filepath.Join(ws.Path(), "scratch.go")
	if err := os.WriteFile(created, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ws.Rollback(ctx, "scratch.go"); err != nil {
		t.Fatalf("Rollback untracked: %v", err)
	}
	if _, err := os.Stat(created); !os.IsNotExist(err) {
		t.Errorf("untracked file should be removed by rollback, stat err = %v", err)
	}
}

func TestCleanup_RemovesWorktreeKeepsBranch(t *testing.T) {
	repo := initRepo(t)
	ctx := context.Background()
	ws, err := Prepare(ctx, Options{Repo: localRepo(repo), Branch: "wolf-fix/test/keep"})
	if err != nil {
		t.Fatal(err)
	}
	wtPath := ws.Path()

	if err := ws.Cleanup(ctx); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	// Worktree dir is gone.
	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Errorf("worktree dir should be gone after cleanup, stat err = %v", err)
	}
	// The branch survives on the origin (v1 deliverable: a reviewable branch).
	out, err := runGit(ctx, repo, "branch", "--list", "wolf-fix/test/keep")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "wolf-fix/test/keep") {
		t.Errorf("fix branch should survive cleanup, branch list:\n%s", out)
	}
	// Idempotent.
	if err := ws.Cleanup(ctx); err != nil {
		t.Errorf("second Cleanup should be a no-op, got %v", err)
	}
}

func TestDiscard_RemovesWorktreeAndBranch(t *testing.T) {
	repo := initRepo(t)
	ctx := context.Background()
	ws, err := Prepare(ctx, Options{Repo: localRepo(repo), Branch: "wolf-fix/test/discard"})
	if err != nil {
		t.Fatal(err)
	}
	wtPath := ws.Path()
	if err := ws.Discard(ctx); err != nil {
		t.Fatalf("Discard: %v", err)
	}
	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Errorf("worktree dir should be gone after discard, stat err = %v", err)
	}
	out, err := runGit(ctx, repo, "branch", "--list", "wolf-fix/test/discard")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "wolf-fix/test/discard") {
		t.Errorf("fix branch should be deleted on discard, branch list:\n%s", out)
	}
}

func TestCommit_PersistsOnBranchAfterCleanup(t *testing.T) {
	repo := initRepo(t)
	ctx := context.Background()
	ws, err := Prepare(ctx, Options{Repo: localRepo(repo), Branch: "wolf-fix/test/commit"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws.Path(), "main.go"), []byte("package main\n\nfunc main() { println(1) }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ws.Commit(ctx, "fix: demo"); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if err := ws.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	out, err := runGit(ctx, repo, "log", "-1", "--format=%s", "wolf-fix/test/commit")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "fix: demo") {
		t.Fatalf("branch log = %q, want commit message to survive cleanup", out)
	}
}

func TestPush_RefusesDefaultBranch(t *testing.T) {
	ws := &Workspace{path: "/tmp", branch: "main", defaultBranch: "main"}
	if _, err := ws.Push(context.Background()); err == nil {
		t.Fatal("expected refuse to push main")
	}
	ws.branch = "master"
	if _, err := ws.Push(context.Background()); err == nil {
		t.Fatal("expected refuse to push master")
	}
}

func TestPrepare_SSHUnsupported(t *testing.T) {
	node := "node-1"
	repo := &models.Repo{SourceType: models.SourceTypeSSH, SourcePath: "/srv/app", RemoteNodeID: &node}
	_, err := Prepare(context.Background(), Options{Repo: repo, Branch: "wolf-fix/test/ssh"})
	if err == nil || !strings.Contains(err.Error(), "ssh") {
		t.Fatalf("expected ssh-unsupported error, got %v", err)
	}
}

func TestPrepare_Validation(t *testing.T) {
	ctx := context.Background()
	if _, err := Prepare(ctx, Options{Branch: "b"}); err == nil {
		t.Error("expected error for nil repo")
	}
	if _, err := Prepare(ctx, Options{Repo: localRepo("/x")}); err == nil {
		t.Error("expected error for empty branch")
	}
}

func TestGitHubCloneURL(t *testing.T) {
	cases := map[string]string{
		"owner/repo":                        "https://github.com/owner/repo.git",
		"github.com/owner/repo":             "https://github.com/owner/repo.git",
		"https://github.com/owner/repo":     "https://github.com/owner/repo.git",
		"https://github.com/owner/repo.git": "https://github.com/owner/repo.git",
		"git@github.com:owner/repo.git":     "https://github.com/owner/repo.git",
	}
	for in, want := range cases {
		got, err := githubCloneURL(in)
		if err != nil {
			t.Errorf("githubCloneURL(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("githubCloneURL(%q) = %q, want %q", in, got, want)
		}
	}
	for _, source := range []string{"nope", "owner/repo/extra", "../repo", "owner/..", "owner/repo?token=secret"} {
		if _, err := githubCloneURL(source); err == nil {
			t.Errorf("accepted malformed source %q", source)
		}
	}
}

func TestGitHubAuthEnv_StoresTokenOutsideCloneArgs(t *testing.T) {
	env, cleanup, err := githubAuthEnv(t.TempDir(), "tok'quoted")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	joined := strings.Join(env, "\n")
	if strings.Contains(joined, "tok'quoted") {
		t.Fatalf("token must not be placed directly in environment values: %s", joined)
	}
	if !strings.Contains(joined, "GIT_ASKPASS=") || !strings.Contains(joined, "GIT_TERMINAL_PROMPT=0") {
		t.Fatalf("missing git askpass env: %s", joined)
	}
}

// TestPrepareGitHub_ClonesAndBranches uses a real local repo as the clone
// source (file:// URL) so no network is touched: clone-for-write mechanics are
// exercised against a real git remote, with the token-derivation path bypassed
// via CloneURL.
func TestPrepareGitHub_ClonesAndBranches(t *testing.T) {
	origin := initRepo(t)
	ctx := context.Background()
	ws, err := Prepare(ctx, Options{
		Repo:     &models.Repo{SourceType: models.SourceTypeGitHub, SourcePath: "owner/repo"},
		Branch:   "wolf-fix/test/gh",
		Token:    "tok",
		CloneURL: "file://" + origin,
	})
	if err != nil {
		t.Fatalf("Prepare github: %v", err)
	}
	defer ws.Cleanup(ctx)

	if ws.Kind() != KindClone {
		t.Errorf("kind = %q, want clone", ws.Kind())
	}
	if _, err := os.Stat(filepath.Join(ws.Path(), "main.go")); err != nil {
		t.Errorf("clone missing seed file: %v", err)
	}
	out, err := runGit(ctx, ws.Path(), "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out); got != "wolf-fix/test/gh" {
		t.Errorf("clone HEAD = %q, want wolf-fix/test/gh", got)
	}
}

func TestPrepareGitHub_RequiresToken(t *testing.T) {
	_, err := Prepare(context.Background(), Options{
		Repo:   &models.Repo{SourceType: models.SourceTypeGitHub, SourcePath: "owner/repo"},
		Branch: "b",
	})
	if err == nil || !strings.Contains(err.Error(), "token") {
		t.Fatalf("expected token-required error, got %v", err)
	}
}

func TestPushRedactsCredentialFromRemoteError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte("#!/bin/sh\necho 'remote rejected push-secret-value' >&2\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	ws := &Workspace{path: dir, tmpRoot: dir, branch: "wolf-fix/review", defaultBranch: "main", token: "push-secret-value"}
	if _, err := ws.Push(t.Context()); err == nil || strings.Contains(err.Error(), ws.token) || !strings.Contains(err.Error(), "remote rejected ***") {
		t.Fatalf("unsafe/lost push error: %v", err)
	}
}

func TestCleanupAndDiscardRetainWorkspaceOnFailure(t *testing.T) {
	repo := initRepo(t)
	ws, err := Prepare(t.Context(), Options{Repo: localRepo(repo), Branch: "wolf-fix/cleanup-retry", BaseDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	root := ws.tmpRoot
	// Git refuses a locked worktree. Cleanup must not bypass it with RemoveAll,
	// or clear the path that a retry needs.
	if _, err := runGit(t.Context(), repo, "worktree", "lock", ws.Path()); err != nil {
		t.Fatal(err)
	}
	if err := ws.Cleanup(t.Context()); err == nil {
		t.Fatal("locked worktree cleanup succeeded")
	}
	if err := ws.Discard(t.Context()); err == nil {
		t.Fatal("locked worktree discard succeeded")
	}
	if _, err := os.Stat(filepath.Join(ws.Path(), "main.go")); err != nil || ws.tmpRoot != root {
		t.Fatalf("failed cleanup lost workspace: %v root=%q", err, ws.tmpRoot)
	}
	if _, err := runGit(t.Context(), repo, "show-ref", "--verify", "refs/heads/"+ws.Branch()); err != nil {
		t.Fatal("failed cleanup deleted branch")
	}
	if _, err := runGit(t.Context(), repo, "worktree", "unlock", ws.Path()); err != nil {
		t.Fatal(err)
	}
	if err := ws.Discard(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := ws.Discard(t.Context()); err != nil {
		t.Fatalf("discard retry: %v", err)
	}
}

func TestDiscardPathRequiresMatchingMetadata(t *testing.T) {
	repo := initRepo(t)
	ws, err := Prepare(t.Context(), Options{Repo: localRepo(repo), Branch: "wolf-fix/retained", BaseDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ws.Cleanup(context.Background()) }()
	if err := DiscardPath(t.Context(), ws.Path(), "wolf-fix/another-job"); err == nil {
		t.Fatal("mismatched branch accepted")
	}
	if err := os.WriteFile(filepath.Join(ws.tmpRoot, workspaceMetaName), []byte("invalid"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := DiscardPath(t.Context(), ws.Path(), ws.Branch()); err == nil {
		t.Fatal("unreadable metadata authorized deletion")
	}
	if _, err := os.Stat(filepath.Join(ws.Path(), "main.go")); err != nil {
		t.Fatalf("retained work deleted: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := ws.Cleanup(ctx); err == nil {
		t.Fatal("cancelled cleanup succeeded")
	}
	if _, err := os.Stat(ws.Path()); err != nil {
		t.Fatal("cancelled cleanup deleted workspace")
	}
}

func TestAcceptRemediationPreservesLocalBranchAndRejectsUnsafeCleanup(t *testing.T) {
	repo := localRepo(initRepo(t))
	ws, err := Prepare(t.Context(), Options{Repo: repo, Branch: "wolf-fix/handoff", BaseDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	rem := &models.Remediation{Branch: ws.Branch(), WorkspacePath: ws.Path()}
	original, err := runGit(t.Context(), repo.SourcePath, "rev-parse", "refs/heads/"+rem.Branch)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws.Path(), "review.txt"), []byte("uncommitted review"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := AcceptRemediation(t.Context(), rem, repo); err == nil {
		t.Fatal("dirty worktree accepted")
	}
	if _, err := os.Stat(filepath.Join(ws.Path(), "review.txt")); err != nil {
		t.Fatal("uncommitted review lost")
	}
	if err := os.Remove(filepath.Join(ws.Path(), "review.txt")); err != nil {
		t.Fatal(err)
	}
	sha, err := AcceptRemediation(t.Context(), rem, repo)
	if err != nil || sha != strings.TrimSpace(original) {
		t.Fatalf("handoff commit: %q %v", sha, err)
	}
	// Retrying after cleanup but before its database acknowledgement is safe:
	// the origin branch, not the deleted workspace, proves the deliverable.
	again, err := AcceptRemediation(t.Context(), rem, repo)
	if err != nil || again != sha {
		t.Fatalf("handoff retry: %q %v", again, err)
	}
	cloneRoot := t.TempDir()
	clonePath := filepath.Join(cloneRoot, "clone")
	if err := os.Mkdir(clonePath, 0o700); err != nil {
		t.Fatal(err)
	}
	clone := &Workspace{path: clonePath, tmpRoot: cloneRoot, branch: rem.Branch, kind: KindClone}
	if err := writeMeta(clone); err != nil {
		t.Fatal(err)
	}
	rem.WorkspacePath = clonePath
	if _, err := AcceptRemediation(t.Context(), rem, repo); err == nil {
		t.Fatal("unpublished clone accepted")
	}
	if _, err := os.Stat(clonePath); err != nil {
		t.Fatal("unpublished clone deleted")
	}
}

func TestPublishedCloneCleanupRequiresExactCleanCommit(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	clonePath := filepath.Join(root, "clone")
	if _, err := runGit(ctx, root, "clone", initRepo(t), clonePath); err != nil {
		t.Fatal(err)
	}
	branch := "wolf-fix/published"
	if _, err := runGit(ctx, clonePath, "checkout", "-b", branch); err != nil {
		t.Fatal(err)
	}
	ws := &Workspace{path: clonePath, tmpRoot: root, branch: branch, kind: KindClone}
	if err := writeMeta(ws); err != nil {
		t.Fatal(err)
	}
	head, err := runGit(ctx, clonePath, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	rem := &models.Remediation{State: models.RemediationFrozen, Branch: branch, WorkspacePath: clonePath, PublishedSHA: "another-commit"}
	repo := &models.Repo{SourceType: models.SourceTypeGitHub, SourcePath: "example/repo"}
	if _, err := AcceptRemediation(ctx, rem, repo); err == nil {
		t.Fatal("unpublished commit was deleted")
	}
	rem.PublishedSHA = strings.TrimSpace(head)
	dirty := filepath.Join(clonePath, "uncommitted.txt")
	if err := os.WriteFile(dirty, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := AcceptRemediation(ctx, rem, repo); err == nil {
		t.Fatal("uncommitted file was deleted")
	}
	if err := os.Remove(dirty); err != nil {
		t.Fatal(err)
	}
	if sha, err := AcceptRemediation(ctx, rem, repo); err != nil || sha != rem.PublishedSHA {
		t.Fatalf("published cleanup: %q %v", sha, err)
	}
	if _, err := os.Stat(clonePath); !os.IsNotExist(err) {
		t.Fatalf("clone still exists: %v", err)
	}
	if sha, err := AcceptRemediation(ctx, rem, repo); err != nil || sha != rem.PublishedSHA {
		t.Fatalf("retry after removal: %q %v", sha, err)
	}
}
