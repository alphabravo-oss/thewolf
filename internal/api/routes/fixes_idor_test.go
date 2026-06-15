package routes_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

// TestFixJobsAreTenantScoped is the regression guard for the IDOR finding on
// the /fixes endpoints: a caller must never see, read the diff/log of, or
// cancel another user's fix job. We seed one job owned by the authenticated
// user and one owned by a different user, then exercise every per-id path.
func TestFixJobsAreTenantScoped(t *testing.T) {
	e := setupTestEnv(t)
	ctx := context.Background()

	mine := &models.FixJob{ID: "job-mine", UserID: e.UserID, RepoID: "r1", Engine: "auto"}
	theirs := &models.FixJob{ID: "job-theirs", UserID: "another-user-9000", RepoID: "r1", Engine: "auto"}
	if err := e.Store.EnqueueFixJob(ctx, mine); err != nil {
		t.Fatalf("enqueue mine: %v", err)
	}
	if err := e.Store.EnqueueFixJob(ctx, theirs); err != nil {
		t.Fatalf("enqueue theirs: %v", err)
	}

	// LIST must return only the caller's job, never the other tenant's.
	w := e.doRequest(http.MethodGet, "/api/fixes", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list fixes: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var list struct {
		Data []struct {
			ID     string `json:"id"`
			UserID string `json:"user_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list.Data) != 1 || list.Data[0].ID != "job-mine" {
		t.Fatalf("list leaked cross-tenant jobs: %+v", list.Data)
	}

	// Every per-id path on the other tenant's job must 404 (not 403 — we don't
	// even confirm the job exists).
	for _, path := range []struct {
		method, url string
	}{
		{http.MethodGet, "/api/fixes/job-theirs"},
		{http.MethodGet, "/api/fixes/job-theirs/diff"},
		{http.MethodDelete, "/api/fixes/job-theirs"},
	} {
		w := e.doRequest(path.method, path.url, nil)
		if w.Code != http.StatusNotFound {
			t.Errorf("%s %s on another user's job: expected 404, got %d: %s",
				path.method, path.url, w.Code, w.Body.String())
		}
	}

	// Sanity: the owner can still read their own job.
	if w := e.doRequest(http.MethodGet, "/api/fixes/job-mine", nil); w.Code != http.StatusOK {
		t.Errorf("owner reading own job: expected 200, got %d: %s", w.Code, w.Body.String())
	}
}
