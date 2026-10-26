package scantarget

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func gitStateFixture(t *testing.T) (string, func(...string)) {
	t.Helper()
	path := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = path
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull,
			"GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=fixture@example.test", "GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=fixture@example.test")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fixture git: %v: %s", err, out)
		}
	}
	run("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(path, "source.txt"), []byte("source\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run("add", "source.txt")
	run("commit", "-m", "fixture")
	return path, run
}

func TestPrepareExistingDoesNotExecuteFSMonitor(t *testing.T) {
	path, run := gitStateFixture(t)
	marker := filepath.Join(t.TempDir(), "executed")
	t.Setenv("WOLF_TEST_SENTINEL", marker)
	helper := filepath.Join(t.TempDir(), "monitor")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\n: > \"$WOLF_TEST_SENTINEL\"\nprintf 'token\\0'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	run("config", "core.fsmonitor", helper)
	prepared, err := PrepareExisting(t.Context(), path, models.SourceTypeLocal)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Cleanup()
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("source preparation executed repository fsmonitor: %v", err)
	}
}

func TestPrepareExistingRejectsFailedGitStatus(t *testing.T) {
	path, _ := gitStateFixture(t)
	if err := os.WriteFile(filepath.Join(path, ".git", "index"), []byte("corrupt index"), 0600); err != nil {
		t.Fatal(err)
	}
	prepared, err := PrepareExisting(t.Context(), path, models.SourceTypeLocal)
	if err == nil {
		if prepared.Cleanup != nil {
			prepared.Cleanup()
		}
		t.Fatalf("failed Git inspection accepted: commit=%q dirty=%q", prepared.CommitSHA, prepared.DirtyState)
	}
}

func TestPrepareExistingSourceStateAndCancellation(t *testing.T) {
	path, _ := gitStateFixture(t)
	check := func(want string, committed bool) {
		t.Helper()
		prepared, err := PrepareExisting(t.Context(), path, models.SourceTypeLocal)
		if err != nil {
			t.Fatal(err)
		}
		defer prepared.Cleanup()
		if prepared.DirtyState != want || (prepared.CommitSHA != "") != committed || prepared.TreeDigest == "" {
			t.Fatalf("wrong source state: %+v", prepared)
		}
	}
	check("clean", true)
	if err := os.WriteFile(filepath.Join(path, "source.txt"), []byte("changed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	check("dirty", true)
	path = t.TempDir()
	check("unknown", false)
	unborn, run := gitStateFixture(t)
	path = unborn
	run("checkout", "--orphan", "unborn")
	check("dirty", false)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	prepared, err := PrepareExisting(ctx, path, models.SourceTypeLocal)
	if prepared.Cleanup != nil {
		prepared.Cleanup()
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

func TestPrepareExistingRejectsBrokenHead(t *testing.T) {
	path, run := gitStateFixture(t)
	run("update-ref", "refs/heads/broken", "HEAD")
	run("symbolic-ref", "HEAD", "refs/heads/broken")
	if err := os.WriteFile(filepath.Join(path, ".git", "refs", "heads", "broken"), []byte("not-a-commit\n"), 0600); err != nil {
		t.Fatal(err)
	}
	prepared, err := PrepareExisting(t.Context(), path, models.SourceTypeLocal)
	if prepared.Cleanup != nil {
		prepared.Cleanup()
	}
	if err == nil {
		t.Fatalf("broken HEAD accepted as unborn: commit=%q state=%q", prepared.CommitSHA, prepared.DirtyState)
	}
}
