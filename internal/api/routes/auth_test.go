package routes_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/alphabravocompany/thewolf/internal/api/routes"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/db"
)

func setupTestRouter(t *testing.T) (*chi.Mux, db.Store) {
	t.Helper()

	store, err := db.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(); err != nil {
		t.Fatal(err)
	}

	auth.SetJWTSecret([]byte("test-secret-key-for-jwt-signing"))
	routes.SetHandler(store, nil)

	r := chi.NewRouter()
	r.Post("/api/auth/register", routes.Register)
	r.Post("/api/auth/login", routes.Login)
	r.Post("/api/auth/logout", routes.Logout)
	r.With(auth.Middleware).Get("/api/auth/me", routes.Me)
	r.With(auth.Middleware).Put("/api/auth/password", routes.ChangePassword)

	return r, store
}

func TestRegisterAndLogin(t *testing.T) {
	r, store := setupTestRouter(t)
	defer store.Close()

	// Register
	body, _ := json.Marshal(map[string]string{
		"email":    "test@example.com",
		"password": "password123",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/register", bytes.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("register: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var regResp struct {
		Data struct {
			AccessToken string `json:"access_token"`
		} `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &regResp)
	if regResp.Data.AccessToken == "" {
		t.Fatal("register: expected access_token in response")
	}

	// Login
	body, _ = json.Marshal(map[string]string{
		"email":    "test@example.com",
		"password": "password123",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("login: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var loginResp struct {
		Data struct {
			AccessToken string `json:"access_token"`
		} `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &loginResp)
	if loginResp.Data.AccessToken == "" {
		t.Fatal("login: expected access_token")
	}

	// Check cookie was set
	cookies := w.Result().Cookies()
	found := false
	for _, c := range cookies {
		if c.Name == "wolf_token" {
			found = true
			if !c.HttpOnly {
				t.Error("wolf_token cookie should be HttpOnly")
			}
		}
	}
	if !found {
		t.Error("login: expected wolf_token cookie")
	}
}

func TestRegisterDuplicate(t *testing.T) {
	r, store := setupTestRouter(t)
	defer store.Close()

	body, _ := json.Marshal(map[string]string{
		"email":    "dupe@example.com",
		"password": "password123",
	})

	// First register succeeds
	req := httptest.NewRequest(http.MethodPost, "/api/auth/register", bytes.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("first register: expected 201, got %d", w.Code)
	}

	// Second register fails
	body, _ = json.Marshal(map[string]string{
		"email":    "dupe@example.com",
		"password": "password456",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/auth/register", bytes.NewReader(body))
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("duplicate register: expected 409, got %d", w.Code)
	}
}

func TestLoginInvalidPassword(t *testing.T) {
	r, store := setupTestRouter(t)
	defer store.Close()

	// Register first
	body, _ := json.Marshal(map[string]string{
		"email":    "test@example.com",
		"password": "password123",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/register", bytes.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Login with wrong password
	body, _ = json.Marshal(map[string]string{
		"email":    "test@example.com",
		"password": "wrongpassword",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("bad login: expected 401, got %d", w.Code)
	}
}

func TestMeEndpoint(t *testing.T) {
	r, store := setupTestRouter(t)
	defer store.Close()

	// Register to get a token
	body, _ := json.Marshal(map[string]string{
		"email":    "me@example.com",
		"password": "password123",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/register", bytes.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var regResp struct {
		Data struct {
			AccessToken string `json:"access_token"`
		} `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &regResp)

	// Get /me
	req = httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	req.Header.Set("Authorization", "Bearer "+regResp.Data.AccessToken)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("me: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var meResp struct {
		Data struct {
			Email string `json:"email"`
		} `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &meResp)
	if meResp.Data.Email != "me@example.com" {
		t.Fatalf("me: expected email me@example.com, got %s", meResp.Data.Email)
	}
}

func TestLogout(t *testing.T) {
	r, store := setupTestRouter(t)
	defer store.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("logout: expected 200, got %d", w.Code)
	}

	// Check cookie is cleared
	cookies := w.Result().Cookies()
	for _, c := range cookies {
		if c.Name == "wolf_token" && c.MaxAge != -1 {
			t.Error("logout: wolf_token cookie should have MaxAge -1")
		}
	}
}
