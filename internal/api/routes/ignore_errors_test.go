package routes_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	gitcmd "github.com/alphabravocompany/thewolf/internal/gitops"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/scan/report"
)

func TestIgnoreErrorsRejectNewScansWithoutChangingCompletedViews(t *testing.T) {
	e := setupTestEnv(t)
	defer e.Store.Close()
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
	sarif, err := report.GenerateSARIF(report.ReportConfig{Findings: []models.Finding{{ToolName: "alpha", Category: models.CategorySAST, Severity: models.SeverityCritical, Status: models.StatusOpen, FilePath: "app.go", RuleID: "R1", Title: "issue"}}})
	if err != nil {
		t.Fatal(err)
	}
	w := e.doRequest(http.MethodPost, "/api/sarif/import", map[string]string{"repo_id": repoID, "sarif": string(sarif)})
	if w.Code != http.StatusCreated {
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
	scan := imported.Data.Scan
	base := "/api/scans/" + scan.ID
	rows, err := e.Store.ListFindingsByScan(ctx, scan.ID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("findings: %+v %v", rows, err)
	}
	rule := map[string]string{"repo_id": repoID, "scope_type": "rule", "scope_value": "R1", "reason": "accepted"}
	name := filepath.Join(repo.SourcePath, ".wolfignore")
	if err := os.WriteFile(name, []byte("** categroy=xss\n"), 0600); err != nil {
		t.Fatal(err)
	}
	retrySARIF, err := report.GenerateSARIF(report.ReportConfig{Findings: []models.Finding{{ToolName: "beta", Category: models.CategorySAST, Severity: models.SeverityHigh, RuleID: "R2", FilePath: "other.go"}}})
	if err != nil {
		t.Fatal(err)
	}
	retryRequest := map[string]string{"repo_id": repoID, "sarif": string(retrySARIF)}
	rejected := e.doRequest(http.MethodPost, "/api/sarif/import", retryRequest)
	if rejected.Code != http.StatusInternalServerError {
		t.Fatalf("invalid ignore import: %d %s", rejected.Code, rejected.Body.String())
	}
	for _, path := range []string{base + "/findings", base + "/findings/stats", base + "/result", base + "/gate", base + "/manifest", base + "/report", base + "/sarif", base + "/tools", "/api/findings", "/api/findings/" + rows[0].ID} {
		w := e.doRequest(http.MethodGet, path, nil)
		if w.Code != http.StatusOK {
			t.Errorf("completed view reread invalid ignore %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	w = e.doRequest(http.MethodPost, "/api/suppressions/preview", rule)
	if w.Code != 200 {
		t.Fatalf("preview with frozen policy: %d %s", w.Code, w.Body.String())
	}
	w = e.doRequest(http.MethodPost, "/api/suppressions", rule)
	if w.Code != 201 {
		t.Fatalf("durable create with frozen policy: %d %s", w.Code, w.Body.String())
	}
	rules, err := e.Store.ListFindingSuppressions(ctx, repoID, false)
	if err != nil || len(rules) != 1 {
		t.Fatalf("durable rule: %+v %v", rules, err)
	}
	if err := e.Store.RevokeFindingSuppression(ctx, rules[0].ID, e.UserID); err != nil {
		t.Fatal(err)
	}
	w = e.doRequest(http.MethodPut, "/api/findings/"+rows[0].ID+"/status", map[string]string{"status": "false_positive"})
	if w.Code != 200 {
		t.Fatalf("triage with frozen policy: %d %s", w.Code, w.Body.String())
	}
	w = e.doRequest(http.MethodPut, "/api/findings/"+rows[0].ID+"/status", map[string]string{"status": "open"})
	if w.Code != 200 {
		t.Fatalf("restore triage: %d %s", w.Code, w.Body.String())
	}
	// Later valid rules also cannot change the completed scan.
	if err := os.WriteFile(name, []byte("** rule=R1 # precise accepted rule\n"), 0600); err != nil {
		t.Fatal(err)
	}
	w = e.doRequest(http.MethodGet, base+"/findings/stats", nil)
	var stats struct {
		Data struct {
			Total int `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &stats); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || stats.Data.Total != 1 {
		t.Fatalf("later source edit changed historical statistics: %d %s", w.Code, w.Body.String())
	}
	if err := os.Remove(name); err != nil {
		t.Fatal(err)
	}
	w = e.doRequest(http.MethodGet, base+"/findings/stats", nil)
	if err := json.Unmarshal(w.Body.Bytes(), &stats); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || stats.Data.Total != 1 {
		t.Fatalf("stats after removal: %d %s", w.Code, w.Body.String())
	}
	// Native Git errors reject new publication while historical views stay available.
	cmd := gitcmd.Command(ctx, nil, "-C", repo.SourcePath, "init", "-q")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("init Git source: %v %s", err, out)
	}
	configPath := filepath.Join(repo.SourcePath, ".git/config")
	config, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("[broken config\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{base + "/findings", base + "/findings/stats", base + "/result", base + "/gate", base + "/manifest", base + "/report", base + "/sarif", base + "/tools", "/api/findings", "/api/findings/" + rows[0].ID} {
		w := e.doRequest(http.MethodGet, path, nil)
		if w.Code != 200 {
			t.Errorf("completed view reread Git %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	rejected = e.doRequest(http.MethodPost, "/api/sarif/import", retryRequest)
	if rejected.Code != 500 {
		t.Fatalf("Git failure import: %d %s", rejected.Code, rejected.Body.String())
	}
	if err := os.WriteFile(configPath, config, 0600); err != nil {
		t.Fatal(err)
	}
	retry := e.doRequest(http.MethodPost, "/api/sarif/import", retryRequest)
	if retry.Code != http.StatusCreated {
		t.Fatalf("corrected identical import retry: %d %s", retry.Code, retry.Body.String())
	}

}
