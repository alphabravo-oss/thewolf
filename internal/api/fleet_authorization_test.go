package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/auth/apikey"
	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestFleetAuthorizationDoesNotElevateAPITokens(t *testing.T) {
	srv, store, adminJWT := newTestServer(t)
	ctx := context.Background()
	admin, err := auth.ValidateToken(adminJWT)
	if err != nil {
		t.Fatal(err)
	}
	other := &models.User{ID: uuid.NewString(), Email: "fleet-other@example.com", PasswordHash: "unused", Role: models.RoleUser}
	if err := store.CreateUser(ctx, other); err != nil {
		t.Fatal(err)
	}
	otherPair, err := auth.GenerateToken(other.ID, other.Email)
	if err != nil {
		t.Fatal(err)
	}
	readScopes := []string{apikey.ScopeReadFindings, apikey.ScopeReadRepos, apikey.ScopeReadScans}
	adminRead := createToken(t, srv, adminJWT, readScopes)
	adminWrite := createToken(t, srv, adminJWT, []string{apikey.ScopeWriteFindings})
	adminSuper := createToken(t, srv, adminJWT, []string{apikey.ScopeAdmin})
	otherRead := createToken(t, srv, otherPair.AccessToken, readScopes)
	var findings, vulns, collections []string
	for _, userID := range []string{admin.UserID, other.ID} {
		repo := &models.Repo{ID: uuid.NewString(), UserID: userID, Name: userID, SourceType: models.SourceTypeLocal, SourcePath: t.TempDir(), DefaultBranch: "main"}
		if err := store.CreateRepo(ctx, repo); err != nil {
			t.Fatal(err)
		}
		col := &models.Collection{ID: uuid.NewString(), UserID: userID, Name: userID}
		if err := store.CreateCollection(ctx, col); err != nil {
			t.Fatal(err)
		}
		if err := store.AddRepoToCollection(ctx, col.ID, repo.ID); err != nil {
			t.Fatal(err)
		}
		collections = append(collections, col.ID)
		now := time.Now().UTC()
		scan := &models.Scan{ID: uuid.NewString(), UserID: userID, RepoID: repo.ID, Branch: "main", Status: models.ScanStatusCompleted, CreatedAt: now, UpdatedAt: now, CompletedAt: &now, ToolsCompleted: `["test"]`}
		if err := store.CreateScan(ctx, scan); err != nil {
			t.Fatal(err)
		}
		f := &models.Finding{ID: uuid.NewString(), ScanID: scan.ID, RepoID: repo.ID, Title: userID, RuleID: userID, ToolName: "test", FilePath: "app.go", Category: models.CategorySAST, Severity: models.SeverityHigh, Status: models.StatusOpen, CreatedAt: now, UpdatedAt: now}
		if err := store.CreateFinding(ctx, f); err != nil {
			t.Fatal(err)
		}
		if err := store.ReconcileVulnerabilities(ctx, scan.ID); err != nil {
			t.Fatal(err)
		}
		vs, err := store.ListVulnerabilitiesByScan(ctx, scan.ID)
		if err != nil || len(vs) != 1 {
			t.Fatalf("canonical fixture: %v, %v", vs, err)
		}
		findings = append(findings, f.ID)
		vulns = append(vulns, vs[0].ID)
	}

	for _, principal := range []struct {
		name, bearer string
		owner        int
		fleet        bool
	}{
		{"admin session", adminJWT, 0, true},
		{"admin read token", adminRead, 0, false},
		{"admin super-scope token", adminSuper, 0, false},
		{"user session", otherPair.AccessToken, 1, false},
		{"user read token", otherRead, 1, false},
	} {
		t.Run(principal.name, func(t *testing.T) {
			mine, foreign := admin.UserID, other.ID
			if principal.owner == 1 {
				mine, foreign = foreign, mine
			}
			for _, path := range []string{"/repos", "/collections", "/scans", "/findings", "/findings/export?format=json", "/findings/aggregate", "/findings/by-repo", "/vulnerabilities", "/fleet/inventory", "/fleet/needs-attention", "/fleet/report"} {
				t.Run(path, func(t *testing.T) {
					w := request(srv, http.MethodGet, "/api/v1"+path, principal.bearer, nil)
					if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), mine) || strings.Contains(w.Body.String(), foreign) != principal.fleet {
						t.Fatalf("fleet=%v: %d %s", principal.fleet, w.Code, w.Body.String())
					}
				})
			}
			for i := range findings {
				status := http.StatusNotFound
				if principal.fleet || i == principal.owner {
					status = http.StatusOK
				}
				if w := request(srv, http.MethodGet, "/api/v1/fleet/report?collection_id="+collections[i], principal.bearer, nil); w.Code != status {
					t.Errorf("collection report: want %d: %d %s", status, w.Code, w.Body.String())
				}
				for _, path := range []string{"/findings/" + findings[i], "/vulnerabilities/" + vulns[i], "/vulnerabilities/" + vulns[i] + "/evidence", "/evidence?vulnerability_id=" + vulns[i]} {
					t.Run(path, func(t *testing.T) {
						w := request(srv, http.MethodGet, "/api/v1"+path, principal.bearer, nil)
						if w.Code != status || (status != http.StatusOK && strings.Contains(w.Body.String(), foreign)) {
							t.Fatalf("want %d: %d %s", status, w.Code, w.Body.String())
						}
					})
				}
			}
			wantRepos := 1
			if principal.fleet {
				wantRepos = 2
			}
			w := request(srv, http.MethodGet, "/api/v1/fleet/posture", principal.bearer, nil)
			var posture struct {
				Data db.FleetPostureResult `json:"data"`
			}
			mustJSON(t, w.Body.Bytes(), &posture)
			if w.Code != http.StatusOK || posture.Data.RepoCount != wantRepos {
				t.Errorf("posture: want %d repos: %d %s", wantRepos, w.Code, w.Body.String())
			}
			for _, path := range []string{"/findings/trends", "/findings/trends/export?format=json"} {
				w := request(srv, http.MethodGet, "/api/v1"+path, principal.bearer, nil)
				var days []struct {
					Repos int `json:"repos"`
				}
				if strings.Contains(path, "/export") {
					mustJSON(t, w.Body.Bytes(), &days)
				} else {
					var envelope struct {
						Data json.RawMessage `json:"data"`
					}
					mustJSON(t, w.Body.Bytes(), &envelope)
					mustJSON(t, envelope.Data, &days)
				}
				if w.Code != http.StatusOK || len(days) == 0 || days[len(days)-1].Repos != wantRepos {
					t.Errorf("trends: want %d repos: %d %s", wantRepos, w.Code, w.Body.String())
				}
			}
		})
	}
	// A write-findings token must not mutate foreign findings, including via
	// bulk requests. Reject before parsing cluster mutations or changing state.
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{http.MethodPut, "/findings/" + findings[1] + "/status", `{"status":"false_positive"}`, http.StatusNotFound},
		{http.MethodPost, "/findings/bulk", `{"ids":["` + findings[0] + `","` + findings[1] + `"],"status":"false_positive"}`, http.StatusForbidden},
		{http.MethodPost, "/vulnerabilities/" + vulns[1] + "/split", `{"finding_ids":["` + findings[1] + `"]}`, http.StatusNotFound},
		{http.MethodPost, "/vulnerabilities/" + vulns[1] + "/merge", `{"vulnerability_id":"` + vulns[0] + `"}`, http.StatusNotFound},
	} {
		w := request(srv, tc.method, "/api/v1"+tc.path, adminWrite, []byte(tc.body))
		if w.Code != tc.status {
			t.Errorf("%s: want %d: %d %s", tc.path, tc.status, w.Code, w.Body.String())
		}
	}
	for _, id := range findings {
		f, err := store.GetFindingByID(ctx, id)
		if err != nil || f.Status != models.StatusOpen {
			t.Fatalf("denied mutation changed finding: %+v, %v", f, err)
		}
	}
	// Explicit token scope remains useful on the token owner's resources.
	w := request(srv, http.MethodPut, "/api/v1/findings/"+findings[0]+"/status", adminRead, []byte(`{"status":"false_positive"}`))
	if w.Code != http.StatusForbidden {
		t.Fatalf("read-only token update: %d %s", w.Code, w.Body.String())
	}
	w = request(srv, http.MethodPut, "/api/v1/findings/"+findings[0]+"/status", adminWrite, []byte(`{"status":"false_positive"}`))
	if w.Code != http.StatusOK {
		t.Fatalf("owner update: %d %s", w.Code, w.Body.String())
	}
	w = request(srv, http.MethodPut, "/api/v1/findings/"+findings[1]+"/status", adminJWT, []byte(`{"status":"false_positive"}`))
	if w.Code != http.StatusOK {
		t.Fatalf("administrator session update: %d %s", w.Code, w.Body.String())
	}
	u, err := store.GetUserByID(ctx, admin.UserID)
	if err != nil {
		t.Fatal(err)
	}
	u.Role = models.RoleUser
	if err := store.UpdateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	w = request(srv, http.MethodGet, "/api/v1/repos", adminJWT, nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), admin.UserID) || strings.Contains(w.Body.String(), other.ID) {
		t.Fatalf("demoted session list: %d %s", w.Code, w.Body.String())
	}
	w = request(srv, http.MethodGet, "/api/v1/findings/"+findings[1], adminJWT, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("demoted session detail: %d %s", w.Code, w.Body.String())
	}
}
