package db

import (
	"context"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestRemediationOpenFreezeDiscard(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	_ = store.CreateUser(ctx, &models.User{ID: "u1", Email: "r@t", PasswordHash: "h"})
	_ = store.CreateRepo(ctx, &models.Repo{ID: "r1", UserID: "u1", Name: "n", SourceType: models.SourceTypeLocal, SourcePath: "/tmp", DefaultBranch: "main"})

	origin := &models.Scan{
		ID: "scan-origin", UserID: "u1", RepoID: "r1",
		Status: models.ScanStatusCompleted,
	}
	if err := store.CreateScan(ctx, origin); err != nil {
		t.Fatalf("CreateScan: %v", err)
	}

	rem := &models.Remediation{
		ID: "rem-1", UserID: "u1", RepoID: "r1",
		OriginScanID: "scan-origin", Branch: "wolf-fix/scan-origin",
		WorkspacePath: "/workspaces/ws-1", State: models.RemediationOpen,
	}
	if err := store.CreateRemediation(ctx, rem); err != nil {
		t.Fatalf("CreateRemediation: %v", err)
	}

	got, err := store.GetOpenRemediationByOrigin(ctx, "scan-origin")
	if err != nil || got == nil || got.ID != "rem-1" {
		t.Fatalf("open = %+v err=%v", got, err)
	}

	got.State = models.RemediationFrozen
	got.PublishedSHA = "abc"
	if err := store.UpdateRemediation(ctx, got); err != nil {
		t.Fatalf("UpdateRemediation: %v", err)
	}
	open, err := store.GetOpenRemediationByOrigin(ctx, "scan-origin")
	if err != nil {
		t.Fatalf("GetOpen after freeze: %v", err)
	}
	if open != nil {
		t.Fatalf("expected no open remediation, got %+v", open)
	}
	latest, err := store.GetLatestRemediationByOrigin(ctx, "scan-origin")
	if err != nil || latest == nil || latest.State != models.RemediationFrozen {
		t.Fatalf("latest after freeze = %+v err=%v", latest, err)
	}

	child := &models.Scan{
		ID: "scan-child", UserID: "u1", RepoID: "r1",
		OriginScanID: "scan-origin", PreviousScanID: "scan-origin",
		RemediationID: "rem-1", Status: models.ScanStatusPending,
		Branch: "wolf-fix/scan-origin", PreparedWorkspace: "/workspaces/ws-1",
	}
	if err := store.CreateScan(ctx, child); err != nil {
		t.Fatalf("CreateScan child: %v", err)
	}
	kids, err := store.ListScansByOrigin(ctx, "scan-origin")
	if err != nil {
		t.Fatalf("ListScansByOrigin: %v", err)
	}
	if len(kids) != 2 {
		t.Fatalf("lineage scans = %d, want 2", len(kids))
	}

	job := &models.FixJob{
		ID: "job-1", UserID: "u1", RepoID: "r1", ScanID: "scan-origin",
		RemediationID: "rem-1", TargetBranch: "wolf-fix/scan-origin",
		WorkspacePath: "/workspaces/ws-1",
	}
	if err := store.EnqueueFixJob(ctx, job); err != nil {
		t.Fatalf("EnqueueFixJob: %v", err)
	}
	jobs, err := store.ListFixJobsByRemediation(ctx, "rem-1")
	if err != nil || len(jobs) != 1 || jobs[0].RemediationID != "rem-1" {
		t.Fatalf("jobs = %+v err=%v", jobs, err)
	}
}
