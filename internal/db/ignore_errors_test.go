package db

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	gitcmd "github.com/alphabravocompany/thewolf/internal/fix/git"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

func TestIgnoreErrorsRollbackSQLite(t *testing.T) {
	s := newTestStore(t)
	testIgnoreErrorsRollback(t, s, s.db, false)
}
func TestIgnoreErrorsRollbackPostgres(t *testing.T) {
	s := newClusterPostgresStore(t)
	testIgnoreErrorsRollback(t, s, s.db, false)
}
func TestGitignoreRollbackSQLite(t *testing.T) {
	s := newTestStore(t)
	testIgnoreErrorsRollback(t, s, s.db, true)
}
func TestGitignoreRollbackPostgres(t *testing.T) {
	s := newClusterPostgresStore(t)
	testIgnoreErrorsRollback(t, s, s.db, true)
}
func testIgnoreErrorsRollback(t *testing.T, s Store, database *sqlx.DB, gitFailure bool) {
	ctx := t.Context()
	user, repo := uuid.NewString(), uuid.NewString()
	root := t.TempDir()
	if gitFailure {
		cmd := gitcmd.Command(ctx, nil, "-C", root, "init", "-q")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("init Git: %v %s", err, out)
		}
	}
	if err := s.CreateUser(ctx, &models.User{ID: user, Email: user + "@example.test", PasswordHash: "hash"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateRepo(ctx, &models.Repo{ID: repo, UserID: user, SourcePath: root, Name: "ignore error"}); err != nil {
		t.Fatal(err)
	}
	scan := &models.Scan{ID: uuid.NewString(), RepoID: repo, UserID: user, Status: models.ScanStatusRunning, ToolsSelected: `["alpha"]`, ToolsCompleted: `["alpha"]`}
	if err := s.CreateScan(ctx, scan); err != nil {
		t.Fatal(err)
	}
	f := models.Finding{ID: uuid.NewString(), ScanID: scan.ID, RepoID: repo, ToolName: "alpha", RuleID: "R1", Category: models.CategorySAST, Severity: models.SeverityHigh, Status: models.StatusOpen, FilePath: "app.go"}
	if err := s.CreateFinding(ctx, &f); err != nil {
		t.Fatal(err)
	}
	rows := []models.Finding{f}
	if gitFailure {
		other := f
		other.ID = uuid.NewString()
		other.RuleID = "R2"
		other.FilePath = "other.go"
		if err := s.CreateFinding(ctx, &other); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, other)
	}
	if err := s.FinalizeFindingClusters(ctx, scan.ID, "", rows); err != nil {
		t.Fatal(err)
	}
	// Uncaptured running scans still evaluate live policy and must roll back
	// mutations on read failure. Completed scans use their frozen policy.
	scan.Status = models.ScanStatusRunning
	if err := s.UpdateScan(ctx, scan); err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcileVulnerabilities(ctx, scan.ID); err != nil {
		t.Fatal(err)
	}
	snapshot := func() string {
		t.Helper()
		var findings []models.Finding
		var scans []models.Scan
		var rules []models.FindingSuppression
		var audits []models.FindingSuppressionAudit
		var canonical []models.Vulnerability
		for _, q := range []struct {
			out any
			sql string
		}{{&findings, "SELECT * FROM findings ORDER BY id"}, {&scans, "SELECT * FROM scans ORDER BY id"}, {&rules, "SELECT * FROM finding_suppressions ORDER BY id"}, {&audits, "SELECT * FROM finding_suppression_audit ORDER BY id"}, {&canonical, "SELECT * FROM vulnerabilities ORDER BY id"}} {
			if err := database.SelectContext(ctx, q.out, q.sql); err != nil {
				t.Fatal(err)
			}
		}
		raw, err := json.Marshal([]any{findings, scans, rules, audits, canonical})
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	name := filepath.Join(root, ".wolfignore")
	invalid := "** category=\n"
	if gitFailure {
		name = filepath.Join(root, ".git/config")
		invalid = "[broken config\n"
	}
	setInvalid := func() {
		t.Helper()
		if err := os.WriteFile(name, []byte(invalid), 0600); err != nil {
			t.Fatal(err)
		}
	}
	expires := time.Now().UTC().Add(time.Hour)
	rule := &models.FindingSuppression{ID: uuid.NewString(), RepoID: repo, ScopeType: models.SuppressionScopeRule, ScopeValue: "R1", Reason: "accepted", CreatedBy: user, ExpiresAt: &expires}
	setInvalid()
	before := snapshot()
	if err := s.CreateFindingSuppression(ctx, rule); err == nil || before != snapshot() {
		t.Fatalf("create did not roll back: %v", err)
	}
	if err := os.Remove(name); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateFindingSuppression(ctx, rule); err != nil {
		t.Fatal(err)
	}
	setInvalid()
	before = snapshot()
	if err := s.RevokeFindingSuppression(ctx, rule.ID, user); err == nil || before != snapshot() {
		t.Fatalf("revoke did not roll back: %v", err)
	}
	if n, err := s.ExpireFindingSuppressions(ctx, expires.Add(time.Second)); err == nil || n != 0 || before != snapshot() {
		t.Fatalf("expiry did not roll back: %d %v", n, err)
	}
	if err := os.Remove(name); err != nil {
		t.Fatal(err)
	}
	if n, err := s.ExpireFindingSuppressions(ctx, expires.Add(time.Second)); err != nil || n != 1 {
		t.Fatalf("corrected expiry retry: %d %v", n, err)
	}
	visible, err := s.ListFindingsByScan(ctx, scan.ID)
	if err != nil || len(visible) != len(rows) || visible[0].Suppressed {
		t.Fatalf("retry lost evidence: %+v %v", visible, err)
	}
}
