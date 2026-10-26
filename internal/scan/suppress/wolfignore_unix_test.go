//go:build unix

package suppress

import (
	gitcmd "github.com/alphabravocompany/thewolf/internal/fix/git"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestWolfIgnoreFIFOIsRejectedWithoutBlocking(t *testing.T) {
	name := filepath.Join(t.TempDir(), ".wolfignore")
	if err := syscall.Mkfifo(name, 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := ParseWolfIgnoreFile(name); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("FIFO accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("FIFO blocked the suppression reader")
	}
}

func TestGitignoreFIFOIsRejectedWithoutBlocking(t *testing.T) {
	repo := initRepo(t, "*.log\n")
	name := filepath.Join(repo, ".gitignore")
	if err := os.Remove(name); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(name, 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := gitcmd.IgnoredPaths(t.Context(), repo, []string{"app.log"}); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("FIFO accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("Git policy FIFO blocked")
	}
}
