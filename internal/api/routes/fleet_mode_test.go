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

// TestAdminSeesAllUsersRepos: fleet visibility is now role-based. The env user
// is the first registered account (an admin), so a foreign user's repos /
// scans / collections all surface to them.
func TestAdminSeesAllUsersRepos(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()

	_, _, _, _ = seedOtherUserRepo(t, env)

	for _, path := range []string{"/api/repos", "/api/scans", "/api/collections"} {
		w := doFleetRequest(t, env, path) // env.Token = the admin
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s: expected 200, got %d: %s", path, w.Code, w.Body.String())
		}
		if got := metaTotal(t, w.Body.Bytes()); got < 1 {
			t.Errorf("admin should see other users' %s, got %d", path, got)
		}
	}
}

// TestRegularUserSeesOnlyOwnRepos: a non-admin sees only what they created, not
// the other user's items (no global fleet_mode toggle exists any more).
func TestRegularUserSeesOnlyOwnRepos(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()
	ctx := context.Background()

	// Another user (regular) owns one repo + collection + scan.
	otherID, _, _, _ := seedOtherUserRepo(t, env)

	// And the admin owns a repo too, which the regular user must NOT see.
	if err := env.Store.CreateRepo(ctx, &models.Repo{
		ID: uuid.New().String(), UserID: env.UserID, Name: "admin-repo",
		SourceType: models.SourceTypeLocal, SourcePath: "/tmp/admin-repo", DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("CreateRepo admin: %v", err)
	}

	// A token for the regular user (role resolves to "user" via the middleware).
	tokens, err := auth.GenerateToken(otherID, "other-fleet@example.com")
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/repos", nil)
	req.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
	w := httptest.NewRecorder()
	fleetTestRouter(env).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/repos: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := metaTotal(t, w.Body.Bytes()); got != 1 {
		t.Errorf("regular user should see only their own repo (1), got %d", got)
	}
}
