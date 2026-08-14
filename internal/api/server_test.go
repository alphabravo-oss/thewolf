package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/api"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/auth/apikey"
	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/secrets"
)

// newTestServer builds a real API server backed by an in-memory database
// and returns it along with a JWT for a freshly registered user.
func newTestServer(t *testing.T) (*api.Server, db.Store, string) {
	t.Helper()
	store, err := db.NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	auth.SetJWTSecret([]byte("test-secret-key-for-jwt-signing"))
	// Production always has a master key (LoadMasterKey auto-generates one);
	// mirror that so encryption-backed endpoints (secrets, MFA) work in tests.
	secrets.SetMasterKey([]byte("0123456789abcdef0123456789abcdef"))
	srv := api.NewServer(store, ":0")

	body, _ := json.Marshal(map[string]string{"email": "dev@example.com", "password": "password1234"})
	w := request(srv, http.MethodPost, "/api/v1/auth/register", "", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("register: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var reg struct {
		Data struct {
			AccessToken string `json:"access_token"`
		} `json:"data"`
	}
	mustJSON(t, w.Body.Bytes(), &reg)
	if reg.Data.AccessToken == "" {
		t.Fatal("register: no access_token returned")
	}
	return srv, store, reg.Data.AccessToken
}

func request(srv *api.Server, method, path, bearer string, body []byte) *httptest.ResponseRecorder {
	var r *http.Request
	if body != nil {
		r = httptest.NewRequest(method, path, bytes.NewReader(body))
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, r)
	return w
}

func mustJSON(t *testing.T, b []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("json decode: %v (body: %s)", err, string(b))
	}
}

// createToken mints an API token via the API using a JWT, returning the
// one-time plaintext secret.
func createToken(t *testing.T, srv *api.Server, jwt string, scopes []string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"name": "test-token", "scopes": scopes})
	w := request(srv, http.MethodPost, "/api/v1/auth/tokens", jwt, body)
	if w.Code != http.StatusCreated {
		t.Fatalf("create token: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Data struct {
			Token  string   `json:"token"`
			Scopes []string `json:"scopes"`
		} `json:"data"`
	}
	mustJSON(t, w.Body.Bytes(), &resp)
	if resp.Data.Token == "" {
		t.Fatal("create token: no plaintext token returned")
	}
	return resp.Data.Token
}

func TestHealthIsPublic(t *testing.T) {
	srv, _, _ := newTestServer(t)
	w := request(srv, http.MethodGet, "/api/v1/health", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("health: expected 200, got %d", w.Code)
	}
}

