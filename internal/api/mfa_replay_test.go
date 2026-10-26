package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/api/routes"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/pquerna/otp/totp"
)

func TestMFAActivationCodeCannotLogin(t *testing.T) {
	srv, store, jwt := newTestServer(t)
	secret, _ := enrollMFAFixture(t, srv, jwt)
	user, err := store.GetUserByEmail(t.Context(), "dev@example.com")
	if err != nil {
		t.Fatal(err)
	}
	code, err := totp.GenerateCode(secret, time.Unix(user.TOTPLastStep*30, 0))
	if err != nil {
		t.Fatal(err)
	}
	challenge := freshChallenge(t, srv, "10.53.0.1")
	body, _ := json.Marshal(map[string]string{"mfa_token": challenge, "code": code})
	w := reqFrom(srv, http.MethodPost, "/api/v1/auth/mfa/login", "", "10.53.0.2", body)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("activation code reused for login: %d", w.Code)
	}
}

func TestMFATOTPCannotReplayWithFreshChallenge(t *testing.T) {
	srv, _, jwt := newTestServer(t)
	secret, _ := enrollMFAFixture(t, srv, jwt)
	code, err := totp.GenerateCode(secret, time.Now().Add(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	first := freshChallenge(t, srv, "10.53.1.1")
	body, _ := json.Marshal(map[string]string{"mfa_token": first, "code": code})
	if w := reqFrom(srv, http.MethodPost, "/api/v1/auth/mfa/login", "", "10.53.1.2", body); w.Code != http.StatusOK {
		t.Fatalf("first login: %d", w.Code)
	}
	second := freshChallenge(t, srv, "10.53.1.3")
	body, _ = json.Marshal(map[string]string{"mfa_token": second, "code": code})
	w := reqFrom(srv, http.MethodPost, "/api/v1/auth/mfa/login", "", "10.53.1.4", body)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("TOTP reused with fresh challenge: %d", w.Code)
	}
	if len(w.Result().Cookies()) != 0 || strings.Contains(w.Body.String(), "access_token") {
		t.Fatal("TOTP replay issued credentials")
	}
	disableBody, _ := json.Marshal(map[string]string{"code": code})
	disabled := request(srv, http.MethodPost, "/api/v1/auth/mfa/disable", jwt, disableBody)
	if disabled.Code != http.StatusBadRequest {
		t.Fatalf("replayed TOTP disabled MFA: %d", disabled.Code)
	}

}

func TestMFAChallengeInvalidAfterReenrollment(t *testing.T) {
	srv, store, jwt := newTestServer(t)
	enrollMFAFixture(t, srv, jwt)
	user, err := store.GetUserByEmail(t.Context(), "dev@example.com")
	if err != nil {
		t.Fatal(err)
	}
	old := freshChallenge(t, srv, "10.53.2.1")
	reset := request(srv, http.MethodPost, "/api/v1/users/"+user.ID+"/mfa/reset", jwt, []byte(`{}`))
	if reset.Code != http.StatusOK {
		t.Fatalf("reset: %d", reset.Code)
	}
	_, codes := enrollMFAFixture(t, srv, jwt)
	before, err := store.GetUserByID(t.Context(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"mfa_token": old, "code": codes[0]})
	w := reqFrom(srv, http.MethodPost, "/api/v1/auth/mfa/login", "", "10.53.2.2", body)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("old enrollment challenge: %d", w.Code)
	}
	after, err := store.GetUserByID(t.Context(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.TOTPRecoveryCodes != before.TOTPRecoveryCodes {
		t.Fatal("old challenge consumed new enrollment's recovery code")
	}
}

type passwordChangeDuringLoginStore struct {
	db.Store
	changed bool
}

func (s *passwordChangeDuringLoginStore) GetUserByEmail(ctx context.Context, email string) (*models.User, error) {
	user, err := s.Store.GetUserByEmail(ctx, email)
	if err != nil || s.changed {
		return user, err
	}
	s.changed = true
	next := *user
	next.PasswordHash, err = auth.HashPassword("replacement password1234")
	if err != nil {
		return nil, err
	}
	if err = s.Store.UpdateUser(ctx, &next); err != nil {
		return nil, err
	}
	if err = s.Store.RevokeAuthSessionsByUser(ctx, user.ID, ""); err != nil {
		return nil, err
	}
	return user, nil
}
func TestMFAChallengeIssuedAcrossPasswordChange(t *testing.T) {
	srv, store, jwt := newTestServer(t)
	_, codes := enrollMFAFixture(t, srv, jwt)
	handler := routes.DefaultHandler
	handler.Store = &passwordChangeDuringLoginStore{Store: store}
	defer func() { handler.Store = store }()
	challenge := freshChallenge(t, srv, "10.53.3.1")
	body, _ := json.Marshal(map[string]string{"mfa_token": challenge, "code": codes[0]})
	w := reqFrom(srv, http.MethodPost, "/api/v1/auth/mfa/login", "", "10.53.3.2", body)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("stale password challenge: %d", w.Code)
	}
}

func TestMFATOTPConcurrentChallenges(t *testing.T) {
	srv, store, jwt := newTestServer(t)
	secret, codes := enrollMFAFixture(t, srv, jwt)
	before, err := store.GetUserByEmail(t.Context(), "dev@example.com")
	if err != nil {
		t.Fatal(err)
	}
	code, err := totp.GenerateCode(secret, time.Now().Add(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	challenges := []string{freshChallenge(t, srv, "10.53.4.1"), freshChallenge(t, srv, "10.53.4.2")}
	handler := routes.DefaultHandler
	handler.Store = &mfaReadBarrierStore{Store: store, user: before.ID, ready: make(chan struct{})}
	defer func() { handler.Store = store }()
	type result struct{ index, status int }
	results := make(chan result, 2)
	for i, challenge := range challenges {
		body, _ := json.Marshal(map[string]string{"mfa_token": challenge, "code": code})
		go func() {
			w := reqFrom(srv, http.MethodPost, "/api/v1/auth/mfa/login", "", fmt.Sprintf("10.53.4.%d", i+3), body)
			results <- result{i, w.Code}
		}()
	}
	wins, conflicts, loser := 0, 0, -1
	for range 2 {
		select {
		case got := <-results:
			if got.status == http.StatusOK {
				wins++
			} else if got.status == http.StatusConflict {
				conflicts++
				loser = got.index
			} else {
				t.Errorf("concurrent TOTP: %d", got.status)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent MFA requests timed out")
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("TOTP races: wins=%d conflicts=%d", wins, conflicts)
	}
	after, err := store.GetUserByID(t.Context(), before.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.TOTPLastStep <= before.TOTPLastStep || after.CredentialVersion != before.CredentialVersion || after.TOTPRecoveryCodes != before.TOTPRecoveryCodes {
		t.Fatal("TOTP exchange corrupted factor state")
	}
	// Losing a snapshot race preserves the other challenge and unused factors.
	body, _ := json.Marshal(map[string]string{"mfa_token": challenges[loser], "code": codes[0]})
	if w := reqFrom(srv, http.MethodPost, "/api/v1/auth/mfa/login", "", "10.53.4.5", body); w.Code != http.StatusOK {
		t.Fatalf("unused challenge/recovery retry: %d", w.Code)
	}
}

func TestMFATOTPInsertFailureRollsBackStep(t *testing.T) {
	srv, store, jwt := newTestServer(t)
	secret, _ := enrollMFAFixture(t, srv, jwt)
	before, err := store.GetUserByEmail(t.Context(), "dev@example.com")
	if err != nil {
		t.Fatal(err)
	}
	code, err := totp.GenerateCode(secret, time.Now().Add(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	challenge := freshChallenge(t, srv, "10.53.5.1")
	handler := routes.DefaultHandler
	handler.Store = mfaSessionInsertFailure{Store: store}
	defer func() { handler.Store = store }()
	body, _ := json.Marshal(map[string]string{"mfa_token": challenge, "code": code})
	failed := reqFrom(srv, http.MethodPost, "/api/v1/auth/mfa/login", "", "10.53.5.2", body)
	if failed.Code != http.StatusInternalServerError {
		t.Fatalf("session insert failure: %d", failed.Code)
	}
	after, err := store.GetUserByID(t.Context(), before.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.TOTPLastStep != before.TOTPLastStep || after.MFARevision != before.MFARevision {
		t.Fatal("failed session insert consumed the step")
	}
	handler.Store = store
	if w := reqFrom(srv, http.MethodPost, "/api/v1/auth/mfa/login", "", "10.53.5.3", body); w.Code != http.StatusOK {
		t.Fatalf("TOTP retry after rollback: %d", w.Code)
	}
}

type passwordChangeBeforeMFACommitStore struct{ db.Store }

func (s passwordChangeBeforeMFACommitStore) CompleteMFALogin(ctx context.Context, hash string, user, previous *models.User, refresh, browser *models.AuthSession) error {
	changed := *previous
	changed.PasswordHash = "different password hash"
	if err := s.Store.UpdateUser(ctx, &changed); err != nil {
		return err
	}
	return s.Store.CompleteMFALogin(ctx, hash, user, previous, refresh, browser)
}
func TestMFAPasswordChangeBeforeCommit(t *testing.T) {
	srv, store, jwt := newTestServer(t)
	_, codes := enrollMFAFixture(t, srv, jwt)
	before, err := store.GetUserByEmail(t.Context(), "dev@example.com")
	if err != nil {
		t.Fatal(err)
	}
	challenge := freshChallenge(t, srv, "10.53.6.1")
	handler := routes.DefaultHandler
	handler.Store = passwordChangeBeforeMFACommitStore{Store: store}
	defer func() { handler.Store = store }()
	body, _ := json.Marshal(map[string]string{"mfa_token": challenge, "code": codes[0]})
	w := reqFrom(srv, http.MethodPost, "/api/v1/auth/mfa/login", "", "10.53.6.2", body)
	if w.Code != http.StatusConflict {
		t.Fatalf("password change before commit: %d", w.Code)
	}
	if len(w.Result().Cookies()) != 0 || strings.Contains(w.Body.String(), "access_token") {
		t.Fatal("stale verification issued credentials")
	}
	after, err := store.GetUserByID(t.Context(), before.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.TOTPRecoveryCodes != before.TOTPRecoveryCodes {
		t.Fatal("stale verification consumed a recovery code")
	}
}
