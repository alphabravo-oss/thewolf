package routes_test

import (
	"bytes"
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
