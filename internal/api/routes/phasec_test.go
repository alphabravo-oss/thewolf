package routes_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestSchedulesCreateListAndInterval(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()
	repoID := env.createRepo(t)

	w := env.doRequest(http.MethodPost, "/api/schedules", map[string]any{
		"repo_id": repoID, "interval_minutes": 7,
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bad interval: expected 400, got %d: %s", w.Code, w.Body.String())
	}

	w = env.doRequest(http.MethodPost, "/api/schedules", map[string]any{
		"repo_id": repoID, "interval_minutes": 60, "profile": "fast",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var created struct {
		Data models.ScanSchedule `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil || created.Data.ID == "" {
		t.Fatalf("create body: %v %s", err, w.Body.String())
	}

	w = env.doRequest(http.MethodGet, "/api/schedules", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var listed struct {
		Data []models.ScanSchedule `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listed); err != nil {
		t.Fatalf("list decode: %v", err)
	}
	if len(listed.Data) != 1 || listed.Data[0].ID != created.Data.ID {
		t.Fatalf("list = %+v", listed.Data)
	}
}

func TestScheduleOwnership404(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()
	repoID := env.createRepo(t)
	w := env.doRequest(http.MethodPost, "/api/schedules", map[string]any{
		"repo_id": repoID, "interval_minutes": 1440,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &created)

	otherID := uuid.NewString()
	if err := env.Store.CreateUser(context.Background(), &models.User{
		ID: otherID, Email: "other-sched@example.com", PasswordHash: "x", Role: models.RoleUser,
	}); err != nil {
		t.Fatal(err)
	}
	tokens, err := auth.GenerateToken(otherID, "other-sched@example.com")
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/schedules/"+created.Data.ID, nil)
	req.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
	got := httptest.NewRecorder()
	env.Router.ServeHTTP(got, req)
	if got.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", got.Code, got.Body.String())
	}
}

func TestGitHubWebhookHMAC(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()
	payload := []byte(`{"ref":"refs/heads/main","repository":{"full_name":"acme/widget"}}`)

	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/github", bytes.NewReader(payload))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-Hub-Signature-256", "sha256=00")
	w := httptest.NewRecorder()
	env.Router.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("empty secret: expected 401, got %d", w.Code)
	}

	if err := env.Store.SetSetting(context.Background(), "github_webhook_secret", "s3cret"); err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodPost, "/api/webhooks/github", bytes.NewReader(payload))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-Hub-Signature-256", "sha256=deadbeef")
	w = httptest.NewRecorder()
	env.Router.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("bad hmac: expected 401, got %d", w.Code)
	}

	mac := hmac.New(sha256.New, []byte("s3cret"))
	_, _ = mac.Write(payload)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	req = httptest.NewRequest(http.MethodPost, "/api/webhooks/github", bytes.NewReader(payload))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-Hub-Signature-256", sig)
	w = httptest.NewRecorder()
	env.Router.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("valid hmac: expected 202, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSampleRepoCreatesRepo(t *testing.T) {
	env := setupTestEnv(t)
	defer env.Store.Close()
	t.Setenv("HOME", t.TempDir())

	w := env.doRequest(http.MethodPost, "/api/setup/sample-repo", nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("sample-repo: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var created struct {
		Data models.Repo `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Data.Name != "sample" || created.Data.ID == "" {
		t.Fatalf("unexpected repo: %+v", created.Data)
	}
	w = env.doRequest(http.MethodPost, "/api/setup/sample-repo", nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("dedup: expected 201, got %d: %s", w.Code, w.Body.String())
	}
}
