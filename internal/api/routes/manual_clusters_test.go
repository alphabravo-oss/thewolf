package routes_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/api/routes"
	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/google/uuid"
)

func TestManualClusterChangesReconcileFindingMembership(t *testing.T) {
	e := setupTestEnv(t)
	defer e.Store.Close()
	ctx := t.Context()
	repo := e.createRepo(t)
	scan := &models.Scan{ID: uuid.NewString(), UserID: e.UserID, RepoID: repo, Status: models.ScanStatusRunning}
	if err := e.Store.CreateScan(ctx, scan); err != nil {
		t.Fatal(err)
	}
	var rows []models.Finding
	for _, tool := range []string{"semgrep", "gosec"} {
		f := models.Finding{ID: uuid.NewString(), ScanID: scan.ID, RepoID: repo, ToolName: tool, Category: models.CategorySAST, RuleID: "rule", FineCategory: "sql-injection", FilePath: "app.go", LineStart: 1, Title: tool + " evidence", Status: models.StatusOpen, Severity: models.SeverityHigh}
		if err := e.Store.CreateFinding(ctx, &f); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, f)
	}
	primary := rows[0]
	primary.CorroboratedBy = []string{"semgrep", "gosec"}
	primary.Confidence = "medium"
	if err := e.Store.FinalizeFindingClusters(ctx, scan.ID, "", []models.Finding{primary}); err != nil {
		t.Fatal(err)
	}
	scan.Status = models.ScanStatusCompleted
	if err := e.Store.UpdateScan(ctx, scan); err != nil {
		t.Fatal(err)
	}
	if err := routes.DualWriteVulnerabilities(ctx, routes.DefaultHandler, scan); err != nil {
		t.Fatal(err)
	}
	vulns, err := e.Store.ListVulnerabilitiesByScan(ctx, scan.ID)
	if err != nil || len(vulns) != 1 {
		t.Fatalf("seed clusters: %+v %v", vulns, err)
	}
	for _, body := range []string{
		`{"finding_ids":["` + rows[1].ID + `"],"unexpected":true}`,
		`{"finding_ids":["` + rows[1].ID + `"]} {}`,
		`{"finding_ids":["` + rows[1].ID + `","unknown"]}`,
		`{"finding_ids":["` + rows[1].ID + `","` + rows[1].ID + `"]}`,
	} {
		req := httptest.NewRequest(http.MethodPost, "/api/vulnerabilities/"+vulns[0].ID+"/split", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+e.Token)
		w := httptest.NewRecorder()
		e.Router.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("invalid split: %d %s", w.Code, w.Body.String())
		}
	}
	scan.Status = models.ScanStatusRunning
	if err := e.Store.UpdateScan(ctx, scan); err != nil {
		t.Fatal(err)
	}
	active := e.doRequest(http.MethodPost, "/api/vulnerabilities/"+vulns[0].ID+"/split", map[string]any{"finding_ids": []string{rows[1].ID}})
	if active.Code != http.StatusConflict {
		t.Fatalf("active scan split: %d %s", active.Code, active.Body.String())
	}
	scan.Status = models.ScanStatusCompleted
	if err := e.Store.UpdateScan(ctx, scan); err != nil {
		t.Fatal(err)
	}
	response := e.doRequest(http.MethodPost, "/api/vulnerabilities/"+vulns[0].ID+"/split", map[string]any{"finding_ids": []string{rows[1].ID}})
	if response.Code != http.StatusOK {
		t.Fatalf("split: %d %s", response.Code, response.Body.String())
	}
	var split struct {
		Data models.Vulnerability `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &split); err != nil {
		t.Fatal(err)
	}
	if split.Data.MergeReason != "Split manually by a user." {
		t.Fatalf("manual split reason: %q", split.Data.MergeReason)
	}
	findings, err := e.Store.ListFindingsByScan(ctx, scan.ID)
	if err != nil || len(findings) != 2 {
		t.Fatalf("split left finding membership unchanged: %+v %v", findings, err)
	}
	for _, f := range findings {
		if f.ClusterID != f.ID || len(f.CorroboratedBy) != 1 || f.Confidence == "medium" || f.CompositeScore <= 0 {
			t.Fatalf("split retained old consensus: %+v", f)
		}
	}
	// Replaying automatic persistence must not undo a deliberate split.
	if err := routes.DualWriteVulnerabilities(ctx, routes.DefaultHandler, scan); err != nil {
		t.Fatal(err)
	}
	after, err := e.Store.ListVulnerabilitiesByScan(ctx, scan.ID)
	if err != nil || len(after) != 2 {
		t.Fatalf("replay changed manual split: %+v %v", after, err)
	}
	response = e.doRequest(http.MethodPost, "/api/vulnerabilities/"+vulns[0].ID+"/merge", map[string]any{"vulnerability_id": split.Data.ID})
	if response.Code != http.StatusOK {
		t.Fatalf("merge: %d %s", response.Code, response.Body.String())
	}
	findings, err = e.Store.ListFindingsByScan(ctx, scan.ID)
	if err != nil || len(findings) != 1 {
		t.Fatalf("merge left finding membership unchanged: %+v %v", findings, err)
	}
	for _, old := range rows {
		f, err := e.Store.GetFindingByID(ctx, old.ID)
		if err != nil || f.Title != old.Title {
			t.Fatalf("manual operation lost native ID/evidence: %+v %v", f, err)
		}
	}
	if err := routes.DualWriteVulnerabilities(ctx, routes.DefaultHandler, scan); err != nil {
		t.Fatal(err)
	}
	after, err = e.Store.ListVulnerabilitiesByScan(ctx, scan.ID)
	if err != nil || len(after) != 1 || after[0].EvidenceCount != 2 {
		t.Fatalf("replay recreated merged cluster: %+v %v", after, err)
	}
	// Evidence read errors must not masquerade as an empty successful response.
	handler := routes.DefaultHandler
	oldStore := handler.Store
	defer func() { handler.Store = oldStore }()
	handler.Store = failingManualEvidenceStore{Store: oldStore}
	for _, path := range []string{"/api/vulnerabilities/" + vulns[0].ID, "/api/vulnerabilities/" + vulns[0].ID + "/evidence"} {
		w := e.doRequest(http.MethodGet, path, nil)
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("evidence read failure: %d %s", w.Code, w.Body.String())
		}
	}

}

type failingManualEvidenceStore struct{ db.Store }

func (s failingManualEvidenceStore) ListEvidenceByVulnerability(context.Context, string) ([]models.VulnerabilityEvidence, error) {
	return nil, errors.New("fixture evidence failure")
}
