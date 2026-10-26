package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/api/routes"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestMFAChallengeSingleUse(t *testing.T) {
	srv, store, jwt := newTestServer(t)
	_, codes := enrollMFAFixture(t, srv, jwt)
	challenge := freshChallenge(t, srv, "10.52.0.1")
	for i, want := range []int{http.StatusOK, http.StatusUnauthorized} {
		body, _ := json.Marshal(map[string]string{"mfa_token": challenge, "code": codes[i]})
		w := reqFrom(srv, http.MethodPost, "/api/v1/auth/mfa/login", "", fmt.Sprintf("10.52.0.%d", i+2), body)
		if w.Code != want {
			t.Fatalf("challenge use %d: %d, want %d", i+1, w.Code, want)
		}
		if i == 1 && (len(w.Result().Cookies()) != 0 || strings.Contains(w.Body.String(), "access_token")) {
			t.Fatal("replay issued credentials")
		}
	}
	user, err := store.GetUserByEmail(t.Context(), "dev@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(user.TOTPRecoveryCodes, auth.HashRecoveryCode(codes[1])) {
		t.Fatal("replayed challenge consumed another recovery code")
	}
}

func TestMFAChallengeMustBePersisted(t *testing.T) {
	srv, store, jwt := newTestServer(t)
	_, codes := enrollMFAFixture(t, srv, jwt)
	user, err := store.GetUserByEmail(t.Context(), "dev@example.com")
	if err != nil {
		t.Fatal(err)
	}
	challenge, err := auth.GenerateMFAChallenge(user.ID, user.Email, user.CredentialVersion)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"mfa_token": challenge, "code": codes[0]})
	w := reqFrom(srv, http.MethodPost, "/api/v1/auth/mfa/login", "", "10.52.1.1", body)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unpersisted challenge: %d", w.Code)
	}
	after, err := store.GetUserByID(t.Context(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.TOTPRecoveryCodes != user.TOTPRecoveryCodes {
		t.Fatal("unpersisted challenge consumed recovery code")
	}
}

type mfaChallengeStorageFailure struct{ db.Store }

func (mfaChallengeStorageFailure) CreateAuthSession(context.Context, *models.AuthSession) error {
	return errors.New("private challenge storage diagnostic")
}

func TestMFAChallengeStorageFailure(t *testing.T) {
	srv, store, jwt := newTestServer(t)
	enrollMFAFixture(t, srv, jwt)
	handler := routes.DefaultHandler
	handler.Store = mfaChallengeStorageFailure{Store: store}
	defer func() { handler.Store = store }()
	w := reqFrom(srv, http.MethodPost, "/api/v1/auth/login", "", "10.52.2.1", []byte(`{"email":"dev@example.com","password":"password1234"}`))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("challenge storage failure: %d", w.Code)
	}
	if len(w.Result().Cookies()) != 0 || strings.Contains(w.Body.String(), "mfa_token") || strings.Contains(w.Body.String(), "private challenge") {
		t.Fatal("failed challenge storage exposed credentials or diagnostics")
	}
}

type mfaSessionInsertFailure struct{ db.Store }

func (s mfaSessionInsertFailure) CompleteMFALogin(ctx context.Context, hash string, user, previous *models.User, refresh, browser *models.AuthSession) error {
	// Fail the second INSERT in the real database transaction, after the recovery
	// update, challenge consumption and refresh INSERT have all been attempted.
	browser.ID = refresh.ID
	return s.Store.CompleteMFALogin(ctx, hash, user, previous, refresh, browser)
}

func TestMFAExchangeSessionFailureIsRetryable(t *testing.T) {
	srv, store, jwt := newTestServer(t)
	_, codes := enrollMFAFixture(t, srv, jwt)
	before, err := store.GetUserByEmail(t.Context(), "dev@example.com")
	if err != nil {
		t.Fatal(err)
	}
	challenge := freshChallenge(t, srv, "10.52.3.1")
	handler := routes.DefaultHandler
	handler.Store = mfaSessionInsertFailure{Store: store}
	defer func() { handler.Store = store }()
	body, _ := json.Marshal(map[string]string{"mfa_token": challenge, "code": codes[0]})
	failed := reqFrom(srv, http.MethodPost, "/api/v1/auth/mfa/login", "", "10.52.3.2", body)
	if failed.Code != http.StatusInternalServerError {
		t.Fatalf("second session insert failure: %d", failed.Code)
	}
	if len(failed.Result().Cookies()) != 0 || strings.Contains(failed.Body.String(), "access_token") {
		t.Fatal("failed transaction issued credentials")
	}
	after, err := store.GetUserByID(t.Context(), before.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.TOTPRecoveryCodes != before.TOTPRecoveryCodes {
		t.Fatal("failed session insert consumed recovery code")
	}
	pending, err := store.GetAuthSessionByHash(t.Context(), auth.HashSessionToken(challenge))
	if err != nil || pending.RevokedAt != nil {
		t.Fatalf("failed session insert consumed challenge: %v", err)
	}
	handler.Store = store
	success := reqFrom(srv, http.MethodPost, "/api/v1/auth/mfa/login", "", "10.52.3.3", body)
	if success.Code != http.StatusOK {
		t.Fatalf("retry: %d", success.Code)
	}
	data := mfaData(t, success.Body.Bytes())
	if got := request(srv, http.MethodGet, "/api/v1/auth/me", data["access_token"].(string), nil); got.Code != http.StatusOK {
		t.Fatalf("issued access token: %d", got.Code)
	}
	cookies := success.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "wolf_token" || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("missing or unsafe browser cookie")
	}
	for _, token := range []string{data["refresh_token"].(string), cookies[0].Value} {
		session, err := store.GetAuthSessionByHash(t.Context(), auth.HashSessionToken(token))
		if err != nil || session.UserID != before.ID || session.RevokedAt != nil {
			t.Fatalf("issued session not persisted: %v", err)
		}
	}
}
