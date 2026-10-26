package routes

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/artifacts"
)

func TestFixLogRelaysConfineArtifactReads(t *testing.T) {
	previous := artifacts.Global
	t.Cleanup(func() { artifacts.Global = previous })
	root := t.TempDir()
	if err := artifacts.Init(root); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret.log")
	if err := os.WriteFile(outside, []byte("outside-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	valid := filepath.Join(root, "valid.log")
	if err := os.WriteFile(valid, []byte("worker-output\n"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.log")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	hardlink := filepath.Join(root, "hardlink.log")
	if err := os.Link(outside, hardlink); err != nil {
		t.Fatal(err)
	}
	for name, relay := range map[string]func(http.ResponseWriter, http.Flusher, string, int64) int64{
		"log": relayLogLines, "console": relayConsoleData,
	} {
		for _, path := range []string{valid, outside, link, hardlink, filepath.Join(root, "missing.log")} {
			t.Run(name+"/"+filepath.Base(path), func(t *testing.T) {
				w := httptest.NewRecorder()
				offset := relay(w, w, path, 0)
				if path == valid {
					if offset != int64(len("worker-output\n")) || !strings.Contains(w.Body.String(), "worker-output") {
						t.Fatalf("valid log not relayed: offset=%d body=%q", offset, w.Body.String())
					}
				} else if offset != 0 || w.Body.Len() != 0 {
					t.Fatalf("unconfined log read: offset=%d body=%q", offset, w.Body.String())
				}
			})
		}
		artifacts.Global = nil
		w := httptest.NewRecorder()
		if offset := relay(w, w, valid, 0); offset != 0 || w.Body.Len() != 0 {
			t.Fatal("uninitialized artifact store read a log")
		}
		if err := artifacts.Init(root); err != nil {
			t.Fatal(err)
		}
	}
}
