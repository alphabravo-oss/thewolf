package db

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

func TestFleetEvidenceProgressAndCoverage(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	scan := func(id, repo, branch string, days int) models.Scan {
		at := now.AddDate(0, 0, -days)
		return models.Scan{ID: id, RepoID: repo, Branch: branch, Status: models.ScanStatusCompleted, ToolsSelected: `["semgrep"]`, ToolsCompleted: `["semgrep"]`, ToolsFailed: `[]`, ReleaseManifestDigest: "sha256:release", CreatedAt: at, CompletedAt: &at}
	}
	finding := func(scanID, repoID, key string) *fleetFinding {
		return &fleetFinding{ID: scanID + key, ScanID: scanID, RepoID: repoID, ToolName: "semgrep", Category: models.CategorySAST, Status: models.StatusOpen, Severity: models.SeverityHigh, Fingerprint: key, StableFingerprint: key, LocationFingerprint: key, Title: key, FilePath: key + ".go", CreatedAt: now}
	}
	fixture := func() *fleetEvidence {
		return &fleetEvidence{repos: []fleetRepo{{"repo", "Payments"}}, scans: []models.Scan{scan("history", "repo", "main", 15), scan("baseline", "repo", "main", 8), scan("latest", "repo", "main", 1)}, findings: map[string][]*fleetFinding{}, failedGates: map[string]bool{}}
	}
	t.Run("counts distinct changes and separates triage", func(t *testing.T) {
		e := fixture()
		e.findings["history"] = []*fleetFinding{finding("history", "repo", "returned")}
		e.findings["baseline"] = []*fleetFinding{finding("baseline", "repo", "fixed"), finding("baseline", "repo", "muted"), finding("baseline", "repo", "unchanged")}
		muted := finding("latest", "repo", "muted")
		muted.Suppressed = true
		moved := finding("latest", "repo", "unchanged")
		moved.LocationFingerprint = "moved-line"
		moved.LineStart = 90
		e.findings["latest"] = []*fleetFinding{finding("latest", "repo", "new"), finding("latest", "repo", "returned"), muted, moved, finding("latest", "repo", "new")}
		p := e.posture(now)
		c := p.Changes
		if c.ComparedBranches != 1 || c.New != 1 || c.Resurfaced != 1 || c.Resolved != 1 || c.Suppressed != 1 {
			t.Fatalf("changes: %+v", c)
		}
		if p.Coverage.FreshRepos != 1 || p.OpenFindingsBySeverity["high"] != 3 {
			t.Fatalf("posture: %+v", p)
		}
		seen := map[string]time.Time{}
		for _, f := range p.Priorities {
			seen[f.Title] = f.FirstSeenAt
		}
		if !seen["returned"].Equal(now.AddDate(0, 0, -15)) || !seen["new"].Equal(now.AddDate(0, 0, -1)) {
			t.Fatalf("priority first seen: %v", seen)
		}
	})
	t.Run("legacy identity fallback and shared evidence stay unchanged", func(t *testing.T) {
		e := fixture()
		known := finding("baseline", "repo", "known")
		fixed := finding("baseline", "repo", "fixed")
		moved := finding("latest", "repo", "known")
		added := finding("latest", "repo", "added")
		moved.LineStart = 90
		for _, f := range []*fleetFinding{known, fixed, moved, added} {
			f.Fingerprint, f.StableFingerprint = "", ""
		}
		e.findings["baseline"] = []*fleetFinding{known, fixed}
		e.findings["latest"] = []*fleetFinding{moved, added}
		original := *moved
		for range 2 {
			p := e.posture(now)
			if p.Changes.New != 1 || p.Changes.Resolved != 1 || p.Changes.Resurfaced != 0 || p.OpenFindingsBySeverity["high"] != 2 {
				t.Fatalf("legacy comparison changed: %+v", p)
			}
			if *moved != original || len(e.findings["latest"]) != 2 {
				t.Fatal("aggregate mutated shared history")
			}
			for _, f := range e.currentOpen(now) {
				if f != moved && f != added {
					t.Fatal("current evidence copied a finding")
				}
			}
		}
	})
	t.Run("old failure clears when latest branch passes", func(t *testing.T) {
		e := fixture()
		e.failedGates["baseline"] = true
		if got := e.posture(now).GatesFailing; got != 0 {
			t.Fatalf("historical gate counted: %d", got)
		}
		e.failedGates["latest"] = true
		if got := e.posture(now).GatesFailing; got != 1 {
			t.Fatalf("latest gate missing: %d", got)
		}
	})
	for _, status := range []models.ScanStatus{models.ScanStatusCancelled, models.ScanStatusFailed, models.ScanStatusCompleted, models.ScanStatusRunning} {
		t.Run("partial evidence retains backlog "+string(status), func(t *testing.T) {
			e := fixture()
			e.findings["baseline"] = []*fleetFinding{finding("baseline", "repo", "known")}
			e.scans[2].Status = status
			e.scans[2].ToolsCompleted = `[]`
			p := e.posture(now)
			if p.OpenFindingsBySeverity["high"] != 1 || p.Changes.Resolved != 0 || p.Changes.ComparedBranches != 0 || p.Coverage.FreshRepos != 0 {
				t.Fatalf("partial scan claimed improvement: %+v", p)
			}
		})
	}
	for _, change := range []struct {
		name   string
		mutate func(*models.Scan)
	}{
		{"tools", func(s *models.Scan) { s.ToolsSelected = `["trivy"]`; s.ToolsCompleted = s.ToolsSelected }},
		{"paths", func(s *models.Scan) { s.IncludePaths = `["src/subset"]` }},
		{"release", func(s *models.Scan) { s.ReleaseManifestDigest = "sha256:new" }},
		{"missing release", func(s *models.Scan) { s.ReleaseManifestDigest = "" }},
		{"malformed scope", func(s *models.Scan) { s.Categories = `{` }},
	} {
		t.Run("excludes changed "+change.name, func(t *testing.T) {
			e := fixture()
			change.mutate(&e.scans[2])
			e.findings["baseline"] = []*fleetFinding{finding("baseline", "repo", "known")}
			p := e.posture(now)
			if p.Changes.ComparedBranches != 0 || p.Changes.Resolved != 0 || p.Changes.Rows[0].Reason == "" {
				t.Fatalf("incomparable: %+v", p.Changes)
			}
		})
	}
	t.Run("another branch still observing issue prevents resolution", func(t *testing.T) {
		e := fixture()
		e.findings["baseline"] = []*fleetFinding{finding("baseline", "repo", "known")}
		e.scans = append(e.scans, scan("other", "repo", "dev", 0))
		f := finding("other", "repo", "known")
		f.Suppressed = true
		e.findings["other"] = []*fleetFinding{f}
		if got := e.posture(now).Changes.Resolved; got != 0 {
			t.Fatalf("still observed, but counted resolved: %d", got)
		}
	})
	t.Run("unknown coverage on an affected branch prevents resolution", func(t *testing.T) {
		e := fixture()
		e.findings["baseline"] = []*fleetFinding{finding("baseline", "repo", "known")}
		old, current := scan("dev-before", "repo", "dev", 8), scan("dev-now", "repo", "dev", 1)
		current.IncludePaths = `["only-a-subdirectory"]`
		e.scans = append(e.scans, old, current)
		e.findings[old.ID] = []*fleetFinding{finding(old.ID, "repo", "known")}
		p := e.posture(now)
		if p.Changes.ComparedBranches != 1 || p.Changes.ExcludedBranches != 1 || p.Changes.Resolved != 0 {
			t.Fatalf("unverified branch credited: %+v", p.Changes)
		}
	})
	t.Run("spreading an existing issue to another branch is not resurfacing", func(t *testing.T) {
		e := fixture()
		e.findings["baseline"] = []*fleetFinding{finding("baseline", "repo", "known")}
		e.findings["latest"] = []*fleetFinding{finding("latest", "repo", "known")}
		e.scans = append(e.scans, scan("dev-before", "repo", "dev", 8), scan("dev-now", "repo", "dev", 1))
		e.findings["dev-now"] = []*fleetFinding{finding("dev-now", "repo", "known")}
		p := e.posture(now)
		if p.Changes.New != 0 || p.Changes.Resurfaced != 0 || p.OpenFindingsBySeverity["high"] != 1 {
			t.Fatalf("branch duplication: %+v", p)
		}
	})
	t.Run("first scan and unscanned repositories are unknown progress", func(t *testing.T) {
		e := fixture()
		e.scans = e.scans[2:]
		e.repos = append(e.repos, fleetRepo{"empty", "Unscanned"})
		p := e.posture(now)
		if p.Changes.ComparedBranches != 0 || p.Changes.ExcludedBranches != 2 || p.Coverage.FreshRepos != 1 {
			t.Fatalf("first scan: %+v", p)
		}
	})
	t.Run("scope order does not matter", func(t *testing.T) {
		a := scan("a", "repo", "main", 8)
		b := scan("b", "repo", "main", 1)
		a.ToolsSelected = `["semgrep","trivy"]`
		b.ToolsSelected = `["trivy","semgrep"]`
		b.ExcludePaths = `[]`
		if scanScope(a) != scanScope(b) {
			t.Fatal("equivalent scope rejected")
		}
	})
	t.Run("expired worker lease is visible", func(t *testing.T) {
		e := fixture()
		expired := now.Add(-time.Minute)
		e.scans[2].Status = models.ScanStatusRunning
		e.scans[2].LeaseExpiresAt = &expired
		p := e.posture(now)
		if len(p.Work) != 1 || !p.Work[0].Stalled {
			t.Fatalf("work: %+v", p.Work)
		}
	})
}

