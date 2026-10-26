package api_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/api/routes"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/auth/apikey"
	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestScanLineageAuthorizesRelatedResources(t *testing.T) {
	srv, store, adminJWT := newTestServer(t)
	ctx := context.Background()
	admin, err := auth.ValidateToken(adminJWT)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"caller", "foreign", ""} {
		if err := store.CreateUser(ctx, &models.User{ID: id, Email: id + "lineage@example.test", PasswordHash: "unused"}); err != nil {
			t.Fatal(err)
		}
	}
	pair, err := auth.GenerateToken("caller", "callerlineage@example.test")
	if err != nil {
		t.Fatal(err)
	}
	userToken := createToken(t, srv, pair.AccessToken, []string{apikey.ScopeReadScans})
	adminToken := createToken(t, srv, adminJWT, []string{apikey.ScopeAdmin})
	for _, principal := range []struct {
		name, user, bearer string
		admin              bool
	}{
		{"user session", "caller", pair.AccessToken, false},
		{"user token", "caller", userToken, false},
		{"administrator session", admin.UserID, adminJWT, true},
		{"administrator token", admin.UserID, adminToken, false},
	} {
		t.Run(principal.name, func(t *testing.T) {
			for _, remOwner := range []string{principal.user, "foreign", ""} {
				t.Run("remediation owner="+remOwner, func(t *testing.T) {
					repo := &models.Repo{ID: uuid.NewString(), UserID: principal.user, Name: "lineage", SourceType: models.SourceTypeLocal}
					if err := store.CreateRepo(ctx, repo); err != nil {
						t.Fatal(err)
					}
					origin := &models.Scan{ID: uuid.NewString(), UserID: principal.user, RepoID: repo.ID, Status: models.ScanStatusCompleted}
					if err := store.CreateScan(ctx, origin); err != nil {
						t.Fatal(err)
					}
					rem := &models.Remediation{ID: uuid.NewString(), UserID: remOwner, RepoID: repo.ID, OriginScanID: origin.ID, WorkspacePath: "private-remediation-" + uuid.NewString()}
					if err := store.CreateRemediation(ctx, rem); err != nil {
						t.Fatal(err)
					}
					var children []models.Scan
					var jobs []models.FixJob
					ownedJobID := uuid.NewString()
					for _, owner := range []string{principal.user, "foreign", ""} {
						child := models.Scan{ID: uuid.NewString(), UserID: owner, RepoID: repo.ID, OriginScanID: origin.ID, Status: models.ScanStatusCompleted, PreparedWorkspace: "private-scan-" + uuid.NewString()}
						if owner == "foreign" {
							child.FixJobID = ownedJobID
							child.FindingCount = 42
						}
						if err := store.CreateScan(ctx, &child); err != nil {
							t.Fatal(err)
						}
						children = append(children, child)
						job := models.FixJob{ID: uuid.NewString(), UserID: owner, RepoID: repo.ID, ScanID: origin.ID, RemediationID: rem.ID, Status: models.FixJobSucceeded, WorkspacePath: "private-job-" + uuid.NewString()}
						if owner == principal.user {
							job.ID = ownedJobID
						}
						if err := store.EnqueueFixJob(ctx, &job); err != nil {
							t.Fatal(err)
						}
						jobs = append(jobs, job)
					}
					w := request(srv, http.MethodGet, "/api/v1/scans/"+origin.ID+"/lineage", principal.bearer, nil)
					if w.Code != http.StatusOK {
						t.Fatalf("lineage: %d %s", w.Code, w.Body.String())
					}
					var result struct {
						Data struct {
							Origin      models.Scan
							Children    []models.Scan
							Remediation *models.Remediation
							Agents      []models.FixJob
						}
					}
					mustJSON(t, w.Body.Bytes(), &result)
					remVisible := remOwner == principal.user || (remOwner == "" && principal.admin)
					if (result.Data.Remediation != nil) != remVisible || strings.Contains(w.Body.String(), rem.WorkspacePath) != remVisible {
						t.Errorf("remediation visibility=%v: %s", remVisible, w.Body.String())
					}
					if result.Data.Origin.ID != origin.ID {
						t.Errorf("origin changed: %+v", result.Data.Origin)
					}
					wantChildren, wantJobs := 0, 0
					for _, child := range children {
						allowed := child.UserID == principal.user || principal.admin
						if allowed {
							wantChildren++
						}
						if strings.Contains(w.Body.String(), child.PreparedWorkspace) != allowed || strings.Contains(w.Body.String(), child.ID) != allowed {
							t.Errorf("child owner=%q visibility=%v: %s", child.UserID, allowed, w.Body.String())
						}
					}
					for _, job := range jobs {
						allowed := job.UserID == principal.user || (job.UserID == "" && principal.admin && remVisible)
						if allowed {
							wantJobs++
						}
						if strings.Contains(w.Body.String(), job.ID) != allowed || strings.Contains(w.Body.String(), job.WorkspacePath) != allowed {
							t.Errorf("job owner=%q visibility=%v: %s", job.UserID, allowed, w.Body.String())
						}
					}
					if len(result.Data.Children) != wantChildren || len(result.Data.Agents) != wantJobs {
						t.Errorf("wanted %d children/%d jobs, got %d/%d", wantChildren, wantJobs, len(result.Data.Children), len(result.Data.Agents))
					}
				})
			}
		})
	}
	// An owned child must not grant access to a missing or inaccessible origin.
	for _, owner := range []string{"foreign", ""} {
		repo := &models.Repo{ID: uuid.NewString(), UserID: "caller", Name: "origin-link", SourceType: models.SourceTypeLocal}
		if err := store.CreateRepo(ctx, repo); err != nil {
			t.Fatal(err)
		}
		origin := &models.Scan{ID: uuid.NewString(), UserID: owner, RepoID: repo.ID, Status: models.ScanStatusCompleted, PreparedWorkspace: "private-origin-" + uuid.NewString()}
		if err := store.CreateScan(ctx, origin); err != nil {
			t.Fatal(err)
		}
		child := &models.Scan{ID: uuid.NewString(), UserID: "caller", RepoID: repo.ID, OriginScanID: origin.ID, Status: models.ScanStatusCompleted}
		if err := store.CreateScan(ctx, child); err != nil {
			t.Fatal(err)
		}
		for _, missing := range []bool{false, true} {
			if missing {
				child.ID = uuid.NewString()
				child.OriginScanID = uuid.NewString()
				if err := store.CreateScan(ctx, child); err != nil {
					t.Fatal(err)
				}
			}
			for _, bearer := range []string{pair.AccessToken, userToken, adminJWT} {
				w := request(srv, http.MethodGet, "/api/v1/scans/"+child.ID+"/lineage", bearer, nil)
				if w.Code != http.StatusOK {
					t.Fatalf("origin fallback: %d %s", w.Code, w.Body.String())
				}
				var result struct{ Data struct{ Origin models.Scan } }
				mustJSON(t, w.Body.Bytes(), &result)
				allowed := bearer == adminJWT && !missing
				wantOrigin := child.ID
				if allowed {
					wantOrigin = origin.ID
				}
				if result.Data.Origin.ID != wantOrigin || strings.Contains(w.Body.String(), origin.PreparedWorkspace) != allowed {
					t.Errorf("origin visibility missing=%v: %s", missing, w.Body.String())
				}
			}
		}
	}
}

