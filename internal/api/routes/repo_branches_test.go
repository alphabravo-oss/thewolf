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

	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/secrets"
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
	t.Setenv("WOLF_BROWSE_ROOTS", dir)
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
	t.Setenv("WOLF_BROWSE_ROOTS", dir)
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

func TestRepoBranchesOnlySelectGitHubCredentialsForTrustedHTTPSOrigin(t *testing.T) {
	store := db.NewTestPostgres(t)
	if err := store.CreateUser(t.Context(), &models.User{ID: "owner", Email: "branch-owner@example.test", PasswordHash: "test-only"}); err != nil {
		t.Fatal(err)
	}
	secrets.SetMasterKey([]byte("0123456789abcdef0123456789abcdef"))
	encrypted, err := secrets.Encrypt("test-only-github-credential")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateSecret(t.Context(), &models.Secret{ID: "branch-secret", UserID: "owner", KeyType: models.KeyTypeGitHubToken, KeyName: "github", EncryptedValue: encrypted}); err != nil {
		t.Fatal(err)
	}
	prev := listRemoteBranches
	t.Cleanup(func() { listRemoteBranches = prev })
	for _, tc := range []struct {
		remote string
		token  bool
	}{
		{"https://github.com/acme/widget.git", true},
		{"https://GITHUB.COM:443/acme/widget.git", true},
		{"https://github.com.evil.test/acme/widget.git", false},
		{"https://github.com@evil.test/acme/widget.git", false},
		{"https://evil.test/github.com/widget.git", false},
		{"https://evil.test/acme/widget.git?host=github.com", false},
		{"http://github.com/acme/widget.git", false},
		{"https://github.com:444/acme/widget.git", false},
	} {
		for _, source := range []models.SourceType{models.SourceTypeGit, models.SourceTypeLocal} {
			t.Run(string(source)+"/"+tc.remote, func(t *testing.T) {
				path := tc.remote
				if source == models.SourceTypeLocal {
					path = t.TempDir()
					t.Setenv("WOLF_BROWSE_ROOTS", path)
					gitRunTest(t, path, "init", "--initial-branch=main")
					gitRunTest(t, path, "remote", "add", "origin", tc.remote)
				}
				called := false
				listRemoteBranches = func(remote, token string) ([]string, error) {
					called = true
					if remote != tc.remote || (token != "") != tc.token {
						t.Errorf("remote/token authority mismatch for %q (credential present=%v)", remote, token != "")
					}
					return []string{"main"}, nil
				}
				_, _, err := repoBranchList(t.Context(), &Handler{Store: store}, &models.Repo{UserID: "owner", SourceType: source, SourcePath: path})
				if err != nil || !called {
					t.Fatalf("branch lookup: called=%v err=%v", called, err)
				}
			})
		}
	}
}

func TestRegisteredLocalBranchesEnforceMetadataAllowlistBeforeRemoteLookup(t *testing.T) {
	previous := listRemoteBranches
	t.Cleanup(func() { listRemoteBranches = previous })
	listRemoteBranches = func(string, string) ([]string, error) {
		t.Fatal("outside metadata triggered a remote lookup")
		return nil, nil
	}
	for _, kind := range []string{"outside-source", "outside-gitdir", "outside-config"} {
		t.Run(kind, func(t *testing.T) {
			allowed, outside := t.TempDir(), t.TempDir()
			t.Setenv("WOLF_BROWSE_ROOTS", allowed)
			gitRunTest(t, outside, "init", "-b", "outside-secret")
			gitRunTest(t, outside, "remote", "add", "origin", "https://github.com/acme/outside.git")
			path := outside
			if kind == "outside-gitdir" {
				path = allowed
				if err := os.WriteFile(filepath.Join(allowed, ".git"), []byte("gitdir: "+filepath.Join(outside, ".git")+"\n"), 0600); err != nil {
					t.Fatal(err)
				}
			} else if kind == "outside-config" {
				path = allowed
				gitRunTest(t, allowed, "init", "-b", "main")
				if err := os.Remove(filepath.Join(allowed, ".git", "config")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(outside, ".git", "config"), filepath.Join(allowed, ".git", "config")); err != nil {
					t.Fatal(err)
				}
			}
			branches, current, err := repoBranchList(t.Context(), &Handler{}, &models.Repo{SourceType: models.SourceTypeLocal, SourcePath: path})
			if err == nil || len(branches) != 0 || current != "" {
				t.Fatalf("outside metadata exposed: %v, %q, %v", branches, current, err)
			}
		})
	}
}
