package routes_test

import (
	"context"
	"encoding/json"
	"fmt"
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
			FilePath:    fmt.Sprintf("%s-%d.go", f.RuleID, i),
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

func TestFleetNeedsAttentionScoresAndOrders(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()
	ctx := context.Background()

	// repoA: one new critical → score = 10
	repoA := createRepoWithSource(t, env, "repo-a", models.SourceTypeLocal)
	scanA := uuid.New().String()
	if err := env.Store.CreateScan(ctx, &models.Scan{
		ID: scanA, UserID: env.UserID, RepoID: repoA, Branch: "main",
		Status: models.ScanStatusCompleted, ToolsSelected: "[]", ToolsCompleted: "[]",
		ToolsFailed: "[]", CoverageSummary: "{}",
	}); err != nil {
		t.Fatalf("scanA: %v", err)
	}
	if err := env.Store.CreateFinding(ctx, &models.Finding{
		ID:          uuid.New().String(),
		ScanID:      scanA,
		RepoID:      repoA,
		Fingerprint: uuid.New().String(),
		ToolName:    "test",
		Category:    models.CategorySAST,
		Severity:    models.SeverityCritical,
		Title:       "t",
		FilePath:    "x.go",
		Status:      models.StatusOpen,
	}); err != nil {
		t.Fatalf("finding A: %v", err)
	}

	// repoB: two new highs → score = 10
	repoB := createRepoWithSource(t, env, "repo-b", models.SourceTypeLocal)
	scanB := uuid.New().String()
	if err := env.Store.CreateScan(ctx, &models.Scan{
		ID: scanB, UserID: env.UserID, RepoID: repoB, Branch: "main",
		Status: models.ScanStatusCompleted, ToolsSelected: "[]", ToolsCompleted: "[]",
		ToolsFailed: "[]", CoverageSummary: "{}",
	}); err != nil {
		t.Fatalf("scanB: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := env.Store.CreateFinding(ctx, &models.Finding{
			ID:          uuid.New().String(),
			ScanID:      scanB,
			RepoID:      repoB,
			Fingerprint: uuid.New().String(),
			ToolName:    "test",
			Category:    models.CategorySAST,
			Severity:    models.SeverityHigh,
			Title:       "t",
			FilePath:    "x.go",
			Status:      models.StatusOpen,
		}); err != nil {
			t.Fatalf("finding B[%d]: %v", i, err)
		}
	}

	// repoC: no findings, never scanned → score = 30 (stale)
	repoC := createRepoWithSource(t, env, "repo-c", models.SourceTypeLocal)

	// repoD: three new criticals → score = 30
	repoD := createRepoWithSource(t, env, "repo-d", models.SourceTypeLocal)
	scanD := uuid.New().String()
	if err := env.Store.CreateScan(ctx, &models.Scan{
		ID: scanD, UserID: env.UserID, RepoID: repoD, Branch: "main",
		Status: models.ScanStatusCompleted, ToolsSelected: "[]", ToolsCompleted: "[]",
		ToolsFailed: "[]", CoverageSummary: "{}",
	}); err != nil {
		t.Fatalf("scanD: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := env.Store.CreateFinding(ctx, &models.Finding{
			ID:          uuid.New().String(),
			ScanID:      scanD,
			RepoID:      repoD,
			Fingerprint: uuid.New().String(),
			ToolName:    "test",
			Category:    models.CategorySAST,
			Severity:    models.SeverityCritical,
			Title:       "t",
			FilePath:    "x.go",
			Status:      models.StatusOpen,
		}); err != nil {
			t.Fatalf("finding D[%d]: %v", i, err)
		}
	}

	_ = repoA
	_ = repoB
	_ = repoC

	w := env.doRequest(http.MethodGet, "/api/fleet/needs-attention", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var got struct {
		Data []struct {
			RepoID string `json:"repo_id"`
			Name   string `json:"name"`
			Reason string `json:"reason"`
			Score  int    `json:"score"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Data) < 3 {
		t.Fatalf("expected at least 3 rows, got %d: %+v", len(got.Data), got.Data)
	}
	// repoD (score 30, new critical) and repoC (score 30, stale never-scanned)
	// should rank above repoA (score 10) and repoB (score 10).
	first := got.Data[0]
	if first.Score < 30 {
		t.Errorf("top-ranked score should be >= 30, got %d (%+v)", first.Score, first)
	}
	if first.Score < got.Data[len(got.Data)-1].Score {
		t.Errorf("results not sorted descending by score: %+v", got.Data)
	}
}

func TestFindingsAggregateByRule(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()
	ctx := context.Background()

	seedRule := func(repoID, rule string, severity models.Severity) {
		scanID := uuid.New().String()
		if err := env.Store.CreateScan(ctx, &models.Scan{
			ID: scanID, UserID: env.UserID, RepoID: repoID, Branch: "main",
			Status: models.ScanStatusCompleted, ToolsSelected: "[]", ToolsCompleted: "[]",
			ToolsFailed: "[]", CoverageSummary: "{}",
		}); err != nil {
			t.Fatalf("seedRule scan: %v", err)
		}
		if err := env.Store.CreateFinding(ctx, &models.Finding{
			ID:          uuid.New().String(),
			ScanID:      scanID,
			RepoID:      repoID,
			Fingerprint: uuid.New().String(),
			ToolName:    "test",
			Category:    models.CategorySAST,
			Severity:    severity,
			Title:       "t",
			FilePath:    "x.go",
			RuleID:      rule,
			Status:      models.StatusOpen,
		}); err != nil {
			t.Fatalf("seedRule finding: %v", err)
		}
	}

	// log4j-1.2 across 4 repos
	for i := 0; i < 4; i++ {
		r := createRepoWithSource(t, env, fmt.Sprintf("log4j-%d", i), models.SourceTypeLocal)
		seedRule(r, "log4j-1.2", models.SeverityHigh)
	}
	// openssl-1.0.2k across 2 repos
	for i := 0; i < 2; i++ {
		r := createRepoWithSource(t, env, fmt.Sprintf("openssl-%d", i), models.SourceTypeLocal)
		seedRule(r, "openssl-1.0.2k", models.SeverityMedium)
	}
	// internal-only in 1 repo
	r := createRepoWithSource(t, env, "internal-1", models.SourceTypeLocal)
	seedRule(r, "internal-only", models.SeverityLow)

	w := env.doRequest(http.MethodGet, "/api/findings/aggregate?group_by=rule_id&limit=10", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var got struct {
		Data []struct {
			Key       string   `json:"key"`
			Repos     int      `json:"repos"`
			Findings  int      `json:"findings"`
			Tool      string   `json:"tool"`
			Title     string   `json:"title"`
			Severity  string   `json:"severity"`
			RepoIDs   []string `json:"repo_ids"`
			RepoNames []string `json:"repo_names"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Data) != 3 {
		t.Fatalf("expected 3 rule_ids, got %d: %+v", len(got.Data), got.Data)
	}
	if got.Data[0].Key != "log4j-1.2" || got.Data[0].Repos != 4 {
		t.Errorf("expected log4j-1.2 first with repos=4, got %+v", got.Data[0])
	}
	if got.Data[0].Tool != "test" || got.Data[0].Severity != string(models.SeverityHigh) {
		t.Errorf("log4j metadata = %+v", got.Data[0])
	}
	if len(got.Data[0].RepoIDs) != 4 || len(got.Data[0].RepoNames) != 4 {
		t.Errorf("log4j repos not paired: ids=%v names=%v", got.Data[0].RepoIDs, got.Data[0].RepoNames)
	}
	wantNames := []string{"log4j-0", "log4j-1", "log4j-2", "log4j-3"}
	if fmt.Sprint(got.Data[0].RepoNames) != fmt.Sprint(wantNames) {
		t.Errorf("log4j names=%v want %v", got.Data[0].RepoNames, wantNames)
	}
	if got.Data[1].Key != "openssl-1.0.2k" || got.Data[1].Repos != 2 {
		t.Errorf("expected openssl-1.0.2k second with repos=2, got %+v", got.Data[1])
	}
}

func TestFindingsByRepo(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()
	ctx := context.Background()

	repoA := createRepoWithSource(t, env, "alpha", models.SourceTypeLocal)
	repoB := createRepoWithSource(t, env, "beta", models.SourceTypeLocal)
	scanA := uuid.New().String()
	scanB := uuid.New().String()
	for _, s := range []struct {
		id, repo string
	}{{scanA, repoA}, {scanB, repoB}} {
		if err := env.Store.CreateScan(ctx, &models.Scan{
			ID: s.id, UserID: env.UserID, RepoID: s.repo, Branch: "main",
			Status: models.ScanStatusCompleted, ToolsSelected: "[]", ToolsCompleted: "[]",
			ToolsFailed: "[]", CoverageSummary: "{}",
		}); err != nil {
			t.Fatal(err)
		}
	}
	mustFinding := func(scan, repo, path, rule string, sev models.Severity) {
		if err := env.Store.CreateFinding(ctx, &models.Finding{
			ID: uuid.New().String(), ScanID: scan, RepoID: repo,
			Fingerprint: uuid.New().String(), ToolName: "gosec",
			Category: models.CategorySAST, Severity: sev, Title: rule,
			FilePath: path, RuleID: rule, Status: models.StatusOpen,
		}); err != nil {
			t.Fatal(err)
		}
	}
	// Distinct file+rule so current-open location dedup does not collapse
	// two different issues in the same repo into one count.
	mustFinding(scanA, repoA, "a.go", "G101", models.SeverityCritical)
	mustFinding(scanA, repoA, "b.go", "G102", models.SeverityHigh)
	mustFinding(scanB, repoB, "c.go", "G103", models.SeverityLow)

	w := env.doRequest(http.MethodGet, "/api/findings/by-repo", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var got struct {
		Data []struct {
			Name     string `json:"name"`
			Total    int    `json:"total"`
			Critical int    `json:"critical"`
			High     int    `json:"high"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Data) != 2 {
		t.Fatalf("repos=%d want 2: %+v", len(got.Data), got.Data)
	}
	if got.Data[0].Name != "alpha" || got.Data[0].Total != 2 || got.Data[0].Critical != 1 || got.Data[0].High != 1 {
		t.Fatalf("alpha first = %+v", got.Data[0])
	}
	if got.Data[1].Name != "beta" || got.Data[1].Total != 1 {
		t.Fatalf("beta = %+v", got.Data[1])
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

func TestFleetPostureDedupsRescansAndBranches(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()
	repoID := env.createRepo(t)
	fp := "loc-same-issue"
	seedScanWithFindingsOn(t, env, repoID, "main", []seedFinding{
		{models.SeverityHigh, "r1", fp, "a.go"},
		{models.SeverityMedium, "r2", "only-main", "main.go"},
	})
	seedScanWithFindingsOn(t, env, repoID, "main", []seedFinding{
		{models.SeverityHigh, "r1", fp, "a.go"},
		{models.SeverityMedium, "r2", "only-main", "main.go"},
	})
	seedScanWithFindingsOn(t, env, repoID, "dev", []seedFinding{
		{models.SeverityHigh, "r1", fp, "a.go"},
		{models.SeverityLow, "r3", "only-dev", "dev.go"},
	})

	w := env.doRequest(http.MethodGet, "/api/fleet/posture", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	sev := got["data"].(map[string]any)["open_findings_by_severity"].(map[string]any)
	// shared issue once + main-only + dev-only = 3, not 6
	if sev["high"].(float64) != 1 || sev["medium"].(float64) != 1 || sev["low"].(float64) != 1 {
		t.Fatalf("expected 1 high / 1 medium / 1 low after dedup, got %v", sev)
	}
}

type seedFinding struct {
	Severity models.Severity
	RuleID   string
	Ident    string
	File     string
}

func seedScanWithFindingsOn(t *testing.T, env *testEnv, repoID, branch string, findings []seedFinding) string {
	t.Helper()
	ctx := context.Background()
	scanID := uuid.New().String()
	if err := env.Store.CreateScan(ctx, &models.Scan{
		ID:              scanID,
		UserID:          env.UserID,
		RepoID:          repoID,
		Branch:          branch,
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
			ID:                  uuid.New().String(),
			ScanID:              scanID,
			RepoID:              repoID,
			Fingerprint:         f.Ident,
			LocationFingerprint: f.Ident,
			ToolName:            "test",
			Category:            models.CategorySAST,
			Severity:            f.Severity,
			Title:               "t",
			FilePath:            f.File,
			RuleID:              f.RuleID,
			Status:              models.StatusOpen,
		}
		if err := env.Store.CreateFinding(ctx, &ff); err != nil {
			t.Fatalf("CreateFinding[%d]: %v", i, err)
		}
	}
	return scanID
}
