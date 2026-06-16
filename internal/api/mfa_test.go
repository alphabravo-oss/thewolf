package api_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"github.com/alphabravocompany/thewolf/internal/api"
)

// mfaData unwraps the {data:{...}} envelope into a generic map.
func mfaData(t *testing.T, body []byte) map[string]interface{} {
	t.Helper()
	var env struct {
		Data map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, body)
	}
	return env.Data
}

func TestMFAEndToEnd(t *testing.T) {
	srv, _, jwt := newTestServer(t)

	// 1. Begin enrollment — setup returns the base32 secret + a QR data URI.
	w := request(srv, http.MethodPost, "/api/v1/auth/mfa/setup", jwt, []byte(`{}`))
	if w.Code != http.StatusOK {
		t.Fatalf("setup: %d %s", w.Code, w.Body.String())
	}
	setup := mfaData(t, w.Body.Bytes())
	secret, _ := setup["secret"].(string)
	if secret == "" {
		t.Fatal("setup: no secret")
	}
	if qr, _ := setup["qr"].(string); len(qr) < 32 {
		t.Fatal("setup: no qr")
	}

	code := func() string {
		c, err := totp.GenerateCode(secret, time.Now())
		if err != nil {
			t.Fatalf("GenerateCode: %v", err)
		}
		return c
	}

	// 2. Activate with a valid code — returns one-time recovery codes.
	body, _ := json.Marshal(map[string]string{"code": code()})
	w = request(srv, http.MethodPost, "/api/v1/auth/mfa/activate", jwt, body)
	if w.Code != http.StatusOK {
		t.Fatalf("activate: %d %s", w.Code, w.Body.String())
	}
	act := mfaData(t, w.Body.Bytes())
	rawCodes, _ := act["recovery_codes"].([]interface{})
	if len(rawCodes) == 0 {
		t.Fatal("activate: no recovery codes")
	}
	recovery := rawCodes[0].(string)

	// 3. Password login now yields a challenge, NOT a session. (Auth endpoints
	// share a per-IP rate limiter, so each login uses a distinct source IP.)
	login, _ := json.Marshal(map[string]string{"email": "dev@example.com", "password": "password1234"})
	w = reqFrom(srv, http.MethodPost, "/api/v1/auth/login", "", "10.9.0.1", login)
	if w.Code != http.StatusOK {
		t.Fatalf("login: %d %s", w.Code, w.Body.String())
	}
	ld := mfaData(t, w.Body.Bytes())
	if ld["mfa_required"] != true {
		t.Fatalf("login: expected mfa_required, got %v", ld)
	}
	if ld["access_token"] != nil {
		t.Fatal("login: must not issue a session before the second factor")
	}
	challenge, _ := ld["mfa_token"].(string)
	if challenge == "" {
		t.Fatal("login: no mfa_token")
	}

	// 3a. The challenge token must NOT work as a session bearer.
	if w := request(srv, http.MethodGet, "/api/v1/auth/me", challenge, nil); w.Code != http.StatusUnauthorized {
		t.Errorf("challenge used as session: expected 401, got %d", w.Code)
	}

	// 4. Complete login with a TOTP code -> real session.
	mfaLogin, _ := json.Marshal(map[string]string{"mfa_token": challenge, "code": code()})
	w = reqFrom(srv, http.MethodPost, "/api/v1/auth/mfa/login", "", "10.9.0.2", mfaLogin)
	if w.Code != http.StatusOK {
		t.Fatalf("mfa/login: %d %s", w.Code, w.Body.String())
	}
	if tok, _ := mfaData(t, w.Body.Bytes())["access_token"].(string); tok == "" {
		t.Fatal("mfa/login: no access_token")
	}

	// 5. A recovery code works once...
	rl, _ := json.Marshal(map[string]string{"mfa_token": freshChallenge(t, srv, "10.9.0.3"), "code": recovery})
	if w := reqFrom(srv, http.MethodPost, "/api/v1/auth/mfa/login", "", "10.9.0.4", rl); w.Code != http.StatusOK {
		t.Fatalf("recovery login: %d %s", w.Code, w.Body.String())
	}
	// ...and is then burned.
	rl2, _ := json.Marshal(map[string]string{"mfa_token": freshChallenge(t, srv, "10.9.0.5"), "code": recovery})
	if w := reqFrom(srv, http.MethodPost, "/api/v1/auth/mfa/login", "", "10.9.0.6", rl2); w.Code != http.StatusUnauthorized {
		t.Errorf("reused recovery code: expected 401, got %d", w.Code)
	}
}

// freshChallenge does a password login from a given source IP and returns a new
// mfa_token.
func freshChallenge(t *testing.T, srv *api.Server, ip string) string {
	t.Helper()
	login, _ := json.Marshal(map[string]string{"email": "dev@example.com", "password": "password1234"})
	w := reqFrom(srv, http.MethodPost, "/api/v1/auth/login", "", ip, login)
	tok, _ := mfaData(t, w.Body.Bytes())["mfa_token"].(string)
	if tok == "" {
		t.Fatalf("freshChallenge: no mfa_token (code %d: %s)", w.Code, w.Body.String())
	}
	return tok
}
