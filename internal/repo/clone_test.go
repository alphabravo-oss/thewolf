package repo

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

func TestCloneOrRefreshFetchesRequestedBranch(t *testing.T) {
	upstream, seed := setupTwoBranchUpstream(t)
	cache := filepath.Join(t.TempDir(), "cache")

	got, err := cloneOrRefresh(cache, upstream, "main", nil)
	if err != nil {
		t.Fatalf("first clone main: %v", err)
	}
	if readFile(t, filepath.Join(got, "a.txt")) != "main-1" {
		t.Fatalf("first clone content = %q, want main-1", readFile(t, filepath.Join(got, "a.txt")))
	}
	if branch := gitHead(t, got); branch != "main" {
		t.Fatalf("first clone branch = %q, want main", branch)
	}

	if err := os.WriteFile(filepath.Join(seed, "a.txt"), []byte("main-2"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, seed, "commit", "-am", "main-2")
	gitRun(t, seed, "push", "origin", "main")

	got, err = cloneOrRefresh(cache, upstream, "main", nil)
	if err != nil {
		t.Fatalf("refresh main: %v", err)
	}
	if readFile(t, filepath.Join(got, "a.txt")) != "main-2" {
		t.Fatalf("refreshed main = %q, want main-2", readFile(t, filepath.Join(got, "a.txt")))
	}

	got, err = cloneOrRefresh(cache, upstream, "dev", nil)
	if err != nil {
		t.Fatalf("switch to dev: %v", err)
	}
	if readFile(t, filepath.Join(got, "a.txt")) != "dev-1" {
		t.Fatalf("dev content = %q, want dev-1", readFile(t, filepath.Join(got, "a.txt")))
	}
	if branch := gitHead(t, got); branch != "dev" {
		t.Fatalf("switched branch = %q, want dev", branch)
	}

	if _, err := cloneOrRefresh(cache, upstream, "does-not-exist", nil); err == nil {
		t.Fatal("expected error for unknown branch")
	}
}

func TestCloneOrRefreshLeavesSingleBranchCache(t *testing.T) {
	upstream, _ := setupTwoBranchUpstream(t)
	cache := filepath.Join(t.TempDir(), "single")

	if _, err := git.PlainClone(cache, false, &git.CloneOptions{
		URL:           upstream,
		ReferenceName: plumbing.NewBranchReferenceName("main"),
		SingleBranch:  true,
	}); err != nil {
		t.Fatalf("seed single-branch clone: %v", err)
	}
	if readFile(t, filepath.Join(cache, "a.txt")) != "main-1" {
		t.Fatal("expected main-1 after single-branch clone")
	}

	got, err := cloneOrRefresh(cache, upstream, "dev", nil)
	if err != nil {
		t.Fatalf("refresh single-branch cache onto dev: %v", err)
	}
	if readFile(t, filepath.Join(got, "a.txt")) != "dev-1" {
		t.Fatalf("dev content = %q, want dev-1", readFile(t, filepath.Join(got, "a.txt")))
	}
	if branch := gitHead(t, got); branch != "dev" {
		t.Fatalf("branch = %q, want dev", branch)
	}
}

func setupTwoBranchUpstream(t *testing.T) (upstream, seed string) {
	t.Helper()
	root := t.TempDir()
	seed = filepath.Join(root, "seed")
	if err := os.Mkdir(seed, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, seed, "init", "--initial-branch=main")
	gitRun(t, seed, "config", "user.email", "wolf@test")
	gitRun(t, seed, "config", "user.name", "wolf")
	gitRun(t, seed, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(seed, "a.txt"), []byte("main-1"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, seed, "add", "a.txt")
	gitRun(t, seed, "commit", "-m", "main-1")
	gitRun(t, seed, "checkout", "-b", "dev")
	if err := os.WriteFile(filepath.Join(seed, "a.txt"), []byte("dev-1"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, seed, "commit", "-am", "dev-1")
	gitRun(t, seed, "checkout", "main")

	upstream = filepath.Join(root, "upstream.git")
	gitRun(t, root, "init", "--bare", "--initial-branch=main", upstream)
	gitRun(t, seed, "remote", "add", "origin", upstream)
	gitRun(t, seed, "push", "-u", "origin", "main")
	gitRun(t, seed, "push", "-u", "origin", "dev")
	return upstream, seed
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=wolf",
		"GIT_AUTHOR_EMAIL=wolf@test",
		"GIT_COMMITTER_NAME=wolf",
		"GIT_COMMITTER_EMAIL=wolf@test",
		"GIT_CONFIG_NOSYSTEM=1",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s: %v", args, out, err)
	}
}

func gitHead(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
