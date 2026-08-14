package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/secrets"
)

func TestScannerCustomBuildAPIContract(t *testing.T) {
	srv, store, jwt := newTestServer(t)
	createBody := []byte(`{
		"variants":["default","jvm"],
		"push":false,
		"platforms":["linux/amd64"],
		"namespace":"enterprise",
		"reason":"API contract test"
	}`)
	missingKey := scannerReleaseRequest(
		srv, jwt, http.MethodPost, "/api/v1/scanners/custom-builds",
		createBody, nil,
	)
	if missingKey.Code != http.StatusPreconditionRequired {
		t.Fatalf("missing key code=%d body=%s", missingKey.Code, missingKey.Body.String())
	}
	created := scannerReleaseRequest(
		srv, jwt, http.MethodPost, "/api/v1/scanners/custom-builds",
		createBody, map[string]string{"Idempotency-Key": "custom-api-create"},
	)
	if created.Code != http.StatusAccepted {
		t.Fatalf("create code=%d body=%s", created.Code, created.Body.String())
	}
	var accepted struct {
		ID        string `json:"id"`
		State     string `json:"state"`
		StatusURL string `json:"status_url"`
		EventsURL string `json:"events_url"`
	}
	mustJSON(t, created.Body.Bytes(), &accepted)
	if accepted.ID == "" || accepted.State != "queued" ||
		accepted.StatusURL != "/api/v1/scanners/custom-builds/"+accepted.ID ||
		accepted.EventsURL != "/api/v1/scanners/custom-builds/"+accepted.ID+"/events" {
		t.Fatalf("accepted response = %#v", accepted)
	}
	replayed := scannerReleaseRequest(
		srv, jwt, http.MethodPost, "/api/v1/scanners/custom-builds",
		createBody, map[string]string{"Idempotency-Key": "custom-api-create"},
	)
	if replayed.Code != http.StatusAccepted ||
		replayed.Header().Get("Idempotent-Replay") != "true" ||
		!strings.Contains(replayed.Body.String(), accepted.ID) {
		t.Fatalf("replay code=%d header=%q body=%s",
			replayed.Code, replayed.Header().Get("Idempotent-Replay"),
			replayed.Body.String())
	}
	conflict := scannerReleaseRequest(
		srv, jwt, http.MethodPost, "/api/v1/scanners/custom-builds",
		[]byte(`{"variants":["rust"],"push":false,"reason":"different"}`),
		map[string]string{"Idempotency-Key": "custom-api-create"},
	)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("conflict code=%d body=%s", conflict.Code, conflict.Body.String())
	}

	show := scannerReleaseRequest(
		srv, jwt, http.MethodGet, accepted.StatusURL, nil, nil,
	)
	if show.Code != http.StatusOK || show.Header().Get("ETag") != `"1"` {
		t.Fatalf("show code=%d ETag=%q body=%s",
			show.Code, show.Header().Get("ETag"), show.Body.String())
	}
	if strings.Contains(show.Body.String(), "secret_reference") ||
		strings.Contains(show.Body.String(), "lease_token") {
		t.Fatalf("capability field leaked from custom build: %s", show.Body.String())
	}
	list := scannerReleaseRequest(
		srv, jwt, http.MethodGet,
		"/api/v1/scanners/custom-builds?state=queued&limit=1",
		nil, nil,
	)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), accepted.ID) {
		t.Fatalf("list code=%d body=%s", list.Code, list.Body.String())
	}
	cancel := scannerReleaseRequest(
		srv, jwt, http.MethodPost, accepted.StatusURL+"/cancel",
		[]byte(`{"reason":"cancel API contract operation"}`),
		map[string]string{
			"Idempotency-Key": "custom-api-cancel",
			"If-Match":        `"1"`,
		},
	)
	if cancel.Code != http.StatusOK ||
		!strings.Contains(cancel.Body.String(), `"state":"cancelled"`) {
		t.Fatalf("cancel code=%d body=%s", cancel.Code, cancel.Body.String())
	}
	events := scannerReleaseRequest(
		srv, jwt, http.MethodGet, accepted.EventsURL, nil, nil,
	)
	if events.Code != http.StatusOK ||
		!strings.Contains(events.Body.String(), "id: 4001") ||
		!strings.Contains(events.Body.String(), "event: error") {
		t.Fatalf("events code=%d body=%s", events.Code, events.Body.String())
	}
	resumed := scannerReleaseRequest(
		srv, jwt, http.MethodGet, accepted.EventsURL, nil,
		map[string]string{"Last-Event-ID": "4001"},
	)
	if resumed.Code != http.StatusOK || resumed.Body.Len() != 0 {
		t.Fatalf("resumed code=%d body=%q", resumed.Code, resumed.Body.String())
	}

	secretID := seedDockerHubSecret(t, store)
	codeQLPush := scannerReleaseRequest(
		srv, jwt, http.MethodPost, "/api/v1/scanners/custom-builds",
		[]byte(fmt.Sprintf(`{
			"variants":["all"],"push":true,
			"platforms":["linux/amd64","linux/arm64"],
			"credential_secret_id":%q,
			"reason":"publish all scanner variants"
		}`, secretID)),
		map[string]string{"Idempotency-Key": "custom-api-codeql-push"},
	)
	if codeQLPush.Code != http.StatusAccepted {
		t.Fatalf("CodeQL push code=%d body=%s", codeQLPush.Code, codeQLPush.Body.String())
	}
}

