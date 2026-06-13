package routes_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/models"
)

// seedScanWithFindings creates a completed scan and an arbitrary number of
// findings with the provided (severity, rule_id) tuples.
func seedScanWithFindings(t *testing.T, env *testEnv, repoID string, findings []struct {
	Severity models.Severity
	RuleID   string
}) string {
	t.Helper()
	ctx := context.Background()
	scanID := uuid.New().String()
	if err := env.Store.CreateScan(ctx, &models.Scan{
		ID:              scanID,
		UserID:          env.UserID,
		RepoID:          repoID,
		Branch:          "main",
		Status:          models.ScanStatusCompleted,
		ToolsSelected:   "[]",
		ToolsCompleted:  "[]",
		ToolsFailed:     "[]",
		CoverageSummary: "{}",
	}); err != nil {
		t.Fatalf("CreateScan: %v", err)
	}
	for i, f := range findings {
		ff := models.Finding{
			ID:          uuid.New().String(),
			ScanID:      scanID,
			RepoID:      repoID,
			Fingerprint: uuid.New().String(),
			ToolName:    "test",
			Category:    models.CategorySAST,
			Severity:    f.Severity,
			Title:       "t",
			FilePath:    "x.go",
			RuleID:      f.RuleID,
			Status:      models.StatusOpen,
		}
		if err := env.Store.CreateFinding(ctx, &ff); err != nil {
			t.Fatalf("CreateFinding[%d]: %v", i, err)
		}
	}
	return scanID
}

func TestFleetPostureEmptyFleet(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()

	w := env.doRequest(http.MethodGet, "/api/fleet/posture", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var got struct {
		Data struct {
			OpenFindings map[string]int `json:"open_findings_by_severity"`
			RepoCount    int            `json:"repo_count"`
			GatesFailing int            `json:"gates_failing"`
			WeekOverWeek map[string]int `json:"week_over_week_delta"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Data.RepoCount != 0 || got.Data.GatesFailing != 0 {
		t.Errorf("empty fleet should report zeros, got %+v", got.Data)
	}
	if got.Data.OpenFindings["critical"] != 0 || got.Data.OpenFindings["high"] != 0 {
		t.Errorf("empty fleet should have zero open findings, got %+v", got.Data.OpenFindings)
	}
}

// createRepoWithSource posts a repo with an explicit source_type via the
// API and returns its ID. The factory short-circuits when the API rejects
// non-local source paths in tests.
func createRepoWithSource(t *testing.T, env *testEnv, name string, sourceType models.SourceType) string {
	t.Helper()
	ctx := context.Background()
	repo := &models.Repo{
		ID:                 uuid.New().String(),
		UserID:             env.UserID,
		Name:               name,
		SourceType:         sourceType,
		SourcePath:         "/tmp/" + name,
		DefaultBranch:      "main",
		DetectedLanguages:  "[]",
		DetectedFrameworks: "[]",
	}
	if err := env.Store.CreateRepo(ctx, repo); err != nil {
		t.Fatalf("CreateRepo(%s): %v", name, err)
	}
	return repo.ID
}

func TestFleetInventoryGroupsByEverything(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()
	ctx := context.Background()

	rLocal := createRepoWithSource(t, env, "local-go", models.SourceTypeLocal)
	rGit := createRepoWithSource(t, env, "github-ts", models.SourceTypeGitHub)
	rSSH := createRepoWithSource(t, env, "ssh-py", models.SourceTypeSSH)

	if err := env.Store.UpdateRepoDetection(ctx, rLocal, `["go"]`, `[]`); err != nil {
		t.Fatalf("update detection: %v", err)
	}
	if err := env.Store.UpdateRepoDetection(ctx, rGit, `["typescript"]`, `[]`); err != nil {
		t.Fatalf("update detection: %v", err)
	}
	if err := env.Store.UpdateRepoDetection(ctx, rSSH, `["python"]`, `[]`); err != nil {
		t.Fatalf("update detection: %v", err)
	}

	w := env.doRequest(http.MethodGet, "/api/fleet/inventory", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var got struct {
		Data struct {
			BySourceType map[string]int `json:"by_source_type"`
			ByCollection map[string]int `json:"by_collection"`
			ByLanguage   map[string]int `json:"by_language"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Data.BySourceType["local"] != 1 || got.Data.BySourceType["github"] != 1 || got.Data.BySourceType["ssh"] != 1 {
		t.Errorf("inventory by_source_type wrong: %+v", got.Data.BySourceType)
	}
	if got.Data.ByLanguage["go"] != 1 || got.Data.ByLanguage["typescript"] != 1 || got.Data.ByLanguage["python"] != 1 {
		t.Errorf("inventory by_language wrong: %+v", got.Data.ByLanguage)
	}
}

func TestFleetPostureCountsOpenFindings(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()

	repoID := env.createRepo(t)
	seedScanWithFindings(t, env, repoID, []struct {
		Severity models.Severity
		RuleID   string
	}{
		{models.SeverityCritical, "rule-a"},
		{models.SeverityHigh, "rule-a"},
		{models.SeverityLow, "rule-b"},
	})

	w := env.doRequest(http.MethodGet, "/api/fleet/posture", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	data := got["data"].(map[string]any)
	sev := data["open_findings_by_severity"].(map[string]any)
	if sev["critical"].(float64) != 1 || sev["high"].(float64) != 1 || sev["low"].(float64) != 1 {
		t.Errorf("expected 1/1/1 critical/high/low, got %v", sev)
	}
	if data["repo_count"].(float64) != 1 {
		t.Errorf("expected repo_count=1, got %v", data["repo_count"])
	}
}
