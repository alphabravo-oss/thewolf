package db

import (
	"encoding/json"
	"github.com/jmoiron/sqlx"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/scan/runner"
	"github.com/alphabravocompany/thewolf/internal/scan/scorer"
	"github.com/google/uuid"
)

func TestPartialClusterSuppressionSQLite(t *testing.T) {
	testPartialClusterSuppression(t, newTestStore(t))
}
func TestPartialClusterSuppressionPostgres(t *testing.T) {
	testPartialClusterSuppression(t, newClusterPostgresStore(t))
}
func testPartialClusterSuppression(t *testing.T, s Store) {
	ctx := t.Context()
	user, repo := uuid.NewString(), uuid.NewString()
	if err := s.CreateUser(ctx, &models.User{ID: user, Email: user + "@example.test", PasswordHash: "hash"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateRepo(ctx, &models.Repo{ID: repo, UserID: user, Name: "cluster suppression", SourcePath: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	scan := &models.Scan{ID: uuid.NewString(), RepoID: repo, UserID: user, Branch: "main", Status: models.ScanStatusRunning, ToolsSelected: `["alpha","beta"]`, ToolsCompleted: `["alpha","beta"]`, ToolsFailed: `[]`}
	if err := s.CreateScan(ctx, scan); err != nil {
		t.Fatal(err)
	}
	rows := []models.Finding{
		{ID: uuid.NewString(), ScanID: scan.ID, RepoID: repo, ToolName: "alpha", Category: models.CategorySAST, Severity: models.SeverityCritical, Status: models.StatusOpen, FilePath: "app.go", RuleID: "alpha-rule", FineCategory: "sql-injection", StableFingerprint: "original-identity", Title: "alpha native"},
		{ID: uuid.NewString(), ScanID: scan.ID, RepoID: repo, ToolName: "beta", Category: models.CategorySAST, Severity: models.SeverityHigh, Status: models.StatusOpen, FilePath: "app.go", RuleID: "beta-rule", FineCategory: "sql-injection", StableFingerprint: "other-native-identity", Title: "beta native"},
	}
	if err := s.CreateFindings(ctx, rows); err != nil {
		t.Fatal(err)
	}
	primaries := runner.Deduplicate(rows)
	scorer.ScoreFindings(primaries)
	if err := s.FinalizeFindingClusters(ctx, scan.ID, "", primaries); err != nil {
		t.Fatal(err)
	}
	if err := s.PersistFindingBaseline(ctx, scan.ID, "", at); err != nil {
		t.Fatal(err)
	}
	scan.Status, scan.CompletedAt, scan.FindingCount = models.ScanStatusCompleted, &at, 1
	if err := s.UpdateScan(ctx, scan); err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcileVulnerabilities(ctx, scan.ID); err != nil {
		t.Fatal(err)
	}
	initial, err := s.ListVulnerabilitiesByScan(ctx, scan.ID)
	if err != nil || len(initial) != 1 {
		t.Fatalf("canonical: %+v %v", initial, err)
	}
	fix := &models.Fix{ID: uuid.NewString(), UserID: user, ScanID: scan.ID}
	if err := s.CreateFix(ctx, fix); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateFixItem(ctx, &models.FixItem{ID: uuid.NewString(), FixID: fix.ID, FindingID: rows[0].ID}); err != nil {
		t.Fatal(err)
	}
	rule := &models.FindingSuppression{ID: uuid.NewString(), RepoID: repo, CreatedBy: user, ScopeType: models.SuppressionScopeRule, ScopeValue: "alpha-rule", Reason: "accepted tool-specific false positive"}
	if err := s.CreateFindingSuppression(ctx, rule); err != nil {
		t.Fatal(err)
	}
	current, err := s.ListFindingsByScan(ctx, scan.ID)
	if err != nil || len(current) != 1 || current[0].Suppressed || current[0].ID != rows[1].ID {
		t.Fatalf("unsuppressed member disappeared behind primary: %+v %v", current, err)
	}

	check := func(primaryID string, hidden bool) {
		t.Helper()
		stored, evidence, err := s.GetScanFindingSnapshot(ctx, scan.ID)
		if err != nil || len(evidence) != 1 || evidence[0].ID != primaryID || evidence[0].Suppressed != hidden {
			t.Fatalf("primary: %+v %v", evidence, err)
		}
		wantCount := 1
		if hidden {
			wantCount = 0
		}
		if stored.FindingCount != wantCount {
			t.Fatalf("header: %+v", stored)
		}
		primary := evidence[0]
		if primary.Confidence != "medium" && primary.Confidence != "" {
			t.Fatalf("confidence: %+v", primary)
		}
		if len(primary.CorroboratedBy) != 2 || primary.BaselineIdentityKey != primary.StableFingerprint || primary.FirstSeenAt == nil {
			t.Fatalf("promoted metadata: %+v", primary)
		}
		expected := primary
		scorer.ScoreFinding(&expected)
		if math.Abs(expected.CompositeScore-primary.CompositeScore) > 1e-6 {
			t.Fatalf("borrowed score: %+v", primary)
		}
		members, err := s.ListFindingClusterEvidence(ctx, rows[0].ID)
		if err != nil || len(members) != 2 {
			t.Fatalf("old URL lost evidence: %+v %v", members, err)
		}
		for _, f := range members {
			if f.ClusterID != primaryID {
				t.Fatalf("stale member link: %+v", f)
			}
		}
		for _, original := range rows {
			f, err := s.GetFindingByID(ctx, original.ID)
			if err != nil || f.Title != original.Title || f.RuleID != original.RuleID || f.StableFingerprint != original.StableFingerprint {
				t.Fatalf("native identity overwritten: %+v %v", f, err)
			}
		}
		items, err := s.ListFixItemsByFix(ctx, fix.ID)
		if err != nil || len(items) != 1 || items[0].FindingID != rows[0].ID {
			t.Fatalf("foreign reference lost: %+v %v", items, err)
		}
		canonical, err := s.ListVulnerabilitiesByScan(ctx, scan.ID)
		if err != nil || len(canonical) != 1 || canonical[0].ID != initial[0].ID || canonical[0].CanonicalKey != initial[0].CanonicalKey || canonical[0].Title != primary.Title || canonical[0].Suppressed != hidden {
			t.Fatalf("canonical identity/metadata drift: %+v %v", canonical, err)
		}
	}
	check(rows[1].ID, false)
	for range 2 {
		if err := s.ReconcileVulnerabilities(ctx, scan.ID); err != nil {
			t.Fatal(err)
		}
		check(rows[1].ID, false)
	}
	second := *rule
	second.ID = uuid.NewString()
	second.ScopeValue = "beta-rule"
	if err := s.CreateFindingSuppression(ctx, &second); err != nil {
		t.Fatal(err)
	}
	check(rows[0].ID, true)
	if err := s.RevokeFindingSuppression(ctx, rule.ID, user); err != nil {
		t.Fatal(err)
	}
	check(rows[0].ID, false)
	if err := s.RevokeFindingSuppression(ctx, second.ID, user); err != nil {
		t.Fatal(err)
	}
	check(rows[0].ID, false)
	expiry := time.Now().UTC().Add(time.Hour)
	temporary := *rule
	temporary.ID = uuid.NewString()
	temporary.ExpiresAt = &expiry
	if err := s.CreateFindingSuppression(ctx, &temporary); err != nil {
		t.Fatal(err)
	}
	check(rows[1].ID, false)
	if n, err := s.ExpireFindingSuppressions(ctx, expiry); err != nil || n != 1 {
		t.Fatalf("expiry: %d %v", n, err)
	}
	check(rows[0].ID, false)
	// Existing manual membership remains one issue during promotion and replay.
	split, err := s.SplitVulnerability(ctx, repo, initial[0].ID, []string{rows[1].ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.MergeVulnerabilities(ctx, repo, split.ID, initial[0].ID); err != nil {
		t.Fatal(err)
	}
	rule.ID = uuid.NewString()
	if err := s.CreateFindingSuppression(ctx, rule); err != nil {
		t.Fatal(err)
	}
	check(rows[1].ID, false)
	if err := s.ReconcileVulnerabilities(ctx, scan.ID); err != nil {
		t.Fatal(err)
	}
	check(rows[1].ID, false)
	if err := s.RevokeFindingSuppression(ctx, rule.ID, user); err != nil {
		t.Fatal(err)
	}
	// Force a late failure after links, history and scores have changed.
	var database *sqlx.DB
	switch store := s.(type) {
	case *SQLiteStore:
		database = store.db
	case *PostgresStore:
		database = store.db
	}
	snapshot := func() string {
		t.Helper()
		var f []models.Finding
		var v []models.Vulnerability
		var scanRows []models.Scan
		var rules []models.FindingSuppression
		var audit []models.FindingSuppressionAudit
		for _, q := range []struct {
			dest  any
			query string
		}{{&f, "SELECT * FROM findings ORDER BY id"}, {&v, "SELECT * FROM vulnerabilities ORDER BY id"}, {&scanRows, "SELECT * FROM scans ORDER BY id"}, {&rules, "SELECT * FROM finding_suppressions ORDER BY id"}, {&audit, "SELECT * FROM finding_suppression_audit ORDER BY id"}} {
			if err := database.Select(q.dest, q.query); err != nil {
				t.Fatal(err)
			}
		}
		data, err := json.Marshal([]any{f, v, scanRows, rules, audit})
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	query := "CREATE TRIGGER reject_promotion BEFORE INSERT ON finding_suppression_audit BEGIN SELECT RAISE(ABORT,'fixture late promotion failure'); END"
	if database.DriverName() == "postgres" {
		if _, err := database.Exec(`CREATE FUNCTION reject_promotion_fn() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture late promotion failure'; END; $$`); err != nil {
			t.Fatal(err)
		}
		query = "CREATE TRIGGER reject_promotion BEFORE INSERT ON finding_suppression_audit FOR EACH ROW EXECUTE FUNCTION reject_promotion_fn()"
	}
	before := snapshot()
	if _, err := database.Exec(query); err != nil {
		t.Fatal(err)
	}
	rule.ID = uuid.NewString()
	if err := s.CreateFindingSuppression(ctx, rule); err == nil {
		t.Fatal("late failure ignored")
	}
	drop := "DROP TRIGGER reject_promotion"
	if database.DriverName() == "postgres" {
		drop += " ON finding_suppression_audit"
	}
	if _, err := database.Exec(drop); err != nil {
		t.Fatal(err)
	}
	if snapshot() != before {
		t.Fatal("promotion did not roll back atomically")
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if err := s.CreateFindingSuppression(ctx, rule); err != nil {
			t.Error(err)
		}
	}()
	go func() {
		defer wg.Done()
		if err := s.ReconcileVulnerabilities(ctx, scan.ID); err != nil {
			t.Error(err)
		}
	}()
	wg.Wait()
	check(rows[1].ID, false)
	// Legacy observations have no reproducible history cutoff. Promotion must not
	// copy a different member's history, or clear an unrelated legacy observation.
	unrelated := models.Finding{ID: uuid.NewString(), ScanID: scan.ID, RepoID: repo, ToolName: "gamma", Category: models.CategorySAST, Severity: models.SeverityLow, Status: models.StatusOpen, FilePath: "other.go", RuleID: "other", BaselineState: "existing"}
	if err := s.CreateFinding(ctx, &unrelated); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(database.Rebind("UPDATE scans SET baseline_resolved_at=NULL,baseline_scan_id='' WHERE id=?"), scan.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeFindingSuppression(ctx, rule.ID, user); err != nil {
		t.Fatal(err)
	}
	for _, original := range rows {
		f, err := s.GetFindingByID(ctx, original.ID)
		if err != nil || f.BaselineState != "" || f.BaselineIdentityKey != "" || f.FirstSeenAt != nil || f.IntroducedInScanID != "" {
			t.Fatalf("legacy promotion invented history: %+v %v", f, err)
		}
	}
	kept, err := s.GetFindingByID(ctx, unrelated.ID)
	if err != nil || kept.BaselineState != "existing" {
		t.Fatalf("unrelated history lost: %+v %v", kept, err)
	}
}
