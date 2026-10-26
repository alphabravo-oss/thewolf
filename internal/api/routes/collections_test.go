package routes_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/api/routes"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/google/uuid"
)

func TestCollectionHistoryFollowsRepositoryAndRejectsForeignReaders(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()
	env.Router.With(auth.Middleware).Get("/api/collections/{id}", routes.GetCollection)
	env.Router.With(auth.Middleware).Get("/api/collections/{id}/metrics", routes.CollectionMetrics)
	ctx := context.Background()
	repoID := env.createRepo(t)
	old := &models.Collection{ID: uuid.NewString(), UserID: env.UserID, Name: "Old"}
	current := &models.Collection{ID: uuid.NewString(), UserID: env.UserID, Name: "Current"}
	for _, col := range []*models.Collection{old, current} {
		if err := env.Store.CreateCollection(ctx, col); err != nil {
			t.Fatal(err)
		}
	}
	for _, colID := range []*string{nil, &old.ID} {
		if err := env.Store.CreateScan(ctx, &models.Scan{ID: uuid.NewString(), UserID: env.UserID, RepoID: repoID, CollectionID: colID, Branch: "main", Status: models.ScanStatusCompleted, ToolsSelected: `["semgrep"]`, ToolsCompleted: `["semgrep"]`, ToolsFailed: `[]`}); err != nil {
			t.Fatal(err)
		}
	}
	if err := env.Store.SetRepoCollection(ctx, repoID, current.ID); err != nil {
		t.Fatal(err)
	}
	w := env.doRequest(http.MethodGet, "/api/collections/"+current.ID, nil)
	var got struct {
		Data struct {
			Repos []models.Repo `json:"repos"`
			Scans []models.Scan `json:"scans"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK || len(got.Data.Repos) != 1 || len(got.Data.Scans) != 2 || got.Data.Scans[0].Repo == nil {
		t.Fatalf("history missing: %d %s", w.Code, w.Body.String())
	}
	prior, err := env.Store.ListScansByCollection(ctx, old.ID)
	if err != nil || len(prior) != 0 {
		t.Fatalf("moved repository remains in old view: %+v %v", prior, err)
	}
	other := &models.User{ID: uuid.NewString(), Email: "other-collection@example.test", PasswordHash: "hash", Role: models.RoleUser}
	if err := env.Store.CreateUser(ctx, other); err != nil {
		t.Fatal(err)
	}
	token, err := auth.GenerateToken(other.ID, other.Email)
	if err != nil {
		t.Fatal(err)
	}
	original := env.Token
	env.Token = token.AccessToken
	for _, suffix := range []string{"", "/metrics"} {
		w := env.doRequest(http.MethodGet, "/api/collections/"+current.ID+suffix, nil)
		if w.Code != http.StatusForbidden {
			t.Fatalf("foreign read: %d %s", w.Code, w.Body.String())
		}
	}
	env.Token = original
	routes.SetHandler(collectionScanReadFailure{env.Store}, nil)
	w = env.doRequest(http.MethodGet, "/api/collections/"+current.ID, nil)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("failed history looked empty: %d %s", w.Code, w.Body.String())
	}
}

type collectionScanReadFailure struct{ db.Store }

func (s collectionScanReadFailure) ListScansByCollection(context.Context, string) ([]models.Scan, error) {
	return nil, errors.New("scan history unavailable")
}
