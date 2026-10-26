package routes_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/api/routes"
	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/google/uuid"
)

type conflictedFixStore struct{ db.Store }

func (s conflictedFixStore) UpdateFixJob(context.Context, *models.FixJob) error {
	return db.ErrFixJobConflict
}

func TestFixMutationConflictReturns409(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()
	env.enableAutofix(t)
	job := &models.FixJob{ID: uuid.NewString(), RepoID: env.createRepo(t), UserID: env.UserID, Status: models.FixJobAwaitingPush, ResultBranch: "wolf-fix/review"}
	if err := env.Store.EnqueueFixJob(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	routes.SetHandler(conflictedFixStore{env.Store}, nil)
	for _, action := range []struct{ method, path string }{{http.MethodDelete, ""}, {http.MethodPost, "/resume"}} {
		result := env.doRequest(action.method, "/api/fixes/"+job.ID+action.path, nil)
		if result.Code != http.StatusConflict {
			t.Fatalf("%s: %d %s", action.method, result.Code, result.Body.String())
		}
	}
	saved, err := env.Store.GetFixJobByID(t.Context(), job.ID)
	if err != nil || saved.Status != job.Status || saved.ResultBranch != job.ResultBranch {
		t.Fatalf("conflict changed state: %+v %v", saved, err)
	}
}

func TestCancelFixRetainsAndRetriesFailedCleanup(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()
	env.enableAutofix(t)
	repo, scan := env.createRepo(t), ""
	scan = env.createScan(t, repo)
	created := env.doRequest(http.MethodPost, "/api/fixes", map[string]any{"repo_id": repo, "scan_id": scan})
	if created.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	var result struct {
		Data models.FixJob `json:"data"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	job := &result.Data
	retained := filepath.Join(t.TempDir(), "worktree")
	if err := os.Mkdir(retained, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(retained, "fix.txt"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	job.Status, job.WorkspacePath, job.ResultBranch = models.FixJobAwaitingPush, retained, job.TargetBranch
	if err := env.Store.UpdateFixJob(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	cancelled := env.doRequest(http.MethodDelete, "/api/fixes/"+job.ID, nil)
	if cancelled.Code != http.StatusInternalServerError || !strings.Contains(cancelled.Body.String(), "cleanup_pending") {
		t.Fatalf("cleanup failure: %d %s", cancelled.Code, cancelled.Body.String())
	}
	saved, err := env.Store.GetFixJobByID(t.Context(), job.ID)
	if err != nil || saved.Status != models.FixJobCancelled || saved.WorkspacePath != retained || saved.PauseReason != models.FixPauseCancellationCleanup || saved.Error == "" {
		t.Fatalf("lost recovery state: %+v %v", saved, err)
	}
	if _, err := os.Stat(filepath.Join(retained, "fix.txt")); err != nil {
		t.Fatal("cleanup failure deleted retained work")
	}
	blocked := env.doRequest(http.MethodPost, "/api/fixes", map[string]any{"repo_id": repo, "scan_id": scan})
	if blocked.Code != http.StatusConflict {
		t.Fatalf("new work bypassed cleanup: %d", blocked.Code)
	}
	accepted := env.doRequest(http.MethodPost, "/api/remediations/"+job.RemediationID+"/accept", nil)
	if accepted.Code != http.StatusConflict {
		t.Fatalf("accept bypassed pending cleanup: %d %s", accepted.Code, accepted.Body.String())
	}
	// The operator resolves the fixture's invalid metadata by removing its
	// disposable directory. Repeated DELETE retries cleanup without running agents.
	if err := os.RemoveAll(retained); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		retry := env.doRequest(http.MethodDelete, "/api/fixes/"+job.ID, nil)
		if retry.Code != http.StatusOK {
			t.Fatalf("cleanup retry: %d %s", retry.Code, retry.Body.String())
		}
	}
	saved, err = env.Store.GetFixJobByID(t.Context(), job.ID)
	if err != nil || saved.PauseReason != "" || saved.WorkspacePath != "" || saved.ResultBranch != "" || saved.Error != "" {
		t.Fatalf("cleanup not acknowledged: %+v %v", saved, err)
	}
	fresh := env.doRequest(http.MethodPost, "/api/fixes", map[string]any{"repo_id": repo, "scan_id": scan})
	if fresh.Code != http.StatusCreated {
		t.Fatalf("cleanup did not unblock work: %d %s", fresh.Code, fresh.Body.String())
	}
}
