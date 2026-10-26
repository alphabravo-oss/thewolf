package routes

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestGitInfo_RejectsOutsideRoot mirrors the BrowseLocal containment test:
// the git-info endpoint reads Git metadata, so the same allow-list check
// must run BEFORE that metadata is read. Otherwise an authenticated user could
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

func TestGitInfoRejectsExternalWorktreeMetadata(t *testing.T) {
	allowed, outside := t.TempDir(), t.TempDir()
	t.Setenv("WOLF_BROWSE_ROOTS", allowed)
	gitInfoCommand(t, outside, "init", "-b", "outside-secret")
	gitInfoCommand(t, outside, "-c", "user.name=fixture", "-c", "user.email=fixture@example.test", "commit", "--allow-empty", "-m", "fixture")
	if err := os.WriteFile(filepath.Join(allowed, ".git"), []byte("gitdir: "+filepath.Join(outside, ".git")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	GitInfo(w, httptest.NewRequest(http.MethodGet, "/api/git-info?path="+url.QueryEscape(allowed), nil))
	if w.Code == http.StatusOK || strings.Contains(w.Body.String(), "outside-secret") {
		t.Fatalf("external metadata was exposed: %d %s", w.Code, w.Body.String())
	}
}

func gitInfoCommand(t *testing.T, path string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = path
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git fixture: %v: %s", err, out)
	}
}

func TestGitInfoAllowsWorktreesOnlyWithAuthorizedCommonDirectory(t *testing.T) {
	allowed := t.TempDir()
	t.Setenv("WOLF_BROWSE_ROOTS", allowed)
	repo := filepath.Join(allowed, "repo")
	if err := os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	gitInfoCommand(t, repo, "init", "-b", "main")
	gitInfoCommand(t, repo, "-c", "user.name=fixture", "-c", "user.email=fixture@example.test", "commit", "--allow-empty", "-m", "fixture")
	worktree := filepath.Join(allowed, "linked")
	gitInfoCommand(t, repo, "worktree", "add", "-b", "linked", worktree)
	gitInfoCommand(t, repo, "pack-refs", "--all", "--prune")
	request := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		GitInfo(w, httptest.NewRequest(http.MethodGet, "/api/git-info?path="+url.QueryEscape(worktree), nil))
		return w
	}
	w := request()
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"current_branch":"linked"`) || !strings.Contains(w.Body.String(), `"branches":["linked","main"]`) {
		t.Fatalf("allowed worktree rejected: %d %s", w.Code, w.Body.String())
	}
	t.Setenv("WOLF_BROWSE_ROOTS", worktree)
	if w := request(); w.Code == http.StatusOK {
		t.Fatalf("worktree admitted metadata outside its sole allowed root: %s", w.Body.String())
	}
	t.Setenv("WOLF_BROWSE_ROOTS", worktree+string(os.PathListSeparator)+repo)
	if w := request(); w.Code != http.StatusOK {
		t.Fatalf("explicitly allowlisted metadata root rejected: %d %s", w.Code, w.Body.String())
	}
	t.Setenv("WOLF_BROWSE_ROOTS", allowed)
	// A permitted per-worktree gitdir is insufficient: its commondir is a
	// separate trust boundary, even when the worktree itself is allowed.
	if err := os.WriteFile(filepath.Join(repo, ".git", "worktrees", "linked", "commondir"), []byte(t.TempDir()), 0600); err != nil {
		t.Fatal(err)
	}
	if w := request(); w.Code == http.StatusOK {
		t.Fatalf("outside common directory accepted: %s", w.Body.String())
	}
}

func TestGitInfoRejectsEscapingMetadataSymlinksAndMalformedPointers(t *testing.T) {
	for _, kind := range []string{"gitdir-symlink", "packed-refs-symlink", "pointer-file", "multiline-pointer", "large-pointer"} {
		t.Run(kind, func(t *testing.T) {
			allowed, outside := t.TempDir(), t.TempDir()
			t.Setenv("WOLF_BROWSE_ROOTS", allowed)
			gitInfoCommand(t, outside, "init", "-b", "outside-secret")
			gitInfoCommand(t, outside, "-c", "user.name=fixture", "-c", "user.email=fixture@example.test", "commit", "--allow-empty", "-m", "fixture")
			gitInfoCommand(t, outside, "pack-refs", "--all", "--prune")
			gitdir := filepath.Join(allowed, ".git")
			switch kind {
			case "gitdir-symlink":
				if err := os.Symlink(filepath.Join(outside, ".git"), gitdir); err != nil {
					t.Fatal(err)
				}
			case "packed-refs-symlink":
				gitInfoCommand(t, allowed, "init", "-b", "main")
				if err := os.Symlink(filepath.Join(outside, ".git", "packed-refs"), filepath.Join(gitdir, "packed-refs")); err != nil {
					t.Fatal(err)
				}
			default:
				value := "gitdir: " + filepath.Join(allowed, "ordinary-file") + "\n"
				if err := os.WriteFile(filepath.Join(allowed, "ordinary-file"), nil, 0600); err != nil {
					t.Fatal(err)
				}
				if kind == "multiline-pointer" {
					value = "gitdir: metadata\ninjected\n"
				} else if kind == "large-pointer" {
					value = "gitdir: " + strings.Repeat("x", 4096)
				}
				if err := os.WriteFile(gitdir, []byte(value), 0600); err != nil {
					t.Fatal(err)
				}
			}
			w := httptest.NewRecorder()
			GitInfo(w, httptest.NewRequest(http.MethodGet, "/api/git-info?path="+url.QueryEscape(allowed), nil))
			if w.Code == http.StatusOK || strings.Contains(w.Body.String(), "outside-secret") {
				t.Fatalf("unsafe metadata accepted: %d %s", w.Code, w.Body.String())
			}
		})
	}
}
