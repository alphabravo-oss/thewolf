package lineage

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestBranchAndOrigin(t *testing.T) {
	if got := BranchName("abc"); got != "wolf-fix/abc" {
		t.Fatalf("BranchName=%q", got)
	}
	root := &models.Scan{ID: "s0"}
	if OriginID(root) != "s0" {
		t.Fatalf("root origin=%q", OriginID(root))
	}
	child := &models.Scan{ID: "s1", OriginScanID: "s0"}
	if OriginID(child) != "s0" {
		t.Fatalf("child origin=%q", OriginID(child))
	}
}

func seedRepo(t *testing.T, store *db.SQLiteStore, userID, repoID string) {
	t.Helper()
	ctx := context.Background()
	_ = store.CreateUser(ctx, &models.User{ID: userID, Email: userID + "@t", PasswordHash: "h"})
	_ = store.CreateRepo(ctx, &models.Repo{
		ID: repoID, UserID: userID, Name: "n",
		SourceType: models.SourceTypeGitHub, SourcePath: "acme/astro", DefaultBranch: "main",
	})
}

func TestEnqueueChildScanFields(t *testing.T) {
	ctx := context.Background()
	store := newLineageStore(t)
	seedRepo(t, store, "u1", "r1")
	origin := &models.Scan{
		ID: "origin-1", UserID: "u1", RepoID: "r1",
		SourceType: models.SourceTypeGitHub, SourcePath: "acme/astro",
		Branch: "main", Profile: "full", ToolsSelected: `["bearer"]`,
		Status: models.ScanStatusCompleted,
	}
	if err := store.CreateScan(ctx, origin); err != nil {
		t.Fatalf("CreateScan: %v", err)
	}
	ws := t.TempDir()
	rem := &models.Remediation{
		ID: "rem-1", UserID: "u1", RepoID: "r1",
		OriginScanID: "origin-1", Branch: BranchName("origin-1"),
		WorkspacePath: ws, State: models.RemediationOpen,
	}
	if err := store.CreateRemediation(ctx, rem); err != nil {
		t.Fatalf("CreateRemediation: %v", err)
	}
	job := &models.FixJob{
		ID: "job-1", UserID: "u1", RepoID: "r1", ScanID: "origin-1",
		RemediationID: "rem-1", Status: models.FixJobAwaitingPush,
		WorkspacePath: ws,
	}
	child, err := EnqueueChildScan(ctx, store, origin, job, rem)
	if err != nil {
		t.Fatalf("EnqueueChildScan: %v", err)
	}
	if child.OriginScanID != "origin-1" {
		t.Fatalf("origin_scan_id=%q", child.OriginScanID)
	}
	if child.PreviousScanID != "origin-1" {
		t.Fatalf("previous_scan_id=%q", child.PreviousScanID)
	}
	if child.RepoID != origin.RepoID || child.SourceType != origin.SourceType {
		t.Fatalf("repo/source mismatch: %+v", child)
	}
	if child.Branch != "wolf-fix/origin-1" {
		t.Fatalf("branch=%q", child.Branch)
	}
	if child.PreparedWorkspace != ws {
		t.Fatalf("prepared_workspace=%q", child.PreparedWorkspace)
	}
	if child.FixJobID != "job-1" || child.RemediationID != "rem-1" {
		t.Fatalf("links %+v", child)
	}
}

