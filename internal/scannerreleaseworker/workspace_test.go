package scannerreleaseworker

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

func TestBuildWorkspaceIsDeterministicBoundedAndSymlinkSafe(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	worker := &Worker{config: Config{WorkspaceRoot: root, RemoveAll: os.RemoveAll}}
	build := &scannerrelease.BuildRun{ID: "build-one", Attempt: 1}
	candidate := &scannerrelease.Candidate{
		ID: "candidate-one", DefinitionCommit: "0123456789abcdef",
		LockDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		PolicyID:   "policy-one", PolicyRevision: 3,
	}
	policy := &scannerrelease.Policy{ID: candidate.PolicyID, Revision: candidate.PolicyRevision}
	first, err := worker.prepareBuildWorkspace(build, candidate, policy)
	if err != nil {
		t.Fatal(err)
	}
	second, err := worker.prepareBuildWorkspace(build, candidate, policy)
	if err != nil || second != first {
		t.Fatalf("deterministic workspace first=%q second=%q err=%v", first, second, err)
	}
	if filepath.Dir(first) != root || filepath.Base(first) != deterministicWorkspaceName(build.ID) {
		t.Fatalf("workspace escaped root: %q", first)
	}
	changed := *candidate
	changed.LockDigest = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if _, err := worker.prepareBuildWorkspace(build, &changed, policy); err == nil ||
		err.Error() != "scanner release build workspace immutable binding mismatch" {
		t.Fatalf("workspace binding mismatch error = %v", err)
	}
	if err := worker.removeBuildWorkspace(build.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(first); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("terminal workspace remains: %v", err)
	}

	outside := t.TempDir()
	symlinkRoot := filepath.Join(t.TempDir(), "workspace-link")
	if err := os.Symlink(outside, symlinkRoot); err != nil {
		t.Fatal(err)
	}
	worker.config.WorkspaceRoot = symlinkRoot
	if _, err := worker.prepareBuildWorkspace(build, candidate, policy); err == nil ||
		err.Error() != "scanner release workspace root must be a real directory" {
		t.Fatalf("symlinked root error = %v", err)
	}
}

func TestBuildWorkspaceRejectsSymlinkedBinding(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	worker := &Worker{config: Config{WorkspaceRoot: root, RemoveAll: os.RemoveAll}}
	build := &scannerrelease.BuildRun{ID: "build-two", Attempt: 1}
	candidate := &scannerrelease.Candidate{
		ID: "candidate-two", DefinitionCommit: "fedcba9876543210",
		LockDigest: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		PolicyID:   "policy-two", PolicyRevision: 5,
	}
	policy := &scannerrelease.Policy{ID: candidate.PolicyID, Revision: candidate.PolicyRevision}
	workspace := filepath.Join(root, deterministicWorkspaceName(build.ID))
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "binding.json")
	if err := os.WriteFile(target, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(workspace, buildWorkspaceBinding)); err != nil {
		t.Fatal(err)
	}
	if _, err := worker.prepareBuildWorkspace(build, candidate, policy); err == nil ||
		err.Error() != "scanner release workspace binding is not a bounded regular file" {
		t.Fatalf("symlinked workspace binding error = %v", err)
	}
}
