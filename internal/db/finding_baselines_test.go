package db

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

func TestFindingBaselineSQLite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	s, err := NewSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	testFindingBaseline(t, s, s.db, func() {
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		reopened, err := NewSQLite(path)
		if err != nil {
			t.Fatal(err)
		}
		*s = *reopened
	})
}
func TestFindingBaselinePostgres(t *testing.T) {
	s := newClusterPostgresStore(t)
	testFindingBaseline(t, s, s.db, func() { reconnectPostgresTestStore(s) })
}
func testFindingBaseline(t *testing.T, s Store, database *sqlx.DB, restart func()) {
	ctx := t.Context()
	user, repo := uuid.NewString(), uuid.NewString()
	if err := s.CreateUser(ctx, &models.User{ID: user, Email: user + "@example.test", PasswordHash: "hash"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateRepo(ctx, &models.Repo{ID: repo, UserID: user, Name: "history", SourceType: models.SourceTypeLocal, SourcePath: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	makeScan := func(id, branch string, offset int, status models.ScanStatus) *models.Scan {
		t.Helper()
		at := start.Add(time.Duration(offset) * time.Hour)
		scan := &models.Scan{ToolsSelected: `["a"]`, ToolsCompleted: `["a"]`, ToolsFailed: `[]`, ID: id, UserID: user, RepoID: repo, Branch: branch, Status: status, CompletedAt: &at, SourceType: models.SourceTypeLocal}
		if err := s.CreateScan(ctx, scan); err != nil {
			t.Fatal(err)
		}
		return scan
	}
	makeFinding := func(id string, scan *models.Scan, key string) *models.Finding {
		t.Helper()
		f := &models.Finding{ID: id, RepoID: repo, ScanID: scan.ID, ToolName: "a", Title: key, RuleID: key, FineCategory: key, Category: models.CategorySAST, Severity: models.SeverityHigh, Status: models.StatusOpen, FilePath: "app.go", StableFingerprint: key, Fingerprint: key}
		if err := s.CreateFinding(ctx, f); err != nil {
			t.Fatal(err)
		}
		return f
	}
	original := makeScan("original", "main", 0, models.ScanStatusCompleted)
	makeFinding("old-reappearing", original, "reappearing")
	makeFinding("old-backlog", original, "backlog")
	// Existing observations survive additive/repeated migration without invented dates.
	for _, column := range []struct{ table, name string }{{"scans", "baseline_scan_id"}, {"scans", "baseline_resolved_at"}, {"findings", "first_seen_at"}, {"findings", "last_seen_at"}} {
		if _, err := database.Exec("ALTER TABLE " + column.table + " DROP COLUMN " + column.name); err != nil {
			t.Fatal(err)
		}
	}
	reconnectPostgresTestStore(s)
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	legacy, err := s.GetFindingByID(ctx, "old-backlog")
	if err != nil || legacy.FirstSeenAt != nil || legacy.LastSeenAt != nil {
		t.Fatalf("invented legacy observation: %+v %v", legacy, err)
	}
	if err := s.PersistFindingBaseline(ctx, original.ID, "", *original.CompletedAt); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateScanBaseline(ctx, &models.ScanBaseline{ID: uuid.NewString(), RepoID: repo, Branch: "main", Name: "auto", ScanID: original.ID, CreatedBy: user}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []models.ScanBaseline{
		{ID: uuid.NewString(), RepoID: repo, Branch: "wrong", Name: "wrong-branch", ScanID: original.ID, CreatedBy: user},
		{ID: uuid.NewString(), RepoID: repo, Branch: "main", Name: "wrong-owner", ScanID: original.ID, CreatedBy: "another-user"},
	} {
		if err := s.CreateScanBaseline(ctx, &bad); !errors.Is(err, ErrInvalidBaseline) {
			t.Fatalf("invalid baseline accepted: %v", err)
		}
	}
	// More than 20 intervening scans must not erase first-seen history.
	var previous *models.Scan
	for i := 1; i <= 24; i++ {
		previous = makeScan(fmt.Sprintf("history-%02d", i), "main", i, models.ScanStatusCompleted)
		makeFinding(fmt.Sprintf("backlog-%02d", i), previous, "backlog")
	}
	makeFinding("previous-new", previous, "new-after-baseline")
	makeFinding("foreign-branch", makeScan("other", "feature", 25, models.ScanStatusCompleted), "branch-only")
	makeFinding("future-evidence", makeScan("future", "main", 100, models.ScanStatusCompleted), "future-only")
	makeFinding("failed-evidence", makeScan("failed", "main", 25, models.ScanStatusFailed), "failed-only")
	if err := s.CreateScanBaseline(ctx, &models.ScanBaseline{ID: uuid.NewString(), RepoID: repo, Branch: "main", Name: "failed", ScanID: "failed", CreatedBy: user}); !errors.Is(err, ErrInvalidBaseline) {
		t.Fatalf("failed scan became baseline: %v", err)
	}
	cancelling := makeScan("cancel-request", "main", 26, models.ScanStatusCompleted)
	cancelling.CancelRequestedAt = cancelling.CompletedAt
	if err := s.UpdateScan(ctx, cancelling); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateScanBaseline(ctx, &models.ScanBaseline{ID: uuid.NewString(), RepoID: repo, Branch: "main", Name: "cancelling", ScanID: cancelling.ID, CreatedBy: user}); !errors.Is(err, ErrInvalidBaseline) {
		t.Fatalf("cancel-request scan became baseline: %v", err)
	}
	partial := makeScan("partial", "main", 27, models.ScanStatusCompleted)
	partial.ToolsSelected = `["a","b"]`
	if err := s.UpdateScan(ctx, partial); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateScanBaseline(ctx, &models.ScanBaseline{ID: uuid.NewString(), RepoID: repo, Branch: "main", Name: "partial", ScanID: partial.ID, CreatedBy: user}); !errors.Is(err, ErrInvalidBaseline) {
		t.Fatalf("incomplete scan became baseline: %v", err)
	}
	current := makeScan("current", "main", 30, models.ScanStatusRunning)
	primary := makeFinding("a-primary", current, "reappearing")
	member := makeFinding("b-member", current, "reappearing")
	backlog := makeFinding("c-backlog", current, "backlog")
	newIssue := makeFinding("d-new-after-baseline", current, "new-after-baseline")
	branch := makeFinding("e-branch", current, "branch-only")
	future := makeFinding("f-future", current, "future-only")
	failed := makeFinding("g-failed", current, "failed-only")
	clusters := []models.Finding{*primary, *backlog, *newIssue, *branch, *future, *failed}
	if err := s.FinalizeFindingClusters(ctx, current.ID, "", clusters); err != nil {
		t.Fatal(err)
	}
	stale, err := s.GetFindingByID(ctx, primary.ID)
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.GetScanByID(ctx, current.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Fail a late row update after the first primary and member have been written.
	trigger := `CREATE TRIGGER reject_baseline BEFORE UPDATE OF baseline_state ON findings WHEN NEW.id = 'g-failed' BEGIN SELECT RAISE(ABORT,'fixture baseline failure'); END`
	if database.DriverName() == "postgres" {
		if _, err := database.Exec(`CREATE FUNCTION reject_baseline_fn() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.id = 'g-failed' THEN RAISE EXCEPTION 'fixture baseline failure'; END IF; RETURN NEW; END $$`); err != nil {
			t.Fatal(err)
		}
		trigger = `CREATE TRIGGER reject_baseline BEFORE UPDATE OF baseline_state ON findings FOR EACH ROW EXECUTE FUNCTION reject_baseline_fn()`
	}
	if _, err := database.Exec(trigger); err != nil {
		t.Fatal(err)
	}
	if err := s.PersistFindingBaseline(ctx, current.ID, "", *current.CompletedAt); err == nil {
		t.Fatal("late write failure ignored")
	}
	after, err := s.GetScanByID(ctx, current.ID)
	if err != nil || after.BaselineResolvedAt != nil || after.FindingsRevision != before.FindingsRevision {
		t.Fatalf("failed transaction changed scan: %+v %v", after, err)
	}
	row, err := s.GetFindingByID(ctx, primary.ID)
	if err != nil || row.BaselineState != "" || row.FirstSeenAt != nil {
		t.Fatalf("partial baseline update: %+v %v", row, err)
	}
	drop := "DROP TRIGGER reject_baseline"
	if database.DriverName() == "postgres" {
		drop += " ON findings"
	}
	if _, err := database.Exec(drop); err != nil {
		t.Fatal(err)
	}
	if err := s.PersistFindingBaseline(ctx, current.ID, "", *current.CompletedAt); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []struct{ id, state, introduced string }{
		{primary.ID, "resurfaced", original.ID}, {member.ID, "resurfaced", original.ID}, {backlog.ID, "existing", original.ID},
		{newIssue.ID, "new", previous.ID}, {branch.ID, "new", current.ID}, {future.ID, "new", current.ID}, {failed.ID, "new", current.ID},
	} {
		row, err := s.GetFindingByID(ctx, expected.id)
		if err != nil || row.BaselineState != expected.state || row.IntroducedInScanID != expected.introduced || row.FirstSeenAt == nil || row.LastSeenAt == nil || !row.LastSeenAt.Equal(*current.CompletedAt) {
			t.Fatalf("history for %s: %+v %v", expected.id, row, err)
		}
	}
	after, err = s.GetScanByID(ctx, current.ID)
	if err != nil || after.BaselineScanID != original.ID || after.BaselineResolvedAt == nil || after.FindingsRevision != before.FindingsRevision+1 {
		t.Fatalf("baseline selection/revision: %+v %v", after, err)
	}
	revision := after.FindingsRevision
	restart()
	// Replaying with a later wall clock uses the pinned historical cutoff and is a no-op.
	if err := s.PersistFindingBaseline(ctx, current.ID, "", start.Add(200*time.Hour)); err != nil {
		t.Fatal(err)
	}
	after, err = s.GetScanByID(ctx, current.ID)
	if err != nil || after.FindingsRevision != revision {
		t.Fatalf("non-idempotent replay: %+v %v", after, err)
	}
	// Older enrichment/progress structs cannot erase authoritative baseline metadata.
	stale.Title = "ignored native change"
	if err := s.UpdateFinding(ctx, stale); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateScan(ctx, current); err != nil {
		t.Fatal(err)
	}
	row, err = s.GetFindingByID(ctx, primary.ID)
	if err != nil || row.BaselineState != "resurfaced" || row.IntroducedInScanID != original.ID || row.FirstSeenAt == nil {
		t.Fatalf("stale update erased baseline: %+v %v", row, err)
	}
	// Triage and repeated baseline commits share scan locks and preserve native state.
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.PersistFindingBaseline(ctx, current.ID, "", *current.CompletedAt); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := s.UpdateFindingStatus(ctx, member.ID, models.StatusFixed); err != nil {
			t.Error(err)
		}
	}()
	wg.Wait()
	row, err = s.GetFindingByID(ctx, primary.ID)
	if err != nil || row.Status != models.StatusFixed || row.BaselineState != "resurfaced" {
		t.Fatalf("triage lost: %+v %v", row, err)
	}
	current.LeaseToken = "owner"
	if err := s.UpdateScan(ctx, current); err != nil {
		t.Fatal(err)
	}
	if err := s.PersistFindingBaseline(ctx, current.ID, "stale", *current.CompletedAt); !errors.Is(err, ErrStaleScanLease) {
		t.Fatalf("stale lease accepted: %v", err)
	}
	current.Status = models.ScanStatusCancelled
	if err := s.UpdateScan(ctx, current); err != nil {
		t.Fatal(err)
	}
	if err := s.PersistFindingBaseline(ctx, current.ID, "owner", *current.CompletedAt); !errors.Is(err, ErrStaleScanLease) {
		t.Fatalf("cancelled scan mutated: %v", err)
	}
}
