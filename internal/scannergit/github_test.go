package scannergit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const (
	baseCommit = "1111111111111111111111111111111111111111"
	baseTree   = "2222222222222222222222222222222222222222"
	newBlob    = "3333333333333333333333333333333333333333"
	newTree    = "4444444444444444444444444444444444444444"
	newCommit  = "5555555555555555555555555555555555555555"
)

func TestGitHubProviderCreatesConflictSafeProposal(t *testing.T) {
	t.Parallel()
	var (
		mu      sync.Mutex
		writes  []string
		blobNum int
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("authorization header = %q", r.Header.Get("Authorization"))
		}
		key := r.Method + " " + r.URL.EscapedPath()
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "git/ref/heads/main"):
			writeJSON(t, w, http.StatusOK, map[string]any{"object": map[string]string{"sha": baseCommit}})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "git/ref/heads/scanner"):
			writeJSON(t, w, http.StatusNotFound, map[string]string{"message": "not found"})
		case key == "GET /repos/acme/definitions/git/commits/"+baseCommit:
			writeJSON(t, w, http.StatusOK, map[string]any{"tree": map[string]string{"sha": baseTree}})
		case key == "POST /repos/acme/definitions/git/blobs":
			mu.Lock()
			blobNum++
			writes = append(writes, "blob")
			mu.Unlock()
			writeJSON(t, w, http.StatusCreated, map[string]string{"sha": newBlob})
		case key == "POST /repos/acme/definitions/git/trees":
			var body struct {
				Tree []struct {
					Path string  `json:"path"`
					SHA  *string `json:"sha"`
				} `json:"tree"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode tree request: %v", err)
			}
			deletionFound := false
			for _, entry := range body.Tree {
				if entry.Path == "scanners/obsolete.lock" && entry.SHA == nil {
					deletionFound = true
				}
			}
			if !deletionFound {
				t.Errorf("tree request did not contain explicit deletion: %#v", body.Tree)
			}
			mu.Lock()
			writes = append(writes, "tree")
			mu.Unlock()
			writeJSON(t, w, http.StatusCreated, map[string]string{"sha": newTree})
		case key == "POST /repos/acme/definitions/git/commits":
			mu.Lock()
			writes = append(writes, "commit")
			mu.Unlock()
			writeJSON(t, w, http.StatusCreated, map[string]string{"sha": newCommit})
		case key == "POST /repos/acme/definitions/git/refs":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["ref"] != "refs/heads/scanner/weekly-2026-31" || body["sha"] != newCommit {
				t.Errorf("create ref body = %#v", body)
			}
			mu.Lock()
			writes = append(writes, "ref")
			mu.Unlock()
			writeJSON(t, w, http.StatusCreated, map[string]any{})
		case key == "GET /repos/acme/definitions/pulls":
			if r.URL.Query().Get("head") != "acme:scanner/weekly-2026-31" {
				t.Errorf("pull query = %q", r.URL.RawQuery)
			}
			writeJSON(t, w, http.StatusOK, []any{})
		case key == "POST /repos/acme/definitions/pulls":
			mu.Lock()
			writes = append(writes, "pull")
			mu.Unlock()
			writeJSON(t, w, http.StatusCreated, map[string]any{
				"number": 42, "html_url": "https://github.example/acme/definitions/pull/42",
			})
		case key == "POST /repos/acme/definitions/issues/42/labels":
			mu.Lock()
			writes = append(writes, "labels")
			mu.Unlock()
			writeJSON(t, w, http.StatusOK, map[string]any{})
		default:
			t.Errorf("unexpected GitHub request %s path=%q escaped=%q", r.Method, r.URL.Path, r.URL.EscapedPath())
			writeJSON(t, w, http.StatusNotFound, map[string]string{"message": "unexpected"})
		}
	}))
	defer server.Close()

	provider := newTestProvider(t, server.URL, "test-token")
	result, err := provider.CreateProposal(context.Background(), Proposal{
		BaseBranch: "main", ExpectedBaseCommit: baseCommit,
		Branch: "scanner/weekly-2026-31", CommitMessage: "chore(scanners): weekly update",
		Title: "Weekly scanner update", Body: "Generated evidence.", Labels: []string{"scanner-release"},
		Files: []File{
			{Path: "scanners/tools.yaml", Content: []byte("tools")},
			{Path: "scanners/scanner-lock.yaml", Content: []byte("lock")},
			{Path: "scanners/obsolete.lock", Delete: true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Commit != newCommit || result.PullRequest != 42 || !result.Created {
		t.Fatalf("proposal result = %#v", result)
	}
	mu.Lock()
	defer mu.Unlock()
	if blobNum != 2 || strings.Join(writes, ",") != "blob,blob,tree,commit,ref,pull,labels" {
		t.Fatalf("writes = %v blobs=%d", writes, blobNum)
	}
}

func TestGitHubProviderRefusesUnknownExistingBranchHead(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "heads/main"):
			writeJSON(t, w, http.StatusOK, map[string]any{"object": map[string]string{"sha": baseCommit}})
		case strings.Contains(r.URL.Path, "heads/scanner"):
			writeJSON(t, w, http.StatusOK, map[string]any{"object": map[string]string{"sha": newCommit}})
		case strings.Contains(r.URL.Path, "git/commits/"+newCommit):
			writeJSON(t, w, http.StatusOK, map[string]any{
				"tree":    map[string]string{"sha": newTree},
				"parents": []map[string]string{{"sha": strings.Repeat("9", 40)}},
			})
		default:
			t.Fatalf("unexpected provider request after conflict: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	provider := newTestProvider(t, server.URL, "test-token")
	_, err := provider.CreateProposal(context.Background(), Proposal{
		BaseBranch: "main", ExpectedBaseCommit: baseCommit, Branch: "scanner/update",
		CommitMessage: "update", Title: "update",
		Files: []File{{Path: "scanners/tools.yaml", Content: []byte("tools")}},
	})
	if err == nil || !strings.Contains(err.Error(), ErrConflict.Error()) {
		t.Fatalf("existing branch conflict = %v", err)
	}
}

func TestGitHubProviderAdoptsExactBranchAfterWorkerCrash(t *testing.T) {
	t.Parallel()
	var writes []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.EscapedPath()
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "git/ref/heads/main"):
			writeJSON(t, w, http.StatusOK, map[string]any{"object": map[string]string{"sha": baseCommit}})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "git/ref/heads/scanner"):
			writeJSON(t, w, http.StatusOK, map[string]any{"object": map[string]string{"sha": newCommit}})
		case key == "GET /repos/acme/definitions/git/commits/"+newCommit:
			writeJSON(t, w, http.StatusOK, map[string]any{
				"tree":    map[string]string{"sha": newTree},
				"parents": []map[string]string{{"sha": baseCommit}},
			})
		case key == "GET /repos/acme/definitions/git/commits/"+baseCommit:
			writeJSON(t, w, http.StatusOK, map[string]any{"tree": map[string]string{"sha": baseTree}})
		case key == "POST /repos/acme/definitions/git/blobs":
			writes = append(writes, "blob")
			writeJSON(t, w, http.StatusCreated, map[string]string{"sha": newBlob})
		case key == "POST /repos/acme/definitions/git/trees":
			writes = append(writes, "tree")
			writeJSON(t, w, http.StatusCreated, map[string]string{"sha": newTree})
		case key == "GET /repos/acme/definitions/pulls":
			writeJSON(t, w, http.StatusOK, []map[string]any{{
				"number": 42, "html_url": "https://github.example/acme/definitions/pull/42",
			}})
		default:
			t.Errorf("unexpected GitHub retry request %s %s", r.Method, r.URL.String())
			writeJSON(t, w, http.StatusNotFound, map[string]string{"message": "unexpected"})
		}
	}))
	defer server.Close()

	provider := newTestProvider(t, server.URL, "test-token")
	result, err := provider.CreateProposal(context.Background(), Proposal{
		BaseBranch: "main", ExpectedBaseCommit: baseCommit,
		Branch: "scanner/retry", CommitMessage: "update", Title: "update",
		Files: []File{{Path: "scanners/tools.yaml", Content: []byte("tools")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Commit != newCommit || result.PullRequest != 42 || result.Created {
		t.Fatalf("adopted proposal = %#v", result)
	}
	if strings.Join(writes, ",") != "blob,tree" {
		t.Fatalf("retry writes = %v; branch/commit/PR must not be rewritten", writes)
	}
}

func TestGitHubProviderValidationAndErrorRedaction(t *testing.T) {
	t.Parallel()
	provider := newTestProvider(t, "https://api.github.example", "token")
	for _, file := range []File{
		{Path: "../secret", Content: []byte("x")},
		{Path: ".git/config", Content: []byte("x")},
		{Path: "scanners\\tools.yaml", Content: []byte("x")},
		{Path: "scanners/deleted.yaml", Content: []byte("x"), Delete: true},
	} {
		_, err := provider.CreateProposal(context.Background(), Proposal{
			BaseBranch: "main", ExpectedBaseCommit: baseCommit, Branch: "scanner/update",
			CommitMessage: "update", Title: "update", Files: []File{file},
		})
		if err == nil || !strings.Contains(err.Error(), ErrValidation.Error()) {
			t.Fatalf("unsafe file accepted: %#v err=%v", file, err)
		}
	}

	secret := "ghp_super_secret_value"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, http.StatusBadGateway, map[string]string{
			"message": "Authorization: Bearer " + secret,
		})
	}))
	defer server.Close()
	provider = newTestProvider(t, server.URL, secret)
	err := provider.SetCommitStatus(context.Background(), baseCommit, CommitStatus{
		State: "success", Context: "scanner-release/gates",
	})
	if err == nil || strings.Contains(err.Error(), secret) || !strings.Contains(err.Error(), "[REDACTED]") {
		t.Fatalf("redacted GitHub error = %v", err)
	}
}

func newTestProvider(t *testing.T, baseURL, token string) *GitHubProvider {
	t.Helper()
	provider, err := NewGitHubProvider(GitHubConfig{
		BaseURL: baseURL, Owner: "acme", Repository: "definitions", Token: token,
	})
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func writeJSON(t *testing.T, w http.ResponseWriter, status int, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatal(err)
	}
}
