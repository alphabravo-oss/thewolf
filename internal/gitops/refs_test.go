package gitops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestBranchesRootUsesPinnedMetadataWithoutGitEnvironment(t *testing.T) {
	path, outside := t.TempDir(), t.TempDir()
	gitRun(t, path, "init", "-b", "main")
	gitRun(t, path, "commit", "--allow-empty", "-m", "fixture")
	gitRun(t, path, "branch", "feature/nested")
	gitRun(t, path, "update-ref", "refs/remotes/origin/main", "HEAD")
	gitRun(t, path, "update-ref", "refs/remotes/other/remote-only", "HEAD")
	gitRun(t, path, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	gitRun(t, path, "tag", "ignored-tag")
	gitRun(t, path, "pack-refs", "--all", "--prune")
	gitRun(t, outside, "init", "-b", "outside-secret")
	gitRun(t, outside, "commit", "--allow-empty", "-m", "outside")
	root, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	old := path + "-held"
	if err := os.Rename(path, old); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(old) })
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	// An unreadable/hostile config and an alternate global Git directory must
	// not influence reference-only inspection. There is no git on PATH.
	if err := os.WriteFile(filepath.Join(old, ".git", "config"), []byte("[include]\n path=/host/secret\n[core]\n fsmonitor=/host/helper\n"), 0000); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_DIR", filepath.Join(outside, ".git"))
	t.Setenv("GIT_WORK_TREE", outside)
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.worktree")
	t.Setenv("GIT_CONFIG_VALUE_0", outside)
	t.Setenv("PATH", t.TempDir())
	branches, current, err := BranchesRoot(t.Context(), root, nil)
	if err != nil || current != "main" || !slices.Equal(branches, []string{"feature/nested", "main", "remote-only"}) {
		t.Fatalf("confined branches = %v, current=%q, err=%v", branches, current, err)
	}
}

func TestLocalBranchesSupportLinkedWorktreeDetachedAndUnborn(t *testing.T) {
	path := t.TempDir()
	gitRun(t, path, "init", "-b", "main")
	if current, err := CurrentBranch(path); err != nil || current != "main" {
		t.Fatalf("unborn branch = %q, %v", current, err)
	}
	gitRun(t, path, "commit", "--allow-empty", "-m", "fixture")
	linked := filepath.Join(t.TempDir(), "linked")
	gitRun(t, path, "worktree", "add", "-b", "linked", linked)
	if current, err := CurrentBranch(linked); err != nil || current != "linked" {
		t.Fatalf("linked current = %q, %v", current, err)
	}
	if branches, err := ListBranches(linked); err != nil || !slices.Equal(branches, []string{"linked", "main"}) {
		t.Fatalf("linked branches = %v, %v", branches, err)
	}
	gitRun(t, linked, "checkout", "--detach")
	if current, err := CurrentBranch(linked); err != nil || current != "HEAD" {
		t.Fatalf("detached current = %q, %v", current, err)
	}
}

func TestBranchesRootRejectsUnsafeAndOversizedMetadata(t *testing.T) {
	for _, kind := range []string{"head-escape", "packed-escape", "ref-escape", "commondir", "large-head", "large-packed", "malformed-head", "malformed-packed", "many-packed", "deep-refs", "missing-objects", "invalid-ref", "reftable", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			path, outside := t.TempDir(), t.TempDir()
			gitRun(t, path, "init", "-b", "main")
			gitRun(t, path, "commit", "--allow-empty", "-m", "fixture")
			root, err := os.OpenRoot(path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = root.Close() }()
			write := func(name, value string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(path, ".git", name), []byte(value), 0600); err != nil {
					t.Fatal(err)
				}
			}
			ctx := t.Context()
			switch kind {
			case "head-escape", "packed-escape", "ref-escape":
				name := map[string]string{"head-escape": "HEAD", "packed-escape": "packed-refs", "ref-escape": "refs/heads/escape"}[kind]
				_ = os.Remove(filepath.Join(path, ".git", name))
				if err := os.WriteFile(filepath.Join(outside, "secret"), []byte(strings.Repeat("a", 40)+" refs/heads/outside-secret\n"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(path, ".git", name)); err != nil {
					t.Fatal(err)
				}
			case "commondir":
				write("commondir", outside)
			case "large-head":
				write("HEAD", strings.Repeat("x", 4097))
			case "large-packed":
				write("packed-refs", strings.Repeat("x", maxRefMetadataBytes+1))
			case "malformed-head":
				write("HEAD", "aaaa-invalid-hash")
			case "malformed-packed":
				write("packed-refs", "aaaa-invalid-hash refs/heads/corrupt\n")
			case "many-packed":
				write("packed-refs", strings.Repeat(strings.Repeat("a", 40)+" refs/heads/branch\n", maxRefEntries+1))
			case "deep-refs":
				if err := os.MkdirAll(filepath.Join(path, ".git", "refs", "heads", strings.Repeat("deep/", 65)), 0700); err != nil {
					t.Fatal(err)
				}
			case "missing-objects":
				if err := os.RemoveAll(filepath.Join(path, ".git", "objects")); err != nil {
					t.Fatal(err)
				}
			case "invalid-ref":
				write("HEAD", "ref: refs/heads/../escape\n")
			case "reftable":
				write("reftable", "")
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			branches, _, err := BranchesRoot(ctx, root, nil)
			if err == nil || len(branches) != 0 {
				t.Fatalf("unsafe metadata returned branches: %v, %v", branches, err)
			}
			if kind == "cancelled" && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation lost: %v", err)
			}
		})
	}
}

