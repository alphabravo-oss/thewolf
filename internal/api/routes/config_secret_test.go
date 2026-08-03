package routes_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/secrets"
)

func TestSecretAPIPersistsNoPlaintextDerivedMask(t *testing.T) {
	secrets.SetMasterKey([]byte("0123456789abcdef0123456789abcdef"))
	env := setupTestEnv(t)
	plaintext := "different-length-secret-4321"

	created := env.doRequest(http.MethodPost, "/api/config/secrets", map[string]interface{}{
		"key_type": "custom", "key_name": "service-key", "value": plaintext,
	})
	if created.Code != http.StatusCreated {
		t.Fatalf("create secret: expected 201, got %d: %s", created.Code, created.Body.String())
	}
	var response struct {
		Data struct {
			ID    string `json:"id"`
			Value string `json:"value"`
		} `json:"data"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.Value != "********" {
		t.Fatalf("secret response mask = %q, want fixed generic mask", response.Data.Value)
	}

	stored, err := env.Store.GetSecretByID(context.Background(), response.Data.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored.MetadataJSON, "4321") ||
		strings.Contains(stored.MetadataJSON, `"masked"`) {
		t.Fatalf("secret metadata contains plaintext-derived mask: %s", stored.MetadataJSON)
	}

	listed := env.doRequest(http.MethodGet, "/api/config/secrets", nil)
	if listed.Code != http.StatusOK {
		t.Fatalf("list secrets: expected 200, got %d: %s", listed.Code, listed.Body.String())
	}
	if strings.Contains(listed.Body.String(), "4321") ||
		!strings.Contains(listed.Body.String(), `"value":"********"`) {
		t.Fatalf("secret list did not use the generic mask: %s", listed.Body.String())
	}
}
