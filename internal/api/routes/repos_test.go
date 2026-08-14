package routes_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/alphabravocompany/thewolf/internal/api/routes"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/scantarget"
	"github.com/alphabravocompany/thewolf/internal/secrets"
)

func setupRepoRouter(t *testing.T) (*chi.Mux, db.Store, string) {
	t.Helper()

	store, err := db.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(); err != nil {
		t.Fatal(err)
	}

	auth.SetJWTSecret([]byte("test-secret-key-for-jwt-signing"))
	routes.SetHandler(store, nil)

	r := chi.NewRouter()
	r.Post("/api/auth/register", routes.Register)
	r.Group(func(r chi.Router) {
		r.Use(auth.Middleware)
		r.Get("/api/repos", routes.ListRepos)
		r.Post("/api/repos", routes.CreateRepo)
		r.Get("/api/repos/{id}", routes.GetRepo)
		r.Get("/api/repos/{id}/fixable", routes.GetRepoFixable)
		r.Put("/api/repos/{id}", routes.UpdateRepo)
		r.Post("/api/repos/{id}/sync", routes.SyncRepo)
		r.Delete("/api/repos/{id}", routes.DeleteRepo)
	})

	// Register a user and get token
	body, _ := json.Marshal(map[string]string{
		"email":    "repouser@test.com",
		"password": "password1234",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/register", bytes.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var resp struct {
		Data struct {
			AccessToken string `json:"access_token"`
		} `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)

	return r, store, resp.Data.AccessToken
}

func TestRepoCRUD(t *testing.T) {
	r, store, token := setupRepoRouter(t)
	defer store.Close()

	// Create repo
	body, _ := json.Marshal(map[string]string{
		"name":        "my-repo",
		"source_type": "local",
		"source_path": "/tmp/my-repo",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/repos", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("create repo: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var createResp struct {
		Data struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &createResp)
	repoID := createResp.Data.ID
	if repoID == "" {
		t.Fatal("create repo: expected ID in response")
	}
	if createResp.Data.Name != "my-repo" {
		t.Fatalf("create repo: expected name my-repo, got %s", createResp.Data.Name)
	}

	// List repos
	req = httptest.NewRequest(http.MethodGet, "/api/repos", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("list repos: expected 200, got %d", w.Code)
	}

	// Get repo
	req = httptest.NewRequest(http.MethodGet, "/api/repos/"+repoID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("get repo: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Update repo
	body, _ = json.Marshal(map[string]string{
		"name": "updated-repo",
	})
	req = httptest.NewRequest(http.MethodPut, "/api/repos/"+repoID, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("update repo: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Delete repo
	req = httptest.NewRequest(http.MethodDelete, "/api/repos/"+repoID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("delete repo: expected 200, got %d", w.Code)
	}

	// Get deleted repo should 404
	req = httptest.NewRequest(http.MethodGet, "/api/repos/"+repoID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("get deleted repo: expected 404, got %d", w.Code)
	}
}

func TestCreateGitHubRepoStoresSelectedCredential(t *testing.T) {
	r, store, token := setupRepoRouter(t)
	defer store.Close()

	secrets.SetMasterKey([]byte("0123456789abcdef0123456789abcdef"))
	user, err := store.GetUserByEmail(context.Background(), "repouser@test.com")
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	encrypted, err := secrets.Encrypt("ghp_selected")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	const secretID = "github-secret-1"
	if err := store.CreateSecret(context.Background(), &models.Secret{
		ID:             secretID,
		UserID:         user.ID,
		KeyType:        models.KeyTypeGitHubToken,
		KeyName:        "private-github",
		EncryptedValue: encrypted,
	}); err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}

	body, _ := json.Marshal(map[string]string{
		"name":                 "private-repo",
		"source_type":          "github",
		"source_path":          "acme/private-repo",
		"credential_secret_id": secretID,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/repos", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("create github repo: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var createResp struct {
		Data struct {
			ID                 string `json:"id"`
			CredentialSecretID string `json:"credential_secret_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if createResp.Data.CredentialSecretID != secretID {
		t.Fatalf("response credential_secret_id = %q, want %q", createResp.Data.CredentialSecretID, secretID)
	}
	stored, err := store.GetRepoByID(context.Background(), createResp.Data.ID)
	if err != nil {
		t.Fatalf("GetRepoByID: %v", err)
	}
	if stored.CredentialSecretID != secretID {
		t.Fatalf("stored credential_secret_id = %q, want %q", stored.CredentialSecretID, secretID)
	}
}

// TestRepoDedup verifies that adding a repo whose path already exists
// returns the existing repo (with deduplicated=true) instead of creating
// a second row — including trailing-slash-insensitive matching.
func TestRepoDedup(t *testing.T) {
	r, store, token := setupRepoRouter(t)
	defer store.Close()

	create := func(name, path string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]string{
			"name":        name,
			"source_type": "local",
			"source_path": path,
		})
		req := httptest.NewRequest(http.MethodPost, "/api/repos", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	decode := func(w *httptest.ResponseRecorder) (id string, dedup bool) {
		var resp struct {
			Data struct {
				ID           string `json:"id"`
				Deduplicated bool   `json:"deduplicated"`
			} `json:"data"`
		}
		json.Unmarshal(w.Body.Bytes(), &resp)
		return resp.Data.ID, resp.Data.Deduplicated
	}

	// First add — a fresh create.
	w := create("pioneer", "/repos/ab/pioneer")
	if w.Code != http.StatusCreated {
		t.Fatalf("first add: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	firstID, dedup := decode(w)
	if firstID == "" || dedup {
		t.Fatalf("first add: id=%q deduplicated=%v (want id set, dedup false)", firstID, dedup)
	}

	// Second add, same path with a trailing slash + different name —
	// should reuse the first repo, not create a new one.
	w = create("pioneer-again", "/repos/ab/pioneer/")
	if w.Code != http.StatusOK {
		t.Fatalf("dup add: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	dupID, dedup := decode(w)
	if !dedup {
		t.Error("dup add: expected deduplicated=true")
	}
	if dupID != firstID {
		t.Errorf("dup add: expected existing id %q, got %q", firstID, dupID)
	}

	// The repo list must still contain exactly one repo.
	req := httptest.NewRequest(http.MethodGet, "/api/repos", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var listResp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &listResp)
	if len(listResp.Data) != 1 {
		t.Fatalf("expected 1 repo after dup add, got %d", len(listResp.Data))
	}
}

// TestRepoFixable exercises GET /repos/{id}/fixable end to end against the real
// local writability probe: a temp git work tree reports writable=true, and a
// path with no .git reports writable=false with a reason.
func TestRepoFixable(t *testing.T) {
	r, store, token := setupRepoRouter(t)
	defer store.Close()

	createRepo := func(path string) string {
		body, _ := json.Marshal(map[string]string{
			"name":        "fixme",
			"source_type": "local",
			"source_path": path,
		})
		req := httptest.NewRequest(http.MethodPost, "/api/repos", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusCreated && w.Code != http.StatusOK {
			t.Fatalf("create repo: got %d: %s", w.Code, w.Body.String())
		}
		var resp struct {
			Data struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		json.Unmarshal(w.Body.Bytes(), &resp)
		return resp.Data.ID
	}

	fixable := func(id string) (int, bool, string) {
		req := httptest.NewRequest(http.MethodGet, "/api/repos/"+id+"/fixable", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		var resp struct {
			Data struct {
				Writable bool   `json:"writable"`
				Reason   string `json:"reason"`
			} `json:"data"`
		}
		json.Unmarshal(w.Body.Bytes(), &resp)
		return w.Code, resp.Data.Writable, resp.Data.Reason
	}

	// A writable git work tree → fixable.
	gitDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(gitDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	code, writable, reason := fixable(createRepo(gitDir))
	if code != http.StatusOK || !writable {
		t.Fatalf("git work tree: got code=%d writable=%v reason=%q", code, writable, reason)
	}

	// A plain dir (no .git) → not fixable, with a reason.
	plainDir := t.TempDir()
	code, writable, reason = fixable(createRepo(plainDir))
	if code != http.StatusOK || writable || reason == "" {
		t.Fatalf("plain dir: got code=%d writable=%v reason=%q", code, writable, reason)
	}

	// Unknown repo → 404.
	req := httptest.NewRequest(http.MethodGet, "/api/repos/does-not-exist/fixable", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown repo: expected 404, got %d", w.Code)
	}
}

func TestRepoUnauthorized(t *testing.T) {
	r, store, _ := setupRepoRouter(t)
	defer store.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/repos", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized: expected 401, got %d", w.Code)
	}
}

func TestSyncRepoPullsAndRecordsCommit(t *testing.T) {
	r, store, token := setupRepoRouter(t)
	defer store.Close()

	restore := routes.SetPrepareRepoForSyncForTest(func(_ context.Context, _ db.Store, repo *models.Repo, branch string) (scantarget.Prepared, error) {
		if repo.SourcePath != "acme/astronomer" {
			t.Errorf("source_path = %q", repo.SourcePath)
		}
		if branch != "main" {
			t.Errorf("branch = %q, want main", branch)
		}
		return scantarget.Prepared{
			Path:       t.TempDir(),
			SourceType: models.SourceTypeGitHub,
			SourcePath: repo.SourcePath,
			CommitSHA:  "abc123def456",
			DirtyState: "clean",
			Cleanup:    func() {},
		}, nil
	})
	t.Cleanup(restore)

	body, _ := json.Marshal(map[string]string{
		"name":        "astronomer",
		"source_type": "github",
		"source_path": "acme/astronomer",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/repos", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/repos/"+created.Data.ID+"/sync", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("sync: %d %s", w.Code, w.Body.String())
	}
	var synced struct {
		Data struct {
			LastCommitSHA     string `json:"last_commit_sha"`
			LastDirtyState    string `json:"last_dirty_state"`
			Branch            string `json:"branch"`
			PreviousCommitSHA string `json:"previous_commit_sha"`
			Changed           bool   `json:"changed"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &synced); err != nil {
		t.Fatal(err)
	}
	if synced.Data.LastCommitSHA != "abc123def456" || !synced.Data.Changed || synced.Data.Branch != "main" {
		t.Fatalf("sync payload = %+v", synced.Data)
	}
	stored, err := store.GetRepoByID(context.Background(), created.Data.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.LastCommitSHA != "abc123def456" {
		t.Fatalf("stored last_commit_sha = %q", stored.LastCommitSHA)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/repos/"+created.Data.ID+"/sync?branch=develop", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	restore2 := routes.SetPrepareRepoForSyncForTest(func(_ context.Context, _ db.Store, _ *models.Repo, branch string) (scantarget.Prepared, error) {
		if branch != "develop" {
			t.Errorf("branch = %q, want develop", branch)
		}
		return scantarget.Prepared{CommitSHA: "abc123def456", DirtyState: "clean", Cleanup: func() {}}, nil
	})
	t.Cleanup(restore2)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("second sync: %d %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &synced); err != nil {
		t.Fatal(err)
	}
	if synced.Data.Changed || synced.Data.PreviousCommitSHA != "abc123def456" {
		t.Fatalf("unchanged sync payload = %+v", synced.Data)
	}
}

func TestSyncRepoRejectsUnknownRepo(t *testing.T) {
	r, store, token := setupRepoRouter(t)
	defer store.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/repos/missing/sync", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}
