//go:build unix

package scantarget

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestWorkspaceDigestV2ReferenceAndSpecialFiles(t *testing.T) {
	root := t.TempDir()
	for name, content := range map[string]string{"a": "one", "b": "two"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	digest, err := workspaceTreeDigest(root)
	const want = "sha256-v2:7c1ffd3b340b5cdeaac0075b1be8652ee95383c5ecce292d6d3623026c1eccbc"
	if err != nil || digest != want {
		t.Fatalf("v2 digest format changed: %q, %v", digest, err)
	}
	if err := syscall.Mkfifo(filepath.Join(root, "fifo"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := workspaceTreeDigest(root); err == nil {
		t.Fatal("special file silently omitted from source identity")
	}
}
