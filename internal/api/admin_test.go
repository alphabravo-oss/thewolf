package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/secrets"
)

// TestAdminOversightAndSecretIsolation covers the account-vs-admin split:
//   - a regular user's secret is NOT visible in another user's personal list
//     (the per-user filter fix), but IS visible in the admin global view;
//   - the admin token/secret views span all users, with secrets masked;
//   - the admin endpoints are role-gated (a non-admin gets 403).
func TestAdminOversightAndSecretIsolation(t *testing.T) {
	srv, store, adminJWT := newTestServer(t) // dev@example.com is the first user => admin
	ctx := context.Background()

	// A second, regular user with one secret + one token.
	otherID := uuid.New().String()
	if err := store.CreateUser(ctx, &models.User{ID: otherID, Email: "other@example.com", PasswordHash: "x"}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	enc, err := secrets.Encrypt("super-secret-token-value")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if err := store.CreateSecret(ctx, &models.Secret{
		ID: uuid.New().String(), UserID: otherID, KeyType: "github_token",
		KeyName: "others-key", EncryptedValue: enc,
	}); err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}
	if err := store.CreateAPIToken(ctx, &models.APIToken{
		ID: uuid.New().String(), UserID: otherID, Name: "others-token",
		TokenHash: "h", TokenPrefix: "wolf_abc", ScopeList: []string{"read:scans"},
	}); err != nil {
		t.Fatalf("CreateAPIToken: %v", err)
	}

	total := func(body []byte) int {
		var env struct {
			Meta struct {
				Total int `json:"total"`
			} `json:"meta"`
		}
		_ = json.Unmarshal(body, &env)
		return env.Meta.Total
	}

	// 1. Per-user isolation: the admin's personal secrets list must NOT include
	//    the other user's secret.
	w := request(srv, http.MethodGet, "/api/v1/config/secrets", adminJWT, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("personal secrets: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "others-key") {
		t.Error("personal secrets list leaked another user's secret")
	}

	// 2. Admin global secrets view includes it, masked.
	w = request(srv, http.MethodGet, "/api/v1/admin/secrets", adminJWT, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("admin secrets: %d %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "others-key") || total(w.Body.Bytes()) < 1 {
		t.Error("admin secrets view should include the other user's secret")
	}
	if strings.Contains(body, "super-secret-token-value") {
		t.Error("admin secrets view leaked a plaintext value")
	}
	if !strings.Contains(body, "*") {
		t.Error("admin secret value should be masked")
	}

	// 3. Admin global tokens view spans users.
	w = request(srv, http.MethodGet, "/api/v1/admin/tokens", adminJWT, nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "others-token") {
		t.Errorf("admin tokens view should include the other user's token: %d %s", w.Code, w.Body.String())
	}

	// 4. Role-gated: a regular user's token is rejected with 403.
	tok, err := auth.GenerateToken(otherID, "other@example.com")
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	for _, path := range []string{"/api/v1/admin/tokens", "/api/v1/admin/secrets"} {
		if w := request(srv, http.MethodGet, path, tok.AccessToken, nil); w.Code != http.StatusForbidden {
			t.Errorf("non-admin %s: expected 403, got %d", path, w.Code)
		}
	}
}
