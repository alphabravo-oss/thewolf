package routes_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

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
	auth.SetSessionResolver(func(ctx context.Context, plaintext string) (*auth.ResolvedSession, error) {
		session, err := store.GetAuthSessionByHash(ctx, auth.HashSessionToken(plaintext))
		if err != nil {
			return nil, nil
		}
		if session.RevokedAt != nil || !session.ExpiresAt.After(time.Now().UTC()) {
			return nil, nil
		}
		user, err := store.GetUserByID(ctx, session.UserID)
		if err != nil {
			return nil, nil
		}
		return &auth.ResolvedSession{SessionID: session.ID, UserID: user.ID, Email: user.Email}, nil
	})
	routes.SetHandler(store, nil)

	r := chi.NewRouter()
	r.Get("/api/auth/settings", routes.AuthSettings)
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
		"password": "password1234",
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
		"password": "password1234",
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

	cookie := findCookie(w.Result().Cookies(), "wolf_token")
	if cookie == nil {
		t.Fatal("login: expected wolf_token cookie")
	}
	if !cookie.HttpOnly {
		t.Fatal("login: wolf_token cookie must be HttpOnly")
	}
	if cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("login: expected SameSite=Strict, got %v", cookie.SameSite)
	}
	if cookie.MaxAge <= 0 {
		t.Fatalf("login: expected positive cookie MaxAge, got %d", cookie.MaxAge)
	}
}

func TestRegisterDuplicate(t *testing.T) {
	r, store := setupTestRouter(t)
	defer store.Close()

	body, _ := json.Marshal(map[string]string{
		"email":    "dupe@example.com",
		"password": "password1234",
	})

	// First register succeeds
	req := httptest.NewRequest(http.MethodPost, "/api/auth/register", bytes.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("first register: expected 201, got %d", w.Code)
	}

	// Registration defaults off after the first account; enable it so the
	// second attempt reaches the duplicate-email check (rather than 403).
	if err := store.SetSetting(context.Background(), "registration_enabled", "true"); err != nil {
		t.Fatalf("enable registration: %v", err)
	}

	// Second register fails
	body, _ = json.Marshal(map[string]string{
		"email":    "dupe@example.com",
		"password": "password4567",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/auth/register", bytes.NewReader(body))
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("duplicate register: expected 409, got %d", w.Code)
	}
}

func TestRegisterRespectsRegistrationDisabledAfterBootstrap(t *testing.T) {
	r, store := setupTestRouter(t)
	defer store.Close()

	if err := store.SetSetting(context.Background(), "registration_enabled", "false"); err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(map[string]string{
		"email":    "first@example.com",
		"password": "password1234",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/register", bytes.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("first register should bootstrap even when disabled, got %d: %s", w.Code, w.Body.String())
	}

	body, _ = json.Marshal(map[string]string{
		"email":    "second@example.com",
		"password": "password1234",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/auth/register", bytes.NewReader(body))
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("second register with disabled registration: expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestRegisterBootstrapCreatesOnlyOneAdminConcurrently(t *testing.T) {
	r, store := setupTestRouter(t)
	defer store.Close()

	if err := store.SetSetting(context.Background(), "registration_enabled", "false"); err != nil {
		t.Fatal(err)
	}

	const requests = 8
	var wg sync.WaitGroup
	statuses := make(chan int, requests)
	for i := 0; i < requests; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			body, _ := json.Marshal(map[string]string{
				"email":    "bootstrap" + string(rune('a'+i)) + "@example.com",
				"password": "password1234",
			})
			req := httptest.NewRequest(http.MethodPost, "/api/auth/register", bytes.NewReader(body))
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			statuses <- w.Code
		}()
	}
	wg.Wait()
	close(statuses)

	created := 0
	for status := range statuses {
		if status == http.StatusCreated {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("expected exactly one bootstrap registration, got %d", created)
	}
	users, err := store.ListUsers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 {
		t.Fatalf("expected exactly one user, got %d", len(users))
	}
}

func TestAuthSettingsReportsRegistrationState(t *testing.T) {
	r, store := setupTestRouter(t)
	defer store.Close()

	if err := store.SetSetting(context.Background(), "registration_enabled", "false"); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/auth/settings", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("auth settings: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Data struct {
			RegistrationEnabled bool `json:"registration_enabled"`
			HasUsers            bool `json:"has_users"`
		} `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Data.RegistrationEnabled {
		t.Fatal("auth settings: expected registration_enabled=false")
	}
	if resp.Data.HasUsers {
		t.Fatal("auth settings: expected has_users=false before bootstrap")
	}
}

func TestLoginInvalidPassword(t *testing.T) {
	r, store := setupTestRouter(t)
	defer store.Close()

	// Register first
	body, _ := json.Marshal(map[string]string{
		"email":    "test@example.com",
		"password": "password1234",
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
		"password": "password1234",
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

func TestMeEndpointWithSessionCookie(t *testing.T) {
	r, store := setupTestRouter(t)
	defer store.Close()

	body, _ := json.Marshal(map[string]string{
		"email":    "cookie@example.com",
		"password": "password1234",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/register", bytes.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	cookie := findCookie(w.Result().Cookies(), "wolf_token")
	if cookie == nil {
		t.Fatal("register: expected wolf_token cookie")
	}

	req = httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	req.AddCookie(cookie)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("me with cookie: expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSessionCookieSecureOnHTTPS(t *testing.T) {
	r, store := setupTestRouter(t)
	defer store.Close()

	body, _ := json.Marshal(map[string]string{
		"email":    "secure@example.com",
		"password": "password1234",
	})
	req := httptest.NewRequest(http.MethodPost, "https://wolf.example/api/auth/register", bytes.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	cookie := findCookie(w.Result().Cookies(), "wolf_token")
	if cookie == nil {
		t.Fatal("register: expected wolf_token cookie")
	}
	if !cookie.Secure {
		t.Fatal("register: HTTPS session cookie must set Secure")
	}
}

func TestLogout(t *testing.T) {
	r, store := setupTestRouter(t)
	defer store.Close()

	body, _ := json.Marshal(map[string]string{
		"email":    "logout@example.com",
		"password": "password1234",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/register", bytes.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	sessionCookie := findCookie(w.Result().Cookies(), "wolf_token")
	if sessionCookie == nil {
		t.Fatal("register: expected wolf_token cookie")
	}

	req = httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	req.AddCookie(sessionCookie)
	w = httptest.NewRecorder()
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
		if c.Name == "wolf_token" && !c.HttpOnly {
			t.Error("logout: wolf_token cookie should be HttpOnly")
		}
		if c.Name == "wolf_token" && c.SameSite != http.SameSiteStrictMode {
			t.Error("logout: wolf_token cookie should use SameSite=Strict")
		}
	}

	req = httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	req.AddCookie(sessionCookie)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("revoked session should be rejected, got %d", w.Code)
	}
}

func findCookie(cookies []*http.Cookie, name string) *http.Cookie {
	for _, c := range cookies {
		if c.Name == name {
			return c
		}
	}
	return nil
}
