package scantarget

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestPrepareExistingUsesWorkspaceWithoutClone(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s: %v", args, out, err)
		}
	}
	run("init")
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("-c", "user.email=t@t", "-c", "user.name=t", "commit", "-m", "init")

	got, err := PrepareExisting(dir, models.SourceTypeGitHub)
	if err != nil {
		t.Fatalf("PrepareExisting: %v", err)
	}
	if got.Path != dir || got.PreparedWorkspace != dir {
		t.Fatalf("path=%q prepared=%q", got.Path, got.PreparedWorkspace)
	}
	if got.SourceType != models.SourceTypeGitHub {
		t.Fatalf("source type=%q", got.SourceType)
	}
	if got.CommitSHA == "" {
		t.Fatal("expected commit sha from existing workspace")
	}
	if got.Cleanup == nil {
		t.Fatal("cleanup must be non-nil no-op")
	}
	got.Cleanup() // must not delete the tree
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("workspace deleted: %v", err)
	}
}
