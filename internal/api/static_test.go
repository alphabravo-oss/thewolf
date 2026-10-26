package api

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStaticUIConfinesSymlinksAndTraversal(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("SPA"), 0600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("outside-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "leak.txt")); err != nil {
		t.Fatal(err)
	}
	handler := spaHandler{dir: dir}
	for _, path := range []string{"/", "/findings/item", "/leak.txt", "/../secret"} {
		w := httptest.NewRecorder()
		handler.serve(w, httptest.NewRequest("GET", path, nil))
		if strings.Contains(w.Body.String(), "outside-secret") {
			t.Fatalf("served outside file through %s", path)
		}
		if (path == "/" || path == "/findings/item") && w.Body.String() != "SPA" {
			t.Fatalf("SPA fallback failed: %s %s", path, w.Body.String())
		}
	}
}