func TestLocalBranchesSupportSHA256References(t *testing.T) {
	path := t.TempDir()
	gitRun(t, path, "init", "--object-format=sha256", "-b", "main")
	gitRun(t, path, "commit", "--allow-empty", "-m", "fixture")
	gitRun(t, path, "branch", "feature")
	gitRun(t, path, "pack-refs", "--all", "--prune")
	if branches, err := ListBranches(path); err != nil || !slices.Equal(branches, []string{"feature", "main"}) {
		t.Fatalf("SHA256 branches = %v, %v", branches, err)
	}
	if current, err := CurrentBranch(path); err != nil || current != "main" {
		t.Fatalf("SHA256 current branch = %q, %v", current, err)
	}
}

func TestBranchesRootDoesNotSpendBranchLimitOnTags(t *testing.T) {
	path := t.TempDir()
	gitRun(t, path, "init", "-b", "main")
	gitRun(t, path, "commit", "--allow-empty", "-m", "fixture")
	var packed strings.Builder
	for i := range maxRefEntries + 1 {
		fmt.Fprintf(&packed, "%s refs/tags/release-%d\n", strings.Repeat("a", 40), i)
	}
	if err := os.WriteFile(filepath.Join(path, ".git", "packed-refs"), []byte(packed.String()), 0600); err != nil {
		t.Fatal(err)
	}
	if branches, err := ListBranches(path); err != nil || !slices.Equal(branches, []string{"main"}) {
		t.Fatalf("tags consumed branch allowance: %v, %v", branches, err)
	}
}

func TestOriginURLUsesConfinedLocalConfigWithoutProcessEnvironment(t *testing.T) {
	path := t.TempDir()
	gitRun(t, path, "init", "-b", "main")
	gitRun(t, path, "remote", "add", "origin", "corp:widget.git")
	gitRun(t, path, "config", "url.https://github.com/acme/.insteadOf", "corp:")
	outside := filepath.Join(t.TempDir(), "outside-config")
	if err := os.WriteFile(outside, []byte("this is deliberately invalid Git configuration"), 0600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, path, "config", "include.path", outside)
	t.Setenv("PATH", t.TempDir())
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "remote.origin.url")
	t.Setenv("GIT_CONFIG_VALUE_0", "https://outside.example/secret.git")
	if origin := OriginURL(path); origin != "https://github.com/acme/widget.git" {
		t.Fatalf("local origin/URL rule changed: %q", origin)
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	if err := os.Remove(filepath.Join(path, ".git", "config")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(path, ".git", "config")); err != nil {
		t.Fatal(err)
	}
	if origin, err := OriginURLRoot(t.Context(), root, nil); err == nil || origin != "" {
		t.Fatalf("escaping config accepted: %q, %v", origin, err)
	}
}

func TestOriginURLSupportsWorktreeConfiguration(t *testing.T) {
	path := t.TempDir()
	gitRun(t, path, "init", "-b", "main")
	gitRun(t, path, "commit", "--allow-empty", "-m", "fixture")
	linked := filepath.Join(t.TempDir(), "linked")
	gitRun(t, path, "worktree", "add", "-b", "linked", linked)
	gitRun(t, path, "config", "extensions.worktreeConfig", "true")
	gitRun(t, linked, "config", "--worktree", "remote.origin.url", "https://github.com/acme/linked.git")
	if origin := OriginURL(linked); origin != "https://github.com/acme/linked.git" {
		t.Fatalf("worktree origin lost: %q", origin)
	}
	gitRun(t, path, "remote", "add", "origin", "https://github.com/acme/common.git")
	if origin := OriginURL(linked); origin != "https://github.com/acme/common.git" {
		t.Fatalf("common origin precedence changed: %q", origin)
	}
}
