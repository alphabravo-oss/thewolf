package worker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/alphabravocompany/thewolf/internal/fix/fixstore"
	"github.com/alphabravocompany/thewolf/internal/fix/orchestrator"
	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestWorkerPublicationCleanup(t *testing.T) {
	for _, outcome := range []string{"removed-before-ack", "cleanup-failed", "pr-failed"} {
		t.Run(outcome, func(t *testing.T) {
			store := newStore(t)
			ctx := t.Context()
			if err := store.CreateUser(ctx, &models.User{ID: "user", Email: "cleanup@example.test", PasswordHash: "unused"}); err != nil {
				t.Fatal(err)
			}
			if err := store.CreateRepo(ctx, &models.Repo{ID: "repo", UserID: "user", Name: "repo", SourceType: models.SourceTypeLocal, SourcePath: t.TempDir()}); err != nil {
				t.Fatal(err)
			}
			if err := store.CreateScan(ctx, &models.Scan{ID: "scan", UserID: "user", RepoID: "repo", Status: models.ScanStatusCompleted}); err != nil {
				t.Fatal(err)
			}
			queued, _, err := store.CreateFixJob(ctx, &models.FixJob{ID: "job", UserID: "user", RepoID: "repo", ScanID: "scan"}, true)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "retained")
			if outcome != "removed-before-ack" {
				// Existing files without Wolf metadata must be preserved.
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			job, err := store.ClaimNextFixJob(ctx, "worker")
			if err != nil || job == nil {
				t.Fatalf("claim: %+v %v", job, err)
			}
			stubRun(t, func(ctx context.Context, j *models.FixJob, _ orchestrator.Deps) (*orchestrator.Result, error) {
				j.Status, j.Pushed, j.PushSHA, j.WorkspacePath = models.FixJobSucceeded, true, "published-sha", path
				if outcome == "pr-failed" {
					j.Status, j.Error = models.FixJobPushFailed, "PR creation failed"
				}
				return &orchestrator.Result{}, store.UpdateFixJob(ctx, j)
			})
			w, err := New(Config{Store: store, Fixstore: fixstore.New(t.TempDir())})
			if err != nil {
				t.Fatal(err)
			}
			logger := zerolog.Nop()
			w.process(ctx, &logger, job)
			got, err := store.GetFixJobByID(ctx, queued.ID)
			if err != nil {
				t.Fatal(err)
			}
			rem, err := store.GetRemediationByID(ctx, queued.RemediationID)
			if err != nil || rem.State != models.RemediationFrozen || rem.PublishedSHA != "published-sha" {
				t.Fatalf("publication receipt: %+v %v", rem, err)
			}
			if outcome == "removed-before-ack" {
				if got.WorkspacePath != "" || rem.WorkspacePath != "" || got.Status != models.FixJobSucceeded {
					t.Fatalf("cleanup not acknowledged: %+v %+v", got, rem)
				}
			} else {
				if got.WorkspacePath != path || rem.WorkspacePath != path {
					t.Fatal("recovery workspace lost")
				}
				if outcome == "cleanup-failed" && !strings.Contains(got.Error, "workspace cleanup failed") {
					t.Fatalf("cleanup error hidden: %+v", got)
				}
				if outcome == "pr-failed" && (got.Status != models.FixJobPushFailed || got.Error != "PR creation failed") {
					t.Fatalf("PR retry was replaced by cleanup: %+v", got)
				}
			}
		})
	}
}
