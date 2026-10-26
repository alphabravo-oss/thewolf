package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportCoverageCLI(t *testing.T) {
	var uploaded map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" || !strings.HasSuffix(r.URL.Path, "/scans/scan-id/test-coverage") {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&uploaded); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"status":"measured"}}`))
	}))
	defer server.Close()
	file := filepath.Join(t.TempDir(), "coverage.out")
	if err := os.WriteFile(file, []byte("mode: set\nexample.com/app/main.go:1.1,2.2 1 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, "scan", "import-coverage", "scan-id", "--commit", strings.Repeat("a", 40), "--format", "go", "--file", file, "--strip-prefix", "example.com/app", "--server", server.URL, "--token", "test", "-o", "json")
	if err != nil {
		t.Fatalf("%v %s", err, out)
	}
	if uploaded["commit_sha"] != strings.Repeat("a", 40) {
		t.Fatal("commit lost")
	}
	if _, err := run(t, "scan", "import-coverage", "scan-id", "--format", "go", "--file", file); err == nil {
		t.Fatal("commit must be explicit")
	}
}
