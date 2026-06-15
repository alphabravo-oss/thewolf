package artifacts

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDeleteScansRemovesNamedScanDirs(t *testing.T) {
	root := t.TempDir()
	store := &Store{root: root}
	scanID := "12345678-aaaa-bbbb-cccc-123456789abc"
	legacy := filepath.Join(root, scanID)
	named := filepath.Join(root, "repo_20260613-120000_12345678")
	other := filepath.Join(root, "repo_20260613-120000_deadbeef")
	for _, dir := range []string{legacy, named, other} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	store.DeleteScans([]string{scanID})

	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy scan dir still exists or stat failed unexpectedly: %v", err)
	}
	if _, err := os.Stat(named); !os.IsNotExist(err) {
		t.Fatalf("named scan dir still exists or stat failed unexpectedly: %v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("unrelated scan dir was removed: %v", err)
	}
}

func TestCleanupOlderThanRemovesOnlyExpiredDirs(t *testing.T) {
	root := t.TempDir()
	store := &Store{root: root}
	oldDir := filepath.Join(root, "old_20260101-000000_aaaaaaaa")
	freshDir := filepath.Join(root, "fresh_20260613-000000_bbbbbbbb")
	for _, dir := range []string{oldDir, freshDir} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	oldTime := time.Now().Add(-72 * time.Hour)
	if err := os.Chtimes(oldDir, oldTime, oldTime); err != nil {
		t.Fatalf("chtimes old: %v", err)
	}

	removed, err := store.CleanupOlderThan(24 * time.Hour)
	if err != nil {
		t.Fatalf("CleanupOlderThan failed: %v", err)
	}
	if len(removed) != 1 || removed[0] != oldDir {
		t.Fatalf("removed = %+v, want [%s]", removed, oldDir)
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Fatalf("old dir still exists or stat failed unexpectedly: %v", err)
	}
	if _, err := os.Stat(freshDir); err != nil {
		t.Fatalf("fresh dir was removed: %v", err)
	}
}