func TestAfterAgentRunEnqueuesAndFreezeOnPush(t *testing.T) {
	ctx := context.Background()
	store := newLineageStore(t)
	seedRepo(t, store, "u1", "r1")
	origin := &models.Scan{
		ID: "origin-2", UserID: "u1", RepoID: "r1",
		SourceType: models.SourceTypeLocal, Status: models.ScanStatusCompleted,
	}
	if err := store.CreateScan(ctx, origin); err != nil {
		t.Fatalf("CreateScan: %v", err)
	}
	ws := filepath.Join(t.TempDir(), "clone")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	rem := &models.Remediation{
		ID: "rem-2", UserID: "u1", RepoID: "r1",
		OriginScanID: "origin-2", Branch: BranchName("origin-2"),
		WorkspacePath: ws, State: models.RemediationOpen,
	}
	if err := store.CreateRemediation(ctx, rem); err != nil {
		t.Fatalf("CreateRemediation: %v", err)
	}
	job := &models.FixJob{
		ID: "job-2", UserID: "u1", RepoID: "r1",
		RemediationID: "rem-2", Status: models.FixJobSucceeded,
		Pushed: true, PushSHA: "deadbeef", WorkspacePath: ws,
	}
	child, err := AfterAgentRun(ctx, store, job)
	if err != nil {
		t.Fatalf("AfterAgentRun: %v", err)
	}
	if child != nil {
		t.Fatalf("push should not enqueue a workspace rescan, got %+v", child)
	}
	latest, _ := store.GetLatestRemediationByOrigin(ctx, "origin-2")
	if latest == nil || latest.State != models.RemediationFrozen || latest.PublishedSHA != "deadbeef" {
		t.Fatalf("expected frozen rem, got %+v", latest)
	}
}

func TestAfterAgentRunEnqueuesWhenAwaitingPush(t *testing.T) {
	ctx := context.Background()
	store := newLineageStore(t)
	seedRepo(t, store, "u1", "r1")
	origin := &models.Scan{
		ID: "origin-w", UserID: "u1", RepoID: "r1",
		SourceType: models.SourceTypeLocal, Status: models.ScanStatusCompleted,
	}
	if err := store.CreateScan(ctx, origin); err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	rem := &models.Remediation{
		ID: "rem-w", UserID: "u1", RepoID: "r1",
		OriginScanID: "origin-w", Branch: BranchName("origin-w"),
		WorkspacePath: ws, State: models.RemediationOpen,
	}
	if err := store.CreateRemediation(ctx, rem); err != nil {
		t.Fatal(err)
	}
	job := &models.FixJob{
		ID: "job-w", UserID: "u1", RepoID: "r1",
		RemediationID: "rem-w", Status: models.FixJobAwaitingPush,
		WorkspacePath: ws,
	}
	child, err := AfterAgentRun(ctx, store, job)
	if err != nil || child == nil {
		t.Fatalf("AfterAgentRun: child=%v err=%v", child, err)
	}
}

func TestSupersedeSiblingsMarksOlderAwaitingPush(t *testing.T) {
	ctx := context.Background()
	store := newLineageStore(t)
	seedRepo(t, store, "u1", "r1")
	origin := &models.Scan{
		ID: "origin-s", UserID: "u1", RepoID: "r1",
		Status: models.ScanStatusCompleted,
	}
	if err := store.CreateScan(ctx, origin); err != nil {
		t.Fatal(err)
	}
	rem := &models.Remediation{
		ID: "rem-s", UserID: "u1", RepoID: "r1",
		OriginScanID: "origin-s", Branch: BranchName("origin-s"),
		State: models.RemediationOpen,
	}
	if err := store.CreateRemediation(ctx, rem); err != nil {
		t.Fatal(err)
	}
	old := &models.FixJob{
		ID: "job-old", UserID: "u1", RepoID: "r1", ScanID: "origin-s",
		RemediationID: "rem-s", Status: models.FixJobAwaitingPush,
		ResultBranch: BranchName("origin-s"),
	}
	if err := store.EnqueueFixJob(ctx, old); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateFixJob(ctx, old); err != nil {
		t.Fatal(err)
	}
	newer := &models.FixJob{
		ID: "job-new", UserID: "u1", RepoID: "r1", ScanID: "origin-s",
		RemediationID: "rem-s", Status: models.FixJobRunning,
		ResultBranch: BranchName("origin-s"),
	}
	if err := SupersedeSiblings(ctx, store, newer); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetFixJobByID(ctx, "job-old")
	if err != nil || got == nil {
		t.Fatalf("get old: %v", err)
	}
	if got.Status != models.FixJobSuperseded {
		t.Fatalf("old status=%q want superseded", got.Status)
	}
}

