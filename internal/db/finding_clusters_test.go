package db

import (
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

func TestFindingClustersSQLite(t *testing.T) { s := newTestStore(t); testFindingClusters(t, s, s.db) }
func TestFindingClustersPostgres(t *testing.T) {
	s := newClusterPostgresStore(t)
	testFindingClusters(t, s, s.db)
}
func newClusterPostgresStore(t *testing.T) *PostgresStore {
	t.Helper()
	dsn := os.Getenv("WOLF_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("WOLF_TEST_POSTGRES_DSN is not configured")
	}
	admin, err := NewPostgres(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	schema := "clusters_" + uuid.NewString()
	if _, err := admin.db.Exec(`CREATE SCHEMA "` + schema + `"`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.db.Exec(`DROP SCHEMA "` + schema + `" CASCADE`); err != nil {
			t.Error(err)
		}
	})
	s, err := NewPostgres(postgresDSNWithSearchPath(t, dsn, schema))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}
func testFindingClusters(t *testing.T, s Store, database *sqlx.DB) {
	t.Helper()
	ctx := t.Context()
	user, repo := uuid.NewString(), uuid.NewString()
	if err := s.CreateUser(ctx, &models.User{ID: user, Email: user + "@example.test", PasswordHash: "hash"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateRepo(ctx, &models.Repo{ID: repo, UserID: user, Name: "clusters", SourceType: models.SourceTypeLocal, SourcePath: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	scan := &models.Scan{ID: uuid.NewString(), UserID: user, RepoID: repo, Status: models.ScanStatusRunning, LeaseToken: "owner", ToolsSelected: `["a","b"]`, ToolsCompleted: `["a","b"]`}
	if err := s.CreateScan(ctx, scan); err != nil {
		t.Fatal(err)
	}
	var raw []models.Finding
	for _, tool := range []string{"a", "b"} {
		f := models.Finding{ID: uuid.NewString(), ScanID: scan.ID, RepoID: repo, ToolName: tool, Category: models.CategorySCA, PackageName: "alpha", PackageVersion: "1", PackageEcosystem: "npm", AdvisoryID: "CVE-2026-1001", RuleID: "CVE-2026-1001", FilePath: "package-lock.json", Title: tool + " native evidence", Severity: models.SeverityHigh, Status: models.StatusOpen}
		if err := s.CreateFinding(ctx, &f); err != nil {
			t.Fatal(err)
		}
		raw = append(raw, f)
	}
	// Simulate an upgrade from the preceding schema, retaining existing IDs.
	if _, err := database.Exec("DROP INDEX IF EXISTS idx_findings_scan_visible_severity"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("DROP INDEX idx_findings_cluster"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("ALTER TABLE findings DROP COLUMN cluster_id"); err != nil {
		t.Fatal(err)
	}
	reconnectPostgresTestStore(s)
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	legacy, err := s.ListFindingsByScan(ctx, scan.ID)
	if err != nil || len(legacy) != 2 {
		t.Fatalf("upgrade lost provisional evidence: %+v %v", legacy, err)
	}
	fix := &models.Fix{ID: uuid.NewString(), ScanID: scan.ID, UserID: user}
	if err := s.CreateFix(ctx, fix); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateFixItem(ctx, &models.FixItem{ID: uuid.NewString(), FixID: fix.ID, FindingID: raw[1].ID}); err != nil {
		t.Fatal(err)
	}
	primary := raw[0]
	primary.Confidence = "medium"
	primary.CorroboratedBy = []string{"a", "b"}
	primary.CompositeScore = 8
	primary.ModuleName = "enriched"
	clusters := []models.Finding{primary}
	for _, token := range []string{"", "wrong"} {
		if err := s.FinalizeFindingClusters(ctx, scan.ID, token, clusters); !errors.Is(err, ErrStaleScanLease) {
			t.Fatalf("stale lease accepted: %v", err)
		}
	}
	bad := primary
	bad.ID = uuid.NewString()
	if err := s.FinalizeFindingClusters(ctx, scan.ID, "owner", []models.Finding{bad}); err == nil {
		t.Fatal("foreign primary accepted")
	}
	if err := s.FinalizeFindingClusters(ctx, scan.ID, "owner", nil); err == nil {
		t.Fatal("unassigned evidence accepted")
	}
	if err := s.FinalizeFindingClusters(ctx, scan.ID, "owner", []models.Finding{primary, primary}); err == nil {
		t.Fatal("duplicate primary accepted")
	}
	otherRepo := uuid.NewString()
	if err := s.CreateRepo(ctx, &models.Repo{ID: otherRepo, UserID: user, Name: "other", SourceType: models.SourceTypeLocal, SourcePath: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(database.Rebind("UPDATE findings SET repo_id = ? WHERE id = ?"), otherRepo, raw[1].ID); err != nil {
		t.Fatal(err)
	}
	if err := s.FinalizeFindingClusters(ctx, scan.ID, "owner", clusters); err == nil {
		t.Fatal("cross-repository evidence assigned")
	}
	if _, err := database.Exec(database.Rebind("UPDATE findings SET repo_id = ? WHERE id = ?"), repo, raw[1].ID); err != nil {
		t.Fatal(err)
	}
	// Triage between aggregate preparation and commit must not hide an open member.
	if err := s.UpdateFindingStatus(ctx, primary.ID, models.StatusFalsePositive); err != nil {
		t.Fatal(err)
	}
	if err := s.FinalizeFindingClusters(ctx, scan.ID, "owner", clusters); err == nil {
		t.Fatal("stale primary hid an open member")
	}
	if err := s.UpdateFindingStatus(ctx, primary.ID, models.StatusOpen); err != nil {
		t.Fatal(err)
	}
	// Fail the metadata write after every membership write, proving rollback.
	trigger := `CREATE TRIGGER reject_cluster_metadata BEFORE UPDATE OF confidence ON findings BEGIN SELECT RAISE(ABORT, 'fixture cluster failure'); END`
	if database.DriverName() == "postgres" {
		if _, err := database.Exec(`CREATE FUNCTION reject_cluster_metadata_fn() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture cluster failure'; END $$`); err != nil {
			t.Fatal(err)
		}
		trigger = `CREATE TRIGGER reject_cluster_metadata BEFORE UPDATE OF confidence ON findings FOR EACH ROW EXECUTE FUNCTION reject_cluster_metadata_fn()`
	}
	if _, err := database.Exec(trigger); err != nil {
		t.Fatal(err)
	}
	if err := s.FinalizeFindingClusters(ctx, scan.ID, "owner", clusters); err == nil {
		t.Fatal("partial commit after metadata failure")
	}
	members, err := s.ListFindingEvidenceByScan(ctx, scan.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range members {
		if f.ClusterID != "" || f.CompositeScore != 0 {
			t.Fatalf("failed transaction changed evidence: %+v", f)
		}
	}
	drop := "DROP TRIGGER reject_cluster_metadata"
	if database.DriverName() == "postgres" {
		drop += " ON findings"
	}
	if _, err := database.Exec(drop); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := s.FinalizeFindingClusters(ctx, scan.ID, "owner", clusters); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	listed, err := s.ListFindingsByScan(ctx, scan.ID)
	if err != nil || len(listed) != 1 || listed[0].ID != primary.ID || listed[0].ClusterID != primary.ID || listed[0].Confidence != "medium" || len(listed[0].CorroboratedBy) != 2 || listed[0].CompositeScore != 8 {
		t.Fatalf("persisted cluster: %+v %v", listed, err)
	}
	for _, old := range raw {
		got, err := s.GetFindingByID(ctx, old.ID)
		if err != nil || got.Title != old.Title || got.ClusterID != primary.ID {
			t.Fatalf("native identity/evidence lost: %+v %v", got, err)
		}
		evidence, err := s.ListFindingClusterEvidence(ctx, old.ID)
		if err != nil || len(evidence) != 2 {
			t.Fatalf("cluster members: %d %v", len(evidence), err)
		}
	}
	if err := s.UpdateFindingStatus(ctx, raw[1].ID, models.StatusFalsePositive); err != nil {
		t.Fatal(err)
	}
	for _, old := range raw {
		f, err := s.GetFindingByID(ctx, old.ID)
		if err != nil || f.Status != models.StatusFalsePositive {
			t.Fatalf("member triage diverged: %+v %v", f, err)
		}
	}
	if err := s.FinalizeFindingClusters(ctx, scan.ID, "owner", clusters); err != nil {
		t.Fatal(err)
	}
	f, err := s.GetFindingByID(ctx, primary.ID)
	if err != nil || f.Status != models.StatusFalsePositive {
		t.Fatalf("retry overwrote triage: %+v %v", f, err)
	}
	items, err := s.ListFixItemsByFix(ctx, fix.ID)
	if err != nil || len(items) != 1 || items[0].FindingID != raw[1].ID {
		t.Fatalf("fix reference changed: %+v %v", items, err)
	}
	stats, err := s.ScanFindingStats(ctx, scan.ID)
	if err != nil || stats.Total != 1 || stats.ByTool["a"] != 1 || stats.ByTool["b"] != 1 {
		t.Fatalf("stats still count members: %+v %v", stats, err)
	}
	byRepo, err := s.ListFindingsByRepo(ctx, repo)
	if err != nil || len(byRepo) != 1 {
		t.Fatalf("repo still counts members: %d %v", len(byRepo), err)
	}
	// Real concurrent transactions must serialize finalization and member triage.
	var wg sync.WaitGroup
	start := make(chan struct{})
	failures := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			if i%2 == 0 {
				failures <- s.FinalizeFindingClusters(ctx, scan.ID, "owner", clusters)
			} else {
				failures <- s.UpdateFindingStatus(ctx, raw[1].ID, models.StatusFalsePositive)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, old := range raw {
		f, err := s.GetFindingByID(ctx, old.ID)
		if err != nil || f.ClusterID != primary.ID || f.Status != models.StatusFalsePositive {
			t.Fatalf("concurrent triage diverged: %+v %v", f, err)
		}
	}
	// The shared canonical table stores suppression as INTEGER on both backends.
	v := &models.Vulnerability{ID: uuid.NewString(), RepoID: repo, ScanID: scan.ID, CanonicalKey: "cluster-fixture", Suppressed: true}
	for _, suppressed := range []bool{true, false} {
		v.Suppressed = suppressed
		if err := s.UpsertVulnerability(ctx, v); err != nil {
			t.Fatal(err)
		}
		got, err := s.GetVulnerabilityByID(ctx, v.ID)
		if err != nil || got.Suppressed != suppressed {
			t.Fatalf("canonical suppression lost: %+v %v", got, err)
		}
	}
	// Reclaim can delete an incomplete primary while preserving completed
	// members and their fix references. No retained row may point at a dead ID.
	if err := s.DeleteFindingsByScanTool(ctx, scan.ID, "a", "owner"); err != nil {
		t.Fatal(err)
	}
	remaining, err := s.ListFindingsByScan(ctx, scan.ID)
	if err != nil || len(remaining) != 1 || remaining[0].ID != raw[1].ID || remaining[0].ClusterID != "" {
		t.Fatalf("recovery stranded retained evidence: %+v %v", remaining, err)
	}
	if err := s.RequestScanCancellation(ctx, scan.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.FinalizeFindingClusters(ctx, scan.ID, "owner", clusters); !errors.Is(err, ErrStaleScanLease) {
		t.Fatalf("cancelled scan finalized: %v", err)
	}
}

// Upgrade fixtures edit a previously current schema to simulate an older one.
// Reconnect before migration, as process startup does, so pgx does not retain a
// prepared SELECT * result shape from the pre-fixture column ordering. No test
// transaction is active here; two idle connections is database/sql's default.
func reconnectPostgresTestStore(store Store) {
	if s, ok := store.(*PostgresStore); ok {
		s.db.SetMaxIdleConns(0)
		s.db.SetMaxIdleConns(2)
	}
}