func TestScanLineageRelatedLoadFailures(t *testing.T) {
	srv, store, jwt := newTestServer(t)
	ctx := context.Background()
	claims, err := auth.ValidateToken(jwt)
	if err != nil {
		t.Fatal(err)
	}
	repo := &models.Repo{ID: uuid.NewString(), UserID: claims.UserID, Name: "load-failure", SourceType: models.SourceTypeLocal}
	if err := store.CreateRepo(ctx, repo); err != nil {
		t.Fatal(err)
	}
	origin := &models.Scan{ID: uuid.NewString(), UserID: claims.UserID, RepoID: repo.ID, Status: models.ScanStatusCompleted}
	if err := store.CreateScan(ctx, origin); err != nil {
		t.Fatal(err)
	}
	child := &models.Scan{ID: uuid.NewString(), UserID: claims.UserID, RepoID: repo.ID, OriginScanID: origin.ID, Status: models.ScanStatusCompleted}
	if err := store.CreateScan(ctx, child); err != nil {
		t.Fatal(err)
	}
	rem := &models.Remediation{ID: uuid.NewString(), UserID: claims.UserID, RepoID: repo.ID, OriginScanID: origin.ID}
	if err := store.CreateRemediation(ctx, rem); err != nil {
		t.Fatal(err)
	}
	handler := routes.DefaultHandler
	t.Cleanup(func() { handler.Store = store })
	for _, failure := range []string{"origin", "children", "remediation", "jobs", "repo-jobs"} {
		t.Run(failure, func(t *testing.T) {
			handler.Store = lineageFailureStore{Store: store, failure: failure, originID: origin.ID}
			w := request(srv, http.MethodGet, "/api/v1/scans/"+child.ID+"/lineage", jwt, nil)
			if w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), "private database diagnostic") {
				t.Errorf("failed lineage read became partial success or disclosed diagnostics: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

type lineageFailureStore struct {
	db.Store
	failure, originID string
}

func (s lineageFailureStore) GetScanByID(ctx context.Context, id string) (*models.Scan, error) {
	if s.failure == "origin" && id == s.originID {
		return nil, errors.New("private database diagnostic")
	}
	return s.Store.GetScanByID(ctx, id)
}
func (s lineageFailureStore) ListScansByOrigin(ctx context.Context, id string) ([]models.Scan, error) {
	if s.failure == "children" {
		return nil, errors.New("private database diagnostic")
	}
	return s.Store.ListScansByOrigin(ctx, id)
}
func (s lineageFailureStore) GetLatestRemediationByOrigin(ctx context.Context, id string) (*models.Remediation, error) {
	if s.failure == "remediation" {
		return nil, errors.New("private database diagnostic")
	}
	return s.Store.GetLatestRemediationByOrigin(ctx, id)
}
func (s lineageFailureStore) ListFixJobsByRemediation(ctx context.Context, id string) ([]models.FixJob, error) {
	if s.failure == "jobs" {
		return nil, errors.New("private database diagnostic")
	}
	return s.Store.ListFixJobsByRemediation(ctx, id)
}
func (s lineageFailureStore) ListFixJobsByUser(ctx context.Context, user, id string) ([]models.FixJob, error) {
	if s.failure == "repo-jobs" {
		return nil, errors.New("private database diagnostic")
	}
	return s.Store.ListFixJobsByUser(ctx, user, id)
}
