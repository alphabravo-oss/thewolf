package routes_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/api/routes"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/models"
)

// fleetTestRouter mounts the list endpoints exercised by the fleet_mode tests.
// setupTestEnv from scans_test.go covers POST /api/repos / scans but does not
// register the matching GET handlers, so the tests register their own group.
func fleetTestRouter(env *testEnv) *chi.Mux {
	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(auth.Middleware)
		r.Get("/api/repos", routes.ListRepos)
		r.Get("/api/scans", routes.ListScans)
		r.Get("/api/collections", routes.ListCollections)
		r.Get("/api/findings", routes.ListFindings)
	})
	return r
}

// doFleetRequest issues an authenticated GET against the fleet-mode test router.
func doFleetRequest(t *testing.T, env *testEnv, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+env.Token)
	w := httptest.NewRecorder()
	fleetTestRouter(env).ServeHTTP(w, req)
	return w
}

// seedOtherUserRepo creates a second user and one repo + collection + scan
// owned by them so we can assert visibility scoping. Returns the foreign IDs.
func seedOtherUserRepo(t *testing.T, env *testEnv) (userID, repoID, colID, scanID string) {
	t.Helper()
	ctx := context.Background()
	userID = uuid.New().String()
	if err := env.Store.CreateUser(ctx, &models.User{
		ID:           userID,
		Email:        "other-fleet@example.com",
		PasswordHash: "hash",
	}); err != nil {
		t.Fatalf("CreateUser other: %v", err)
	}
	repoID = uuid.New().String()
	if err := env.Store.CreateRepo(ctx, &models.Repo{
		ID:            repoID,
		UserID:        userID,
		Name:          "other-fleet-repo",
		SourceType:    models.SourceTypeLocal,
		SourcePath:    "/tmp/other-fleet-repo",
		DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("CreateRepo other: %v", err)
	}
	colID = uuid.New().String()
	if err := env.Store.CreateCollection(ctx, &models.Collection{
		ID:          colID,
		UserID:      userID,
		Name:        "other-fleet-collection",
		Description: "",
		ScanConfig:  "{}",
	}); err != nil {
		t.Fatalf("CreateCollection other: %v", err)
	}
	scanID = uuid.New().String()
	if err := env.Store.CreateScan(ctx, &models.Scan{
		ID:              scanID,
		UserID:          userID,
		RepoID:          repoID,
		Branch:          "main",
		Status:          models.ScanStatusCompleted,
		ToolsSelected:   "[]",
		ToolsCompleted:  "[]",
		ToolsFailed:     "[]",
		CoverageSummary: "{}",
	}); err != nil {
		t.Fatalf("CreateScan other: %v", err)
	}
	return userID, repoID, colID, scanID
}

// metaTotal decodes the {data:[...], meta:{total:N}} envelope and returns N.
func metaTotal(t *testing.T, body []byte) int {
	t.Helper()
	var got struct {
		Meta struct {
			Total int `json:"total"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode meta: %v", err)
	}
	return got.Meta.Total
}

// TestFleetModeOffHidesOtherUsersRepos confirms the default fleet_mode=false
// posture: a foreign user's repos / collections / scans do not surface to the
// test caller.
func TestFleetModeOffHidesOtherUsersRepos(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()

	_, _, _, _ = seedOtherUserRepo(t, env)

	for _, path := range []string{"/api/repos", "/api/scans", "/api/collections"} {
		w := doFleetRequest(t, env, path)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s: expected 200, got %d: %s", path, w.Code, w.Body.String())
		}
		if got := metaTotal(t, w.Body.Bytes()); got != 0 {
			t.Errorf("fleet_mode=false: expected 0 rows at %s, got %d", path, got)
		}
	}
}

// TestFleetModeOnExposesOtherUsersRepos flips fleet_mode and confirms that
// the foreign user's repo, collection, and scan all become visible.
func TestFleetModeOnExposesOtherUsersRepos(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()

	_, _, _, _ = seedOtherUserRepo(t, env)

	if err := env.Store.SetSetting(context.Background(), "fleet_mode", "true"); err != nil {
		t.Fatalf("SetSetting fleet_mode=true: %v", err)
	}

	for _, path := range []string{"/api/repos", "/api/scans", "/api/collections"} {
		w := doFleetRequest(t, env, path)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s: expected 200, got %d: %s", path, w.Code, w.Body.String())
		}
		if got := metaTotal(t, w.Body.Bytes()); got < 1 {
			t.Errorf("fleet_mode=true: expected >=1 row at %s, got %d", path, got)
		}
	}
}
