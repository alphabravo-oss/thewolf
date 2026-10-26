package routes_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/api/routes"
	"github.com/alphabravocompany/thewolf/internal/artifacts"
	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/google/uuid"
)

func TestManualClustersRefreshScanViews(t *testing.T) {
	previousArtifacts := artifacts.Global
	if err := artifacts.Init(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { artifacts.Global = previousArtifacts })
	e := setupTestEnv(t)
	defer e.Store.Close()
	ctx := t.Context()
	repo := e.createRepo(t)
	storedRepo, err := e.Store.GetRepoByID(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	storedRepo.SourcePath = t.TempDir()
	if err := e.Store.UpdateRepo(ctx, storedRepo); err != nil {
		t.Fatal(err)
	}
	scan := &models.Scan{ID: uuid.NewString(), UserID: e.UserID, RepoID: repo, Status: models.ScanStatusRunning, ToolsSelected: `["alpha","beta"]`, ToolsCompleted: `["alpha","beta"]`, ToolsFailed: `[]`, FindingCount: 1}
	if err := e.Store.CreateScan(ctx, scan); err != nil {
		t.Fatal(err)
	}
	var rows []models.Finding
	for _, tool := range []string{"alpha", "beta"} {
		f := models.Finding{ID: uuid.NewString(), ScanID: scan.ID, RepoID: repo, ToolName: tool, RuleID: "one", Category: models.CategorySAST, Severity: models.SeverityHigh, Title: tool, FilePath: "app.go", LineStart: 1, Status: models.StatusOpen}
		if err := e.Store.CreateFinding(ctx, &f); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, f)
	}
	primary := rows[0]
	primary.CorroboratedBy = []string{"alpha", "beta"}
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
	gate := e.doRequest(http.MethodGet, "/api/scans/"+scan.ID+"/gate", nil)
	if gate.Code != http.StatusOK {
		t.Fatalf("gate: %d %s", gate.Code, gate.Body.String())
	}
	vulns, err := e.Store.ListVulnerabilitiesByScan(ctx, scan.ID)
	if err != nil || len(vulns) != 1 {
		t.Fatalf("canonical: %+v %v", vulns, err)
	}
	split := e.doRequest(http.MethodPost, "/api/vulnerabilities/"+vulns[0].ID+"/split", map[string]any{"finding_ids": []string{rows[1].ID}})
	if split.Code != http.StatusOK {
		t.Fatalf("split: %d %s", split.Code, split.Body.String())
	}
	header := e.doRequest(http.MethodGet, "/api/scans/"+scan.ID, nil)
	var env struct {
		Data models.Scan `json:"data"`
	}
	if err := json.Unmarshal(header.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if header.Code != http.StatusOK || env.Data.FindingCount != 2 {
		t.Fatalf("stale scan header: %d %s", header.Code, header.Body.String())
	}
	result := e.doRequest(http.MethodGet, "/api/scans/"+scan.ID+"/result", nil)
	var summary struct {
		Data struct {
			Totals struct {
				Findings int `json:"findings"`
			} `json:"totals"`
			Gate models.QualityGateResult `json:"quality_gate"`
		} `json:"data"`
	}
	if err := json.Unmarshal(result.Body.Bytes(), &summary); err != nil {
		t.Fatal(err)
	}
	var counts struct {
		Fail int `json:"fail_count"`
	}
	if err := json.Unmarshal([]byte(summary.Data.Gate.SummaryJSON), &counts); err != nil {
		t.Fatal(err)
	}
	if result.Code != http.StatusOK || summary.Data.Totals.Findings != 2 || counts.Fail != 2 {
		t.Fatalf("stale compact gate: %d %s", result.Code, result.Body.String())
	}
	// The gate and compact response must identify the very same finding snapshot.
	if summary.Data.Gate.FindingsRevision != env.Data.FindingsRevision || env.Data.FindingsRevision == 0 {
		t.Fatalf("revision mismatch: gate=%d header=%d", summary.Data.Gate.FindingsRevision, env.Data.FindingsRevision)
	}
	for _, suffix := range []string{"/report", "/sarif"} {
		response := e.doRequest(http.MethodGet, "/api/scans/"+scan.ID+suffix, nil)
		if response.Code != http.StatusOK || response.Header().Get("X-Wolf-Findings-Revision") != strconv.FormatInt(env.Data.FindingsRevision, 10) {
			t.Fatalf("report snapshot: %d %v %s", response.Code, response.Header(), response.Body.String())
		}
	}
	// A later repo-local rule cannot change completed-scan visibility during regrouping.
	if err := os.WriteFile(filepath.Join(storedRepo.SourcePath, ".wolfignore"), []byte("app.go\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var splitEnv struct {
		Data models.Vulnerability `json:"data"`
	}
	if err := json.Unmarshal(split.Body.Bytes(), &splitEnv); err != nil {
		t.Fatal(err)
	}
	merged := e.doRequest(http.MethodPost, "/api/vulnerabilities/"+vulns[0].ID+"/merge", map[string]string{"vulnerability_id": splitEnv.Data.ID})
	if merged.Code != http.StatusOK {
		t.Fatalf("merge: %d %s", merged.Code, merged.Body.String())
	}
	saved, err := e.Store.GetScanByID(ctx, scan.ID)
	if err != nil || saved.FindingCount != 1 {
		t.Fatalf("historical header: %+v %v", saved, err)
	}
	gate = e.doRequest(http.MethodGet, "/api/scans/"+scan.ID+"/gate", nil)
	var visibleGate struct {
		Data struct {
			FindingCount int `json:"finding_count"`
			Evaluation   struct {
				Summary struct {
					Fail int `json:"fail_count"`
				} `json:"summary"`
			} `json:"evaluation"`
		} `json:"data"`
	}
	if err := json.Unmarshal(gate.Body.Bytes(), &visibleGate); err != nil {
		t.Fatal(err)
	}
	if gate.Code != http.StatusOK || visibleGate.Data.FindingCount != 1 || visibleGate.Data.Evaluation.Summary.Fail != 1 {
		t.Fatalf("suppressed gate disagrees: %d %s", gate.Code, gate.Body.String())
	}
	sarif := e.doRequest(http.MethodGet, "/api/scans/"+scan.ID+"/sarif", nil)
	var audit struct {
		Runs []struct {
			Results []struct {
				Suppressions []json.RawMessage `json:"suppressions"`
			} `json:"results"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(sarif.Body.Bytes(), &audit); err != nil {
		t.Fatal(err)
	}
	if sarif.Code != http.StatusOK || len(audit.Runs) != 1 || len(audit.Runs[0].Results) != 1 || len(audit.Runs[0].Results[0].Suppressions) != 0 {
		t.Fatalf("SARIF suppression disagrees: %d %s", sarif.Code, sarif.Body.String())
	}
	handler := routes.DefaultHandler
	original := handler.Store
	defer func() { handler.Store = original }()
	handler.Store = mutateDuringGateStore{Store: original, finding: rows[0]}
	for _, suffix := range []string{"/gate", "/result"} {
		w := e.doRequest(http.MethodGet, "/api/scans/"+scan.ID+suffix, nil)
		if w.Code != http.StatusConflict {
			t.Fatalf("concurrent gate save: %d %s", w.Code, w.Body.String())
		}
	}

	for _, failure := range []string{"snapshot", "policy", "runs"} {
		handler.Store = failingScanViewStore{Store: original, failure: failure}
		if failure == "snapshot" {
			comparison := e.doRequest(http.MethodGet, "/api/scans/"+scan.ID+"/diff?baseline_scan_id="+scan.ID, nil)
			if comparison.Code != http.StatusInternalServerError || strings.Contains(comparison.Body.String(), "fixture snapshot failure") {
				t.Fatalf("comparison snapshot error must be private and remain a server failure: %d %s", comparison.Code, comparison.Body.String())
			}
		}
		w := e.doRequest(http.MethodGet, "/api/scans/"+scan.ID+"/result", nil)
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("%s failure became success: %d %s", failure, w.Code, w.Body.String())
		}
	}

}

type mutateDuringGateStore struct {
	db.Store
	finding models.Finding
}

func (s mutateDuringGateStore) UpsertQualityGateResult(ctx context.Context, result *models.QualityGateResult) error {
	if err := s.Store.UpdateFindingStatus(ctx, s.finding.ID, models.StatusOpen); err != nil {
		return err
	}
	return s.Store.UpsertQualityGateResult(ctx, result)
}

type failingScanViewStore struct {
	db.Store
	failure string
}

func (s failingScanViewStore) GetScanFindingSnapshot(ctx context.Context, id string) (*models.Scan, []models.Finding, error) {
	if s.failure == "snapshot" {
		return nil, nil, errors.New("fixture snapshot failure")
	}
	return s.Store.GetScanFindingSnapshot(ctx, id)
}
func (s failingScanViewStore) ListQualityPolicies(ctx context.Context, scope, id string) ([]models.QualityPolicy, error) {
	if s.failure == "policy" {
		return nil, errors.New("fixture policy failure")
	}
	return s.Store.ListQualityPolicies(ctx, scope, id)
}
func (s failingScanViewStore) ListScannerRunRecords(ctx context.Context, id string) ([]models.ScannerRunRecord, error) {
	if s.failure == "runs" {
		return nil, errors.New("fixture runs failure")
	}
	return s.Store.ListScannerRunRecords(ctx, id)
}
