package routes_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/api/routes"
	"github.com/alphabravocompany/thewolf/internal/artifacts"
	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestImportCompletionFailuresAndRetry(t *testing.T) {
	for _, step := range []string{"findings", "clusters", "baseline", "canonical", "runs", "artifact", "gate", "commit", "conflict", "continuous-conflict", "clean"} {
		t.Run(step, func(t *testing.T) {
			previous := artifacts.Global
			if err := artifacts.Init(t.TempDir()); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { artifacts.Global = previous })
			e := setupTestEnv(t)
			defer e.Store.Close()
			repo := e.createRepo(t)
			original := routes.DefaultHandler.Store
			failure := &importCompletionFailureStore{Store: original, step: step}
			routes.DefaultHandler.Store = failure
			defer func() { routes.DefaultHandler.Store = original }()
			payload := map[string]string{"repo_id": repo, "sarif": `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"fixture"}},"results":[]}]}`}
			if step != "clean" {
				payload["sarif"] = `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"fixture"}},"results":[{"ruleId":"one","level":"error","message":{"text":"finding"}}]}]}`
			}
			w := e.doRequest(http.MethodPost, "/api/sarif/import", payload)
			if step != "clean" && step != "conflict" {
				if w.Code != http.StatusInternalServerError {
					t.Fatalf("%s: %d %s", step, w.Code, w.Body.String())
				}
				scan, err := original.GetScanByID(t.Context(), failure.scanID)
				if err != nil || scan.Status != models.ScanStatusFailed {
					t.Fatalf("failed import appeared complete: %+v %v", scan, err)
				}
				imports, err := original.ListSARIFImportsByRepo(t.Context(), repo)
				if err != nil || len(imports) != 0 {
					t.Fatalf("failed import reserved checksum: %+v %v", imports, err)
				}
				gates, err := original.ListQualityGateResults(t.Context(), scan.ID)
				if err != nil || len(gates) != 0 {
					t.Fatalf("failed import published success gate: %+v %v", gates, err)
				}
				routes.DefaultHandler.Store = original
				w = e.doRequest(http.MethodPost, "/api/sarif/import", payload)
			}
			if w.Code != http.StatusCreated {
				t.Fatalf("retry/clean import: %d %s", w.Code, w.Body.String())
			}
			var body struct {
				Data struct {
					Scan models.Scan `json:"scan"`
				} `json:"data"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			scan := body.Data.Scan
			if scan.Status != models.ScanStatusCompleted || scan.ToolsCompleted != `["fixture"]` {
				t.Fatalf("import completion metadata: %+v", scan)
			}
			gate, err := original.GetQualityGateResult(t.Context(), scan.ID, "default")
			if err != nil || gate.FindingsRevision != scan.FindingsRevision || gate.Status == "pending" {
				t.Fatalf("final gate: %+v %v", gate, err)
			}
			if step == "conflict" && (scan.FindingCount != 0 || failure.commits != 2) {
				t.Fatalf("import retry lost triage: %+v calls=%d", scan, failure.commits)
			}
			if step == "clean" && gate.Status != "pass" {
				t.Fatalf("clean run lost its scanner evidence: %+v", gate)
			}
			if step == "continuous-conflict" && failure.commits != 3 {
				t.Fatalf("unbounded import retry: %d", failure.commits)
			}
			if step == "conflict" {
				files, err := original.ListScanArtifacts(t.Context(), scan.ID)
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, file := range files {
					if filepath.Base(file.FilePath) != "findings.json" || file.FindingsRevision == nil || *file.FindingsRevision != scan.FindingsRevision {
						continue
					}
					data, err := artifacts.ReadArtifact(file)
					if err != nil {
						t.Fatal(err)
					}
					var report struct {
						Findings []models.Finding `json:"findings"`
					}
					if err := json.Unmarshal(data, &report); err != nil {
						t.Fatal(err)
					}
					if len(report.Findings) != 1 || !report.Findings[0].Suppressed {
						t.Fatalf("import published obsolete report: %s", data)
					}
					found = true
				}
				if !found {
					t.Fatal("missing fresh imported report")
				}
			}
			duplicate := e.doRequest(http.MethodPost, "/api/sarif/import", payload)
			if duplicate.Code != http.StatusConflict {
				t.Fatalf("duplicate: %d %s", duplicate.Code, duplicate.Body.String())
			}
			imports, err := original.ListSARIFImportsByRepo(t.Context(), repo)
			if err != nil || len(imports) != 1 || imports[0].ScanID != scan.ID {
				t.Fatalf("duplicate changed published import: %+v %v", imports, err)
			}
			scans, err := original.ListScansByRepo(t.Context(), repo)
			if err != nil {
				t.Fatal(err)
			}
			for _, s := range scans {
				if s.ID != scan.ID && s.Status != models.ScanStatusFailed {
					t.Fatalf("orphan success after import failure: %+v", s)
				}
			}
		})
	}
}

type importCompletionFailureStore struct {
	db.Store
	step, scanID string
	commits      int
}

func (s *importCompletionFailureStore) CreateScan(ctx context.Context, scan *models.Scan) error {
	if scan.Status != models.ScanStatusRunning {
		return errors.New("import was published before its evidence")
	}
	s.scanID = scan.ID
	return s.Store.CreateScan(ctx, scan)
}
func (s *importCompletionFailureStore) CreateFindings(ctx context.Context, findings []models.Finding) error {
	if s.step == "findings" {
		return errors.New("fixture findings failure")
	}
	return s.Store.CreateFindings(ctx, findings)
}
func (s *importCompletionFailureStore) FinalizeFindingClusters(ctx context.Context, id, lease string, findings []models.Finding) error {
	if s.step == "clusters" {
		return errors.New("fixture clusters failure")
	}
	return s.Store.FinalizeFindingClusters(ctx, id, lease, findings)
}
func (s *importCompletionFailureStore) PersistFindingBaseline(ctx context.Context, id, lease string, at time.Time) error {
	if s.step == "baseline" {
		return errors.New("fixture baseline failure")
	}
	return s.Store.PersistFindingBaseline(ctx, id, lease, at)
}
func (s *importCompletionFailureStore) UpsertScannerRunRecord(ctx context.Context, run *models.ScannerRunRecord) error {
	if s.step == "runs" {
		return errors.New("fixture run failure")
	}
	return s.Store.UpsertScannerRunRecord(ctx, run)
}
func (s *importCompletionFailureStore) CreateScanArtifact(ctx context.Context, artifact *models.ScanArtifact) error {
	if s.step == "artifact" {
		return errors.New("fixture artifact failure")
	}
	return s.Store.CreateScanArtifact(ctx, artifact)
}
func (s *importCompletionFailureStore) ListQualityPolicies(ctx context.Context, scope, id string) ([]models.QualityPolicy, error) {
	if s.step == "gate" {
		return nil, errors.New("fixture policy failure")
	}
	return s.Store.ListQualityPolicies(ctx, scope, id)
}
func (s *importCompletionFailureStore) CompleteScan(ctx context.Context, scan *models.Scan, completion db.ScanCompletion) (bool, error) {
	if s.step == "commit" || s.step == "canonical" {
		return false, errors.New("fixture commit failure")
	}
	s.commits++
	if s.step == "conflict" || s.step == "continuous-conflict" {
		rows, err := s.Store.ListFindingsByScan(ctx, scan.ID)
		if err != nil {
			return false, err
		}
		if len(rows) != 1 {
			return false, errors.New("fixture expects one import finding")
		}
		if !rows[0].Suppressed || s.step == "continuous-conflict" {
			rows[0].Suppressed = true
			rows[0].Title += " concurrently changed"
			if err := s.Store.UpdateFinding(ctx, &rows[0]); err != nil {
				return false, err
			}
		}
	}
	return s.Store.CompleteScan(ctx, scan, completion)
}
