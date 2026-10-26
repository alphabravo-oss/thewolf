//go:build unix

package artifacts

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestOpenRejectsFIFOWithoutBlocking(t *testing.T) {
	previous := Global
	t.Cleanup(func() { Global = previous })
	if err := Init(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(Global.Root(), "fifo")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		f, err := Open(path)
		if f != nil {
			_ = f.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("FIFO accepted")
		}
	case <-time.After(time.Second):
		// Unblock a regressed blocking open before restoring the global store.
		f, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err == nil {
			_ = f.Close()
		}
		<-done
		t.Fatal("FIFO open blocked")
	}
}
