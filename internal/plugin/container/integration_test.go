//go:build integration

// Integration tests for the container shim. Require:
//   - docker daemon reachable
//   - wolf-scanners:dev image built (run `make scanners-build` first)
//
// Run with:  go test -tags=integration ./internal/plugin/container/...
package container

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func init() {
	// Ensure the test image is registered in the ImageReady cache, since
	// EnsureImage is normally called at wolf startup. We do this once for
	// the suite.
	cfg := &Config{
		Image:      "wolf-scanners:dev",
		PullPolicy: PullNever, // tests assume the image was pre-built
		Network:    "bridge",
		UID:        os.Getuid(),
		GID:        os.Getgid(),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = EnsureImage(ctx, cfg) // best-effort; tests that need this assert ImageReady
	SetDefault(cfg)
}

func requireImage(t *testing.T) *Config {
	t.Helper()
	cfg := Default()
	if cfg == nil || !ImageReady(cfg) {
		t.Skip("wolf-scanners:dev not present; run `make scanners-build` first")
	}
	return cfg
}

// fixtureRepo returns an absolute path to a tiny Python repo containing a
// known Bandit B602 (subprocess shell=True) finding. Created in t.TempDir()
// so the file ownership matches the host user (--user $(id -u) in the shim).
func fixtureRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := "import subprocess\n" +
		"def go(x): subprocess.Popen(x, shell=True)\n"
	if err := os.WriteFile(filepath.Join(dir, "app.py"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestShim_Bandit_E2E runs bandit through the shim against a fixture repo
// and asserts at least one finding. This is the canonical "scanner backend
// is actually working" test from docs/PLAN-containerized-scanner-execution.md §7 (Pass gate P1).
func TestShim_Bandit_E2E(t *testing.T) {
	cfg := requireImage(t)
	repo := fixtureRepo(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cmd := CommandContext(ctx, cfg,
		Options{RepoDir: repo},
		"bandit", "-r", "/scan", "-f", "json", "--exit-zero")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("bandit failed: %v\nstdout: %s", err, string(out))
	}
	if !bytes.Contains(out, []byte("B602")) {
		t.Errorf("expected at least one B602 finding (shell=True), got:\n%s", string(out))
	}
}

// TestShim_CancelMidScan starts a long-running tool (semgrep on a moderately
// sized fixture) and cancels mid-flight; asserts the container is gone
// within 5 seconds of cancellation.
func TestShim_CancelMidScan(t *testing.T) {
	cfg := requireImage(t)
	repo := fixtureRepo(t)

	ctx, cancel := context.WithCancel(context.Background())
	cmd := CommandContext(ctx, cfg,
		Options{RepoDir: repo},
		"semgrep", "scan", "--json", "--jobs", "1", "/scan")

	// Run async; cancel after 200ms.
	done := make(chan error, 1)
	go func() { done <- cmd.Run() }()

	time.Sleep(200 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// Good — cmd returned (with context-canceled or kill).
	case <-time.After(10 * time.Second):
		t.Fatal("cmd did not exit within 10s of context cancel — docker kill might not have fired")
	}

	// Verify no orphan wolf-scan-semgrep-* containers within 5s.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		out, _ := exec.Command("docker", "ps", "-a",
			"--filter", "name=wolf-scan-semgrep-",
			"--format", "{{.Names}}").Output()
		if len(strings.TrimSpace(string(out))) == 0 {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Error("orphan wolf-scan-semgrep-* container(s) remain after cancel")
}

// TestShim_HostPathTranslation_DooD verifies that when cfg.HostReposRoot and
// cfg.InContainerReposRoot are configured, bind mounts use the host-side path.
// We can't easily simulate true DooD here, but we can confirm BuildDockerArgs
// emits the host path.
func TestShim_HostPathTranslation_DooD(t *testing.T) {
	cfg := &Config{
		Image:                "wolf-scanners:dev",
		Network:              "bridge",
		UID:                  1000,
		GID:                  1000,
		HostReposRoot:        "/Users/me/projects",
		InContainerReposRoot: "/repos",
	}
	_, args, err := BuildDockerArgs(cfg,
		Options{RepoDir: "/repos/myrepo"},
		"bandit", "-r", "/scan")
	if err != nil {
		t.Fatalf("BuildDockerArgs: %v", err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-v /Users/me/projects/myrepo:/scan:ro") {
		t.Errorf("expected DooD translation host path in bind, got: %s", joined)
	}
	if strings.Contains(joined, "-v /repos/myrepo:/scan") {
		t.Errorf("untranslated /repos/myrepo path should not appear: %s", joined)
	}
}