func seedDockerHubSecret(t *testing.T, store interface {
	GetUserByEmail(context.Context, string) (*models.User, error)
	CreateSecret(context.Context, *models.Secret) error
}) string {
	t.Helper()
	user, err := store.GetUserByEmail(context.Background(), "dev@example.com")
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := secrets.Encrypt("registry-token")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	id := uuid.NewString()
	if err := store.CreateSecret(context.Background(), &models.Secret{
		ID: id, UserID: user.ID,
		KeyType: models.KeyTypeDockerHubToken, KeyName: "registry-user",
		EncryptedValue: encrypted, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestScannerCustomBuildAPIDoesNotExposeSecretReference(t *testing.T) {
	srv, store, jwt := newTestServer(t)
	seedDockerHubSecret(t, store)
	secretsForUser, err := store.ListSecretsByUser(
		context.Background(),
		func() string {
			user, userErr := store.GetUserByEmail(
				context.Background(), "dev@example.com",
			)
			if userErr != nil {
				t.Fatal(userErr)
			}
			return user.ID
		}(),
	)
	if err != nil || len(secretsForUser) != 1 {
		t.Fatalf("secrets = %#v err=%v", secretsForUser, err)
	}
	body, _ := json.Marshal(map[string]any{
		"variants": []string{"default"}, "push": true,
		"platforms":            []string{"linux/amd64"},
		"credential_secret_id": secretsForUser[0].ID,
		"reason":               "secret response contract",
	})
	created := scannerReleaseRequest(
		srv, jwt, http.MethodPost, "/api/v1/scanners/custom-builds",
		body, map[string]string{"Idempotency-Key": "custom-secret-contract"},
	)
	if created.Code != http.StatusAccepted {
		t.Fatalf("create code=%d body=%s", created.Code, created.Body.String())
	}
	var accepted struct {
		ID string `json:"id"`
	}
	mustJSON(t, created.Body.Bytes(), &accepted)
	show := scannerReleaseRequest(
		srv, jwt, http.MethodGet,
		"/api/v1/scanners/custom-builds/"+accepted.ID,
		nil, nil,
	)
	if show.Code != http.StatusOK {
		t.Fatalf("show code=%d body=%s", show.Code, show.Body.String())
	}
	if strings.Contains(show.Body.String(), secretsForUser[0].ID) ||
		strings.Contains(show.Body.String(), "secret_reference") ||
		strings.Contains(show.Body.String(), "publish_version") {
		t.Fatalf("secret/capability reference leaked: %s", show.Body.String())
	}
}
