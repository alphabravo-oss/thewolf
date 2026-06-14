package api_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/api/routes"
	"github.com/alphabravocompany/thewolf/internal/auth/apikey"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/scannerbuild"
	"github.com/alphabravocompany/thewolf/internal/secrets"
)

// stubScannerBuild swaps routes.ScannerBuildFn for the duration of a test so
// no real docker daemon is invoked, recording the requests it received.
func stubScannerBuild(t *testing.T, fn func(ctx context.Context, req scannerbuild.BuildRequest, onLine func(string)) (scannerbuild.BuildResult, error)) {
	t.Helper()
	prev := routes.ScannerBuildFn
	routes.ScannerBuildFn = fn
	t.Cleanup(func() { routes.ScannerBuildFn = prev })
}

func TestBuildScannerImage_RequiresAuth(t *testing.T) {
	srv, _, _ := newTestServer(t)
	// No bearer token at all -> 401.
	if w := request(srv, http.MethodPost, "/api/v1/scanners/images/default/build", "", []byte(`{}`)); w.Code != http.StatusUnauthorized {
		t.Fatalf("no creds: expected 401, got %d: %s", w.Code, w.Body.String())
	}
}

func TestBuildScannerImage_ReadOnlyTokenForbidden(t *testing.T) {
	srv, _, jwt := newTestServer(t)
	// A read:config token cannot trigger a build (needs write:config).
	token := createToken(t, srv, jwt, []string{apikey.ScopeReadConfig})
	if w := request(srv, http.MethodPost, "/api/v1/scanners/images/default/build", token, []byte(`{}`)); w.Code != http.StatusForbidden {
		t.Fatalf("read-only token: expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestBuildScannerImage_PushWithoutSecretIs404(t *testing.T) {
	srv, _, jwt := newTestServer(t)
	// Stub the builder so a regression that skips the credential check
	// can't reach a real docker invocation.
	stubScannerBuild(t, func(context.Context, scannerbuild.BuildRequest, func(string)) (scannerbuild.BuildResult, error) {
		t.Fatalf("builder must not be called when the dockerhub_token secret is missing")
		return scannerbuild.BuildResult{}, nil
	})
	w := request(srv, http.MethodPost, "/api/v1/scanners/images/default/build", jwt, []byte(`{"push":true}`))
	if w.Code != http.StatusNotFound {
		t.Fatalf("push without secret: expected 404, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "dockerhub_token") {
		t.Errorf("404 body should hint at the missing dockerhub_token secret: %s", w.Body.String())
	}
}

func TestBuildScannerImage_PushFalseHappyPathNoSecret(t *testing.T) {
	srv, _, jwt := newTestServer(t)
	var got scannerbuild.BuildRequest
	stubScannerBuild(t, func(_ context.Context, req scannerbuild.BuildRequest, onLine func(string)) (scannerbuild.BuildResult, error) {
		got = req
		onLine("step 1/3 building")
		return scannerbuild.BuildResult{
			Refs:          []string{"alphabravodevops/wolf-scanners:0.1.0"},
			LoadedLocally: true,
		}, nil
	})

	// push=false must NOT require any dockerhub_token secret.
	w := request(srv, http.MethodPost, "/api/v1/scanners/images/default/build", jwt, []byte(`{"push":false}`))
	if w.Code != http.StatusOK {
		t.Fatalf("push=false: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got.Push {
		t.Errorf("builder got push=true for a push:false request")
	}
	if got.Variant != "default" {
		t.Errorf("variant = %q, want default", got.Variant)
	}
	if got.Namespace != "alphabravodevops" {
		t.Errorf("namespace = %q, want alphabravodevops default", got.Namespace)
	}
	if got.DockerHubUser != "" || got.DockerHubPAT != "" {
		t.Errorf("push=false must not carry credentials: user=%q patSet=%v", got.DockerHubUser, got.DockerHubPAT != "")
	}
	body := w.Body.String()
	if !strings.Contains(body, "data: step 1/3 building") {
		t.Errorf("expected streamed build line, got: %s", body)
	}
	if !strings.Contains(body, "event: done") {
		t.Errorf("expected terminal done event, got: %s", body)
	}
}

func TestBuildScannerImage_PushHappyPathWithSecret(t *testing.T) {
	srv, store, jwt := newTestServer(t)

	// Wire up encryption and seed a dockerhub_token secret.
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	secrets.SetMasterKey(key)
	enc, err := secrets.Encrypt("dckr_pat_value")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	// The secret belongs to the JWT user (dev@example.com); look up its ID.
	u, err := store.GetUserByEmail(context.Background(), "dev@example.com")
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	now := time.Now()
	if err := store.CreateSecret(context.Background(), &models.Secret{
		ID:             uuid.New().String(),
		UserID:         u.ID,
		KeyType:        models.KeyTypeDockerHubToken,
		KeyName:        "myuser",
		EncryptedValue: enc,
		CreatedAt:      now,
		UpdatedAt:      now,
	}); err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}

	var got scannerbuild.BuildRequest
	stubScannerBuild(t, func(_ context.Context, req scannerbuild.BuildRequest, onLine func(string)) (scannerbuild.BuildResult, error) {
		got = req
		onLine("pushing")
		return scannerbuild.BuildResult{
			Refs:   []string{"alphabravodevops/wolf-scanners:0.1.0", "alphabravodevops/wolf-scanners:latest"},
			Digest: "sha256:abc",
		}, nil
	})

	w := request(srv, http.MethodPost, "/api/v1/scanners/images/default/build", jwt, []byte(`{"push":true}`))
	if w.Code != http.StatusOK {
		t.Fatalf("push=true with secret: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !got.Push {
		t.Errorf("builder should have received push=true")
	}
	if got.DockerHubUser != "myuser" {
		t.Errorf("DockerHubUser = %q, want myuser (from key_name)", got.DockerHubUser)
	}
	if got.DockerHubPAT != "dckr_pat_value" {
		t.Errorf("DockerHubPAT not decrypted from the secret value")
	}
	body := w.Body.String()
	if !strings.Contains(body, "event: done") {
		t.Errorf("expected terminal done event, got: %s", body)
	}
	// The PAT must never appear in the SSE stream.
	if strings.Contains(body, "dckr_pat_value") {
		t.Errorf("PAT leaked into the SSE stream: %s", body)
	}
}

func TestBuildAllScannerImages_PushFalseHappyPath(t *testing.T) {
	srv, _, jwt := newTestServer(t)
	var variants []string
	stubScannerBuild(t, func(_ context.Context, req scannerbuild.BuildRequest, onLine func(string)) (scannerbuild.BuildResult, error) {
		variants = append(variants, req.Variant)
		onLine("building " + req.Variant)
		return scannerbuild.BuildResult{Refs: []string{req.Variant + ":local"}, LoadedLocally: true}, nil
	})

	w := request(srv, http.MethodPost, "/api/v1/scanners/images/build-all", jwt, []byte(`{"push":false}`))
	if w.Code != http.StatusOK {
		t.Fatalf("build-all push=false: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	want := []string{"default", "jvm", "rust", "codeql"}
	if strings.Join(variants, ",") != strings.Join(want, ",") {
		t.Errorf("variants built = %v, want %v", variants, want)
	}
	body := w.Body.String()
	if !strings.Contains(body, "data: [jvm] building jvm") {
		t.Errorf("expected per-variant prefixed line, got: %s", body)
	}
}

func TestBuildScannerImage_UnknownVariantIs404(t *testing.T) {
	srv, _, jwt := newTestServer(t)
	if w := request(srv, http.MethodPost, "/api/v1/scanners/images/nope/build", jwt, []byte(`{}`)); w.Code != http.StatusNotFound {
		t.Fatalf("unknown variant: expected 404, got %d: %s", w.Code, w.Body.String())
	}
}
