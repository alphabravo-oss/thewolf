package routes_test

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/artifacts"
	"github.com/alphabravocompany/thewolf/internal/finding/gates"
	"github.com/alphabravocompany/thewolf/internal/finding/sarifio"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/scan/report"
)

func TestLockfileSuppressionViewsAndArtifacts(t *testing.T) {
	t.Setenv("WOLF_DISABLE_DEFAULT_SUPPRESSIONS", "")
	e := setupTestEnv(t)
	defer e.Store.Close()
	repo := e.createRepo(t)
	var input []models.Finding
	for _, path := range []string{"Cargo.lock", "web/package-lock.json", "services/api/go.sum"} {
		for _, category := range []models.Category{models.CategorySCA, models.CategorySAST, models.CategorySecrets, models.CategoryLicense} {
			key := string(category) + "-" + path
			input = append(input, models.Finding{ToolName: string(category), Category: category, FineCategory: string(category), RuleID: key, Title: key, FilePath: path, Severity: models.SeverityHigh, Status: models.StatusOpen, PackageName: "alpha", PackageVersion: "1", AdvisoryID: key})
		}
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
	scan := imported.Data.Scan
	if w.Code != http.StatusCreated || scan.Status != models.ScanStatusCompleted || scan.FindingCount != 6 {
		t.Fatalf("completion counts: %d %s", w.Code, w.Body.String())
	}
	base := "/api/scans/" + scan.ID
	checkFindings := func(findings []models.Finding, audit bool) {
		t.Helper()
		want := 6
		if audit {
			want = 12
		}
		if len(findings) != want {
			t.Fatalf("findings=%d, want %d", len(findings), want)
		}
		seen := map[string]bool{}
		for _, f := range findings {
			key := string(f.Category) + "-" + f.FilePath
			if seen[key] {
				t.Fatalf("duplicate occurrence: %s", key)
			}
			seen[key] = true
			suppressed := f.Category == models.CategorySAST || f.Category == models.CategorySecrets
			if f.Suppressed != suppressed || (!audit && suppressed) || (suppressed && f.SuppressedReason != "default:lockfile") {
				t.Fatalf("wrong lockfile visibility: %+v", f)
			}
		}
		for _, f := range input {
			if (audit || f.Category == models.CategorySCA || f.Category == models.CategoryLicense) && !seen[string(f.Category)+"-"+f.FilePath] {
				t.Fatalf("lost occurrence: %+v", f)
			}
		}
	}
	for _, suffix := range []string{"/findings", "/findings?include_suppressed=true"} {
		w := e.doRequest(http.MethodGet, base+suffix, nil)
		var body struct {
			Data []models.Finding `json:"data"`
			Meta struct {
				Total int `json:"total"`
			} `json:"meta"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if w.Code != http.StatusOK || body.Meta.Total != len(body.Data) {
			t.Fatalf("list: %d %s", w.Code, w.Body.String())
		}
		checkFindings(body.Data, strings.Contains(suffix, "?"))
		for _, f := range body.Data {
			detail := e.doRequest(http.MethodGet, "/api/findings/"+f.ID, nil)
			var item struct {
				Data struct {
					models.Finding
					Evidence []models.Finding `json:"evidence"`
				} `json:"data"`
			}
			if err := json.Unmarshal(detail.Body.Bytes(), &item); err != nil {
				t.Fatal(err)
			}
			if detail.Code != http.StatusOK || item.Data.ID != f.ID || item.Data.Suppressed != f.Suppressed || len(item.Data.Evidence) != 1 || item.Data.Evidence[0].Suppressed != f.Suppressed {
				t.Fatalf("detail differs: %d %s", detail.Code, detail.Body.String())
			}
		}
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
	if gate.Code != http.StatusOK || evaluated.Data.Evaluation.Status != gates.StatusFail || evaluated.Data.Evaluation.Summary.FailCount != 3 || evaluated.Data.FindingCount != 6 {
		t.Fatalf("gate lost lockfile issues: %d %s", gate.Code, gate.Body.String())
	}
	for _, rule := range evaluated.Data.Evaluation.MatchedRules {
		for _, id := range rule.FindingIDs {
			f, err := e.Store.GetFindingByID(t.Context(), id)
			if err != nil || f.Category != models.CategorySCA {
				t.Fatalf("wrong gate finding: %+v %v", f, err)
			}
		}
	}
	persisted, err := e.Store.GetQualityGateResult(t.Context(), scan.ID, "default")
	if err != nil || persisted.Status != gates.StatusFail || persisted.FindingsRevision != scan.FindingsRevision {
		t.Fatalf("persisted gate: %+v %v", persisted, err)
	}
	// SARIF is an audit format: keep suppressed results, with suppression markers.
	live := e.doRequest(http.MethodGet, base+"/sarif", nil)
	roundtrip, err := sarifio.Import(live.Body.Bytes())
	if live.Code != http.StatusOK || err != nil {
		t.Fatalf("live SARIF: %d %v", live.Code, err)
	}
	checkFindings(roundtrip.Findings, true)
	files, err := e.Store.ListScanArtifacts(t.Context(), scan.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, file := range files {
		name := filepath.Base(file.FilePath)
		if name != "findings.json" && name != "combined.sarif" && name != "manifest.json" && name != "FIX-HIGH.md" && name != "FIX-ALL.md" {
			continue
		}
		bytes, err := artifacts.ReadArtifact(file)
		if err != nil {
			t.Fatal(err)
		}
		downloaded := e.doRequest(http.MethodGet, base+"/artifacts/"+file.ID+"/download", nil)
		if downloaded.Code != http.StatusOK || downloaded.Body.String() != string(bytes) || downloaded.Header().Get("X-Wolf-Artifact-Integrity") != "verified" {
			t.Fatalf("download changed %s", name)
		}
		found[name] = true
		switch name {
		case "findings.json":
			var body struct {
				Findings []models.Finding `json:"findings"`
			}
			if err := json.Unmarshal(bytes, &body); err != nil {
				t.Fatal(err)
			}
			checkFindings(body.Findings, true)
		case "combined.sarif":
			body, err := sarifio.Import(bytes)
			if err != nil {
				t.Fatal(err)
			}
			checkFindings(body.Findings, true)
		case "manifest.json":
			var body report.Manifest
			if err := json.Unmarshal(bytes, &body); err != nil {
				t.Fatal(err)
			}
			if body.Counts.Visible != 6 || body.Counts.Suppressed != 6 || body.Counts.AfterDedupe != 12 {
				t.Fatalf("manifest counts: %+v", body.Counts)
			}
		default:
			main, appendix, _ := strings.Cut(string(bytes), "<details>\n<summary><strong>Suppressed")
			for _, f := range input {
				want := f.Category == models.CategorySCA || f.Category == models.CategoryLicense
				if strings.Contains(main, f.Title) != want {
					t.Fatalf("%s visibility disagrees for %s", name, f.Title)
				}
				if name == "FIX-ALL.md" && !want && !strings.Contains(appendix, f.Title) {
					t.Fatalf("audit appendix lost %s", f.Title)
				}
			}
		}
	}
	rows, err := e.Store.ListFindingsByScan(t.Context(), scan.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range rows {
		w := e.doRequest(http.MethodPut, "/api/findings/"+f.ID+"/status", map[string]string{"status": "wont_fix"})
		var body struct {
			Data models.Finding `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		suppressed := f.Category == models.CategorySAST || f.Category == models.CategorySecrets
		if w.Code != http.StatusOK || body.Data.Status != models.StatusWontFix || body.Data.Suppressed != suppressed {
			t.Fatalf("triage response differs: %d %s", w.Code, w.Body.String())
		}
	}
	if len(found) != 5 {
		t.Fatalf("missing published reports: %+v", found)
	}
}
