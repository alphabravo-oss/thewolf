package gitops

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestCaptureDiffDoesNotExecuteHelpers(t *testing.T) {
	repo := t.TempDir()
	gitRun(t, repo, "init", "-b", "main")
	gitRun(t, repo, "config", "user.name", "test")
	gitRun(t, repo, "config", "user.email", "test@example.com")
	file := filepath.Join(repo, "file.txt")
	if err := os.WriteFile(file, []byte("before\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-m", "initial")
	if err := os.WriteFile(file, []byte("after\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "config", "diff.external", "sh -c 'echo invoked > helper-executed'")
	gitRun(t, repo, "config", "core.fsmonitor", "sh -c 'echo invoked > helper-executed'")
	diff, err := CaptureDiff(repo)
	if err != nil || !strings.Contains(diff, "+after") {
		t.Fatalf("diff = %q, %v", diff, err)
	}
	if _, err := os.Stat(filepath.Join(repo, "helper-executed")); !os.IsNotExist(err) {
		t.Fatalf("diff helper executed: %v", err)
	}
}

func TestBranchName(t *testing.T) {
	tests := []struct {
		scanID   string
		category string
		want     string
	}{
		{"abc123", "security", "wolf-fix/abc123/security"},
		{"def456", "Code Quality", "wolf-fix/def456/code-quality"},
		{"ghi789", "sast", "wolf-fix/ghi789/sast"},
		{"scan-1", "CONTAINER", "wolf-fix/scan-1/container"},
	}

	for _, tt := range tests {
		got := BranchName(tt.scanID, tt.category)
		if got != tt.want {
			t.Errorf("BranchName(%q, %q) = %q, want %q", tt.scanID, tt.category, got, tt.want)
		}
	}
}

func TestSanitizePath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"wolf-fix/abc/sast", "wolf-fix_abc_sast"},
		{"path with spaces", "path_with_spaces"},
		{"simple", "simple"},
	}

	for _, tt := range tests {
		got := sanitizePath(tt.input)
		if got != tt.want {
			t.Errorf("sanitizePath(%q) = %q, want %q", tt.input, tt.want, got)
		}
	}
}

func TestOriginURL(t *testing.T) {
	dir := t.TempDir()
	if got := OriginURL(dir); got != "" {
		t.Fatalf("OriginURL(non-git) = %q, want empty", got)
	}
	gitRun(t, dir, "init")
	if got := OriginURL(dir); got != "" {
		t.Fatalf("OriginURL(no remote) = %q, want empty", got)
	}
	gitRun(t, dir, "remote", "add", "origin", "https://github.com/acme/widget.git")
	if got := OriginURL(dir); got != "https://github.com/acme/widget.git" {
		t.Fatalf("OriginURL = %q", got)
	}
}

func TestListRemoteBranches(t *testing.T) {
	root := t.TempDir()
	seed := filepath.Join(root, "seed")
	if err := os.Mkdir(seed, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, seed, "init", "--initial-branch=main")
	gitRun(t, seed, "config", "user.email", "wolf@test")
	gitRun(t, seed, "config", "user.name", "wolf")
	gitRun(t, seed, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(seed, "a.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, seed, "add", "a.txt")
	gitRun(t, seed, "commit", "-m", "main")
	gitRun(t, seed, "branch", "dev")

	upstream := filepath.Join(root, "upstream.git")
	gitRun(t, root, "init", "--bare", "--initial-branch=main", upstream)
	gitRun(t, seed, "remote", "add", "origin", upstream)
	gitRun(t, seed, "push", "-u", "origin", "main")
	gitRun(t, seed, "push", "-u", "origin", "dev")

	got, err := ListRemoteBranches(upstream, "")
	if err != nil {
		t.Fatalf("ListRemoteBranches: %v", err)
	}
	if !slices.Contains(got, "main") || !slices.Contains(got, "dev") {
		t.Fatalf("branches = %v, want main and dev", got)
	}
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

func TestGitHubTokenOriginBoundary(t *testing.T) {
	for _, remote := range []string{"https://github.com/acme/widget.git", "https://GITHUB.COM:443/acme/widget.git"} {
		if !IsGitHubTokenRemote(remote) {
			t.Fatalf("expected trusted HTTPS origin: %q", remote)
		}
	}
	for _, remote := range []string{
		"http://github.com/acme/widget.git", "https://github.com:444/acme/widget.git",
		"https://github.com.evil.test/acme/widget.git", "https://evilgithub.com/acme/widget.git",
		"https://github.com@evil.test/acme/widget.git", "https://evil.test/github.com/widget.git",
		"https://evil.test/acme/widget.git?host=github.com", "https://evil.test/acme/widget.git#github.com",
		"https://user@github.com/acme/widget.git", "https://github.com./acme/widget.git",
		"git@github.com:acme/widget.git", "ssh://git@github.com/acme/widget.git", "/tmp/github.com/widget",
		"https://github.com:invalid/acme/widget.git",
	} {
		if IsGitHubTokenRemote(remote) {
			t.Errorf("untrusted origin accepted: %q", remote)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("credential origin rejection must precede any outbound request")
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()
	if _, err := ListRemoteBranches(server.URL+"/github.com/widget.git", "test-only-token-sentinel"); err == nil {
		t.Fatal("expected remote origin rejection")
	}
}
