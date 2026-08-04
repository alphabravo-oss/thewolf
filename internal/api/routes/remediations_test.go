package routes_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/api"
	"github.com/alphabravocompany/thewolf/internal/api/routes"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/auth/apikey"
	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/remediate"
	"github.com/alphabravocompany/thewolf/internal/remediate/driver"
	"github.com/alphabravocompany/thewolf/internal/secrets"
)

// remediationServer bundles a real API server — so these tests exercise the
// actual scope middleware chain from server.go, not a hand-rolled router —
// with the store and owning user needed to mint scoped requests directly.
type remediationServer struct {
	*api.Server
	store  db.Store
	userID string
}

// newTestServer builds a real API server with the remediation Runner wired
// to a fake, in-memory driver (deterministic, no docker dependency) and a
// permissive default Config: enabled, yolo allowed. Individual tests that
// need a different Config use newTestServerWithConfig.
func newTestServer(t *testing.T) *remediationServer {
	t.Helper()
	store, err := db.NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("db.NewSQLite: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	auth.SetJWTSecret([]byte("test-secret-key-for-jwt-signing"))
	secrets.SetMasterKey([]byte("0123456789abcdef0123456789abcdef"))
	srv := api.NewServer(store, ":0")
	// api.NewServer binds auth.HumanAuthorizationResolver/RoleResolver/
	// SetSessionResolver to this store — package-level state shared by every
	// test in this binary (this package's test files import "internal/api"
	// for a real, scope-enforcing server). Left bound, they outlive this
	// test's store (closed by the Cleanup above) and break unrelated JWT
	// logins in sibling tests with "current authorization could not be
	// resolved" once a later test's request hits the closed DB through this
	// dangling resolver. Nothing else in this package needs role/session
	// resolution — only the API token resolver, kept below for withScopes —
	// so clear them rather than leave them dangling.
	auth.HumanAuthorizationResolver = nil
	auth.RoleResolver = nil
	auth.SetSessionResolver(nil)

	cfg := remediate.Config{Enabled: true, MaxTurns: 10, AllowYolo: true}
	routes.RemediationConfig = cfg
	routes.RemediationRunner = remediate.NewRunner(store, driver.NewFake(nil, nil), cfg)

	user := &models.User{ID: uuid.NewString(), Email: "remediation-" + uuid.NewString() + "@example.test", PasswordHash: "hash", Role: models.RoleUser}
	if err := store.CreateUser(context.Background(), user); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	return &remediationServer{Server: srv, store: store, userID: user.ID}
}

// newTestServerWithConfig is newTestServer with the remediation Runner's
// Config overridden after the fact — used by TestYoloRejectedWhenNotAllowed,
// which needs AllowYolo=false without disabling remediation entirely.
func newTestServerWithConfig(t *testing.T, cfg remediate.Config) *remediationServer {
	t.Helper()
	srv := newTestServer(t)
	routes.RemediationConfig = cfg
	routes.RemediationRunner = remediate.NewRunner(srv.store, driver.NewFake(nil, nil), cfg)
	return srv
}

// remediateConfigYoloDisabled is the Config for TestYoloRejectedWhenNotAllowed:
// remediation is enabled, but neither gate may be turned off without it.
func remediateConfigYoloDisabled() remediate.Config {
	return remediate.Config{Enabled: true, MaxTurns: 10, AllowYolo: false}
}

// withScopes mints an API token scoped to exactly the given scopes, owned by
// the server's test user, and attaches it to req as a bearer credential.
func (s *remediationServer) withScopes(t *testing.T, req *http.Request, scopes ...string) {
	t.Helper()
	plaintext, hash, prefix, err := apikey.Generate()
	if err != nil {
		t.Fatalf("apikey.Generate: %v", err)
	}
	if err := s.store.CreateAPIToken(context.Background(), &models.APIToken{
		ID: uuid.NewString(), UserID: s.userID, Name: "test-token",
		TokenHash: hash, TokenPrefix: prefix, ScopeList: scopes,
	}); err != nil {
		t.Fatalf("CreateAPIToken: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+plaintext)
}

// seedRemediationSession persists a session for the server's test user, with
// sane defaults the caller can override via mutate.
func seedRemediationSession(t *testing.T, s *remediationServer, mutate func(*models.RemediationSession)) string {
	t.Helper()
	sess := &models.RemediationSession{
		ID:               uuid.NewString(),
		UserID:           s.userID,
		RepoID:           "r-1",
		ScanID:           "sc-1",
		Status:           models.RemediationPending,
		PlanGateEnabled:  true,
		PatchGateEnabled: true,
		MaxTurns:         10,
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}
	if mutate != nil {
		mutate(sess)
	}
	if err := s.store.CreateRemediationSession(context.Background(), sess); err != nil {
		t.Fatalf("CreateRemediationSession: %v", err)
	}
	return sess.ID
}

// seedSessionPending seeds a session still in its initial (pending) state —
// the state ApprovePlan/ApprovePatches must refuse to act on.
func seedSessionPending(t *testing.T, s *remediationServer) string {
	return seedRemediationSession(t, s, nil)
}

func decodeJSON(t *testing.T, body []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(body, v); err != nil {
		t.Fatalf("json decode: %v (body: %s)", err, string(body))
	}
}

func TestCreateRemediationRequiresWriteScope(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/remediations",
		strings.NewReader(`{"scan_id":"sc-1","repo_id":"r-1"}`))
	req.Header.Set("Content-Type", "application/json")
	srv.withScopes(t, req, "read:fixes") // read only

	rec := httptest.NewRecorder()
	srv.Router.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestApprovePlanRejectsWrongState(t *testing.T) {
	srv := newTestServer(t)
	id := seedSessionPending(t, srv)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/remediations/"+id+"/plan/approve", nil)
	srv.withScopes(t, req, "write:fixes")
	rec := httptest.NewRecorder()
	srv.Router.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (session not awaiting approval): %s", rec.Code, rec.Body.String())
	}
}

func TestYoloRejectedWhenNotAllowed(t *testing.T) {
	srv := newTestServerWithConfig(t, remediateConfigYoloDisabled())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/remediations",
		strings.NewReader(`{"scan_id":"sc-1","repo_id":"r-1","plan_gate_enabled":false,"patch_gate_enabled":false}`))
	req.Header.Set("Content-Type", "application/json")
	srv.withScopes(t, req, "write:fixes")

	rec := httptest.NewRecorder()
	srv.Router.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (yolo not allowed): %s", rec.Code, rec.Body.String())
	}
}

