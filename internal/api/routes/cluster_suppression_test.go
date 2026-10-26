package routes_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/finding/gates"
	"github.com/alphabravocompany/thewolf/internal/finding/sarifio"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/scan/report"
)

func TestPartialClusterSuppressionPreviewAndViews(t *testing.T) {
	e := setupTestEnv(t)
	defer e.Store.Close()
	ctx := t.Context()
	repo := e.createRepo(t)
	input := []models.Finding{
		{ToolName: "alpha", Category: models.CategorySAST, Severity: models.SeverityCritical, Status: models.StatusOpen, FilePath: "app.go", RuleID: "alpha-rule", FineCategory: "sql-injection", Title: "alpha native"},
		{ToolName: "beta", Category: models.CategorySAST, Severity: models.SeverityHigh, Status: models.StatusOpen, FilePath: "app.go", RuleID: "beta-rule", FineCategory: "sql-injection", Title: "beta native"},
	}
	data, err := report.GenerateSARIF(report.ReportConfig{Findings: input})
	if err != nil {
		t.Fatal(err)
	}
	w := e.doRequest(http.MethodPost, "/api/sarif/import", map[string]string{"repo_id": repo, "sarif": string(data)})
	var imported struct {
		Data struct {
			Scan models.Scan `json:"scan"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &imported); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusCreated {
		t.Fatalf("import: %d %s", w.Code, w.Body.String())
	}
	scan := imported.Data.Scan
	base := "/api/scans/" + scan.ID
	rows, err := e.Store.ListFindingEvidenceByScan(ctx, scan.ID)
	if err != nil || len(rows) != 2 {
		t.Fatalf("evidence: %+v %v", rows, err)
	}
	ids := map[string]string{}
	for _, f := range rows {
		ids[f.ToolName] = f.ID
	}
	canonical, err := e.Store.ListVulnerabilitiesByScan(ctx, scan.ID)
	if err != nil || len(canonical) != 1 {
		t.Fatalf("canonical: %+v %v", canonical, err)
	}
	saveRule := func(tool string, hidden int) models.FindingSuppression {
		t.Helper()
		req := map[string]string{"repo_id": repo, "scope_type": "rule", "scope_value": tool + "-rule", "reason": "accepted"}
		w := e.doRequest(http.MethodPost, "/api/suppressions/preview", req)
		var body struct {
			Data struct {
				Count    int              `json:"count"`
				Hidden   int              `json:"hidden_count"`
				Evidence int              `json:"matched_evidence_count"`
				Findings []models.Finding `json:"findings"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if w.Code != http.StatusOK || body.Data.Count != 1 || body.Data.Hidden != hidden || body.Data.Evidence != 1 || len(body.Data.Findings) != 1 {
			t.Fatalf("preview: %d %s", w.Code, w.Body.String())
		}
		created := e.doRequest(http.MethodPost, "/api/suppressions", req)
		var out struct {
			Data models.FindingSuppression `json:"data"`
		}
		if err := json.Unmarshal(created.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if created.Code != http.StatusCreated {
			t.Fatalf("create: %d %s", created.Code, created.Body.String())
		}
		return out.Data
	}
	check := func(tool string, hidden bool) {
		t.Helper()
		count := 1
		status := gates.StatusFail
		if hidden {
			count = 0
			status = gates.StatusPass
		}
		list := e.doRequest(http.MethodGet, base+"/findings", nil)
		var findings struct {
			Data []models.Finding `json:"data"`
		}
		if err := json.Unmarshal(list.Body.Bytes(), &findings); err != nil {
			t.Fatal(err)
		}
		if list.Code != http.StatusOK || len(findings.Data) != count || (!hidden && findings.Data[0].ID != ids[tool]) {
			t.Fatalf("list: %d %s", list.Code, list.Body.String())
		}
		gate := e.doRequest(http.MethodGet, base+"/gate", nil)
		var evaluated struct {
			Data struct {
				Evaluation   gates.Evaluation `json:"evaluation"`
				FindingCount int              `json:"finding_count"`
			} `json:"data"`
		}
		if err := json.Unmarshal(gate.Body.Bytes(), &evaluated); err != nil {
			t.Fatal(err)
		}
		if gate.Code != http.StatusOK || evaluated.Data.FindingCount != count || evaluated.Data.Evaluation.Status != status || evaluated.Data.Evaluation.Summary.FailCount != count {
			t.Fatalf("gate: %d %s", gate.Code, gate.Body.String())
		}
		canonicalNow, err := e.Store.ListVulnerabilitiesByScan(ctx, scan.ID)
		if err != nil || len(canonicalNow) != 1 || canonicalNow[0].ID != canonical[0].ID || canonicalNow[0].Suppressed != hidden || canonicalNow[0].Title != tool+" native" {
			t.Fatalf("canonical: %+v %v", canonicalNow, err)
		}
		detail := e.doRequest(http.MethodGet, "/api/findings/"+ids["alpha"], nil)
		var original struct {
			Data struct {
				models.Finding
				Evidence []models.Finding `json:"evidence"`
			} `json:"data"`
		}
		if err := json.Unmarshal(detail.Body.Bytes(), &original); err != nil {
			t.Fatal(err)
		}
		if detail.Code != http.StatusOK || original.Data.ID != ids["alpha"] || len(original.Data.Evidence) != 2 || original.Data.ClusterID != ids[tool] {
			t.Fatalf("old detail: %d %s", detail.Code, detail.Body.String())
		}
		manifest := e.doRequest(http.MethodGet, base+"/manifest", nil)
		var m report.Manifest
		if err := json.Unmarshal(manifest.Body.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		if manifest.Code != http.StatusOK || m.Counts.Visible != count || m.Counts.AfterDedupe != 1 {
			t.Fatalf("manifest: %d %s", manifest.Code, manifest.Body.String())
		}
		sarif := e.doRequest(http.MethodGet, base+"/sarif", nil)
		imported, err := sarifio.Import(sarif.Body.Bytes())
		if err != nil || sarif.Code != http.StatusOK || len(imported.Findings) != 1 || imported.Findings[0].ToolName != tool || imported.Findings[0].Suppressed != hidden {
			t.Fatalf("live SARIF: %+v %v", imported, err)
		}
	}
	// A rule matching only the non-primary member must still appear in preview.
	peek := e.doRequest(http.MethodPost, "/api/suppressions/preview", map[string]string{"repo_id": repo, "scope_type": "rule", "scope_value": "beta-rule", "reason": "accepted"})
	var memberPreview struct {
		Data struct {
			Count    int              `json:"count"`
			Hidden   int              `json:"hidden_count"`
			Evidence int              `json:"matched_evidence_count"`
			Findings []models.Finding `json:"findings"`
		} `json:"data"`
	}
	if err := json.Unmarshal(peek.Body.Bytes(), &memberPreview); err != nil {
		t.Fatal(err)
	}
	if peek.Code != http.StatusOK || memberPreview.Data.Count != 1 || memberPreview.Data.Hidden != 0 || memberPreview.Data.Evidence != 1 || len(memberPreview.Data.Findings) != 1 || memberPreview.Data.Findings[0].ID != ids["alpha"] {
		t.Fatalf("non-primary preview omitted: %d %s", peek.Code, peek.Body.String())
	}
	alpha := saveRule("alpha", 0)
	check("beta", false)
	beta := saveRule("beta", 1)
	check("alpha", true)
	for _, rule := range []models.FindingSuppression{alpha, beta} {
		w := e.doRequest(http.MethodDelete, "/api/suppressions/"+rule.ID, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("revoke: %d %s", w.Code, w.Body.String())
		}
		check("alpha", false)
	}
}
