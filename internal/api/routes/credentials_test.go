package routes_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/secrets"
)

func TestCredentialAPIEncryptsAndNeverReturnsPlaintext(t *testing.T) {
	secrets.SetMasterKey([]byte("0123456789abcdef0123456789abcdef"))
	env := setupTestEnv(t)
	plaintext := "super-secret-token-1234"

	created := env.doRequest(http.MethodPost, "/api/credentials", map[string]interface{}{
		"type": "git_https", "name": "Git service", "secret": plaintext,
		"username": "scanner-bot", "allowed_hosts": []string{"GIT.EXAMPLE.COM."},
	})
	if created.Code != http.StatusCreated {
		t.Fatalf("create credential: expected 201, got %d: %s", created.Code, created.Body.String())
	}
	if strings.Contains(created.Body.String(), plaintext) {
		t.Fatal("credential create response leaked plaintext")
	}
	var response struct {
		Data struct {
			ID           string                 `json:"id"`
			Masked       string                 `json:"masked"`
			AllowedHosts []string               `json:"allowed_hosts"`
			Metadata     map[string]interface{} `json:"metadata"`
		} `json:"data"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.Masked != "********" {
		t.Fatalf("unexpected masked value %q", response.Data.Masked)
	}
	if len(response.Data.AllowedHosts) != 1 || response.Data.AllowedHosts[0] != "git.example.com" {
		t.Fatalf("unexpected allowed hosts: %#v", response.Data.AllowedHosts)
	}
	if response.Data.Metadata["username"] != "scanner-bot" {
		t.Fatalf("unexpected public metadata: %#v", response.Data.Metadata)
	}

	stored, err := env.Store.GetSecretByID(context.Background(), response.Data.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.EncryptedValue == plaintext || strings.Contains(stored.EncryptedValue, plaintext) {
		t.Fatal("credential database row contains plaintext")
	}
	if strings.Contains(stored.MetadataJSON, "1234") ||
		strings.Contains(stored.MetadataJSON, `"masked"`) {
		t.Fatalf("credential metadata contains plaintext-derived mask: %s", stored.MetadataJSON)
	}
	decrypted, err := secrets.Decrypt(stored.EncryptedValue)
	if err != nil || decrypted != plaintext {
		t.Fatalf("decrypt stored credential: value=%q err=%v", decrypted, err)
	}

	listed := env.doRequest(http.MethodGet, "/api/credentials", nil)
	if listed.Code != http.StatusOK || strings.Contains(listed.Body.String(), plaintext) ||
		strings.Contains(listed.Body.String(), "1234") {
		t.Fatalf("credential list leaked or failed: %d %s", listed.Code, listed.Body.String())
	}
}

func TestCredentialAPIIgnoresLegacySecretDerivedMask(t *testing.T) {
	secrets.SetMasterKey([]byte("0123456789abcdef0123456789abcdef"))
	env := setupTestEnv(t)
	encrypted, err := secrets.Encrypt("legacy-secret-9876")
	if err != nil {
		t.Fatal(err)
	}
	credential := &models.Secret{
		ID: uuid.NewString(), UserID: env.UserID, KeyType: models.KeyTypeGitHTTPS,
		KeyName: "legacy", EncryptedValue: encrypted, AllowedHosts: `["git.example.com"]`,
		MetadataJSON: `{"username":"bot","masked":"**************9876"}`,
	}
	if err := env.Store.CreateSecret(context.Background(), credential); err != nil {
		t.Fatal(err)
	}

	got := env.doRequest(http.MethodGet, "/api/credentials/"+credential.ID, nil)
	if got.Code != http.StatusOK {
		t.Fatalf("get legacy credential: expected 200, got %d: %s", got.Code, got.Body.String())
	}
	if strings.Contains(got.Body.String(), "9876") ||
		!strings.Contains(got.Body.String(), `"masked":"********"`) {
		t.Fatalf("legacy credential exposed a secret-derived mask: %s", got.Body.String())
	}
}

func TestCredentialAPIHidesKnownHostsAndEnforcesOwnership(t *testing.T) {
	secrets.SetMasterKey([]byte("0123456789abcdef0123456789abcdef"))
	env := setupTestEnv(t)
	knownHosts := "git.example.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest"

	created := env.doRequest(http.MethodPost, "/api/credentials", map[string]interface{}{
		"type": "ssh_private_key", "name": "SSH key", "secret": "PRIVATE-KEY-MATERIAL",
		"known_hosts": knownHosts, "allowed_hosts": []string{"git.example.com"},
	})
	if created.Code != http.StatusCreated {
		t.Fatalf("create SSH credential: expected 201, got %d: %s", created.Code, created.Body.String())
	}
	if strings.Contains(created.Body.String(), knownHosts) || strings.Contains(created.Body.String(), "PRIVATE-KEY-MATERIAL") {
		t.Fatal("SSH credential response leaked private material")
	}

	otherUserID := uuid.NewString()
	if err := env.Store.CreateUser(context.Background(), &models.User{
		ID: otherUserID, Email: "other-credential@example.test", PasswordHash: "hash",
	}); err != nil {
		t.Fatal(err)
	}
	encrypted, err := secrets.Encrypt("other-secret")
	if err != nil {
		t.Fatal(err)
	}
	other := &models.Secret{
		ID: uuid.NewString(), UserID: otherUserID, KeyType: models.KeyTypeGitHTTPS,
		KeyName: "other", EncryptedValue: encrypted, AllowedHosts: `["git.example.com"]`,
	}
	if err := env.Store.CreateSecret(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	got := env.doRequest(http.MethodGet, "/api/credentials/"+other.ID, nil)
	if got.Code != http.StatusNotFound {
		t.Fatalf("cross-owner credential read: expected 404, got %d: %s", got.Code, got.Body.String())
	}
}

func TestCredentialAPIValidatesBindingsAndRequiredMetadata(t *testing.T) {
	secrets.SetMasterKey([]byte("0123456789abcdef0123456789abcdef"))
	env := setupTestEnv(t)
	tests := []map[string]interface{}{
		{"type": "git_https", "name": "missing username", "secret": "secret", "allowed_hosts": []string{"git.example.com"}},
		{"type": "ssh_private_key", "name": "missing hosts", "secret": "secret", "allowed_hosts": []string{"git.example.com"}},
		{"type": "git_https", "name": "bad binding", "secret": "secret", "username": "bot", "allowed_hosts": []string{"https://git.example.com"}},
		{"type": "git_https", "name": "bad wildcard", "secret": "secret", "username": "bot", "allowed_hosts": []string{"*.*.example.com"}},
		{"type": "git_https", "name": "bad label", "secret": "secret", "username": "bot", "allowed_hosts": []string{"bad_host.example.com"}},
		{"type": "git_https", "name": "empty label", "secret": "secret", "username": "bot", "allowed_hosts": []string{"git..example.com"}},
	}
	for _, body := range tests {
		response := env.doRequest(http.MethodPost, "/api/credentials", body)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("expected validation failure for %#v, got %d: %s", body, response.Code, response.Body.String())
		}
	}
}
