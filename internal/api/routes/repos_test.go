package routes_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
		r.Put("/api/repos/{id}", routes.UpdateRepo)
		r.Delete("/api/repos/{id}", routes.DeleteRepo)
	})

	// Register a user and get token
	body, _ := json.Marshal(map[string]string{
		"email":    "repouser@test.com",
		"password": "password123",
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