func TestCreateRemediationUnauthenticated(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/remediations",
		strings.NewReader(`{"scan_id":"sc-1","repo_id":"r-1"}`))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	srv.Router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestCreateRemediationValidatesRequiredFields(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/remediations", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	srv.withScopes(t, req, "write:fixes")

	rec := httptest.NewRecorder()
	srv.Router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateRemediationDisabledReturns403(t *testing.T) {
	srv := newTestServerWithConfig(t, remediate.Config{Enabled: false, AllowYolo: true, MaxTurns: 10})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/remediations",
		strings.NewReader(`{"scan_id":"sc-1","repo_id":"r-1"}`))
	req.Header.Set("Content-Type", "application/json")
	srv.withScopes(t, req, "write:fixes")

	rec := httptest.NewRecorder()
	srv.Router.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (remediation disabled): %s", rec.Code, rec.Body.String())
	}
}

func TestGetRemediationNotFound(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/remediations/does-not-exist", nil)
	srv.withScopes(t, req, "read:fixes")

	rec := httptest.NewRecorder()
	srv.Router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestGetRemediationAndListRoundtrip(t *testing.T) {
	srv := newTestServer(t)
	id := seedSessionPending(t, srv)

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/remediations/"+id, nil)
	srv.withScopes(t, getReq, "read:fixes")
	getRec := httptest.NewRecorder()
	srv.Router.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200: %s", getRec.Code, getRec.Body.String())
	}
	var got struct {
		Data models.RemediationSession `json:"data"`
	}
	decodeJSON(t, getRec.Body.Bytes(), &got)
	if got.Data.ID != id {
		t.Errorf("GET id = %q, want %q", got.Data.ID, id)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/remediations", nil)
	srv.withScopes(t, listReq, "read:fixes")
	listRec := httptest.NewRecorder()
	srv.Router.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("LIST status = %d, want 200: %s", listRec.Code, listRec.Body.String())
	}
	var list struct {
		Data []models.RemediationSession `json:"data"`
	}
	decodeJSON(t, listRec.Body.Bytes(), &list)
	found := false
	for _, s := range list.Data {
		if s.ID == id {
			found = true
		}
	}
	if !found {
		t.Errorf("LIST did not include seeded session %s: %+v", id, list.Data)
	}
}

