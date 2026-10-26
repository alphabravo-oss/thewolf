package routes

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestBrowseBoundaries(t *testing.T) {
	allowed, outside := t.TempDir(), t.TempDir()
	t.Setenv("WOLF_BROWSE_ROOTS", allowed)
	for _, name := range []string{"escape", "dangling"} {
		target := outside
		if name == "dangling" {
			target = filepath.Join(outside, "missing")
		}
		if err := os.Symlink(target, filepath.Join(allowed, name)); err != nil {
			t.Fatal(err)
		}
	}
	for _, handler := range []http.HandlerFunc{BrowseLocal, GitInfo} {
		for _, path := range []string{outside, filepath.Join(outside, "missing"), filepath.Join(allowed, "escape"), filepath.Join(allowed, "dangling"), allowed + "-sibling"} {
			w := httptest.NewRecorder()
			handler(w, httptest.NewRequest(http.MethodGet, "/?path="+url.QueryEscape(path), nil))
			if w.Code != http.StatusForbidden {
				t.Fatalf("%s: status %d, body %s", path, w.Code, w.Body.String())
			}
		}
	}
	if !isUnderRoot(allowed, []string{string(filepath.Separator)}) {
		t.Fatal("filesystem root did not admit descendant")
	}
	if isUnderRoot(allowed+"-sibling", []string{allowed}) {
		t.Fatal("sibling prefix admitted")
	}
}

func TestBrowseDirectoryRemainsConfinedAfterRename(t *testing.T) {
	allowed, outside := t.TempDir(), t.TempDir()
	dir := filepath.Join(allowed, "repo")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "inside"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	root, _, err := openBrowseDirectory(dir, []string{allowed})
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := os.Rename(dir, dir+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, dir); err != nil {
		t.Fatal(err)
	}
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil || len(entries) != 1 || entries[0].Name() != "inside" {
		t.Fatalf("directory escaped: %v %v", entries, err)
	}
}

// TestBrowseLocal_RejectsTraversal verifies the path-containment check.
// Before the fix, filepath.Clean was the only normalization — but
// "/home/alice/../../etc" cleans to "/etc" and still resolves to a
// readable directory on most servers, so an authenticated user could
// enumerate arbitrary paths. The hardening pins browsing to one or
// more allow-listed roots.
func TestBrowseLocal_RejectsTraversal(t *testing.T) {
	// Allow only a temp dir; "/etc" must NOT be browsable from inside it.
	allowed := t.TempDir()
	t.Setenv("WOLF_BROWSE_ROOTS", allowed)

	cases := []struct {
		name       string
		path       string
		wantStatus int
	}{
		{"allow listed root itself", allowed, http.StatusOK},
		{"subdir of root", filepath.Join(allowed, "sub"), http.StatusNotFound}, // doesn't exist; passes containment, fails stat
		{"escape via dotdot to root parent", filepath.Join(allowed, "..", ".."), http.StatusForbidden},
		{"absolute outside root", "/etc", http.StatusForbidden},
	}

	if err := os.MkdirAll(filepath.Join(allowed, "ok"), 0o750); err != nil {
		t.Fatalf("setup: %v", err)
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/browse?path="+c.path, nil)
			w := httptest.NewRecorder()
			BrowseLocal(w, req)
			if w.Code != c.wantStatus {
				t.Errorf("status = %d, want %d (body: %s)", w.Code, c.wantStatus, w.Body.String())
			}
		})
	}
}

// TestBrowseLocal_DefaultsToFirstRoot covers the empty-path branch.
func TestBrowseLocal_DefaultsToFirstRoot(t *testing.T) {
	allowed := t.TempDir()
	t.Setenv("WOLF_BROWSE_ROOTS", allowed)

	if err := os.MkdirAll(filepath.Join(allowed, "alpha"), 0o750); err != nil {
		t.Fatalf("setup: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/browse", nil)
	w := httptest.NewRecorder()
	BrowseLocal(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Data struct {
			Current string     `json:"current"`
			Entries []dirEntry `json:"entries"`
		} `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// EvalSymlinks may resolve macOS /private/var/folders/.../T/... — accept
	// either the symlink original or the resolved form.
	if filepath.Base(resp.Data.Current) != filepath.Base(allowed) {
		t.Errorf("current = %q, want it to end in %q", resp.Data.Current, filepath.Base(allowed))
	}
	if len(resp.Data.Entries) != 1 || resp.Data.Entries[0].Name != "alpha" {
		t.Errorf("entries = %+v, want one 'alpha' dir", resp.Data.Entries)
	}
}
