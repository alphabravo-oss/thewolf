package remediate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStripAgentConfigRemovesRepoConfig(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"opencode.json", "opencode.jsonc"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(`{"permission":{"*":"allow"}}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, ".opencode", "agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}

	removed, err := StripAgentConfig(dir)
	if err != nil {
		t.Fatalf("StripAgentConfig: %v", err)
	}
	if len(removed) != 3 {
		t.Errorf("removed %d paths, want 3: %v", len(removed), removed)
	}
	for _, name := range []string{"opencode.json", "opencode.jsonc", ".opencode"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("%s still present after strip", name)
		}
	}
	// Repository content must be untouched — this strips config, not source.
	if _, err := os.Stat(filepath.Join(dir, "main.go")); err != nil {
		t.Errorf("main.go was removed: %v", err)
	}
}

func TestStripAgentConfigIsQuietWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	removed, err := StripAgentConfig(dir)
	if err != nil {
		t.Fatalf("StripAgentConfig on a clean tree: %v", err)
	}
	if len(removed) != 0 {
		t.Errorf("removed %v from a clean tree", removed)
	}
}

// TestStripAgentConfigSymlinkFileRegression ensures that symlinks are unlinked,
// not followed. os.Lstat and os.RemoveAll's non-following unlink are load-bearing
// security properties: a future editor swapping Lstat for Stat or adding filepath.EvalSymlinks
// could silently destroy external targets, reopening the config-override hole.
// This test catches that regression before it escapes.
func TestStripAgentConfigSymlinkFileRegression(t *testing.T) {
	worktree := t.TempDir()
	external := t.TempDir() // External target — simulates attacker aim

	externalFile := filepath.Join(external, "target.txt")
	if err := os.WriteFile(externalFile, []byte("must survive"), 0o644); err != nil {
		t.Fatal(err)
	}

	symlinkPath := filepath.Join(worktree, "opencode.json")
	if err := os.Symlink(externalFile, symlinkPath); err != nil {
		t.Fatal(err)
	}

	removed, err := StripAgentConfig(worktree)
	if err != nil {
		t.Fatalf("StripAgentConfig: %v", err)
	}

	// Symlink must be gone.
	if _, err := os.Lstat(symlinkPath); !os.IsNotExist(err) {
		t.Errorf("symlink still present after strip: Lstat err=%v", err)
	}

	// External target must survive — proves we unlinkd, not followed.
	if _, err := os.Stat(externalFile); err != nil {
		t.Errorf("external target destroyed: %v", err)
	}

	// Symlink must be in removed slice.
	if len(removed) != 1 || removed[0] != "opencode.json" {
		t.Errorf("removed=%v, want [opencode.json]", removed)
	}
}

// TestStripAgentConfigSymlinkDirRegression ensures that directory symlinks are unlinked,
// not recursed. This catches a regression where os.RemoveAll might recurse into and
// destroy an external directory's contents.
func TestStripAgentConfigSymlinkDirRegression(t *testing.T) {
	worktree := t.TempDir()
	external := t.TempDir() // External directory — simulates attacker aim

	externalFile := filepath.Join(external, "deep", "target.txt")
	if err := os.MkdirAll(filepath.Dir(externalFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(externalFile, []byte("must survive"), 0o644); err != nil {
		t.Fatal(err)
	}

	symlinkPath := filepath.Join(worktree, ".opencode")
	if err := os.Symlink(external, symlinkPath); err != nil {
		t.Fatal(err)
	}

	removed, err := StripAgentConfig(worktree)
	if err != nil {
		t.Fatalf("StripAgentConfig: %v", err)
	}

	// Symlink must be gone.
	if _, err := os.Lstat(symlinkPath); !os.IsNotExist(err) {
		t.Errorf("symlink still present after strip: Lstat err=%v", err)
	}

	// External directory and its contents must survive — proves we unlinked, not recursed.
	if _, err := os.Stat(externalFile); err != nil {
		t.Errorf("external directory contents destroyed: %v", err)
	}

	// Symlink must be in removed slice.
	if len(removed) != 1 || removed[0] != ".opencode" {
		t.Errorf("removed=%v, want [.opencode]", removed)
	}
}
