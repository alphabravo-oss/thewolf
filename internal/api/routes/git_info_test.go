package routes

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// TestGitInfo_RejectsOutsideRoot mirrors the BrowseLocal containment test:
// the git-info endpoint shells out to `git`, so the same allow-list check
// must run BEFORE git is invoked. Otherwise an authenticated user could
// probe whether arbitrary server paths are git repos.
func TestGitInfo_RejectsOutsideRoot(t *testing.T) {
	allowed := t.TempDir()
	t.Setenv("WOLF_BROWSE_ROOTS", allowed)

	cases := []struct {
		name       string
		path       string
		wantStatus int
	}{
		{"missing path param", "", http.StatusBadRequest},
		{"absolute outside root", "/etc", http.StatusForbidden},
		{"escape via dotdot", filepath.Join(allowed, "..", ".."), http.StatusForbidden},
		{"nonexistent under root", filepath.Join(allowed, "nope"), http.StatusNotFound},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			url := "/api/git-info"
			if c.path != "" {
				url += "?path=" + c.path
			}
			req := httptest.NewRequest(http.MethodGet, url, nil)
			w := httptest.NewRecorder()
			GitInfo(w, req)
			if w.Code != c.wantStatus {
				t.Errorf("status = %d, want %d (body: %s)", w.Code, c.wantStatus, w.Body.String())
			}
		})
	}
}

// TestGitInfo_NotAGitRepo verifies that pointing at a plain directory under
// the allow-list returns a 200 with is_git=false (rather than a 500), so the
// UI can render "not a git repo" without surfacing an error toast.
func TestGitInfo_NotAGitRepo(t *testing.T) {
	allowed := t.TempDir()
	t.Setenv("WOLF_BROWSE_ROOTS", allowed)

	plain := filepath.Join(allowed, "plain")
	if err := os.MkdirAll(plain, 0o750); err != nil {
		t.Fatalf("setup: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/git-info?path="+plain, nil)
	w := httptest.NewRecorder()
	GitInfo(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	var body struct {
		Data struct {
			IsGit bool `json:"is_git"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Data.IsGit {
		t.Errorf("is_git = true, want false for a plain directory")
	}
}
