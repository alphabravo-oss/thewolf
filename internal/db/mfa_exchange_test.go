package db

import (
	"errors"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/google/uuid"
)

func TestMFAExchangePostgres(t *testing.T) { testMFAExchange(t, []Store{newClusterPostgresStore(t)}) }

func testMFAExchange(t *testing.T, stores []Store) {
	ctx, store := t.Context(), stores[0]
	user := &models.User{ID: uuid.NewString(), Email: uuid.NewString() + "@example.test", PasswordHash: "unused"}
	if err := store.CreateUser(ctx, user); err != nil {
		t.Fatal(err)
	}
	before, err := store.GetUserByID(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	user = before
	enabled := *user
	enabled.TOTPSecret = "encrypted fixture"
	enabled.TOTPEnabled = true
	enabled.TOTPRecoveryCodes = `["a","b"]`
	if err := store.UpdateUserMFA(ctx, &enabled, user); err != nil {
		t.Fatal(err)
	}
	newSession := func(prefix string) *models.AuthSession {
		return &models.AuthSession{ID: uuid.NewString(), UserID: user.ID, SessionHash: uuid.NewString(), SessionPrefix: prefix, CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Minute)}
	}
	for _, fault := range []string{"refresh insert", "browser insert", "expired", "revoked", "wrong purpose", "absent", "stale MFA"} {
		t.Run(fault, func(t *testing.T) {
			challenge, refresh, browser := newSession("mfa"), newSession("refresh"), newSession("wfs_test")
			switch fault {
			case "expired":
				challenge.ExpiresAt = time.Now().UTC().Add(-time.Minute)
			case "revoked":
				now := time.Now().UTC()
				challenge.RevokedAt = &now
			case "wrong purpose":
				challenge.SessionPrefix = "refresh"
			}
			if fault != "absent" {
				if err := store.CreateAuthSession(ctx, challenge); err != nil {
					t.Fatal(err)
				}
			}
			if fault == "refresh insert" {
				refresh.ID = challenge.ID
			}
			if fault == "browser insert" {
				browser.ID = challenge.ID
			}
			previous, next := enabled, enabled
			next.TOTPRecoveryCodes = `["b"]`
			if fault == "stale MFA" {
				previous.TOTPSecret = "stale"
			}
			err := store.CompleteMFALogin(ctx, challenge.SessionHash, &next, &previous, refresh, browser)
			if err == nil {
				t.Fatal("failed transaction succeeded")
			}
			switch fault {
			case "expired", "revoked", "wrong purpose", "absent":
				if !errors.Is(err, ErrMFAChallenge) {
					t.Fatalf("challenge rejection: %v", err)
				}
			case "stale MFA":
				if !errors.Is(err, ErrMFAChanged) {
					t.Fatalf("MFA conflict: %v", err)
				}
			}
			stored, err := store.GetUserByID(ctx, user.ID)
			if err != nil {
				t.Fatal(err)
			}
			if stored.TOTPRecoveryCodes != enabled.TOTPRecoveryCodes {
				t.Fatal("failed transaction consumed recovery code")
			}
			for _, session := range []*models.AuthSession{refresh, browser} {
				if _, err := store.GetAuthSessionByHash(ctx, session.SessionHash); err == nil {
					t.Fatal("failed transaction retained a session")
				}
			}
			if fault != "absent" {
				old, err := store.GetAuthSessionByHash(ctx, challenge.SessionHash)
				if err != nil {
					t.Fatal(err)
				}
				if (old.RevokedAt == nil) != (challenge.RevokedAt == nil) {
					t.Fatal("failed transaction consumed challenge")
				}
			}
			// Failed insert can be retried with exactly the same factor and challenge.
			if fault == "refresh insert" || fault == "browser insert" {
				refresh.ID, browser.ID = uuid.NewString(), uuid.NewString()
				if err := store.CompleteMFALogin(ctx, challenge.SessionHash, &next, &previous, refresh, browser); err != nil {
					t.Fatal(err)
				}
				// Restore the test factor state for the next independent scenario.
				restore := enabled
				if err := store.UpdateUserMFA(ctx, &restore, &next); err != nil {
					t.Fatal(err)
				}
				enabled = restore
			}
		})
	}
	// A TOTP-style no-op factor update must still consume a challenge exactly once.
	challenge := newSession("mfa")
	if err := store.CreateAuthSession(ctx, challenge); err != nil {
		t.Fatal(err)
	}
	type result struct {
		err              error
		refresh, browser *models.AuthSession
	}
	results := make(chan result, 16)
	for i := 0; i < 16; i++ {
		go func() {
			next := enabled
			refresh, browser := newSession("refresh"), newSession("wfs_test")
			err := stores[i%len(stores)].CompleteMFALogin(ctx, challenge.SessionHash, &next, &enabled, refresh, browser)
			results <- result{err, refresh, browser}
		}()
	}
	successes, replays := 0, 0
	for range 16 {
		got := <-results
		if got.err == nil {
			successes++
		} else if errors.Is(got.err, ErrMFAChallenge) || errors.Is(got.err, ErrMFAChanged) {
			replays++
		} else {
			t.Fatal(got.err)
		}
		for _, session := range []*models.AuthSession{got.refresh, got.browser} {
			_, err := store.GetAuthSessionByHash(ctx, session.SessionHash)
			if (err == nil) != (got.err == nil) {
				t.Fatalf("session persisted=%v for exchange error=%v", err == nil, got.err)
			}
		}
	}
	if successes != 1 || replays != 15 {
		t.Fatalf("concurrent exchanges: successes=%d replays=%d", successes, replays)
	}
	old, err := store.GetAuthSessionByHash(ctx, challenge.SessionHash)
	if err != nil || old.RevokedAt == nil {
		t.Fatalf("winning challenge was not consumed: %v", err)
	}
}
