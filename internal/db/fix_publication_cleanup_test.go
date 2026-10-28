package db

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestPublishedFixCleanupRetainsFailuresAndRetries(t *testing.T) {
	var store Store = newClusterPostgresStore(t)
	ctx := t.Context()
	if err := store.CreateUser(ctx, &models.User{ID: "user", Email: "publish@example.test", PasswordHash: "unused"}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateRepo(ctx, &models.Repo{ID: "repo", UserID: "user", Name: "repo", SourceType: models.SourceTypeGitHub}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateScan(ctx, &models.Scan{ID: "scan", UserID: "user", RepoID: "repo", Status: models.ScanStatusCompleted}); err != nil {
		t.Fatal(err)
	}
	job, _, err := store.CreateFixJob(ctx, &models.FixJob{ID: "job", UserID: "user", RepoID: "repo", ScanID: "scan"}, true)
	if err != nil {
		t.Fatal(err)
	}
	job.Status, job.WorkspacePath, job.PushSHA, job.Pushed = models.FixJobPushFailed, "/retained/clone", "published-sha", true
	if err := store.UpdateFixJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	rem, err := store.GetRemediationByID(ctx, job.RemediationID)
	if err != nil {
		t.Fatal(err)
	}
	publishedAt := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	rem.State, rem.WorkspacePath, rem.PublishedSHA, rem.PublishedAt = models.RemediationFrozen, job.WorkspacePath, job.PushSHA, &publishedAt
	if err := store.UpdateRemediation(ctx, rem); err != nil {
		t.Fatal(err)
	}
	calls := 0
	cleanupErr := errors.New("workspace locked")
	cleanup := func(context.Context, *models.Remediation, *models.Repo) (string, error) {
		calls++
		return job.PushSHA, cleanupErr
	}
	if err := store.AcceptFixRemediation(ctx, rem, cleanup); !errors.Is(err, ErrFixFrozen) || calls != 0 {
		t.Fatalf("failed PR lost its checkout: %v", err)
	}
	job.Status = models.FixJobSucceeded
	if err := store.UpdateFixJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := store.AcceptFixRemediation(ctx, rem, cleanup); !errors.Is(err, ErrFixFrozen) || calls != 0 {
		t.Fatalf("missing PR evidence allowed cleanup: %v", err)
	}
	job.Summary = `{"pr_url":"https://github.com/example/repo/pull/1"}`
	if err := store.UpdateFixJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := store.AcceptFixRemediation(ctx, rem, cleanup); !errors.Is(err, cleanupErr) {
		t.Fatalf("cleanup failure: %v", err)
	}
	cleanupErr = nil
	drop := failFixJobAcknowledgement(t, store)
	if err := store.AcceptFixRemediation(ctx, rem, cleanup); err == nil {
		t.Fatal("late database failure ignored")
	}
	drop()
	retained, err := store.GetRemediationByID(ctx, rem.ID)
	if err != nil || retained.WorkspacePath != job.WorkspacePath || retained.State != models.RemediationFrozen {
		t.Fatalf("cleanup failure lost recovery metadata: %+v %v", retained, err)
	}
	if err := store.AcceptFixRemediation(ctx, rem, cleanup); err != nil {
		t.Fatal(err)
	}
	saved, err := store.GetFixJobByID(ctx, job.ID)
	if err != nil || saved.WorkspacePath != "" || saved.Summary != job.Summary || !saved.Pushed || saved.PushSHA != job.PushSHA || saved.Revision <= job.Revision {
		t.Fatalf("publication lost or workspace not released: %+v %v", saved, err)
	}
	if rem.WorkspacePath != "" || !rem.PublishedAt.Equal(publishedAt) {
		t.Fatalf("remediation receipt: %+v", rem)
	}
	before := calls
	if err := store.AcceptFixRemediation(ctx, rem, cleanup); err != nil || calls != before {
		t.Fatalf("repeated cleanup not idempotent: %v", err)
	}
}
