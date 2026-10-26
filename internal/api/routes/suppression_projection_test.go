package routes_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/api/routes"
	"github.com/alphabravocompany/thewolf/internal/artifacts"
	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/scan/report"
	"github.com/google/uuid"
)

func projectionFixture(t *testing.T) (*testEnv, *models.Scan, *models.Repo) {
	t.Helper()
	e := setupTestEnv(t)
	t.Cleanup(func() { _ = e.Store.Close() })
	ctx := t.Context()
	repoID := e.createRepo(t)
	repo, err := e.Store.GetRepoByID(ctx, repoID)
	if err != nil {
		t.Fatal(err)
	}
	repo.SourcePath = t.TempDir()
	if err := e.Store.UpdateRepo(ctx, repo); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	scan := &models.Scan{ID: uuid.NewString(), RepoID: repoID, UserID: e.UserID, Status: models.ScanStatusCompleted, CompletedAt: &now, FindingCount: 1, ToolsSelected: `["alpha"]`, ToolsCompleted: `["alpha"]`, ToolsFailed: `[]`}
	if err := e.Store.CreateScan(ctx, scan); err != nil {
		t.Fatal(err)
	}
	if err := e.Store.CreateFinding(ctx, &models.Finding{ID: uuid.NewString(), ScanID: scan.ID, RepoID: repoID, ToolName: "alpha", Category: models.CategorySAST, Severity: models.SeverityCritical, Status: models.StatusOpen, FilePath: "app.go", RuleID: "R1", Title: "issue"}); err != nil {
		t.Fatal(err)
	}
	scan, err = e.Store.GetScanByID(ctx, scan.ID)
	if err != nil {
		t.Fatal(err)
	}
	return e, scan, repo
}

