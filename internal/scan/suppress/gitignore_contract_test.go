package suppress

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	gitcmd "github.com/alphabravocompany/thewolf/internal/fix/git"
	"github.com/alphabravocompany/thewolf/internal/models"
)

func runIgnoreGit(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := gitcmd.Command(t.Context(), nil, append([]string{"-C", root}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
}
func writeIgnoreFixture(t *testing.T, name, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}
func TestGitignoreScopeAndTrackedSemantics(t *testing.T) {
	repo := initRepo(t, "*.log\nblocked/\n!blocked/keep.log\n/root-only.env\n")
	writeIgnoreFixture(t, filepath.Join(repo, "nested/.gitignore"), "!keep.log\n*.secret\n")
	writeIgnoreFixture(t, filepath.Join(repo, ".git/info/exclude"), "local.tmp\n")
	writeIgnoreFixture(t, filepath.Join(repo, "tracked.log"), "tracked\n")
	runIgnoreGit(t, repo, "add", "-f", "tracked.log")
	paths := []string{"./app.log", "nested/keep.log", "nested/other.log", "nested/value.secret", "blocked/keep.log", "root-only.env", "nested/root-only.env", "local.tmp", "tracked.log", "../escape.log", "a/../bad.log", "nul\x00.log"}
	ignored, err := gitcmd.IgnoredPaths(t.Context(), repo, paths)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"./app.log": true, "nested/other.log": true, "nested/value.secret": true, "blocked/keep.log": true, "root-only.env": true, "local.tmp": true}
	if !reflect.DeepEqual(ignored, want) {
		t.Fatalf("native semantics: got %v want %v", ignored, want)
	}
	child := filepath.Join(repo, "child")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	if got, err := gitcmd.IgnoredPaths(t.Context(), child, []string{"app.log"}); err != nil || len(got) != 0 {
		t.Fatalf("inherited parent policy: %v %v", got, err)
	}
	// A linked worktree's .git file must continue to work with explicit discovery.
	runIgnoreGit(t, repo, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "commit", "-qm", "fixture")
	linked := filepath.Join(t.TempDir(), "linked")
	runIgnoreGit(t, repo, "worktree", "add", "--detach", linked)
	writeIgnoreFixture(t, filepath.Join(linked, ".gitignore"), "*.log\n")
	got, err := gitcmd.IgnoredPaths(t.Context(), linked, []string{"app.log", "tracked.log"})
	if err != nil || !got["app.log"] || got["tracked.log"] {
		t.Fatalf("linked worktree: %v %v", got, err)
	}
	exclude := filepath.Join(repo, ".git/info/exclude")
	if err := os.Remove(exclude); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(exclude, 0700); err != nil {
		t.Fatal(err)
	}
	if got, err := gitcmd.IgnoredPaths(t.Context(), linked, []string{"app.log"}); err == nil || len(got) != 0 {
		t.Fatalf("linked common excludes silently ignored: %v %v", got, err)
	}
}

func TestGitignoreIgnoresAmbientConfigurationAndHelpers(t *testing.T) {
	repo := initRepo(t, "*.log\n")
	other := initRepo(t, "*\n")
	external := filepath.Join(t.TempDir(), "exclude")
	writeIgnoreFixture(t, external, "*.env\n")
	global := filepath.Join(t.TempDir(), "config")
	writeIgnoreFixture(t, global, "[core]\n excludesFile = "+external+"\n")
	hook := filepath.Join(t.TempDir(), "fsmonitor")
	sentinel := filepath.Join(t.TempDir(), "ran")
	writeIgnoreFixture(t, hook, "#!/bin/sh\ntouch '"+sentinel+"'\n")
	if err := os.Chmod(hook, 0700); err != nil {
		t.Fatal(err)
	}
	runIgnoreGit(t, repo, "config", "core.fsmonitor", hook)
	runIgnoreGit(t, repo, "config", "core.excludesFile", external)
	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
	t.Setenv("GIT_WORK_TREE", other)
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	t.Setenv("GIT_CONFIG_SYSTEM", global)
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.excludesFile")
	t.Setenv("GIT_CONFIG_VALUE_0", external)
	xdg := t.TempDir()
	writeIgnoreFixture(t, filepath.Join(xdg, "git/ignore"), "*.env\n")
	t.Setenv("XDG_CONFIG_HOME", xdg)
	ignored, err := gitcmd.IgnoredPaths(t.Context(), repo, []string{"app.log", "private.env", "app.go"})
	if err != nil || !reflect.DeepEqual(ignored, map[string]bool{"app.log": true}) {
		t.Fatalf("ambient decision: %v %v", ignored, err)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatalf("fsmonitor executed: %v", err)
	}
}

func TestGitignoreFailuresAreAtomic(t *testing.T) {
	repo := initRepo(t, "*.log\n")
	for _, tc := range []struct {
		name, script string
		ok           bool
	}{
		{"missing binary", "", false},
		{"fatal", "exit 128", false},
		{"partial failure", "printf 'app.log\\000'; exit 128", false},
		{"partial no-match", "printf 'app.log\\000'; exit 1", false},
		{"warning", "printf 'app.log\\000'; printf 'unreadable policy' >&2", false},
		{"unframed", "printf 'app.log\\n'", false},
		{"unexpected path", "printf 'other.log\\000'", false},
		{"normal no-match", "exit 1", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := t.TempDir()
			if tc.script != "" {
				name := filepath.Join(bin, "git")
				writeIgnoreFixture(t, name, "#!/bin/sh\n"+tc.script+"\n")
				if err := os.Chmod(name, 0700); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", bin)
			findings := []models.Finding{{FilePath: "vendor/app.go"}, {FilePath: "app.log"}, {FilePath: "existing.go", Suppressed: true, SuppressedReason: "imported"}}
			before := append([]models.Finding(nil), findings...)
			_, n, err := ApplyRepo(t.Context(), findings, repo)
			if tc.ok {
				if err != nil || n != 1 || !findings[0].Suppressed {
					t.Fatalf("no-match failed: %d %v", n, err)
				}
				return
			}
			if err == nil || n != 0 || !reflect.DeepEqual(before, findings) {
				t.Fatalf("partial decisions: %d %v %+v", n, err, findings)
			}
		})
	}
}

func TestGitignoreCancellationAndCorruptMetadata(t *testing.T) {
	repo := initRepo(t, "*.log\n")
	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := gitcmd.IgnoredPaths(ctx, repo, []string{"app.log"}); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	})
	t.Run("deadline", func(t *testing.T) {
		bin := t.TempDir()
		name := filepath.Join(bin, "git")
		writeIgnoreFixture(t, name, "#!/bin/sh\nexec /bin/sleep 30\n")
		if err := os.Chmod(name, 0700); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", bin)
		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()
		start := time.Now()
		_, err := gitcmd.IgnoredPaths(ctx, repo, []string{"app.log"})
		if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
			t.Fatalf("deadline not propagated promptly: %v", err)
		}
	})
	writeIgnoreFixture(t, filepath.Join(repo, ".git/config"), "[broken config\n")
	if got, err := gitcmd.IgnoredPaths(t.Context(), repo, []string{"app.log"}); err == nil || len(got) != 0 {
		t.Fatalf("corrupt metadata treated as clean: %v %v", got, err)
	}
}

