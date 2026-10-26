package git

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCommandIgnoresInheritedGitRedirection(t *testing.T) {
	repo, other := t.TempDir(), t.TempDir()
	gitRun(t, repo, "init")
	gitRun(t, other, "init")
	config := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(config, []byte("[wolf]\n injected = true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{
		"GIT_DIR": filepath.Join(other, ".git"), "GIT_WORK_TREE": other,
		"GIT_CONFIG_GLOBAL": config, "GIT_CONFIG_COUNT": "1", "GIT_CONFIG_KEY_0": "wolf.injected", "GIT_CONFIG_VALUE_0": "true",
		"GIT_CONFIG_PARAMETERS": "'wolf.injected=true'",
	} {
		t.Setenv(key, value)
	}
	cmd := Command(t.Context(), nil, "rev-parse", "--show-toplevel")
	cmd.Dir = repo
	out, err := cmd.CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != repo {
		t.Fatalf("repository redirected: %q, %v", out, err)
	}
	cmd = Command(t.Context(), nil, "config", "--get", "wolf.injected")
	cmd.Dir = repo
	if out, err = cmd.CombinedOutput(); err == nil || len(out) != 0 {
		t.Fatalf("inherited config reached Git: %q, %v", out, err)
	}
}

func TestCommandCommitSkipsHooksAndSigning(t *testing.T) {
	repo := t.TempDir()
	gitRun(t, repo, "init")
	helper := filepath.Join(t.TempDir(), "helper")
	marker := filepath.Join(t.TempDir(), "executed")
	t.Setenv("WOLF_TEST_SENTINEL", marker)
	if err := os.WriteFile(helper, []byte("#!/bin/sh\n: > \"$WOLF_TEST_SENTINEL\"\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, hook := range []string{"pre-commit", "prepare-commit-msg", "commit-msg", "post-commit"} {
		if err := os.WriteFile(filepath.Join(repo, ".git", "hooks", hook), []byte("#!/bin/sh\n: > \"$WOLF_TEST_SENTINEL\"\nexit 1\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	gitRun(t, repo, "config", "commit.gpgSign", "true")
	gitRun(t, repo, "config", "gpg.program", helper)
	for key, value := range map[string]string{"GIT_AUTHOR_NAME": "fixture", "GIT_AUTHOR_EMAIL": "fixture@example.test", "GIT_COMMITTER_NAME": "fixture", "GIT_COMMITTER_EMAIL": "fixture@example.test"} {
		t.Setenv(key, value)
	}
	cmd := Command(t.Context(), nil, "commit", "--allow-empty", "-m", "safe commit")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("commit: %v: %s", err, out)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("repository hook/signer executed: %v", err)
	}
	cmd = Command(t.Context(), nil, "log", "-1", "--format=%an <%ae>")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil || strings.TrimSpace(string(out)) != "fixture <fixture@example.test>" {
		t.Fatalf("identity lost: %q, %v", out, err)
	}
}

func TestCommandUsesExplicitAskpassWithoutCredentialHelpers(t *testing.T) {
	repo := t.TempDir()
	gitRun(t, repo, "init")
	marker := filepath.Join(t.TempDir(), "executed")
	t.Setenv("WOLF_TEST_SENTINEL", marker)
	helper := filepath.Join(t.TempDir(), "helper")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\n: > \"$WOLF_TEST_SENTINEL\"\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "config", "credential.helper", helper)
	gitRun(t, repo, "config", "core.askPass", helper)
	t.Setenv("GIT_ASKPASS", helper)
	askpass := filepath.Join(t.TempDir(), "askpass")
	if err := os.WriteFile(askpass, []byte("#!/bin/sh\ncase \"$1\" in *Username*) printf '%s' \"$WOLF_TEST_USER\";; *) printf '%s' \"$WOLF_TEST_PASSWORD\";; esac\n"), 0700); err != nil {
		t.Fatal(err)
	}
	var authenticated atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if !ok || username != "fixture-user" || password != "fixture-password" {
			w.Header().Set("WWW-Authenticate", `Basic realm="fixture"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		authenticated.Store(true)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	cmd := Command(t.Context(), []string{"GIT_ASKPASS=" + askpass, "WOLF_TEST_USER=fixture-user", "WOLF_TEST_PASSWORD=fixture-password"}, "ls-remote", server.URL+"/fixture.git")
	cmd.Dir = repo
	// The fixture deliberately has no repository; only the real HTTP auth exchange is under test.
	if err := cmd.Run(); err == nil {
		t.Fatal("missing repository unexpectedly succeeded")
	}
	if !authenticated.Load() {
		t.Fatal("explicit askpass did not authenticate")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("unapproved credential helper executed: %v", err)
	}
}

func TestCommandKeepsExplicitSSHTransport(t *testing.T) {
	repo := t.TempDir()
	gitRun(t, repo, "init")
	marker := filepath.Join(t.TempDir(), "explicit-transport")
	t.Setenv("WOLF_TEST_SENTINEL", marker)
	helper := filepath.Join(t.TempDir(), "ssh-helper")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\n: > \"$WOLF_TEST_SENTINEL\"\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	// No network: the caller-selected transport intentionally fails after recording invocation.
	cmd := Command(t.Context(), []string{"GIT_SSH_COMMAND=" + helper}, "ls-remote", "ssh://git@example.invalid/repo")
	cmd.Dir = repo
	if err := cmd.Run(); err == nil {
		t.Fatal("failing transport succeeded")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("explicit SSH transport lost: %v", err)
	}
}
