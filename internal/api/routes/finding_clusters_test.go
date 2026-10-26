package routes_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/google/uuid"
)

func TestFindingClusterAPIKeepsEvidenceAndOwnership(t *testing.T) {
	e := setupTestEnv(t)
	defer e.Store.Close()
	ctx := t.Context()
	u, err := e.Store.GetUserByID(ctx, e.UserID)
	if err != nil {
		t.Fatal(err)
	}
	u.Role = models.RoleUser
	if err := e.Store.UpdateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	for _, own := range []bool{true, false} {
		owner := e.UserID
		if !own {
			owner = uuid.NewString()
			if err := e.Store.CreateUser(ctx, &models.User{ID: owner, Email: owner + "@example.test", PasswordHash: "hash"}); err != nil {
				t.Fatal(err)
			}
		}
		repo := &models.Repo{ID: uuid.NewString(), UserID: owner, Name: "cluster", SourceType: models.SourceTypeLocal, SourcePath: t.TempDir()}
		if err := e.Store.CreateRepo(ctx, repo); err != nil {
			t.Fatal(err)
		}
		scan := &models.Scan{ID: uuid.NewString(), UserID: owner, RepoID: repo.ID, Status: models.ScanStatusRunning}
		if err := e.Store.CreateScan(ctx, scan); err != nil {
			t.Fatal(err)
		}
		var rows []models.Finding
		for _, tool := range []string{"trivy", "grype"} {
			f := models.Finding{ID: uuid.NewString(), ScanID: scan.ID, RepoID: repo.ID, ToolName: tool, Category: models.CategorySCA, PackageName: "alpha", PackageVersion: "1", PackageEcosystem: "npm", RuleID: "CVE-2026-1001", AdvisoryID: "CVE-2026-1001", FilePath: "package-lock.json", Title: tool + " evidence", Severity: models.SeverityHigh, Status: models.StatusOpen}
			if err := e.Store.CreateFinding(ctx, &f); err != nil {
				t.Fatal(err)
			}
			rows = append(rows, f)
		}
		primary := rows[0]
		primary.CorroboratedBy = []string{"trivy", "grype"}
		primary.Confidence = "medium"
		if err := e.Store.FinalizeFindingClusters(ctx, scan.ID, "", []models.Finding{primary}); err != nil {
			t.Fatal(err)
		}
		response := e.doRequest(http.MethodGet, "/api/scans/"+scan.ID+"/findings", nil)
		if !own {
			if response.Code != http.StatusNotFound {
				t.Fatalf("foreign cluster list: %d %s", response.Code, response.Body.String())
			}
		} else {
			var payload struct {
				Data []models.Finding `json:"data"`
				Meta struct {
					Total int `json:"total"`
				} `json:"meta"`
			}
			if response.Code != http.StatusOK {
				t.Fatalf("cluster list: %d %s", response.Code, response.Body.String())
			}
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if len(payload.Data) != 1 || payload.Meta.Total != 1 || payload.Data[0].ID != primary.ID || len(payload.Data[0].CorroboratedBy) != 2 {
				t.Fatalf("list disagrees with cluster: %s", response.Body.String())
			}
		}
		if own {
			for _, tool := range []string{"trivy", "grype"} {
				response := e.doRequest(http.MethodGet, "/api/scans/"+scan.ID+"/findings?tool="+tool, nil)
				var payload struct {
					Data []models.Finding `json:"data"`
				}
				if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
					t.Fatal(err)
				}
				if response.Code != http.StatusOK || len(payload.Data) != 1 || payload.Data[0].ID != primary.ID {
					t.Fatalf("contributor tool filter lost cluster: %s", response.Body.String())
				}
			}
		}
		for _, member := range rows {
			response := e.doRequest(http.MethodGet, "/api/findings/"+member.ID, nil)
			if !own {
				if response.Code != http.StatusNotFound {
					t.Fatalf("foreign evidence leaked: %d %s", response.Code, response.Body.String())
				}
				continue
			}
			var payload struct {
				Data struct {
					models.Finding
					Evidence []models.Finding `json:"evidence"`
				} `json:"data"`
			}
			if response.Code != http.StatusOK {
				t.Fatalf("evidence: %d %s", response.Code, response.Body.String())
			}
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Data.ID != member.ID || payload.Data.ClusterID != primary.ID || len(payload.Data.Evidence) != 2 {
				t.Fatalf("member link/evidence lost: %s", response.Body.String())
			}
		}
	}
}
