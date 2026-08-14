package repo

import (
	"os"
	"path/filepath"
	"testing"
)

// ---------------------------------------------------------------------------
// TestLocalRepo (ValidateLocal)
// ---------------------------------------------------------------------------

func TestLocalRepo(t *testing.T) {
	t.Run("valid directory returns LocalInfo", func(t *testing.T) {
		dir := t.TempDir()
		info, err := ValidateLocal(dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info == nil {
			t.Fatal("expected non-nil LocalInfo")
		}
		abs, _ := filepath.Abs(dir)
		if info.Path != abs {
			t.Errorf("Path = %q, want %q", info.Path, abs)
		}
	})

	t.Run("non-existent path returns error", func(t *testing.T) {
		_, err := ValidateLocal("/tmp/wolf-nonexistent-path-12345")
		if err == nil {
			t.Error("expected error for non-existent path")
		}
	})

	t.Run("file path returns error", func(t *testing.T) {
		dir := t.TempDir()
		fpath := filepath.Join(dir, "file.txt")
		if err := os.WriteFile(fpath, []byte("hello"), 0644); err != nil {
			t.Fatal(err)
		}
		_, err := ValidateLocal(fpath)
		if err == nil {
			t.Error("expected error for file (not directory)")
		}
	})

	t.Run("directory without git is not a git repo", func(t *testing.T) {
		dir := t.TempDir()
		info, err := ValidateLocal(dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if info.IsGitRepo {
			t.Error("expected IsGitRepo=false for plain directory")
		}
	})

	t.Run("directory with .git is a git repo", func(t *testing.T) {
		dir := t.TempDir()
		gitDir := filepath.Join(dir, ".git")
		if err := os.Mkdir(gitDir, 0755); err != nil {
			t.Fatal(err)
		}
		info, err := ValidateLocal(dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !info.IsGitRepo {
			t.Error("expected IsGitRepo=true when .git exists")
		}
	})

	t.Run("resolves relative path to absolute", func(t *testing.T) {
		// Use the current directory which should always exist.
		info, err := ValidateLocal(".")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !filepath.IsAbs(info.Path) {
			t.Errorf("expected absolute path, got %q", info.Path)
		}
	})
}

// ---------------------------------------------------------------------------
// TestCollection
// ---------------------------------------------------------------------------

// mockStore implements the subset of db.Store needed by CollectionManager.
// We only implement the collection-related methods since that is what we test.

// Since CollectionManager depends on db.Store which is a large interface,
// we test the manager's constructor and verify it wires correctly. Integration
// tests with a real store live in internal/db/sqlite_test.go.

func TestCollectionManager(t *testing.T) {
	t.Run("NewCollectionManager returns non-nil", func(t *testing.T) {
		// Pass nil store just to verify the constructor.
		m := NewCollectionManager(nil)
		if m == nil {
			t.Fatal("expected non-nil CollectionManager")
		}
	})
}

func TestGitHubCacheUsesWorkspaceRoot(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WOLF_WORKSPACE_ROOT", root)

	got, err := getCacheDir()
	if err != nil {
		t.Fatalf("getCacheDir: %v", err)
	}
	want := filepath.Join(root, ".wolf-cache", "repos")
	if got != want {
		t.Fatalf("cache dir = %q, want %q", got, want)
	}
}
