package routes_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/auth"
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

func TestFixerConsoleRequiresAdminAndIsTenantScoped(t *testing.T) {
	e := setupTestEnv(t)
	auth.RoleResolver = func(ctx context.Context, userID string) string {
		if userID == e.UserID {
			return "admin"
		}
		return "user"
	}
	t.Cleanup(func() { auth.RoleResolver = nil })

	w := e.doRequest(http.MethodPost, "/api/fixes/consoles", map[string]string{
		"kind": "login", "engine": "claude",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create console: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var created struct {
		Data models.FixerConsole `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.Data.ID == "" || created.Data.Status != models.FixerConsoleQueued {
		t.Fatalf("created = %+v", created.Data)
	}

	w = e.doRequest(http.MethodGet, "/api/fixes/consoles/"+created.Data.ID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get own console: %d %s", w.Code, w.Body.String())
	}

	theirs := &models.FixerConsole{
		ID:     "cons-theirs",
		UserID: "another-admin",
		Kind:   models.FixerConsoleLogin,
		Engine: "codex",
		Status: models.FixerConsoleQueued,
	}
	if err := e.Store.EnqueueFixerConsole(context.Background(), theirs); err != nil {
		t.Fatalf("enqueue theirs: %v", err)
	}
	if w := e.doRequest(http.MethodGet, "/api/fixes/consoles/cons-theirs", nil); w.Code != http.StatusNotFound {
		t.Errorf("cross-tenant get: expected 404, got %d: %s", w.Code, w.Body.String())
	}
	if w := e.doRequest(http.MethodPost, "/api/fixes/consoles/cons-theirs/input", map[string]string{"data": "x"}); w.Code != http.StatusNotFound {
		t.Errorf("cross-tenant input: expected 404, got %d: %s", w.Code, w.Body.String())
	}
	if w := e.doRequest(http.MethodDelete, "/api/fixes/consoles/cons-theirs", nil); w.Code != http.StatusNotFound {
		t.Errorf("cross-tenant cancel: expected 404, got %d: %s", w.Code, w.Body.String())
	}

	w = e.doRequest(http.MethodPost, "/api/fixes/consoles", map[string]string{"kind": "shell"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("shell without setting: expected 403, got %d: %s", w.Code, w.Body.String())
	}
	if err := e.Store.SetSetting(context.Background(), "fixer_console_shell", "true"); err != nil {
		t.Fatalf("set setting: %v", err)
	}
	w = e.doRequest(http.MethodPost, "/api/fixes/consoles", map[string]string{"kind": "shell"})
	if w.Code != http.StatusCreated {
		t.Fatalf("shell with setting: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	auth.RoleResolver = func(context.Context, string) string { return "user" }
	w = e.doRequest(http.MethodPost, "/api/fixes/consoles", map[string]string{
		"kind": "login", "engine": "claude",
	})
	if w.Code != http.StatusForbidden {
		t.Fatalf("non-admin create: expected 403, got %d: %s", w.Code, w.Body.String())
	}
}
