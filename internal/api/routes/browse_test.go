package routes

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

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