func TestFleetEvidenceQueriesAndTrends(t *testing.T) {
	store := newTestStore(t)
	testFleetEvidenceQueries(t, store, store.db)
}

func TestFleetEvidencePostgres(t *testing.T) {
	dsn := os.Getenv("WOLF_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("WOLF_TEST_POSTGRES_DSN is not configured")
	}
	admin, err := NewPostgres(dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := "fleet_evidence_" + uuid.NewString()
	if _, err := admin.db.Exec(`CREATE SCHEMA "` + schema + `"`); err != nil {
		_ = admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.db.Exec(`DROP SCHEMA IF EXISTS "` + schema + `" CASCADE`); _ = admin.Close() })
	store, err := NewPostgres(postgresDSNWithSearchPath(t, dsn, schema))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	testFleetEvidenceQueries(t, store, store.db)
}

func testFleetEvidenceQueries(t *testing.T, store Store, database *sqlx.DB) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	if err := store.CreateUser(ctx, &models.User{ID: "owner", Email: "fleet@example.test", PasswordHash: "test-only"}); err != nil {
		t.Fatal(err)
	}
	repo := &models.Repo{ID: uuid.NewString(), UserID: "owner", Name: "Payments", SourceType: models.SourceTypeLocal, SourcePath: "/repo"}
	if err := store.CreateRepo(ctx, repo); err != nil {
		t.Fatal(err)
	}
	makeScan := func(id, branch, origin string, status models.ScanStatus, at time.Time) {
		s := &models.Scan{ID: id, UserID: "owner", RepoID: repo.ID, Branch: branch, OriginScanID: origin, Status: status, ToolsSelected: `["semgrep"]`, ToolsCompleted: `["semgrep"]`, ToolsFailed: `[]`, CompletedAt: &at, ReleaseManifestDigest: "sha256:release"}
		if err := store.CreateScan(ctx, s); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(database.Rebind(`UPDATE scans SET created_at = ? WHERE id = ?`), at, id); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"one", "two", "three"} {
		makeScan(id, "main", "", models.ScanStatusCompleted, now.Add(-time.Hour))
		f := &models.Finding{ID: uuid.NewString(), RepoID: repo.ID, ScanID: id, ToolName: "semgrep", Category: models.CategorySAST, FilePath: "a.go", RuleID: "test", LocationFingerprint: "same", Status: models.StatusOpen, Severity: models.SeverityHigh, Title: "Same finding", Description: "Full detail survives aggregates", CompositeScore: 8.5, SARIFData: "raw-evidence", CorroboratedBy: []string{"semgrep"}}
		if err := store.CreateFinding(ctx, f); err != nil {
			t.Fatal(err)
		}
	}
	// Remediation child scans cannot clear the source branch or count as coverage.
	makeScan("child", "main", "one", models.ScanStatusCompleted, now)
	// Legacy root scans of a wolf-fix/* branch are remediation evidence too.
	makeScan("fixroot", "wolf-fix/one", "", models.ScanStatusCompleted, now)
	if err := store.CreateFinding(ctx, &models.Finding{ID: uuid.NewString(), RepoID: repo.ID, ScanID: "fixroot", ToolName: "semgrep", Category: models.CategorySAST, FilePath: "fix.go", RuleID: "fix", Status: models.StatusOpen, Severity: models.SeverityHigh, Title: "Fix branch only"}); err != nil {
		t.Fatal(err)
	}
	p, err := store.FleetPosture(ctx, "owner", false, "")
	if err != nil {
		t.Fatal(err)
	}
	if p.OpenFindingsBySeverity["high"] != 1 || len(p.Coverage.Rows) != 1 || p.Coverage.Rows[0].Branch != "main" {
		t.Fatalf("posture: %+v", p)
	}
	full, err := store.ListCurrentOpenFindings(ctx, "owner", false, "")
	if err != nil || len(full) != 1 || full[0].Description != "Full detail survives aggregates" || full[0].CompositeScore != 8.5 || full[0].SARIFData != "raw-evidence" || len(full[0].CorroboratedBy) != 1 {
		t.Fatalf("full finding fields lost: %+v %v", full, err)
	}
	page, err := store.QueryCurrentFindings(ctx, "owner", false, "", "", FindingListQuery{Limit: 1})
	if err != nil || page.Total != len(full) || len(page.Findings) != 1 || page.Findings[0].ID != full[0].ID || page.Findings[0].Description != full[0].Description || page.Findings[0].SARIFData != "" {
		t.Fatalf("paged history differs from current evidence: %+v %v", page, err)
	}
	suppressed := &models.Finding{ID: uuid.NewString(), RepoID: repo.ID, ScanID: p.Coverage.Rows[0].ScanID, ToolName: "semgrep", Category: models.CategorySAST, FilePath: "muted.go", RuleID: "muted", Status: models.StatusOpen, Suppressed: true, Severity: models.SeverityHigh, Title: "Suppressed"}
	if err := store.CreateFinding(ctx, suppressed); err != nil {
		t.Fatal(err)
	}
	full, err = store.ListCurrentOpenFindings(ctx, "owner", false, "")
	if err != nil || len(full) != 2 {
		t.Fatalf("suppressed records lost from compatibility API: %+v %v", full, err)
	}
	trends, err := store.FleetFindingTrends(ctx, "owner", false, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(trends) == 0 || trends[len(trends)-1].Counts["total"] != 1 {
		t.Fatalf("rescans inflated trend: %+v", trends)
	}
	hidden, err := store.FleetPosture(ctx, "other", false, "")
	if err != nil || hidden.RepoCount != 0 || len(hidden.Priorities) != 0 {
		t.Fatalf("owner scope leaked: %+v %v", hidden, err)
	}
	empty, err := store.FleetPosture(ctx, "owner", true, "missing-collection")
	if err != nil || empty.RepoCount != 0 || len(empty.Coverage.Rows) != 0 {
		t.Fatalf("collection scope leaked: %+v %v", empty, err)
	}
	// Collection scan history follows current membership, including API scans
	// without collection_id. Run this contract on both SQL backends.
	col := &models.Collection{ID: uuid.NewString(), UserID: "owner", Name: "Services"}
	if err := store.CreateCollection(ctx, col); err != nil {
		t.Fatal(err)
	}
	if err := store.SetRepoCollection(ctx, repo.ID, col.ID); err != nil {
		t.Fatal(err)
	}
	collectionScans, err := store.ListScansByCollection(ctx, col.ID)
	if err != nil || len(collectionScans) != 5 {
		t.Fatalf("collection lost repository history: %d %v", len(collectionScans), err)
	}
	// Repository filtering must scope the history queries as well as the page,
	// while still intersecting ownership and collection membership.
	otherRepo := &models.Repo{ID: uuid.NewString(), UserID: "owner", Name: "Other"}
	if err := store.CreateRepo(ctx, otherRepo); err != nil {
		t.Fatal(err)
	}
	otherScan := &models.Scan{ID: uuid.NewString(), RepoID: otherRepo.ID, UserID: "owner", Status: models.ScanStatusCompleted}
	if err := store.CreateScan(ctx, otherScan); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateFinding(ctx, &models.Finding{ID: uuid.NewString(), ScanID: otherScan.ID, RepoID: otherRepo.ID, ToolName: "semgrep", Severity: models.SeverityHigh, Status: models.StatusOpen}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		user, collection, repo string
		wantRepos, wantScans   int
	}{
		{"owner", "", repo.ID, 1, 3},
		{"owner", col.ID, repo.ID, 1, 3},
		{"owner", col.ID, otherRepo.ID, 0, 0},
		{"other", "", repo.ID, 0, 0},
		{"owner", "", "missing", 0, 0},
	} {
		e, err := loadFleetEvidence(ctx, database, tc.user, false, tc.collection, tc.repo)
		if err != nil {
			t.Fatal(err)
		}
		if len(e.repos) != tc.wantRepos || len(e.scans) != tc.wantScans || len(e.findings) != tc.wantScans {
			t.Fatalf("history scope %+v: repos=%d scans=%d finding scans=%d", tc, len(e.repos), len(e.scans), len(e.findings))
		}
		for _, findings := range e.findings {
			for _, f := range findings {
				if f.RepoID != tc.repo {
					t.Fatal("unrelated repository evidence loaded")
				}
			}
		}
	}
	// Failed queries must not degrade to a reassuring empty dashboard.
	if _, err := database.Exec(`DROP TABLE quality_gate_results`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FleetPosture(ctx, "owner", false, ""); err == nil || !strings.Contains(err.Error(), "quality_gate_results") {
		t.Fatalf("query error was swallowed: %v", err)
	}
}
