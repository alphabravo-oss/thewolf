package workspace

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestRollbackPreservesFilesOnGitFailure(t *testing.T) {
	for _, name := range []string{"locked-index", "cancelled", "invalid-head"} {
		t.Run(name, func(t *testing.T) {
			repo := initRepo(t)
			ws := &Workspace{path: repo}
			file := filepath.Join(repo, "main.go")
			const edits = "uncommitted edits must survive a failed rollback\n"
			if err := os.WriteFile(file, []byte(edits), 0600); err != nil {
				t.Fatal(err)
			}
			ctx := t.Context()
			switch name {
			case "locked-index":
				if err := os.WriteFile(filepath.Join(repo, ".git", "index.lock"), nil, 0600); err != nil {
					t.Fatal(err)
				}
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "invalid-head":
				if err := os.WriteFile(filepath.Join(repo, ".git", "HEAD"), []byte("ref: refs/heads/missing\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := ws.Rollback(ctx, "main.go"); err == nil {
				t.Fatal("failed Git operation reported rollback success")
			}
			if content, err := os.ReadFile(file); err != nil || string(content) != edits {
				t.Fatalf("failed rollback destroyed edits: %q %v", content, err)
			}
		})
	}
}

func TestGitOutputExcludesDiagnosticStderr(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(`#!/bin/sh
printf 'machine-output'
echo diagnostic >&2
if [ "$WOLF_GIT_FAIL" = yes ]; then exit 1; fi
`), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	for _, fail := range []string{"no", "yes"} {
		t.Setenv("WOLF_GIT_FAIL", fail)
		for _, withEnv := range []bool{false, true} {
			var output string
			var err error
			if withEnv {
				output, err = runGitWithEnv(t.Context(), bin, nil, "version")
			} else {
				output, err = runGit(t.Context(), bin, "version")
			}
			if fail == "no" && (err != nil || output != "machine-output") {
				t.Fatalf("diagnostics polluted machine output: %q %v", output, err)
			}
			if fail == "yes" && (err == nil || !strings.Contains(err.Error(), "diagnostic")) {
				t.Fatalf("failure lost diagnostic: %q %v", output, err)
			}
		}
	}
}

func TestRollbackConfinesPathsAndPreservesMetadata(t *testing.T) {
	repo := initRepo(t)
	ws := &Workspace{path: repo}
	outside := t.TempDir()
	victim := filepath.Join(outside, "victim")
	if err := os.WriteFile(victim, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(repo, "escape")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", ".", "..", "../" + filepath.Base(outside) + "/victim", victim, ".git", ".git/config", "escape/victim"} {
		if err := ws.Rollback(t.Context(), name); err == nil {
			t.Errorf("rollback accepted unsafe path %q", name)
		}
	}
	if content, err := os.ReadFile(victim); err != nil || string(content) != "keep" {
		t.Fatalf("outside file changed: %q %v", content, err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".git", "config")); err != nil {
		t.Fatal("Git metadata removed", err)
	}
}

func TestChangedFilesAndRollbackPreserveLiteralNames(t *testing.T) {
	repo := initRepo(t)
	ws := &Workspace{path: repo, defaultBranch: "main"}
	names := []string{" space.go ", "line\nbreak.go", "quote\".go", "*.go", ":(glob)*", "-dash.go"}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(repo, name), []byte("original\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := runGit(t.Context(), repo, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(t.Context(), repo, "-c", "user.name=test", "-c", "user.email=test@example.test", "commit", "-m", "literal filenames"); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(repo, name), []byte("changed\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	changed, err := ws.ChangedFiles(t.Context())
	if err != nil || len(changed) != len(names) {
		t.Fatalf("changed paths: %q %v", changed, err)
	}
	for _, name := range names {
		if !slices.Contains(changed, name) {
			t.Fatalf("lost literal filename %q in %q", name, changed)
		}
		if err := ws.Rollback(t.Context(), name); err != nil {
			t.Fatalf("rollback %q: %v", name, err)
		}
		if content, err := os.ReadFile(filepath.Join(repo, name)); err != nil || string(content) != "original\n" {
			t.Fatalf("wrong rollback for %q: %q %v", name, content, err)
		}
	}
	for _, stage := range []bool{false, true} {
		name := " new\nfile.go "
		if err := os.WriteFile(filepath.Join(repo, name), []byte("new\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if stage {
			if _, err := ws.Diff(t.Context()); err != nil { // intent-to-add
				t.Fatal(err)
			}
		}
		if changed, err := ws.ChangedFiles(t.Context()); err != nil || !slices.Contains(changed, name) {
			t.Fatalf("new path missing: %q %v", changed, err)
		}
		if err := ws.Rollback(t.Context(), name); err != nil {
			t.Fatal(err)
		}
		if out, err := runGit(t.Context(), repo, "status", "--porcelain", "-z"); err != nil || strings.TrimSpace(out) != "" {
			t.Fatalf("rollback left index/worktree residue: %q %v", out, err)
		}
	}
}
