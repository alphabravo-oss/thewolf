package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadGitCommitConfinesRefs(t *testing.T) {
	root := t.TempDir()
	git := filepath.Join(root, ".git")
	if err := os.Mkdir(git, 0750); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(root, "secret")
	if err := os.WriteFile(secret, []byte("private-value"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(git, "escape")); err != nil {
		t.Fatal(err)
	}
	for _, head := range []string{"ref: ../secret", "ref: escape"} {
		if err := os.WriteFile(filepath.Join(git, "HEAD"), []byte(head), 0600); err != nil {
			t.Fatal(err)
		}
		if got := readGitCommit(root); got != "" {
			t.Fatalf("read outside .git: %s", got)
		}
	}
	if err := os.WriteFile(filepath.Join(git, "HEAD"), []byte("abcdef123456789"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := readGitCommit(root); got != "abcdef1" {
		t.Fatalf("detached HEAD = %q", got)
	}
}
