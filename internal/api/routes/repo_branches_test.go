package routes

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestRepoBranchListGitHubUsesLiveRemote(t *testing.T) {
	prev := listRemoteBranches
	t.Cleanup(func() { listRemoteBranches = prev })

	var gotURL, gotToken string
	listRemoteBranches = func(remoteURL, token string) ([]string, error) {
		gotURL, gotToken = remoteURL, token
		return []string{"release", "main", "dev"}, nil
	}

	branches, current, err := repoBranchList(context.Background(), &Handler{}, &models.Repo{
		SourceType:    models.SourceTypeGitHub,
		SourcePath:    "acme/widget",
		DefaultBranch: "main",
	})
	if err != nil {
		t.Fatalf("repoBranchList: %v", err)
	}
	if gotURL != "https://github.com/acme/widget.git" {
		t.Fatalf("remote URL = %q", gotURL)
	}
	if gotToken != "" {
		t.Fatalf("token = %q, want empty without a stored secret", gotToken)
	}
	if current != "main" {
		t.Fatalf("current = %q, want main", current)
	}
	if !slices.Contains(branches, "dev") || !slices.Contains(branches, "release") {
		t.Fatalf("branches = %v, want live remote names", branches)
	}
}

func TestRepoBranchListGitHubSurfacesRemoteError(t *testing.T) {
	prev := listRemoteBranches
	t.Cleanup(func() { listRemoteBranches = prev })
	listRemoteBranches = func(string, string) ([]string, error) {
		return nil, errors.New("ls-remote failed")
	}

	_, _, err := repoBranchList(context.Background(), &Handler{}, &models.Repo{
		SourceType:    models.SourceTypeGitHub,
		SourcePath:    "acme/widget",
		DefaultBranch: "main",
	})
	if err == nil || !strings.Contains(err.Error(), "ls-remote failed") {
		t.Fatalf("error = %v, want ls-remote failure", err)
	}
}

func TestRepoBranchListLocalOriginUsesLiveRemote(t *testing.T) {
	dir := t.TempDir()
	gitRunTest(t, dir, "init", "--initial-branch=main")
	gitRunTest(t, dir, "remote", "add", "origin", "https://github.com/acme/local.git")

	prev := listRemoteBranches
	t.Cleanup(func() { listRemoteBranches = prev })
	var gotURL string
	listRemoteBranches = func(remoteURL, _ string) ([]string, error) {
		gotURL = remoteURL
		return []string{"main", "feature"}, nil
	}

	branches, _, err := repoBranchList(context.Background(), &Handler{}, &models.Repo{
		SourceType:    models.SourceTypeLocal,
		SourcePath:    dir,
		DefaultBranch: "main",
	})
	if err != nil {
		t.Fatalf("repoBranchList: %v", err)
	}
	if gotURL != "https://github.com/acme/local.git" {
		t.Fatalf("origin URL = %q", gotURL)
	}
	if !slices.Contains(branches, "feature") {
		t.Fatalf("branches = %v, want live origin names", branches)
	}
}

func TestRepoBranchListLocalWithoutOriginUsesCheckout(t *testing.T) {
	dir := t.TempDir()
	gitRunTest(t, dir, "init", "--initial-branch=main")
	gitRunTest(t, dir, "config", "user.email", "wolf@test")
	gitRunTest(t, dir, "config", "user.name", "wolf")
	gitRunTest(t, dir, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRunTest(t, dir, "add", "a.txt")
	gitRunTest(t, dir, "commit", "-m", "init")
	gitRunTest(t, dir, "branch", "topic")

	prev := listRemoteBranches
	t.Cleanup(func() { listRemoteBranches = prev })
	listRemoteBranches = func(string, string) ([]string, error) {
		t.Fatal("ListRemoteBranches should not run without origin")
		return nil, nil
	}

	branches, current, err := repoBranchList(context.Background(), &Handler{}, &models.Repo{
		SourceType:    models.SourceTypeLocal,
		SourcePath:    dir,
		DefaultBranch: "main",
	})
	if err != nil {
		t.Fatalf("repoBranchList: %v", err)
	}
	if current != "main" {
		t.Fatalf("current = %q, want main", current)
	}
	if !slices.Contains(branches, "main") || !slices.Contains(branches, "topic") {
		t.Fatalf("branches = %v, want local checkout names", branches)
	}
}

func gitRunTest(t *testing.T, dir string, args ...string) {
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
