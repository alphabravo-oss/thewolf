package routes_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/api/routes"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/github"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/secrets"
)

func TestListOrgGitHubRepos_HappyPath(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()

	// Make sure crypto is wired up for secret encryption.
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	secrets.SetMasterKey(key)

	// Spin up a fake GitHub server.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/orgs/acme/repos") {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `[{"name":"api","full_name":"acme/api","default_branch":"main","private":true,"archived":false},{"name":"web","full_name":"acme/web","default_branch":"main","private":false,"archived":false}]`)
	}))
	defer srv.Close()

	// Drive the client against the fake server.
	github.DefaultBaseURL = srv.URL
	t.Cleanup(func() { github.DefaultBaseURL = "" })

	// Seed a github_token secret for the test user.
	enc, err := secrets.Encrypt("ghp_x")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	now := time.Now()
	if err := env.Store.CreateSecret(context.Background(), &models.Secret{
		ID:             uuid.New().String(),
		UserID:         env.UserID,
		KeyType:        models.KeyTypeGitHubToken,
		KeyName:        "primary",
		EncryptedValue: enc,
		CreatedAt:      now,
		UpdatedAt:      now,
	}); err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}

	// Mount the new route on a fresh router (testEnv router doesn't have it).
	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(auth.Middleware)
		r.Post("/api/sources/github/list-org-repos", routes.ListOrgGitHubRepos)
	})

	body, _ := json.Marshal(map[string]string{"org": "acme"})
	req := httptest.NewRequest(http.MethodPost, "/api/sources/github/list-org-repos", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+env.Token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Data []github.Repo `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Data) != 2 || resp.Data[0].FullName != "acme/api" {
		t.Errorf("got %+v", resp.Data)
	}
}

func TestListAccessibleGitHubRepos_EmptyOrg(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()

	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 3)
	}
	secrets.SetMasterKey(key)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user/repos" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `[{"name":"wolf","full_name":"acme/wolf","default_branch":"main","private":true,"archived":false}]`)
	}))
	defer srv.Close()
	github.DefaultBaseURL = srv.URL
	t.Cleanup(func() { github.DefaultBaseURL = "" })

	enc, err := secrets.Encrypt("ghp_x")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := env.Store.CreateSecret(context.Background(), &models.Secret{
		ID: uuid.New().String(), UserID: env.UserID, KeyType: models.KeyTypeGitHubToken,
		KeyName: "pat", EncryptedValue: enc, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(auth.Middleware)
		r.Post("/api/sources/github/list-org-repos", routes.ListOrgGitHubRepos)
	})
	req := httptest.NewRequest(http.MethodPost, "/api/sources/github/list-org-repos", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+env.Token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Data []github.Repo `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != 1 || resp.Data[0].FullName != "acme/wolf" {
		t.Fatalf("got %+v", resp.Data)
	}
}

func TestListOrgGitHubRepos_NoSecret(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()

	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(auth.Middleware)
		r.Post("/api/sources/github/list-org-repos", routes.ListOrgGitHubRepos)
	})

	body, _ := json.Marshal(map[string]string{"org": "acme"})
	req := httptest.NewRequest(http.MethodPost, "/api/sources/github/list-org-repos", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+env.Token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 (no secret), got %d: %s", w.Code, w.Body.String())
	}
}
