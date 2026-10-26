package db

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/scan/runner"
	"github.com/alphabravocompany/thewolf/internal/scan/suppress"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

func TestScanIgnorePolicySQLite(t *testing.T) {
	name := filepath.Join(t.TempDir(), "policy.db")
	s, err := NewSQLite(name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	want := testScanIgnorePolicy(t, s, s.db)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewSQLite(name)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	checkReopenedScanPolicy(t, reopened, want)
}
func TestScanIgnorePolicyPostgres(t *testing.T) {
	s := newClusterPostgresStore(t)
	want := testScanIgnorePolicy(t, s, s.db)
	var schema string
	if err := s.db.Get(&schema, "SELECT current_schema()"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewPostgres(postgresDSNWithSearchPath(t, os.Getenv("WOLF_TEST_POSTGRES_DSN"), schema))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	checkReopenedScanPolicy(t, reopened, want)
}
func checkReopenedScanPolicy(t *testing.T, store Store, want *models.Scan) {
	t.Helper()
	scan, findings, err := store.GetScanFindingSnapshot(t.Context(), want.ID)
	if err != nil || scan.IgnorePolicyJSON != want.IgnorePolicyJSON || scan.FindingCount != 1 {
		t.Fatalf("reopen lost policy/count: %+v %v", scan, err)
	}
	if _, _, err := suppress.ApplyScan(t.Context(), findings, scan, ""); err != nil {
		t.Fatal(err)
	}
	visible := 0
	for _, f := range findings {
		if !f.Suppressed {
			visible++
		}
	}
	if visible != 1 {
		t.Fatalf("reopened visibility=%d", visible)
	}
}
func testScanIgnorePolicy(t *testing.T, store Store, database *sqlx.DB) *models.Scan {
	ctx := t.Context()
	user, repo := uuid.NewString(), uuid.NewString()
	root := t.TempDir()
	if err := store.CreateUser(ctx, &models.User{ID: user, Email: user + "@example.test", PasswordHash: "hash"}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateRepo(ctx, &models.Repo{ID: repo, UserID: user, Name: "snapshot", SourcePath: root}); err != nil {
		t.Fatal(err)
	}
	scan := &models.Scan{ID: uuid.NewString(), RepoID: repo, UserID: user, Status: models.ScanStatusRunning, LeaseToken: "owner"}
	if err := store.CreateScan(ctx, scan); err != nil {
		t.Fatal(err)
	}
	rows := []models.Finding{
		{ID: uuid.NewString(), ScanID: scan.ID, RepoID: repo, ToolName: "alpha", FilePath: "app.go", RuleID: "R1", FineCategory: "sql-injection", Category: models.CategorySAST, Severity: models.SeverityCritical, Status: models.StatusOpen},
		{ID: uuid.NewString(), ScanID: scan.ID, RepoID: repo, ToolName: "alpha", FilePath: "app.go", RuleID: "R2", FineCategory: "sql-injection", Category: models.CategorySAST, Severity: models.SeverityHigh, Status: models.StatusOpen},
	}
	if err := store.CreateFindings(ctx, rows); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, database.Rebind("UPDATE scans SET status='completed', finding_count=99 WHERE id=?"), scan.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, "ALTER TABLE scans DROP COLUMN ignore_policy_json"); err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(); err != nil {
		t.Fatal(err)
	}
	legacy, err := store.GetScanByID(ctx, scan.ID)
	if err != nil || legacy.IgnorePolicyJSON != "" || legacy.FindingCount != 2 {
		t.Fatalf("legacy migration fabricated policy or kept stale count: %+v %v", legacy, err)
	}
	// Resume this synthetic migration fixture explicitly; ordinary leased
	// progress writes correctly cannot reopen a terminal scan.
	if _, err := database.ExecContext(ctx, database.Rebind("UPDATE scans SET status='running' WHERE id=?"), scan.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateScan(ctx, scan); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(root, ".wolfignore")
	if err := os.WriteFile(name, []byte("** category=\n"), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := store.GetScanByID(ctx, scan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CaptureScanIgnorePolicy(ctx, scan.ID, "owner", root); err == nil {
		t.Fatal("invalid policy captured")
	}
	after, err := store.GetScanByID(ctx, scan.ID)
	if err != nil || after.IgnorePolicyJSON != "" || after.FindingsRevision != before.FindingsRevision {
		t.Fatalf("failed capture changed state: %+v %v", after, err)
	}
	if err := os.WriteFile(name, []byte("app.go rule=R1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := store.CaptureScanIgnorePolicy(ctx, scan.ID, "stale", root); !errors.Is(err, ErrStaleScanLease) {
		t.Fatalf("stale lease capture: %v", err)
	}
	trigger := `CREATE TRIGGER reject_ignore_policy BEFORE UPDATE OF ignore_policy_json ON scans BEGIN SELECT RAISE(ABORT,'fixture policy failure'); END`
	if database.DriverName() == "postgres" {
		if _, err := database.Exec(`CREATE FUNCTION reject_ignore_policy_fn() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture policy failure'; END $$`); err != nil {
			t.Fatal(err)
		}
		trigger = `CREATE TRIGGER reject_ignore_policy BEFORE UPDATE OF ignore_policy_json ON scans FOR EACH ROW EXECUTE FUNCTION reject_ignore_policy_fn()`
	}
	if _, err := database.Exec(trigger); err != nil {
		t.Fatal(err)
	}
	if err := store.CaptureScanIgnorePolicy(ctx, scan.ID, "owner", root); err == nil {
		t.Fatal("late write failure accepted")
	}
	failed, failedRows, err := store.GetScanFindingSnapshot(ctx, scan.ID)
	if err != nil || failed.IgnorePolicyJSON != "" || failed.FindingsRevision != before.FindingsRevision {
		t.Fatalf("late failure changed scan: %+v %v", failed, err)
	}
	for _, f := range failedRows {
		if f.Suppressed {
			t.Fatal("late failure left source flags committed")
		}
	}
	drop := "DROP TRIGGER reject_ignore_policy"
	if database.DriverName() == "postgres" {
		drop += " ON scans"
	}
	if _, err := database.Exec(drop); err != nil {
		t.Fatal(err)
	}
	if err := store.CaptureScanIgnorePolicy(ctx, scan.ID, "owner", root); err != nil {
		t.Fatal(err)
	}
	frozen, err := store.GetScanByID(ctx, scan.ID)
	if err != nil || frozen.IgnorePolicyJSON == "" || frozen.FindingsRevision != before.FindingsRevision+1 {
		t.Fatalf("missing versioned snapshot: %+v %v", frozen, err)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if err := store.CaptureScanIgnorePolicy(ctx, scan.ID, "owner", root); err != nil {
		t.Fatal(err)
	}
	after, err = store.GetScanByID(ctx, scan.ID)
	if err != nil || after.IgnorePolicyJSON != frozen.IgnorePolicyJSON || after.FindingsRevision != frozen.FindingsRevision {
		t.Fatalf("retry replaced snapshot: %+v %v", after, err)
	}
	scan.CommitSHA = "different-source"
	if err := store.UpdateScan(ctx, scan); err != nil {
		t.Fatal(err)
	}
	if err := store.CaptureScanIgnorePolicy(ctx, scan.ID, "owner", root); err == nil {
		t.Fatal("source-changing retry reused old policy")
	}
	scan.CommitSHA = frozen.CommitSHA
	if err := store.UpdateScan(ctx, scan); err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(); err != nil {
		t.Fatal(err)
	}
	after, err = store.GetScanByID(ctx, scan.ID)
	if err != nil || after.IgnorePolicyJSON != frozen.IgnorePolicyJSON {
		t.Fatalf("migration reset policy: %+v %v", after, err)
	}
	if err := store.FinalizeFindingClusters(ctx, scan.ID, "owner", runner.Deduplicate(rows)); err != nil {
		t.Fatal(err)
	}
	// Whole-struct progress writes must never overwrite the captured policy.
	scan.Status = models.ScanStatusCompleted
	if err := store.UpdateScan(ctx, scan); err != nil {
		t.Fatal(err)
	}
	rule := &models.FindingSuppression{ID: uuid.NewString(), RepoID: repo, CreatedBy: user, ScopeType: models.SuppressionScopeRule, ScopeValue: "R2", Reason: "temporary"}
	if err := store.CreateFindingSuppression(ctx, rule); err != nil {
		t.Fatal(err)
	}
	after, err = store.GetScanByID(ctx, scan.ID)
	if err != nil || after.FindingCount != 0 || after.IgnorePolicyJSON != frozen.IgnorePolicyJSON {
		t.Fatalf("durable create lost source policy/count: %+v %v", after, err)
	}
	if err := store.RevokeFindingSuppression(ctx, rule.ID, user); err != nil {
		t.Fatal(err)
	}
	after, findings, err := store.GetScanFindingSnapshot(ctx, scan.ID)
	if err != nil || after.FindingCount != 1 {
		t.Fatalf("revoke count: %+v %v", after, err)
	}
	projected, n, err := suppress.ApplyScan(ctx, findings, after, root)
	if err != nil || n != 0 || len(projected) != 1 || projected[0].ID != rows[1].ID {
		t.Fatalf("source policy after revoke: %+v %v", projected, err)
	}
	newPath := rows[0]
	newPath.ID, newPath.FilePath = uuid.NewString(), "not-observed.go"
	if err := store.CreateFinding(ctx, &newPath); err == nil {
		t.Fatal("accepted an uncaptured path into a frozen scan")
	}
	unchanged, err := store.GetScanByID(ctx, scan.ID)
	if err != nil || unchanged.FindingsRevision != after.FindingsRevision {
		t.Fatalf("rejected path advanced revision: %+v %v", unchanged, err)
	}
	return unchanged
}
