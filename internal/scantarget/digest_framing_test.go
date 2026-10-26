package scantarget

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceDigestDistinguishesEntryBoundariesTypesAndModes(t *testing.T) {
	left, right := t.TempDir(), t.TempDir()
	write := func(root, name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	different := func(t *testing.T) {
		t.Helper()
		a, err := workspaceTreeDigest(left)
		if err != nil {
			t.Fatal(err)
		}
		b, err := workspaceTreeDigest(right)
		if err != nil {
			t.Fatal(err)
		}
		if a == b {
			t.Fatal("different source trees have the same digest")
		}
	}
	t.Run("entry-boundaries", func(t *testing.T) {
		write(left, "a", "one")
		write(left, "b", "two")
		write(right, "a", "one\x00b\x00two")
		different(t)
	})
	t.Run("file-types", func(t *testing.T) {
		left, right = t.TempDir(), t.TempDir()
		if err := os.Symlink("target", filepath.Join(left, "a")); err != nil {
			t.Fatal(err)
		}
		write(right, "a", "symlink\x00target")
		different(t)
	})
	t.Run("executable-mode", func(t *testing.T) {
		left, right = t.TempDir(), t.TempDir()
		write(left, "a", "same")
		write(right, "a", "same")
		if err := os.Chmod(filepath.Join(left, "a"), 0700); err != nil {
			t.Fatal(err)
		}
		different(t)
	})
}

func TestWorkspaceDigestV2IncludesDirectoriesAndIgnoresGitPointers(t *testing.T) {
	root := t.TempDir()
	digest := func() string {
		t.Helper()
		value, err := workspaceTreeDigest(root)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	before := digest()
	if err := os.Mkdir(filepath.Join(root, "empty"), 0700); err != nil {
		t.Fatal(err)
	}
	after := digest()
	if before == after {
		t.Fatal("empty directory missing from tree identity")
	}
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: /outside/metadata"), 0600); err != nil {
		t.Fatal(err)
	}
	if digest() != after {
		t.Fatal("Git pointer metadata affected content identity")
	}
}
