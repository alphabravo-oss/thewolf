//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package gitops

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestBranchesRootRejectsFIFOWithoutBlocking(t *testing.T) {
	path := t.TempDir()
	gitRun(t, path, "init", "-b", "main")
	fifo := filepath.Join(path, ".git", "packed-refs")
	if err := unix.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	done := make(chan error, 1)
	go func() {
		_, _, err := BranchesRoot(t.Context(), root, nil)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("FIFO accepted as reference metadata")
		}
	case <-time.After(2 * time.Second):
		// Release an incorrectly blocking reader before failing the test.
		file, err := os.OpenFile(fifo, os.O_WRONLY|unix.O_NONBLOCK, 0)
		if err == nil {
			_ = file.Close()
		}
		<-done
		t.Fatal("reference inspection blocked on a FIFO")
	}
}
