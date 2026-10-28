package db

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

func TestManualBaselinePostgres(t *testing.T) {
	s := newClusterPostgresStore(t)
	testManualBaseline(t, s, s.db)
}
func testManualBaseline(t *testing.T, s Store, database *sqlx.DB) {
	ctx := t.Context()
	user, repo := uuid.NewString(), uuid.NewString()
	if err := s.CreateUser(ctx, &models.User{ID: user, Email: user + "@example.test", PasswordHash: "hash"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateRepo(ctx, &models.Repo{ID: repo, UserID: user, Name: "history", SourceType: models.SourceTypeLocal, SourcePath: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	scan := func(id string, offset int) *models.Scan {
		at := start.Add(time.Duration(offset) * time.Hour)
		result := &models.Scan{ID: id, RepoID: repo, UserID: user, Branch: "main", Status: models.ScanStatusCompleted, CompletedAt: &at, ToolsSelected: `["a"]`, ToolsCompleted: `["a"]`, ToolsFailed: `[]`}
		if err := s.CreateScan(ctx, result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	finding := func(id string, scan *models.Scan, key string, severity models.Severity) *models.Finding {
		f := &models.Finding{ID: id, ScanID: scan.ID, RepoID: repo, ToolName: "a", RuleID: key, FineCategory: key, StableFingerprint: key, Fingerprint: key, FilePath: key + ".go", Title: key, Severity: severity, Category: models.CategorySAST, Status: models.StatusOpen}
		if err := s.CreateFinding(ctx, f); err != nil {
			t.Fatal(err)
		}
		return f
	}
	base := scan("base", 0)
	finding("base-a", base, "a", models.SeverityCritical)
	if err := s.PersistFindingBaseline(ctx, base.ID, "", *base.CompletedAt); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateScanBaseline(ctx, &models.ScanBaseline{ID: uuid.NewString(), RepoID: repo, Branch: "main", Name: "auto", ScanID: base.ID, CreatedBy: user}); err != nil {
		t.Fatal(err)
	}
	current := scan("current", 1)
	a := finding("current-a", current, "a", models.SeverityCritical)
	b := finding("current-b", current, "b", models.SeverityHigh)
	if err := s.PersistFindingBaseline(ctx, current.ID, "", *current.CompletedAt); err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcileVulnerabilities(ctx, current.ID); err != nil {
		t.Fatal(err)
	}
	vulns, err := s.ListVulnerabilitiesByScan(ctx, current.ID)
	if err != nil || len(vulns) != 2 {
		t.Fatalf("seed: %+v %v", vulns, err)
	}
	var aID, bID string
	for _, v := range vulns {
		if v.CanonicalKey == "a" {
			aID = v.ID
		}
		if v.CanonicalKey == "b" {
			bID = v.ID
		}
	}
	snapshot := func() string {
		t.Helper()
		var findings []models.Finding
		var scans []models.Scan
		var vulnerabilities []models.Vulnerability
		var evidence []models.VulnerabilityEvidence
		var bindings []string
		for _, q := range []struct {
			dest any
			sql  string
		}{{&findings, "SELECT * FROM findings ORDER BY id"}, {&scans, "SELECT * FROM scans ORDER BY id"}, {&vulnerabilities, "SELECT * FROM vulnerabilities ORDER BY id"}, {&evidence, "SELECT * FROM vulnerability_evidence ORDER BY id"}, {&bindings, "SELECT baseline_identity_key FROM findings ORDER BY id"}} {
			if err := database.Select(q.dest, q.sql); err != nil {
				t.Fatal(err)
			}
		}
		data, err := json.Marshal([]any{findings, scans, vulnerabilities, evidence, bindings})
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	rejectHistory := func() func() {
		t.Helper()
		trigger := `CREATE TRIGGER reject_manual_history BEFORE UPDATE OF baseline_identity_key ON findings BEGIN SELECT RAISE(ABORT,'fixture manual history failure'); END`
		if database.DriverName() == "postgres" {
			if _, err := database.Exec(`CREATE OR REPLACE FUNCTION reject_manual_history_fn() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture manual history failure'; END $$`); err != nil {
				t.Fatal(err)
			}
			trigger = `CREATE TRIGGER reject_manual_history BEFORE UPDATE OF baseline_identity_key ON findings FOR EACH ROW EXECUTE FUNCTION reject_manual_history_fn()`
		}
		if _, err := database.Exec(trigger); err != nil {
			t.Fatal(err)
		}
		return func() {
			drop := "DROP TRIGGER reject_manual_history"
			if database.DriverName() == "postgres" {
				drop += " ON findings"
			}
			if _, err := database.Exec(drop); err != nil {
				t.Fatal(err)
			}
		}
	}
	before := snapshot()
	drop := rejectHistory()
	if _, err := s.MergeVulnerabilities(ctx, repo, bID, aID); err == nil {
		t.Fatal("late merge history failure ignored")
	}
	drop()
	if snapshot() != before {
		t.Fatal("merge history failure left membership, canonical or revision changes")
	}
	if _, err := s.MergeVulnerabilities(ctx, repo, bID, aID); err != nil {
		t.Fatal(err)
	}
	merged, err := s.GetFindingByID(ctx, b.ID)
	if err != nil || merged.ClusterID != a.ID || merged.BaselineState != "existing" || merged.IntroducedInScanID != base.ID {
		t.Fatalf("merged member disagrees with its issue: %+v %v", merged, err)
	}
	// A later automatic observation of B must not inherit unrelated A's origin.
	later := scan("later", 2)
	laterB := finding("later-b", later, "b", models.SeverityHigh)
	if err := s.PersistFindingBaseline(ctx, later.ID, "", *later.CompletedAt); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetFindingByID(ctx, laterB.ID)
	if err != nil || got.IntroducedInScanID != current.ID || got.FirstSeenAt == nil || !got.FirstSeenAt.Equal(*current.CompletedAt) {
		t.Fatalf("manual association contaminated later native history: %+v %v", got, err)
	}
	before = snapshot()
	drop = rejectHistory()
	if _, err := s.SplitVulnerability(ctx, repo, aID, []string{b.ID}); err == nil {
		t.Fatal("late split history failure ignored")
	}
	drop()
	if snapshot() != before {
		t.Fatal("split history failure left partial changes")
	}
	split, err := s.SplitVulnerability(ctx, repo, aID, []string{b.ID})
	if err != nil {
		t.Fatal(err)
	}
	got, err = s.GetFindingByID(ctx, b.ID)
	if err != nil || got.ClusterID != b.ID || got.BaselineState != "new" || got.IntroducedInScanID != current.ID || got.FirstSeenAt == nil || !got.FirstSeenAt.Equal(*current.CompletedAt) {
		t.Fatalf("split kept borrowed baseline history: %+v %v", got, err)
	}
	if split.BaselineState != "new" {
		t.Fatalf("canonical state stale: %+v", split)
	}
	if err := s.ReconcileVulnerabilities(ctx, later.ID); err != nil {
		t.Fatal(err)
	}
	laterVulns, err := s.ListVulnerabilitiesByScan(ctx, later.ID)
	if err != nil || len(laterVulns) != 1 {
		t.Fatalf("later canonical: %+v %v", laterVulns, err)
	}
	var wg sync.WaitGroup
	wg.Add(4)
	go func() {
		defer wg.Done()
		if _, err := s.MergeVulnerabilities(ctx, repo, laterVulns[0].ID, split.ID); err != nil {
			t.Error(err)
		}
	}()
	go func() {
		defer wg.Done()
		if err := s.PersistFindingBaseline(ctx, current.ID, "", *current.CompletedAt); err != nil {
			t.Error(err)
		}
	}()
	go func() {
		defer wg.Done()
		if err := s.PersistFindingBaseline(ctx, later.ID, "", *later.CompletedAt); err != nil {
			t.Error(err)
		}
	}()
	go func() {
		defer wg.Done()
		if err := s.UpdateFindingStatus(ctx, laterB.ID, models.StatusFixed); err != nil {
			t.Error(err)
		}
	}()
	wg.Wait()
	for _, id := range []string{b.ID, laterB.ID} {
		row, err := s.GetFindingByID(ctx, id)
		if err != nil || row.BaselineState != "new" || row.IntroducedInScanID != current.ID || row.BaselineIdentityKey != "b" {
			t.Fatalf("cross-scan history changed: %+v %v", row, err)
		}
	}
	row, err := s.GetFindingByID(ctx, laterB.ID)
	if err != nil || row.Status != models.StatusFixed {
		t.Fatalf("triage lost: %+v %v", row, err)
	}
	// Current schema upgrade does not guess the identity behind legacy dates.
	if _, err := database.Exec("ALTER TABLE findings DROP COLUMN baseline_identity_key"); err != nil {
		t.Fatal(err)
	}
	reconnectPostgresTestStore(s)
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	row, err = s.GetFindingByID(ctx, b.ID)
	if err != nil || row.BaselineIdentityKey != "" || row.FirstSeenAt == nil {
		t.Fatalf("legacy binding invented or dates lost: %+v %v", row, err)
	}
	if err := s.PersistFindingBaseline(ctx, current.ID, "", *current.CompletedAt); err != nil {
		t.Fatal(err)
	}
	row, err = s.GetFindingByID(ctx, b.ID)
	if err != nil || row.BaselineIdentityKey != "b" || row.IntroducedInScanID != current.ID {
		t.Fatalf("legacy rederivation: %+v %v", row, err)
	}
	legacy := scan("legacy", 4)
	x := finding("legacy-x", legacy, "x", models.SeverityCritical)
	y := finding("legacy-y", legacy, "y", models.SeverityHigh)
	z := finding("legacy-z", legacy, "z", models.SeverityHigh)
	if _, err := database.Exec(database.Rebind("UPDATE findings SET baseline_state = 'existing', introduced_in_scan_id = 'old', first_seen_at = ?, last_seen_at = ? WHERE scan_id = ?"), start, *legacy.CompletedAt, legacy.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcileVulnerabilities(ctx, legacy.ID); err != nil {
		t.Fatal(err)
	}
	legacyVulns, err := s.ListVulnerabilitiesByScan(ctx, legacy.ID)
	if err != nil || len(legacyVulns) != 3 {
		t.Fatalf("legacy canonical: %+v %v", legacyVulns, err)
	}
	ids := map[string]string{}
	for _, v := range legacyVulns {
		ids[v.CanonicalKey] = v.ID
	}
	if _, err := s.MergeVulnerabilities(ctx, repo, ids["y"], ids["x"]); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{x.ID, y.ID} {
		row, err := s.GetFindingByID(ctx, id)
		if err != nil || row.BaselineState != "" || row.IntroducedInScanID != "" || row.FirstSeenAt != nil || row.LastSeenAt != nil {
			t.Fatalf("legacy regrouping retained unverified history: %+v %v", row, err)
		}
	}
	row, err = s.GetFindingByID(ctx, z.ID)
	if err != nil || row.BaselineState != "existing" || row.IntroducedInScanID != "old" || row.FirstSeenAt == nil || !row.FirstSeenAt.Equal(start) {
		t.Fatalf("regrouping erased unrelated legacy history: %+v %v", row, err)
	}
}
