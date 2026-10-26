package routes_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/finding/gates"
	"github.com/alphabravocompany/thewolf/internal/finding/sarifio"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/scan/report"
	"github.com/google/uuid"
)

func TestSuppressionPreviewAndExpirationViews(t *testing.T) {
	e := setupTestEnv(t)
	defer e.Store.Close()
	ctx := t.Context()
	repo := e.createRepo(t)
	var scans []models.Scan
	for i, branch := range []string{"main", "main", "dev"} {
		data, err := report.GenerateSARIF(report.ReportConfig{Findings: []models.Finding{{ToolName: "fixture", Category: models.CategorySAST, FilePath: "app.go", RuleID: "rule", Title: branch + time.Duration(i).String(), Severity: models.SeverityHigh, Status: models.StatusOpen}}})
		if err != nil {
			t.Fatal(err)
		}
		w := e.doRequest(http.MethodPost, "/api/sarif/import", map[string]string{"repo_id": repo, "branch": branch, "sarif": string(data)})
		var body struct {
			Data struct {
				Scan models.Scan `json:"scan"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if w.Code != http.StatusCreated {
			t.Fatalf("import: %d %s", w.Code, w.Body.String())
		}
		scans = append(scans, body.Data.Scan)
	}
	request := map[string]string{"repo_id": repo, "scope_type": "rule", "scope_value": "rule", "reason": "accepted", "branch": "main"}
	preview := func(branch, id string, count int) {
		t.Helper()
		request["branch"] = branch
		w := e.doRequest(http.MethodPost, "/api/suppressions/preview", request)
		var body struct {
			Data struct {
				ScanID   string           `json:"scan_id"`
				Count    int              `json:"count"`
				Findings []models.Finding `json:"findings"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if w.Code != http.StatusOK || body.Data.Count != count || len(body.Data.Findings) != count || body.Data.ScanID != id {
			t.Fatalf("latest preview: %d %s", w.Code, w.Body.String())
		}
	}
	preview("main", scans[1].ID, 1)
	preview("", scans[2].ID, 1)
	preview("missing", "", 0)
	// A latest empty scan must not silently fall back to older, nonempty evidence.
	empty := &models.Scan{ID: uuid.NewString(), RepoID: repo, UserID: e.UserID, Branch: "dev", Status: models.ScanStatusRunning}
	if err := e.Store.CreateScan(ctx, empty); err != nil {
		t.Fatal(err)
	}
	preview("dev", empty.ID, 0)
	request["branch"] = "main"
	request["expires_at"] = time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	w := e.doRequest(http.MethodPost, "/api/suppressions", request)
	var created struct {
		Data models.FindingSuppression `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	check := func(suppressed bool) {
		t.Helper()
		for i, scan := range scans {
			hidden := suppressed && scan.Branch == "main"
			count := 1
			if hidden {
				count = 0
			}
			saved, err := e.Store.GetScanByID(ctx, scan.ID)
			if err != nil || saved.FindingCount != count {
				t.Fatalf("scan %d count: %+v %v", i, saved, err)
			}
			list := e.doRequest(http.MethodGet, "/api/scans/"+scan.ID+"/findings", nil)
			var body struct {
				Data []models.Finding `json:"data"`
			}
			if err := json.Unmarshal(list.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if list.Code != http.StatusOK || len(body.Data) != count {
				t.Fatalf("list: %d %s", list.Code, list.Body.String())
			}
			gate := e.doRequest(http.MethodGet, "/api/scans/"+scan.ID+"/gate", nil)
			var evaluation struct {
				Data struct {
					Evaluation gates.Evaluation `json:"evaluation"`
				} `json:"data"`
			}
			if err := json.Unmarshal(gate.Body.Bytes(), &evaluation); err != nil {
				t.Fatal(err)
			}
			status := gates.StatusFail
			if hidden {
				status = gates.StatusPass
			}
			if gate.Code != http.StatusOK || evaluation.Data.Evaluation.Status != status || evaluation.Data.Evaluation.Summary.FailCount != count {
				t.Fatalf("gate: %d %s", gate.Code, gate.Body.String())
			}
			manifest := e.doRequest(http.MethodGet, "/api/scans/"+scan.ID+"/manifest", nil)
			var current report.Manifest
			if err := json.Unmarshal(manifest.Body.Bytes(), &current); err != nil {
				t.Fatal(err)
			}
			if manifest.Code != http.StatusOK || current.Counts.Visible != count || current.FindingsRevision == nil || *current.FindingsRevision != saved.FindingsRevision {
				t.Fatalf("stale manifest: %d %s", manifest.Code, manifest.Body.String())
			}
			sarif := e.doRequest(http.MethodGet, "/api/scans/"+scan.ID+"/sarif", nil)
			parsed, err := sarifio.Import(sarif.Body.Bytes())
			if err != nil || sarif.Code != http.StatusOK || len(parsed.Findings) != 1 || parsed.Findings[0].Suppressed != hidden {
				t.Fatalf("SARIF visibility: %+v %v", parsed, err)
			}
		}
	}
	check(true)
	if n, err := e.Store.ExpireFindingSuppressions(ctx, *created.Data.ExpiresAt); err != nil || n != 1 {
		t.Fatalf("expiry: %d %v", n, err)
	}
	check(false)
	for _, suffix := range []string{"", "&include_inactive=true"} {
		w := e.doRequest(http.MethodGet, "/api/suppressions?repo_id="+repo+suffix, nil)
		var body struct {
			Data []models.FindingSuppression `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		want := 0
		if suffix != "" {
			want = 1
		}
		if w.Code != http.StatusOK || len(body.Data) != want || (want == 1 && body.Data[0].Status != models.SuppressionStatusExpired) {
			t.Fatalf("expiry listing: %d %s", w.Code, w.Body.String())
		}
	}
	request["expires_at"] = time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	if w := e.doRequest(http.MethodPost, "/api/suppressions", request); w.Code != http.StatusBadRequest {
		t.Fatalf("expired creation accepted: %d %s", w.Code, w.Body.String())
	}
}
