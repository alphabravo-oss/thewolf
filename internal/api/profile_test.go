package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestUpdateProfile(t *testing.T) {
	srv, store, jwt := newTestServer(t) // dev@example.com / password1234

	body := func(v map[string]any) []byte { b, _ := json.Marshal(v); return b }
	me := func() models.User {
		w := request(srv, http.MethodGet, "/api/v1/auth/me", jwt, nil)
		var env struct {
			Data models.User `json:"data"`
		}
		mustJSON(t, w.Body.Bytes(), &env)
		return env.Data
	}

	// 1. Display name updates without a password.
	if w := request(srv, http.MethodPut, "/api/v1/auth/profile", jwt, body(map[string]any{"display_name": "Dev User"})); w.Code != http.StatusOK {
		t.Fatalf("set display_name: %d %s", w.Code, w.Body.String())
	}
	if got := me().DisplayName; got != "Dev User" {
		t.Errorf("display_name = %q, want %q", got, "Dev User")
	}

	// 2. Changing email with the wrong password is rejected.
	if w := request(srv, http.MethodPut, "/api/v1/auth/profile", jwt, body(map[string]any{"email": "new@example.com", "current_password": "wrong-password"})); w.Code != http.StatusUnauthorized {
		t.Errorf("email change w/ wrong pw: expected 401, got %d", w.Code)
	}

	// 3. Changing email with the correct password succeeds.
	if w := request(srv, http.MethodPut, "/api/v1/auth/profile", jwt, body(map[string]any{"email": "new@example.com", "current_password": "password1234"})); w.Code != http.StatusOK {
		t.Fatalf("email change: %d %s", w.Code, w.Body.String())
	}
	if got := me().Email; got != "new@example.com" {
		t.Errorf("email = %q, want new@example.com", got)
	}

	// 4. Taking another user's email conflicts.
	if err := store.CreateUser(context.Background(), &models.User{
		ID: uuid.New().String(), Email: "taken@example.com", PasswordHash: "x",
	}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if w := request(srv, http.MethodPut, "/api/v1/auth/profile", jwt, body(map[string]any{"email": "taken@example.com", "current_password": "password1234"})); w.Code != http.StatusConflict {
		t.Errorf("duplicate email: expected 409, got %d", w.Code)
	}
}