func TestRunningManifestCountsFollowPolicyWithoutLosingProducerMetadata(t *testing.T) {
	t.Setenv("WOLF_DISABLE_DEFAULT_SUPPRESSIONS", "")
	e, scan, repo := projectionFixture(t)
	ctx := t.Context()
	scan.Status = models.ScanStatusRunning
	if err := e.Store.UpdateScan(ctx, scan); err != nil {
		t.Fatal(err)
	}
	saved := map[string]any{"scan_id": scan.ID, "findings_revision": scan.FindingsRevision, "counts": report.Counts{RawFindings: 7, AfterDedupe: 1, Visible: 1, HighSeverity: 1}, "producer_extension": map[string]any{"trace": "preserve"}, "scanners_run": []string{"alpha"}}
	payload, err := json.Marshal(saved)
	if err != nil {
		t.Fatal(err)
	}
	record, err := artifacts.Publish(ctx, scan.ID, "manifest.json", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	record.ArtifactType = models.ArtifactManifest
	record.FindingsRevision = &scan.FindingsRevision
	if err := e.Store.CreateScanArtifact(ctx, record); err != nil {
		t.Fatal(err)
	}
	base := "/api/scans/" + scan.ID
	name := filepath.Join(repo.SourcePath, ".wolfignore")
	if err := os.WriteFile(name, []byte("app.go rule=R1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	w := e.doRequest(http.MethodGet, base+"/manifest", nil)
	var live struct {
		Counts    report.Counts     `json:"counts"`
		Revision  int64             `json:"findings_revision"`
		Extension map[string]string `json:"producer_extension"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &live); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || w.Header().Get("Cache-Control") != "private, no-store" || live.Counts.Visible != 0 || live.Counts.Suppressed != 1 || live.Counts.RawFindings != 7 || live.Revision != scan.FindingsRevision || live.Extension["trace"] != "preserve" {
		t.Fatalf("stale current policy manifest: %d %s", w.Code, w.Body.String())
	}
	if err := os.Remove(name); err != nil {
		t.Fatal(err)
	}
	w = e.doRequest(http.MethodGet, base+"/manifest", nil)
	if err := json.Unmarshal(w.Body.Bytes(), &live); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || live.Counts.Visible != 1 || live.Counts.Suppressed != 0 {
		t.Fatalf("removed policy stayed hidden: %d %s", w.Code, w.Body.String())
	}
	// Default-environment edits also affect live counts without a new finding
	// revision. A changed finding revision must retain producer extensions too.
	if err := e.Store.CreateFinding(ctx, &models.Finding{ID: uuid.NewString(), ScanID: scan.ID, RepoID: repo.ID, ToolName: "beta", Category: models.CategorySAST, Severity: models.SeverityHigh, Status: models.StatusOpen, FilePath: "vendor/app.go", RuleID: "R2", Title: "vendor issue"}); err != nil {
		t.Fatal(err)
	}
	w = e.doRequest(http.MethodGet, base+"/manifest", nil)
	if err := json.Unmarshal(w.Body.Bytes(), &live); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || live.Counts.Visible != 1 || live.Extension["trace"] != "preserve" || live.Revision != scan.FindingsRevision+1 {
		t.Fatalf("revision refresh lost metadata: %d %s", w.Code, w.Body.String())
	}
	t.Setenv("WOLF_DISABLE_DEFAULT_SUPPRESSIONS", "1")
	w = e.doRequest(http.MethodGet, base+"/manifest", nil)
	if err := json.Unmarshal(w.Body.Bytes(), &live); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || live.Counts.Visible != 2 || live.Revision != scan.FindingsRevision+1 {
		t.Fatalf("environment change stale: %d %s", w.Code, w.Body.String())
	}
	download := e.doRequest(http.MethodGet, base+"/artifacts/"+record.ID+"/download", nil)
	if download.Code != 200 || !bytes.Equal(download.Body.Bytes(), payload) {
		t.Fatal("historical manifest rewritten")
	}
}

type changeRulesAfterProjectionStore struct {
	db.Store
	name string
}

func (s changeRulesAfterProjectionStore) ListScannerRunRecords(ctx context.Context, id string) ([]models.ScannerRunRecord, error) {
	if err := os.WriteFile(s.name, []byte("app.go rule=R1\n"), 0600); err != nil {
		return nil, err
	}
	return s.Store.ListScannerRunRecords(ctx, id)
}
func TestScanResultUsesOneSuppressionProjection(t *testing.T) {
	e, scan, repo := projectionFixture(t)
	h := routes.DefaultHandler
	original := h.Store
	t.Cleanup(func() { h.Store = original })
	h.Store = changeRulesAfterProjectionStore{Store: original, name: filepath.Join(repo.SourcePath, ".wolfignore")}
	w := e.doRequest(http.MethodGet, "/api/scans/"+scan.ID+"/result", nil)
	var body struct {
		Data struct {
			Totals struct {
				Findings int `json:"findings"`
			} `json:"totals"`
			Gate models.QualityGateResult `json:"quality_gate"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || body.Data.Totals.Findings != 1 || body.Data.Gate.Status != "fail" {
		t.Fatalf("one response mixed rule versions: %d %s", w.Code, w.Body.String())
	}
	h.Store = original
	w = e.doRequest(http.MethodGet, "/api/scans/"+scan.ID+"/result", nil)
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || body.Data.Totals.Findings != 1 || body.Data.Gate.Status != "fail" {
		t.Fatalf("legacy completed scan invented a later policy: %d %s", w.Code, w.Body.String())
	}
}

type changeRulesDuringPublicationStore struct {
	db.Store
	name string
}

func (s changeRulesDuringPublicationStore) CreateScanArtifact(ctx context.Context, a *models.ScanArtifact) error {
	if err := s.Store.CreateScanArtifact(ctx, a); err != nil {
		return err
	}
	if a.ArtifactType == models.ArtifactManifest {
		return os.WriteFile(s.name, []byte("app.go rule=R1\n"), 0600)
	}
	return nil
}
func TestPublicationGateUsesWrittenSuppressionProjection(t *testing.T) {
	e, _, repo := projectionFixture(t)
	ctx := t.Context()
	h := routes.DefaultHandler
	original := h.Store
	t.Cleanup(func() { h.Store = original })
	h.Store = changeRulesDuringPublicationStore{Store: original, name: filepath.Join(repo.SourcePath, ".wolfignore")}
	sarif, err := report.GenerateSARIF(report.ReportConfig{Findings: []models.Finding{{ToolName: "alpha", Category: models.CategorySAST, Severity: models.SeverityCritical, Status: models.StatusOpen, FilePath: "app.go", RuleID: "R1", Title: "issue"}}})
	if err != nil {
		t.Fatal(err)
	}
	w := e.doRequest(http.MethodPost, "/api/sarif/import", map[string]string{"repo_id": repo.ID, "sarif": string(sarif)})
	if w.Code != 201 {
		t.Fatalf("import: %d %s", w.Code, w.Body.String())
	}
	var imported struct {
		Data struct {
			Scan models.Scan `json:"scan"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &imported); err != nil {
		t.Fatal(err)
	}
	result, err := e.Store.GetQualityGateResult(ctx, imported.Data.Scan.ID, "default")
	if err != nil || result.Status != "fail" {
		t.Fatalf("gate disagrees with written projection: %+v %v", result, err)
	}
	records, err := e.Store.ListScanArtifacts(ctx, imported.Data.Scan.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range records {
		if a.ArtifactType == models.ArtifactManifest {
			content, err := artifacts.ReadArtifact(a)
			if err != nil {
				t.Fatal(err)
			}
			var m report.Manifest
			if err := json.Unmarshal(content, &m); err != nil {
				t.Fatal(err)
			}
			if m.Counts.Visible != 1 || m.Counts.Suppressed != 0 {
				t.Fatalf("written projection changed: %+v", m.Counts)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("missing published manifest")
	}
}

type changeRulesDuringEvidenceStore struct {
	db.Store
	name string
}

func (s changeRulesDuringEvidenceStore) ListFindingClusterEvidence(ctx context.Context, id string) ([]models.Finding, error) {
	if err := os.WriteFile(s.name, []byte("app.go rule=R1\n"), 0600); err != nil {
		return nil, err
	}
	return s.Store.ListFindingClusterEvidence(ctx, id)
}
func TestFindingDetailUsesOneSuppressionProjection(t *testing.T) {
	e, scan, repo := projectionFixture(t)
	ctx := t.Context()
	scan.Status = models.ScanStatusRunning
	if err := e.Store.UpdateScan(ctx, scan); err != nil {
		t.Fatal(err)
	}
	findings, err := e.Store.ListFindingsByScan(ctx, scan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Store.FinalizeFindingClusters(ctx, scan.ID, "", findings); err != nil {
		t.Fatal(err)
	}
	h := routes.DefaultHandler
	original := h.Store
	t.Cleanup(func() { h.Store = original })
	h.Store = changeRulesDuringEvidenceStore{Store: original, name: filepath.Join(repo.SourcePath, ".wolfignore")}
	w := e.doRequest(http.MethodGet, "/api/findings/"+findings[0].ID, nil)
	var body struct {
		Data struct {
			models.Finding
			Evidence []models.Finding `json:"evidence"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || !body.Data.Suppressed || len(body.Data.Evidence) != 1 || !body.Data.Evidence[0].Suppressed {
		t.Fatalf("detail mixed rule versions: %d %s", w.Code, w.Body.String())
	}
}