func TestApprovePatchesRejectsWrongState(t *testing.T) {
	srv := newTestServer(t)
	id := seedSessionPending(t, srv)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/remediations/"+id+"/patches/approve", nil)
	srv.withScopes(t, req, "write:fixes")
	rec := httptest.NewRecorder()
	srv.Router.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (session not awaiting patch approval): %s", rec.Code, rec.Body.String())
	}
}

// TestApprovePatchesHappyPathCompletes exercises a real, successful call
// into the Runner. ApprovePatches is the only approve/reject path that never
// touches the driver (runLandingPhase is still a stub per session.go, ahead
// of Task 13), so it can be asserted synchronously without a fake plan/patch
// fixture or a race against a background goroutine.
func TestApprovePatchesHappyPathCompletes(t *testing.T) {
	srv := newTestServer(t)
	id := seedRemediationSession(t, srv, func(s *models.RemediationSession) {
		s.Status = models.RemediationPatchReview
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/remediations/"+id+"/patches/approve", nil)
	srv.withScopes(t, req, "write:fixes")
	rec := httptest.NewRecorder()
	srv.Router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Data models.RemediationSession `json:"data"`
	}
	decodeJSON(t, rec.Body.Bytes(), &got)
	if got.Data.Status != models.RemediationCompleted {
		t.Errorf("status after approval = %q, want %q", got.Data.Status, models.RemediationCompleted)
	}
}

func TestRejectRemediationPlanHappyPathTerminates(t *testing.T) {
	srv := newTestServer(t)
	id := seedRemediationSession(t, srv, func(s *models.RemediationSession) {
		s.Status = models.RemediationPlanReview
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/remediations/"+id+"/plan/reject",
		strings.NewReader(`{"reason":"wrong approach"}`))
	req.Header.Set("Content-Type", "application/json")
	srv.withScopes(t, req, "write:fixes")
	rec := httptest.NewRecorder()
	srv.Router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Data models.RemediationSession `json:"data"`
	}
	decodeJSON(t, rec.Body.Bytes(), &got)
	if got.Data.Status != models.RemediationRejected {
		t.Errorf("status after rejection = %q, want %q", got.Data.Status, models.RemediationRejected)
	}
	if got.Data.FailureReason != "wrong approach" {
		t.Errorf("failure_reason = %q, want %q", got.Data.FailureReason, "wrong approach")
	}
}

func TestCancelRemediation(t *testing.T) {
	srv := newTestServer(t)
	id := seedSessionPending(t, srv)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/remediations/"+id, nil)
	srv.withScopes(t, req, "write:fixes")
	rec := httptest.NewRecorder()
	srv.Router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Data models.RemediationSession `json:"data"`
	}
	decodeJSON(t, rec.Body.Bytes(), &got)
	if got.Data.Status != models.RemediationCancelled {
		t.Errorf("status = %q, want %q", got.Data.Status, models.RemediationCancelled)
	}

	// Cancelling an already-finished session is a conflict, not a silent no-op.
	again := httptest.NewRequest(http.MethodDelete, "/api/v1/remediations/"+id, nil)
	srv.withScopes(t, again, "write:fixes")
	againRec := httptest.NewRecorder()
	srv.Router.ServeHTTP(againRec, again)
	if againRec.Code != http.StatusConflict {
		t.Fatalf("second cancel status = %d, want 409: %s", againRec.Code, againRec.Body.String())
	}
}

func TestCancelRemediationNotOwnedIsNotFound(t *testing.T) {
	srv := newTestServer(t)
	other := &models.User{ID: uuid.NewString(), Email: "other@example.test", PasswordHash: "hash", Role: models.RoleUser}
	if err := srv.store.CreateUser(context.Background(), other); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	id := seedRemediationSession(t, srv, func(s *models.RemediationSession) { s.UserID = other.ID })

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/remediations/"+id, nil)
	srv.withScopes(t, req, "write:fixes")
	rec := httptest.NewRecorder()
	srv.Router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (not owned), got body: %s", rec.Code, rec.Body.String())
	}
}
