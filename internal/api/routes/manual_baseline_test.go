package routes_test

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/artifacts"
	"github.com/alphabravocompany/thewolf/internal/finding/gates"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/google/uuid"
)

func TestManualBaselineViews(t *testing.T) {
	for _, pinned := range []bool{true, false} {
		t.Run(strconv.FormatBool(pinned), func(t *testing.T) {
			previous := artifacts.Global
			if err := artifacts.Init(t.TempDir()); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { artifacts.Global = previous })
			e := setupTestEnv(t)
			defer e.Store.Close()
			ctx := t.Context()
			repo := e.createRepo(t)
			start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			newScan := func(at time.Time) *models.Scan {
				s := &models.Scan{ID: uuid.NewString(), RepoID: repo, UserID: e.UserID, Branch: "main", Profile: "fast", Status: models.ScanStatusCompleted, CompletedAt: &at, ToolsSelected: `["alpha"]`, ToolsCompleted: `["alpha"]`, ToolsFailed: `[]`}
				if err := e.Store.CreateScan(ctx, s); err != nil {
					t.Fatal(err)
				}
				return s
			}
			newFinding := func(s *models.Scan, key string, severity models.Severity) *models.Finding {
				f := &models.Finding{ID: uuid.NewString(), RepoID: repo, ScanID: s.ID, ToolName: "alpha", RuleID: key, FineCategory: key, Fingerprint: key, StableFingerprint: key, Title: key, FilePath: key + ".go", Category: models.CategorySAST, Severity: severity, Status: models.StatusOpen}
				if !pinned {
					f.BaselineState, f.IntroducedInScanID = "existing", "unverified-history"
				}
				if err := e.Store.CreateFinding(ctx, f); err != nil {
					t.Fatal(err)
				}
				return f
			}
			base := newScan(start)
			newFinding(base, "a", models.SeverityCritical)
			if err := e.Store.PersistFindingBaseline(ctx, base.ID, "", start); err != nil {
				t.Fatal(err)
			}
			if err := e.Store.CreateScanBaseline(ctx, &models.ScanBaseline{ID: uuid.NewString(), RepoID: repo, Branch: "main", Name: "auto", ScanID: base.ID, CreatedBy: e.UserID}); err != nil {
				t.Fatal(err)
			}
			current := newScan(start.Add(time.Hour))
			newFinding(current, "a", models.SeverityCritical)
			b := newFinding(current, "b", models.SeverityHigh)
			if pinned {
				if err := e.Store.PersistFindingBaseline(ctx, current.ID, "", *current.CompletedAt); err != nil {
					t.Fatal(err)
				}
			}
			if err := e.Store.ReconcileVulnerabilities(ctx, current.ID); err != nil {
				t.Fatal(err)
			}
			vulns, err := e.Store.ListVulnerabilitiesByScan(ctx, current.ID)
			if err != nil || len(vulns) != 2 {
				t.Fatalf("canonical: %+v %v", vulns, err)
			}
			var aID, bID string
			for _, v := range vulns {
				if v.CanonicalKey == "a" {
					aID = v.ID
				} else {
					bID = v.ID
				}
			}
			checkGate := func(status string, fail int) {
				t.Helper()
				w := e.doRequest(http.MethodGet, "/api/scans/"+current.ID+"/gate", nil)
				var body struct {
					Data struct {
						Evaluation gates.Evaluation         `json:"evaluation"`
						Result     models.QualityGateResult `json:"result"`
					} `json:"data"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				stored, err := e.Store.GetScanByID(ctx, current.ID)
				if err != nil {
					t.Fatal(err)
				}
				if w.Code != http.StatusOK || body.Data.Evaluation.Status != status || body.Data.Evaluation.Summary.FailCount != fail || body.Data.Result.FindingsRevision != stored.FindingsRevision {
					t.Fatalf("gate: %d %s", w.Code, w.Body.String())
				}
			}
			if pinned {
				checkGate(gates.StatusFail, 1)
			} else {
				checkGate(gates.StatusPass, 0) // Legacy claims precede manual regrouping.
			}
			merged := e.doRequest(http.MethodPost, "/api/vulnerabilities/"+aID+"/merge", map[string]string{"vulnerability_id": bID})
			if merged.Code != http.StatusOK {
				t.Fatalf("merge: %d %s", merged.Code, merged.Body.String())
			}
			if pinned {
				checkGate(gates.StatusPass, 0)
			} else {
				checkGate(gates.StatusIncomplete, 0)
			}
			split := e.doRequest(http.MethodPost, "/api/vulnerabilities/"+aID+"/split", map[string]any{"finding_ids": []string{b.ID}})
			var splitBody struct {
				Data models.Vulnerability `json:"data"`
			}
			if err := json.Unmarshal(split.Body.Bytes(), &splitBody); err != nil {
				t.Fatal(err)
			}
			if split.Code != http.StatusOK {
				t.Fatalf("split: %d %s", split.Code, split.Body.String())
			}
			state, introduction := "", ""
			var firstSeen *time.Time
			if pinned {
				state, introduction, firstSeen = "new", current.ID, current.CompletedAt
				checkGate(gates.StatusFail, 1)
			} else {
				checkGate(gates.StatusIncomplete, 0)
			}
			if splitBody.Data.BaselineState != state {
				t.Fatalf("canonical baseline disagrees: %+v", splitBody.Data)
			}
			w := e.doRequest(http.MethodGet, "/api/scans/"+current.ID+"/sarif", nil)
			var sarif struct {
				Runs []struct {
					Results []struct {
						Properties struct {
							ID           string     `json:"wolfFindingId"`
							State        string     `json:"baselineState"`
							Introduction string     `json:"introducedInScanId"`
							FirstSeen    *time.Time `json:"firstSeenAt"`
						} `json:"properties"`
					} `json:"results"`
				} `json:"runs"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &sarif); err != nil {
				t.Fatal(err)
			}
			stored, err := e.Store.GetScanByID(ctx, current.ID)
			if err != nil {
				t.Fatal(err)
			}
			if w.Code != http.StatusOK || w.Header().Get("X-Wolf-Findings-Revision") != strconv.FormatInt(stored.FindingsRevision, 10) {
				t.Fatalf("SARIF revision: %d %s", w.Code, w.Body.String())
			}
			found := false
			for _, run := range sarif.Runs {
				for _, result := range run.Results {
					p := result.Properties
					if p.ID != b.ID {
						continue
					}
					found = true
					if p.State != state || p.Introduction != introduction || (p.FirstSeen == nil) != (firstSeen == nil) || (firstSeen != nil && !p.FirstSeen.Equal(*firstSeen)) {
						t.Fatalf("SARIF baseline disagrees: %+v", p)
					}
				}
			}
			if !found {
				t.Fatal("split finding missing from SARIF")
			}
		})
	}
}
