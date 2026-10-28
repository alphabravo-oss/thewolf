package routes

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/go-chi/chi/v5"
)

func TestDASTApprovalAPI(t *testing.T) {
	store := db.NewTestPostgres(t)
	old := DefaultHandler
	t.Cleanup(func() { DefaultHandler = old })
	SetHandler(store, plugin.NewRegistry())
	ctx := t.Context()
	if err := store.CreateUser(ctx, &models.User{ID: "owner", Email: "dast@example.test", PasswordHash: "unused"}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateRepo(ctx, &models.Repo{ID: "repo", UserID: "owner", Name: "DAST", SourcePath: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	r.HandleFunc("/repos/{id}/dast-target", RepoDASTTarget)
	r.Post("/repos/{id}/dast-target/approve", RepoDASTTarget)
	r.Post("/scans", CreateScan)
	call := func(method, path, body, role string, want int) models.DASTTarget {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req = req.WithContext(context.WithValue(req.Context(), auth.UserContextKey, &auth.Claims{UserID: "owner", Role: role}))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		var result struct{ Data models.DASTTarget }
		_ = json.Unmarshal(w.Body.Bytes(), &result)
		return result.Data
	}
	path := "/repos/repo/dast-target"
	call("POST", "/scans", `{"repo_id":"repo","target":"http://169.254.169.254/"}`, "admin", 400)
	call("PUT", path, `{"url":"http://169.254.169.254/","revision":0}`, "user", 400)
	target := call("PUT", path, `{"url":"http://127.0.0.1:18941/","mode":"full","revision":0}`, "user", 200)
	if target.Approved() {
		t.Fatal("save granted approval")
	}
	if _, err := resolveDAST(ctx, store, "repo"); !plugin.IsNotApplicable(err) {
		t.Fatalf("unapproved target: %v", err)
	}
	call("POST", path+"/approve", `{"revision":1,"allow_private":true,"approve_active":true}`, "user", 403)
	call("POST", path+"/approve", `{"revision":1,"allow_private":true}`, "admin", 400)
	call("POST", path+"/approve", `{"revision":1,"approve_active":true}`, "admin", 400)
	target = call("POST", path+"/approve", `{"revision":1,"allow_private":true,"approve_active":true}`, "admin", 200)
	if !target.Approved() {
		t.Fatal("admin approval missing")
	}
	resolved, err := resolveDAST(ctx, store, "repo")
	if err != nil || resolved.Target.URL != target.URL {
		t.Fatalf("execution target: %+v %v", resolved, err)
	}
	call("PUT", path, `{"url":"http://127.0.0.1:18941/","mode":"baseline","revision":2}`, "user", 200)
	call("POST", path+"/approve", `{"revision":2,"allow_private":true,"approve_active":true}`, "admin", 409)
	if _, err := resolveDAST(ctx, store, "repo"); !plugin.IsNotApplicable(err) {
		t.Fatal("mode change retained approval")
	}
}
