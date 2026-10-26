package api_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/artifacts"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/auth/apikey"
	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestLegacyOwnerlessResourcesRequireAdministratorSession(t *testing.T) {
	srv, store, adminJWT := newTestServer(t)
	ctx := context.Background()
	// A legacy empty-ID principal permits real FK-backed ownerless rows without
	// disabling constraints or mocking the authorization reads.
	for _, u := range []*models.User{
		{ID: "", Email: "legacy-unused@example.com", PasswordHash: "unused"},
		{ID: "regular", Email: "regular@example.com", PasswordHash: "unused"},
	} {
		if err := store.CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	pair, err := auth.GenerateToken("regular", "regular@example.com")
	if err != nil {
		t.Fatal(err)
	}
	adminToken := createToken(t, srv, adminJWT, []string{apikey.ScopeAdmin})
	repo := &models.Repo{ID: uuid.NewString(), Name: "legacy-private", SourceType: models.SourceTypeLocal, SourcePath: t.TempDir(), DefaultBranch: "main"}
	if err := store.CreateRepo(ctx, repo); err != nil {
		t.Fatal(err)
	}
	col := &models.Collection{ID: uuid.NewString(), Name: "legacy-private"}
	if err := store.CreateCollection(ctx, col); err != nil {
		t.Fatal(err)
	}
	if err := store.AddRepoToCollection(ctx, col.ID, repo.ID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	scan := &models.Scan{ID: uuid.NewString(), RepoID: repo.ID, Status: models.ScanStatusCompleted, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateScan(ctx, scan); err != nil {
		t.Fatal(err)
	}
	f := &models.Finding{ID: uuid.NewString(), ScanID: scan.ID, RepoID: repo.ID, ToolName: "test", RuleID: "R1", FilePath: "app.go", Title: "legacy-private", Category: models.CategorySAST, Severity: models.SeverityHigh, Status: models.StatusOpen, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateFinding(ctx, f); err != nil {
		t.Fatal(err)
	}
	if err := store.ReconcileVulnerabilities(ctx, scan.ID); err != nil {
		t.Fatal(err)
	}
	vs, err := store.ListVulnerabilitiesByScan(ctx, scan.ID)
	if err != nil || len(vs) != 1 {
		t.Fatalf("vulnerabilities: %v, %v", vs, err)
	}
	job := &models.FixJob{ID: uuid.NewString(), RepoID: repo.ID, ScanID: scan.ID, Engine: "auto"}
	if err := store.EnqueueFixJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	rem := &models.Remediation{ID: uuid.NewString(), RepoID: repo.ID, OriginScanID: scan.ID, State: models.RemediationOpen}
	if err := store.CreateRemediation(ctx, rem); err != nil {
		t.Fatal(err)
	}
	secret := &models.Secret{ID: uuid.NewString(), KeyType: "github_token", KeyName: "legacy-private", EncryptedValue: "unused"}
	if err := store.CreateSecret(ctx, secret); err != nil {
		t.Fatal(err)
	}
	previous := artifacts.Global
	if err := artifacts.Init(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { artifacts.Global = previous })
	a, err := artifacts.Publish(ctx, scan.ID, "test.log", strings.NewReader("legacy-private raw evidence"))
	if err != nil {
		t.Fatal(err)
	}
	a.ArtifactType = models.ArtifactLog
	if err := store.CreateScanArtifact(ctx, a); err != nil {
		t.Fatal(err)
	}
	base := "/scans/" + scan.ID
	for _, path := range []struct {
		path   string
		denied int
	}{
		{"/repos/" + repo.ID, http.StatusNotFound},
		{"/collections/" + col.ID, http.StatusForbidden},
		{base, http.StatusNotFound},
		{base + "/findings", http.StatusNotFound},
		{base + "/tools/test/output", http.StatusNotFound},
		{base + "/artifacts/" + a.ID + "/download", http.StatusNotFound},
		{"/findings/export?format=json&scan_id=" + scan.ID, http.StatusNotFound},
		{"/findings/" + f.ID, http.StatusNotFound},
		{"/vulnerabilities/" + vs[0].ID, http.StatusNotFound},
		{"/evidence?vulnerability_id=" + vs[0].ID, http.StatusNotFound},
		{"/fixes/" + job.ID, http.StatusNotFound},
	} {
		t.Run(path.path, func(t *testing.T) {
			for _, bearer := range []string{pair.AccessToken, adminToken} {
				w := request(srv, http.MethodGet, "/api/v1"+path.path, bearer, nil)
				if w.Code != path.denied || strings.Contains(w.Body.String(), "legacy-private") {
					t.Errorf("denied read: want %d: %d %s", path.denied, w.Code, w.Body.String())
				}
			}
			w := request(srv, http.MethodGet, "/api/v1"+path.path, adminJWT, nil)
			if w.Code != http.StatusOK {
				t.Errorf("administrator legacy access: %d %s", w.Code, w.Body.String())
			}
		})
	}
	for _, tc := range []struct {
		method, path, body string
		denied             int
	}{
		{http.MethodPut, "/repos/" + repo.ID, `{"name":"changed"}`, http.StatusNotFound},
		{http.MethodPut, "/collections/" + col.ID, `{"name":"changed"}`, http.StatusForbidden},
		{http.MethodPut, "/findings/" + f.ID + "/status", `{"status":"false_positive"}`, http.StatusNotFound},
		{http.MethodDelete, "/fixes/" + job.ID, "", http.StatusNotFound},
		{http.MethodPost, "/remediations/" + rem.ID + "/accept", `{}`, http.StatusNotFound},
		{http.MethodDelete, "/config/secrets/" + secret.ID, "", http.StatusNotFound},
	} {
		t.Run(tc.path, func(t *testing.T) {
			for _, bearer := range []string{pair.AccessToken, adminToken} {
				w := request(srv, tc.method, "/api/v1"+tc.path, bearer, []byte(tc.body))
				if w.Code != tc.denied {
					t.Errorf("denied mutation: want %d: %d %s", tc.denied, w.Code, w.Body.String())
				}
			}
		})
	}
	gotRepo, err := store.GetRepoByID(ctx, repo.ID)
	if err != nil || gotRepo.Name != repo.Name {
		t.Fatalf("repo changed: %+v %v", gotRepo, err)
	}
	gotCol, err := store.GetCollectionByID(ctx, col.ID)
	if err != nil || gotCol.Name != col.Name {
		t.Fatalf("collection changed: %+v %v", gotCol, err)
	}
	gotFinding, err := store.GetFindingByID(ctx, f.ID)
	if err != nil || gotFinding.Status != models.StatusOpen {
		t.Fatalf("finding changed: %+v %v", gotFinding, err)
	}
	gotJob, err := store.GetFixJobByID(ctx, job.ID)
	if err != nil || gotJob.Status != models.FixJobQueued {
		t.Fatalf("job changed: %+v %v", gotJob, err)
	}
	gotRem, err := store.GetRemediationByID(ctx, rem.ID)
	if err != nil || gotRem.State != models.RemediationOpen {
		t.Fatalf("remediation changed: %+v %v", gotRem, err)
	}
	if _, err := store.GetSecretByID(ctx, secret.ID); err != nil {
		t.Fatal("secret was deleted", err)
	}
	// Administrators can still recover legacy records without assigning them
	// to the first ordinary caller or manufacturing an ownership migration.
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodPut, "/repos/" + repo.ID, `{"name":"recovered"}`},
		{http.MethodDelete, "/fixes/" + job.ID, ""},
		{http.MethodPost, "/remediations/" + rem.ID + "/accept", `{}`},
		{http.MethodDelete, "/config/secrets/" + secret.ID, ""},
	} {
		w := request(srv, tc.method, "/api/v1"+tc.path, adminJWT, []byte(tc.body))
		// A legacy remediation without a branch is reachable (authorized) but has nothing to hand off.
		if w.Code != http.StatusOK && w.Code != http.StatusNoContent && !strings.Contains(w.Body.String(), `"nothing_to_handoff"`) {
			t.Errorf("admin recovery %s: %d %s", tc.path, w.Code, w.Body.String())
		}
	}
}
