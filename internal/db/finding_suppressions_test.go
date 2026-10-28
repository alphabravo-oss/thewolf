package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

func TestSuppressionLifecyclePostgres(t *testing.T) {
	s := newClusterPostgresStore(t)
	testSuppressionLifecycle(t, s, s.db, func() { reconnectPostgresTestStore(s) })
}
func testSuppressionLifecycle(t *testing.T, s Store, database *sqlx.DB, restart func()) {
	ctx := t.Context()
	user, repo := uuid.NewString(), uuid.NewString()
	if err := s.CreateUser(ctx, &models.User{ID: user, Email: user + "@example.test", PasswordHash: "hash"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateRepo(ctx, &models.Repo{ID: repo, UserID: user, Name: "suppression", SourcePath: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	scans := map[string]*models.Scan{}
	for _, branch := range []string{"main", "dev", ""} {
		scan := &models.Scan{ID: uuid.NewString(), RepoID: repo, UserID: user, Branch: branch, Status: models.ScanStatusCompleted, FindingCount: 2}
		if err := s.CreateScan(ctx, scan); err != nil {
			t.Fatal(err)
		}
		scans[branch] = scan
		for _, key := range []string{"a", "z"} {
			f := &models.Finding{ID: scan.ID + key, ScanID: scan.ID, RepoID: repo, FilePath: key + ".go", RuleID: "rule", ToolName: "tool", Category: models.CategorySAST, Status: models.StatusOpen, Severity: models.SeverityHigh, CompositeScore: 7.5}
			if err := s.CreateFinding(ctx, f); err != nil {
				t.Fatal(err)
			}
		}
	}
	scan := scans["main"]
	if err := s.ReconcileVulnerabilities(ctx, scan.ID); err != nil {
		t.Fatal(err)
	}
	stale, err := s.GetFindingByID(ctx, scan.ID+"a")
	if err != nil {
		t.Fatal(err)
	}
	before, _, err := s.GetScanFindingSnapshot(ctx, scan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertQualityGateResult(ctx, &models.QualityGateResult{ID: uuid.NewString(), ScanID: scan.ID, ScanStatus: models.ScanStatusCompleted, FindingsRevision: before.FindingsRevision, PolicyID: "default", Status: "fail", SummaryJSON: "{}", MatchedRulesJSON: "[]"}); err != nil {
		t.Fatal(err)
	}
	rule := &models.FindingSuppression{ID: uuid.NewString(), RepoID: repo, CreatedBy: user, ScopeType: models.SuppressionScopeRule, ScopeValue: "rule", Branch: "main", Reason: "accepted"}
	snapshot := func() string {
		t.Helper()
		var findings []models.Finding
		var vulnerabilities []models.Vulnerability
		var rules []models.FindingSuppression
		var audits []models.FindingSuppressionAudit
		var scans []models.Scan
		for _, q := range []struct {
			dest  any
			query string
		}{{&vulnerabilities, "SELECT * FROM vulnerabilities ORDER BY id"}, {&findings, "SELECT * FROM findings ORDER BY id"}, {&rules, "SELECT * FROM finding_suppressions ORDER BY id"}, {&audits, "SELECT * FROM finding_suppression_audit ORDER BY id"}, {&scans, "SELECT * FROM scans ORDER BY id"}} {
			if err := database.Select(q.dest, q.query); err != nil {
				t.Fatal(err)
			}
		}
		data, err := json.Marshal([]any{findings, rules, audits, scans, vulnerabilities})
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	reject := func(table, event string) func() {
		t.Helper()
		query := "CREATE TRIGGER reject_suppression BEFORE " + event + " ON " + table + " BEGIN SELECT RAISE(ABORT,'fixture suppression failure'); END"
		if database.DriverName() == "postgres" {
			if _, err := database.Exec(`CREATE OR REPLACE FUNCTION reject_suppression_fn() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture suppression failure'; END; $$`); err != nil {
				t.Fatal(err)
			}
			query = "CREATE TRIGGER reject_suppression BEFORE " + event + " ON " + table + " FOR EACH ROW EXECUTE FUNCTION reject_suppression_fn()"
		}
		if _, err := database.Exec(query); err != nil {
			t.Fatal(err)
		}
		return func() {
			drop := "DROP TRIGGER reject_suppression"
			if database.DriverName() == "postgres" {
				drop += " ON " + table
			}
			if _, err := database.Exec(drop); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Every mutation must roll back the rule, flags, scan counts/revisions and audit.
	for _, fault := range []struct{ table, event string }{{"finding_suppressions", "INSERT"}, {"findings", "UPDATE"}, {"scans", "UPDATE"}, {"vulnerabilities", "UPDATE"}, {"finding_suppression_audit", "INSERT"}} {
		old := snapshot()
		remove := reject(fault.table, fault.event)
		err := s.CreateFindingSuppression(ctx, rule)
		remove()
		if err == nil || snapshot() != old {
			t.Fatalf("create rollback %s: %v", fault.table, err)
		}
	}
	if err := s.CreateFindingSuppression(ctx, rule); err != nil {
		t.Fatal(err)
	}
	assertFlags := func(id string) {
		t.Helper()
		canonical, err := s.ListVulnerabilitiesByScan(ctx, scan.ID)
		if err != nil || len(canonical) != 2 {
			t.Fatalf("canonical observations: %+v %v", canonical, err)
		}
		for _, v := range canonical {
			if v.Suppressed != (id != "") || v.ScanID != scan.ID {
				t.Fatalf("canonical suppression: %+v", v)
			}
		}
		for branch, scan := range scans {
			findings, err := s.ListFindingEvidenceByScan(ctx, scan.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, f := range findings {
				if f.SuppressionID == "external-id" {
					if !f.Suppressed || f.SuppressedReason != "imported accepted risk" {
						t.Fatal("imported decision lost")
					}
					continue
				}
				want := id != "" && branch == "main"
				if f.Suppressed != want || (want && f.SuppressionID != id) || (!want && f.SuppressionID != "") {
					t.Fatalf("branch %q: %+v", branch, f)
				}
				if f.CompositeScore != 7.5 {
					t.Fatal("rule mutation overwrote score")
				}
			}
			saved, err := s.GetScanByID(ctx, scan.ID)
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			for _, f := range findings {
				if f.SuppressionID != "external-id" {
					count++
				}
			}
			if id != "" && branch == "main" {
				count = 0
			}
			if saved.FindingCount != count {
				t.Fatalf("count=%d want=%d branch=%q", saved.FindingCount, count, branch)
			}
		}
	}
	assertFlags(rule.ID)
	if _, err := s.GetQualityGateResult(ctx, scan.ID, "default"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("stale gate survived: %v", err)
	}
	after, err := s.GetScanByID(ctx, scan.ID)
	if err != nil || after.FindingsRevision != before.FindingsRevision+1 {
		t.Fatalf("revision did not advance once: %+v %v", after, err)
	}
	// A stale enrichment snapshot cannot erase a newer durable decision.
	if err := s.UpdateFinding(ctx, stale); err != nil {
		t.Fatal(err)
	}
	assertFlags(rule.ID)
	// New rows use the current rules inside their insert transaction.
	late := *stale
	late.ID = uuid.NewString()
	late.FilePath = "late.go"
	if err := s.CreateFinding(ctx, &late); err != nil {
		t.Fatal(err)
	}
	if !late.Suppressed || late.SuppressionID != rule.ID {
		t.Fatal("new observation ignored durable rule")
	}
	// Import-owned suppression with an unrelated ID remains an independent audit decision.
	independent := late
	independent.ID = uuid.NewString()
	independent.SuppressionID = "external-id"
	independent.SuppressedReason = "imported accepted risk"
	if err := s.CreateFinding(ctx, &independent); err != nil {
		t.Fatal(err)
	}

	for _, fault := range []struct{ table, event string }{{"finding_suppressions", "UPDATE"}, {"findings", "UPDATE"}, {"finding_suppression_audit", "INSERT"}} {
		old := snapshot()
		remove := reject(fault.table, fault.event)
		err := s.RevokeFindingSuppression(ctx, rule.ID, user)
		remove()
		if err == nil || snapshot() != old {
			t.Fatalf("revoke rollback %s: %v", fault.table, err)
		}
	}
	// A newer temporary rule takes precedence, then expiry must fall back to the
	// still-active older rule instead of exposing accepted findings.
	expiry := time.Now().UTC().Add(time.Hour)
	temporary := *rule
	temporary.ID = uuid.NewString()
	temporary.ExpiresAt = &expiry
	temporary.Reason = "temporary"
	if err := s.CreateFindingSuppression(ctx, &temporary); err != nil {
		t.Fatal(err)
	}
	assertFlags(temporary.ID)
	for _, fault := range []struct{ table, event string }{{"findings", "UPDATE"}, {"finding_suppression_audit", "INSERT"}} {
		old := snapshot()
		remove := reject(fault.table, fault.event)
		n, err := s.ExpireFindingSuppressions(ctx, expiry)
		remove()
		if err == nil || n != 0 || snapshot() != old {
			t.Fatalf("expiry rollback %s: %d %v", fault.table, n, err)
		}
	}
	restart()
	// The store was reopened; use its current connection for remaining inspections.
	if store, ok := s.(*PostgresStore); ok {
		database = store.db
	}
	if n, err := s.ExpireFindingSuppressions(ctx, expiry); err != nil || n != 1 {
		t.Fatalf("expiry: %d %v", n, err)
	}
	assertFlags(rule.ID)
	if n, err := s.ExpireFindingSuppressions(ctx, expiry); err != nil || n != 0 {
		t.Fatalf("repeat expiry: %d %v", n, err)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if err := s.RevokeFindingSuppression(ctx, rule.ID, user); err != nil {
			t.Error(err)
		}
	}()
	go func() {
		defer wg.Done()
		if err := s.UpdateFindingStatus(ctx, scan.ID+"a", models.StatusWontFix); err != nil {
			t.Error(err)
		}
	}()
	wg.Wait()
	assertFlags("")
	f, err := s.GetFindingByID(ctx, scan.ID+"a")
	if err != nil || f.Status != models.StatusWontFix {
		t.Fatalf("triage lost: %+v %v", f, err)
	}
	var audits []models.FindingSuppressionAudit
	if err := database.Select(&audits, "SELECT * FROM finding_suppression_audit ORDER BY created_at,id"); err != nil {
		t.Fatal(err)
	}
	for _, audit := range audits {
		wantActor := user
		if audit.Action == "expired" {
			wantActor = ""
			if audit.SuppressionID != temporary.ID {
				t.Fatal("wrong rule expired")
			}
		}
		if audit.ActorID != wantActor {
			t.Fatalf("audit actor: %+v", audit)
		}
	}
	if len(audits) != 4 {
		t.Fatalf("audit entries=%d want=4", len(audits))
	}
	if err := s.RevokeFindingSuppression(ctx, rule.ID, user); err != nil {
		t.Fatal(err)
	}
	var total int
	if err := database.Get(&total, "SELECT COUNT(*) FROM finding_suppression_audit"); err != nil || total != 4 {
		t.Fatalf("revoke not idempotent: %d %v", total, err)
	}
}
