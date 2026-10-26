package api_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/auth"
)

func TestRefreshRotationRejectsReplayAndRevokesSuccessor(t *testing.T) {
	srv, _, _ := newTestServer(t)
	login := request(srv, "POST", "/api/v1/auth/login", "", []byte(`{"email":"dev@example.com","password":"password1234"}`))
	if login.Code != http.StatusOK {
		t.Fatalf("login: %d", login.Code)
	}
	var envelope struct {
		Data auth.TokenPair `json:"data"`
	}
	if err := json.Unmarshal(login.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	first := envelope.Data
	if w := request(srv, "GET", "/api/v1/auth/me", first.RefreshToken, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("refresh authenticated API: %d", w.Code)
	}
	refresh := func(token string) int {
		body, _ := json.Marshal(map[string]string{"refresh_token": token})
		w := request(srv, "POST", "/api/v1/auth/refresh", "", body)
		if w.Code == http.StatusOK {
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("credentials can be cached")
			}
			if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
		}
		return w.Code
	}
	if code := refresh(first.RefreshToken); code != http.StatusOK {
		t.Fatalf("refresh: %d", code)
	}
	second := envelope.Data
	if second.RefreshToken == first.RefreshToken {
		t.Fatal("refresh did not rotate")
	}
	if w := request(srv, "GET", "/api/v1/auth/me", second.AccessToken, nil); w.Code != http.StatusOK {
		t.Fatalf("new access token: %d", w.Code)
	}
	if code := refresh(first.RefreshToken); code != http.StatusUnauthorized {
		t.Fatalf("replay accepted: %d", code)
	}
	if code := refresh(second.RefreshToken); code != http.StatusUnauthorized {
		t.Fatalf("successor not revoked: %d", code)
	}
}

func TestLogoutRevokesRefreshCredential(t *testing.T) {
	srv, _, _ := newTestServer(t)
	login := request(srv, "POST", "/api/v1/auth/login", "", []byte(`{"email":"dev@example.com","password":"password1234"}`))
	var envelope struct {
		Data auth.TokenPair `json:"data"`
	}
	if err := json.Unmarshal(login.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"refresh_token": envelope.Data.RefreshToken})
	if w := request(srv, "POST", "/api/v1/auth/logout", envelope.Data.AccessToken, body); w.Code != http.StatusOK {
		t.Fatalf("logout: %d %s", w.Code, w.Body.String())
	}
	if w := request(srv, "POST", "/api/v1/auth/refresh", "", body); w.Code != http.StatusUnauthorized {
		t.Fatalf("refresh after logout: %d", w.Code)
	}
}