func TestGitignoreLiteralProtocolHasNoInjectedPaths(t *testing.T) {
	repo := initRepo(t, "secret.env\n")
	paths := []string{"prefix\nsecret.env", "secret.env"}
	got, err := gitcmd.IgnoredPaths(t.Context(), repo, paths)
	if err != nil || got[paths[0]] || !got[paths[1]] {
		t.Fatalf("newline injection: %v %v", got, err)
	}
	// Backslashes and quotes are literal filename characters on Unix, not Git's
	// line-based C-quoting protocol. Newline/Unicode fixtures run in the other case.
	got, err = gitcmd.IgnoredPaths(t.Context(), repo, []string{strings.Repeat("q", 100) + ".go"})
	if err != nil || len(got) != 0 {
		t.Fatalf("no-match: %v %v", got, err)
	}
}

func TestGitignoreUnreadablePolicyIsNotNoMatch(t *testing.T) {
	for _, name := range []string{".gitignore", "nested/.gitignore", ".git/info/exclude"} {
		t.Run(name, func(t *testing.T) {
			repo := initRepo(t, "*.log\n")
			target := filepath.Join(repo, name)
			if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if err := os.MkdirAll(target, 0700); err != nil {
				t.Fatal(err)
			}
			if got, err := gitcmd.IgnoredPaths(t.Context(), repo, []string{"nested/app.log"}); err == nil || len(got) != 0 {
				t.Fatalf("unreadable policy silently ignored: %v %v", got, err)
			}
		})
	}
	repo := initRepo(t, "*.log\n")
	name := filepath.Join(repo, ".gitignore")
	if err := os.Remove(name); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "rules")
	writeIgnoreFixture(t, outside, "*\n")
	if err := os.Symlink(outside, name); err != nil {
		t.Fatal(err)
	}
	if got, err := gitcmd.IgnoredPaths(t.Context(), repo, []string{"app.log"}); err == nil || len(got) != 0 {
		t.Fatalf("symlink policy accepted: %v %v", got, err)
	}
}