func TestScannerReleaseMetricsArePublicAndPrometheusCompatible(t *testing.T) {
	srv, _, _ := newTestServer(t)
	w := request(srv, http.MethodGet, "/api/v1/metrics", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("metrics: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if contentType := w.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "text/plain") {
		t.Fatalf("metrics content type = %q", contentType)
	}
	if body := w.Body.String(); !strings.Contains(body, "wolf_scanner_release_database_ready 1") {
		t.Fatalf("metrics do not include database readiness:\n%s", body)
	}
}

func TestStaticUIRequestsDoNotConsumeAPIRateLimit(t *testing.T) {
	uiDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(uiDir, "assets"), 0o755); err != nil {
		t.Fatalf("mkdir assets: %v", err)
	}
	if err := os.WriteFile(filepath.Join(uiDir, "index.html"), []byte("<!doctype html><main>wolf</main>"), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}
	if err := os.WriteFile(filepath.Join(uiDir, "assets", "app.js"), []byte("console.log('wolf')"), 0o644); err != nil {
		t.Fatalf("write asset: %v", err)
	}
	t.Setenv("WOLF_UI_DIR", uiDir)

	srv, _, _ := newTestServer(t)
	for i := 0; i < 200; i++ {
		w := request(srv, http.MethodGet, "/assets/app.js", "", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("static asset request %d: expected 200, got %d", i+1, w.Code)
		}
	}

	w := request(srv, http.MethodGet, "/api/v1/health", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("health after static requests: expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestProtectedEndpointRejectsMissingCredential(t *testing.T) {
	srv, _, _ := newTestServer(t)
	w := request(srv, http.MethodGet, "/api/v1/scans", "", nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without credential, got %d", w.Code)
	}
}

func TestTokenScopeAllowsAndDenies(t *testing.T) {
	srv, _, jwt := newTestServer(t)
	token := createToken(t, srv, jwt, []string{apikey.ScopeReadScans})

	// read:scans -> GET /scans is allowed.
	if w := request(srv, http.MethodGet, "/api/v1/scans", token, nil); w.Code != http.StatusOK {
		t.Errorf("read:scans token on GET /scans: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// read:scans -> POST /repos requires write:repos, must be 403.
	body, _ := json.Marshal(map[string]string{"url": "https://example.com/x.git"})
	if w := request(srv, http.MethodPost, "/api/v1/repos", token, body); w.Code != http.StatusForbidden {
		t.Errorf("read:scans token on POST /repos: expected 403, got %d", w.Code)
	}

	// read:scans -> GET /users requires admin, must be 403.
	if w := request(srv, http.MethodGet, "/api/v1/users", token, nil); w.Code != http.StatusForbidden {
		t.Errorf("non-admin token on GET /users: expected 403, got %d", w.Code)
	}
}

func TestWriteScopeImpliesRead(t *testing.T) {
	srv, _, jwt := newTestServer(t)
	token := createToken(t, srv, jwt, []string{apikey.ScopeWriteScans})
	// write:scans must satisfy the read:scans requirement on GET /scans.
	if w := request(srv, http.MethodGet, "/api/v1/scans", token, nil); w.Code != http.StatusOK {
		t.Errorf("write:scans token on GET /scans: expected 200, got %d", w.Code)
	}
}

func TestJWTHoldsEveryScope(t *testing.T) {
	srv, _, jwt := newTestServer(t)
	// A JWT is the UI: it must reach an admin-only endpoint.
	if w := request(srv, http.MethodGet, "/api/v1/users", jwt, nil); w.Code != http.StatusOK {
		t.Errorf("JWT on GET /users: expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestRevokedTokenIsRejected(t *testing.T) {
	srv, _, jwt := newTestServer(t)
	token := createToken(t, srv, jwt, []string{apikey.ScopeReadScans})

	// Find the token's ID and revoke it.
	w := request(srv, http.MethodGet, "/api/v1/auth/tokens", jwt, nil)
	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	mustJSON(t, w.Body.Bytes(), &list)
	if len(list.Data) != 1 {
		t.Fatalf("expected 1 token, got %d", len(list.Data))
	}
	if dw := request(srv, http.MethodDelete, "/api/v1/auth/tokens/"+list.Data[0].ID, jwt, nil); dw.Code != http.StatusNoContent {
		t.Fatalf("revoke: expected 204, got %d", dw.Code)
	}
	if rw := request(srv, http.MethodGet, "/api/v1/scans", token, nil); rw.Code != http.StatusUnauthorized {
		t.Errorf("revoked token: expected 401, got %d", rw.Code)
	}
}

func TestExpiredTokenIsRejected(t *testing.T) {
	srv, store, _ := newTestServer(t)
	plaintext, hash, prefix, _ := apikey.Generate()
	past := time.Now().Add(-time.Hour)
	if err := store.CreateAPIToken(context.Background(), &models.APIToken{
		ID: "expired-1", UserID: "dev", Name: "old", TokenHash: hash,
		TokenPrefix: prefix, ScopeList: apikey.ScopeSet{apikey.ScopeReadScans},
		ExpiresAt: &past,
	}); err != nil {
		t.Fatalf("CreateAPIToken: %v", err)
	}
	if w := request(srv, http.MethodGet, "/api/v1/scans", plaintext, nil); w.Code != http.StatusUnauthorized {
		t.Errorf("expired token: expected 401, got %d", w.Code)
	}
}

func TestUnknownTokenIsRejected(t *testing.T) {
	srv, _, _ := newTestServer(t)
	if w := request(srv, http.MethodGet, "/api/v1/scans", "wolf_bogusbogusbogus", nil); w.Code != http.StatusUnauthorized {
		t.Errorf("unknown token: expected 401, got %d", w.Code)
	}
}

func TestCreateTokenRejectsBadScope(t *testing.T) {
	srv, _, jwt := newTestServer(t)
	body, _ := json.Marshal(map[string]any{"name": "x", "scopes": []string{"read:nonsense"}})
	if w := request(srv, http.MethodPost, "/api/v1/auth/tokens", jwt, body); w.Code != http.StatusBadRequest {
		t.Errorf("bad scope: expected 400, got %d", w.Code)
	}
}

func TestLegacyAPIPathRedirectsToV1(t *testing.T) {
	srv, _, _ := newTestServer(t)
	w := request(srv, http.MethodGet, "/api/health", "", nil)
	if w.Code != http.StatusTemporaryRedirect {
		t.Fatalf("/api/health: expected 307, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/api/v1/health" {
		t.Errorf("expected redirect to /api/v1/health, got %q", loc)
	}
	if w.Header().Get("Deprecation") != "true" {
		t.Error("legacy alias should set the Deprecation header")
	}
}

func TestCredentialedCORSRejectsUntrustedOrigins(t *testing.T) {
	srv, _, _ := newTestServer(t)

	allowedReq := httptest.NewRequest(http.MethodOptions, "/api/v1/auth/login", nil)
	allowedReq.Header.Set("Origin", "http://localhost:3000")
	allowedReq.Header.Set("Access-Control-Request-Method", "POST")
	allowed := httptest.NewRecorder()
	srv.Router.ServeHTTP(allowed, allowedReq)
	if got := allowed.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:3000" {
		t.Fatalf("trusted origin should be allowed, got %q", got)
	}

	blockedReq := httptest.NewRequest(http.MethodOptions, "/api/v1/auth/login", nil)
	blockedReq.Header.Set("Origin", "https://evil.example")
	blockedReq.Header.Set("Access-Control-Request-Method", "POST")
	blocked := httptest.NewRecorder()
	srv.Router.ServeHTTP(blocked, blockedReq)
	if got := blocked.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("untrusted origin should not be allowed, got %q", got)
	}
}

func TestOpenAPISpecIsServedPublicly(t *testing.T) {
	srv, _, _ := newTestServer(t)
	w := request(srv, http.MethodGet, "/api/v1/openapi.json", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("openapi.json: expected 200, got %d", w.Code)
	}
	var doc map[string]any
	mustJSON(t, w.Body.Bytes(), &doc)
	if doc["openapi"] == nil || doc["paths"] == nil {
		t.Error("openapi.json is missing required fields")
	}

	docsW := request(srv, http.MethodGet, "/api/v1/docs", "", nil)
	if docsW.Code != http.StatusOK {
		t.Errorf("/docs: expected 200, got %d", docsW.Code)
	}
	if b, _ := io.ReadAll(docsW.Body); !bytes.Contains(b, []byte("swagger")) {
		t.Error("/docs did not render Swagger UI")
	}
}

// TestSwaggerUIIsFullyOffline verifies the docs page references no external
// CDN and that its CSS/JS load from the binary's embedded assets with the
// correct MIME types — so the page renders with no internet access.
func TestSwaggerUIIsFullyOffline(t *testing.T) {
	srv, _, _ := newTestServer(t)

	htmlW := request(srv, http.MethodGet, "/api/v1/docs", "", nil)
	html := htmlW.Body.String()
	for _, cdn := range []string{"unpkg.com", "cdn.redoc.ly", "cdn.jsdelivr", "//http"} {
		if bytes.Contains([]byte(html), []byte(cdn)) {
			t.Errorf("Swagger UI page references external host %q — not offline", cdn)
		}
	}

	cases := []struct {
		path, wantType string
	}{
		{"/api/v1/docs/static/swagger-ui.css", "text/css"},
		{"/api/v1/docs/static/swagger-ui-bundle.js", "application/javascript"},
		{"/api/v1/docs/static/redoc.standalone.js", "application/javascript"},
	}
	for _, c := range cases {
		w := request(srv, http.MethodGet, c.path, "", nil)
		if w.Code != http.StatusOK {
			t.Errorf("%s: expected 200, got %d", c.path, w.Code)
		}
		if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, c.wantType) {
			t.Errorf("%s: expected Content-Type %s, got %q", c.path, c.wantType, ct)
		}
		if w.Body.Len() < 1000 {
			t.Errorf("%s: asset suspiciously small (%d bytes)", c.path, w.Body.Len())
		}
	}
}
