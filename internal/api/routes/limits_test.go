package routes_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/api/routes"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/pkg/entitlement"
)

func TestCommunityRepoLimit(t *testing.T) {
	t.Setenv(entitlement.LimitsEnv, "1")
	entitlement.SetActive(entitlement.Community{})
	t.Cleanup(func() { entitlement.SetActive(nil) })

	r, store, token := setupRepoRouter(t)
	defer store.Close()

	create := func(i int) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]string{
			"name":        fmt.Sprintf("r%d", i),
			"source_type": "local",
			"source_path": fmt.Sprintf("/tmp/limit-r%d", i),
		})
		req := httptest.NewRequest(http.MethodPost, "/api/repos", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	for i := 0; i < entitlement.SyntheticRepos; i++ {
		w := create(i)
		if w.Code != http.StatusCreated {
			t.Fatalf("repo %d: %d %s", i, w.Code, w.Body.String())
		}
	}
	over := create(entitlement.SyntheticRepos)
	if over.Code != http.StatusConflict {
		t.Fatalf("over: expected 409, got %d %s", over.Code, over.Body.String())
	}
}

func TestCommunityUserLimitOnRegister(t *testing.T) {
	t.Setenv(entitlement.LimitsEnv, "1")
	entitlement.SetActive(entitlement.Community{})
	t.Cleanup(func() { entitlement.SetActive(nil) })

	r, store, _ := setupRepoRouter(t)
	defer store.Close()
	if err := store.SetSetting(t.Context(), "registration_enabled", "true"); err != nil {
		t.Fatal(err)
	}

	register := func(email string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]string{"email": email, "password": "password1234"})
		req := httptest.NewRequest(http.MethodPost, "/api/auth/register", bytes.NewReader(body))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	// setupRepoRouter already created the first user.
	for i := 1; i < entitlement.SyntheticUsers; i++ {
		w := register(fmt.Sprintf("u%d@test.com", i))
		if w.Code != http.StatusCreated && w.Code != http.StatusOK {
			t.Fatalf("user %d: %d %s", i, w.Code, w.Body.String())
		}
	}
	over := register("overflow@test.com")
	if over.Code != http.StatusConflict {
		t.Fatalf("over: expected 409, got %d %s", over.Code, over.Body.String())
	}
}

func TestCommunityWorkerLimit(t *testing.T) {
	t.Setenv(entitlement.LimitsEnv, "1")
	entitlement.SetActive(entitlement.Community{})
	t.Cleanup(func() { entitlement.SetActive(nil) })

	_, store, _ := setupRepoRouter(t)
	defer store.Close()
	users, err := store.ListUsers(t.Context())
	if err != nil || len(users) == 0 {
		t.Fatalf("users: %v", err)
	}
	repo := &models.Repo{
		ID: "repo-limit-1", UserID: users[0].ID, Name: "limit",
		SourceType: models.SourceTypeLocal, SourcePath: t.TempDir(), DefaultBranch: "main",
	}
	if err := store.CreateRepo(t.Context(), repo); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateScan(t.Context(), &models.Scan{
		ID: "scan-limit-1", UserID: users[0].ID, RepoID: repo.ID,
		Status: models.ScanStatusPending, Branch: "main",
	}); err != nil {
		t.Fatal(err)
	}
	if err := routes.CheckCommunityLimit(t.Context(), store, "workers"); err == nil {
		t.Fatal("expected worker ceiling")
	}
}

func TestCommunityLimitsOffByDefault(t *testing.T) {
	t.Setenv(entitlement.LimitsEnv, "")
	entitlement.SetActive(entitlement.Community{})
	t.Cleanup(func() { entitlement.SetActive(nil) })
	if err := routes.CheckCommunityLimit(t.Context(), nil, "repos"); err != nil {
		t.Fatal(err)
	}
}
