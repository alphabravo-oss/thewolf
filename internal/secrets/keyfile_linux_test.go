package secrets

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestKeyFilePermissionDeniedPreservesWritableKey(t *testing.T) {
	if path := os.Getenv("WOLF_KEYFILE_PERMISSION_CHILD"); path != "" {
		generated := false
		_, err := LoadOrCreateKeyFile(path, func() ([]byte, error) {
			generated = true
			return []byte("replacement"), nil
		})
		if !errors.Is(err, os.ErrPermission) || generated {
			t.Fatalf("read denial must fail without generating: generated=%v, err=%v", generated, err)
		}
		return
	}
	// A write-only key reproduces the old read-error/overwrite failure. Drop
	// privileges when tests run as root so the read really returns EACCES.
	dir, err := os.MkdirTemp("", "wolf-key-permission-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(dir, "keyfile.test")
	if err := os.WriteFile(child, binary, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "test.key")
	original := []byte("existing write-only key must survive")
	if err := os.WriteFile(path, original, 0200); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, child, "-test.run=^TestKeyFilePermissionDeniedPreservesWritableKey$")
	cmd.Dir = dir
	cmd.Env = []string{"WOLF_KEYFILE_PERMISSION_CHILD=" + path}
	if os.Geteuid() == 0 {
		if err := os.Chown(path, 65534, 65534); err != nil {
			t.Fatal(err)
		}
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 65534, Gid: 65534}}
	}
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("permission check: %v: %s", err, output)
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(path); err != nil || !bytes.Equal(data, original) {
		t.Fatal("unreadable existing key changed")
	}
}