func TestBuildRunsPairsChildAndDelta(t *testing.T) {
	origin := &models.Scan{ID: "o", FindingCount: 100}
	child := models.Scan{
		ID: "c1", FixJobID: "j1", FindingCount: 80,
		Status: models.ScanStatusCompleted,
	}
	job := models.FixJob{
		ID: "j1", ScanID: "o", Status: models.FixJobAwaitingPush,
		RunIndex: 1, PlannedRuns: 3,
		Summary: `{"kept":12,"muted":5,"unfixable":1,"remaining":80}`,
	}
	runs := BuildRuns(origin, []models.Scan{child}, []models.FixJob{job})
	if len(runs) != 1 {
		t.Fatalf("runs=%d", len(runs))
	}
	r := runs[0]
	if r.InputFindings != 100 || r.OutputFindings == nil || *r.OutputFindings != 80 {
		t.Fatalf("counts %+v", r)
	}
	if r.Delta == nil || *r.Delta != -20 || r.Kept != 12 || r.Muted != 5 {
		t.Fatalf("delta/summary %+v", r)
	}
}

func TestMaybeEnqueueNextRun(t *testing.T) {
	ctx := context.Background()
	store := newLineageStore(t)
	seedRepo(t, store, "u1", "r1")
	origin := &models.Scan{
		ID: "origin-n", UserID: "u1", RepoID: "r1",
		Status: models.ScanStatusCompleted, FindingCount: 10,
	}
	if err := store.CreateScan(ctx, origin); err != nil {
		t.Fatal(err)
	}
	rem := &models.Remediation{
		ID: "rem-n", UserID: "u1", RepoID: "r1",
		OriginScanID: "origin-n", Branch: BranchName("origin-n"),
		State: models.RemediationOpen,
	}
	if err := store.CreateRemediation(ctx, rem); err != nil {
		t.Fatal(err)
	}
	prev := &models.FixJob{
		ID: "job-n1", UserID: "u1", RepoID: "r1", ScanID: "origin-n",
		RemediationID: "rem-n", Status: models.FixJobAwaitingPush,
		Engine: "opencode", Mode: models.FixModeDryRun,
		PlannedRuns: 3, RunIndex: 1, MaxLoops: 2,
	}
	if err := store.EnqueueFixJob(ctx, prev); err != nil {
		t.Fatal(err)
	}
	child := &models.Scan{
		ID: "child-n", UserID: "u1", RepoID: "r1",
		OriginScanID: "origin-n", FixJobID: "job-n1",
		RemediationID: "rem-n", Status: models.ScanStatusCompleted,
		FindingCount: 8,
	}
	if err := store.CreateScan(ctx, child); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateFinding(ctx, &models.Finding{
		ID: "f1", ScanID: child.ID, RepoID: "r1", ToolName: "gosec",
		Title: "x", FilePath: "a.go", Status: models.StatusOpen,
		Severity: models.SeverityHigh, Category: models.CategorySAST,
		Fingerprint: "fp-f1",
	}); err != nil {
		t.Fatal(err)
	}
	next, err := MaybeEnqueueNextRun(ctx, store, child)
	if err != nil || next == nil {
		t.Fatalf("next=%v err=%v", next, err)
	}
	if next.RunIndex != 2 || next.PlannedRuns != 3 || next.ScanID != child.ID {
		t.Fatalf("next %+v", next)
	}
	again, err := MaybeEnqueueNextRun(ctx, store, child)
	if err != nil || again != nil {
		t.Fatalf("duplicate next=%v err=%v", again, err)
	}
}

func newLineageStore(t *testing.T) *db.SQLiteStore {
	t.Helper()
	store, err := db.NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}
