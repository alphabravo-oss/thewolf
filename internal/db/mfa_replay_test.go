package db

import (
	"errors"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

func TestMFAReplayPostgres(t *testing.T) {
	first := newClusterPostgresStore(t)
	dsn := testDSNFor(t, first)
	second, err := NewPostgres(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	id := testMFAReplay(t, []Store{first, second})
	_ = first.Close()
	_ = second.Close()
	reopened, err := NewPostgres(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	verifyMFAReplayRestart(t, reopened, id)
}
func verifyMFAReplayRestart(t *testing.T, store Store, id string) {
	t.Helper()
	user, err := store.GetUserByID(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if user.TOTPLastStep != 101 || user.CredentialVersion != 2 || user.MFARevision != 3 || user.PasswordHash != "replacement hash" || user.Role != models.RoleAdmin {
		t.Fatal("credential/replay state did not survive pool closure and migration rerun")
	}
	old := *user
	old.TOTPLastStep = 100
	if err := store.UpdateUserMFA(t.Context(), &old, user); !errors.Is(err, ErrMFAChanged) {
		t.Fatalf("reopened store permitted step rollback: %v", err)
	}
}
func testMFAReplay(t *testing.T, stores []Store) string {
	t.Helper()
	ctx, store := t.Context(), stores[0]
	user := &models.User{ID: uuid.NewString(), Email: uuid.NewString() + "@example.test", PasswordHash: "original hash"}
	if err := store.CreateUser(ctx, user); err != nil {
		t.Fatal(err)
	}
	initial, err := store.GetUserByID(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	reset := *initial
	if err := store.UpdateUserMFA(ctx, &reset, initial); err != nil {
		t.Fatal(err)
	}
	stale := *initial
	stale.TOTPSecret = "pending secret"
	if err := store.UpdateUserMFA(ctx, &stale, initial); !errors.Is(err, ErrMFAChanged) {
		t.Fatalf("equal-state reset did not invalidate stale setup: %v", err)
	}
	enabled := reset
	enabled.TOTPSecret = "encrypted fixture"
	enabled.TOTPEnabled = true
	enabled.TOTPRecoveryCodes = `["a","b"]`
	enabled.TOTPLastStep = 100
	if err := store.UpdateUserMFA(ctx, &enabled, &reset); err != nil {
		t.Fatal(err)
	}
	newSession := func(prefix string) *models.AuthSession {
		return &models.AuthSession{ID: uuid.NewString(), UserID: user.ID, SessionHash: uuid.NewString(), SessionPrefix: prefix, CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Minute)}
	}
	type result struct {
		err                         error
		challenge, refresh, browser *models.AuthSession
	}
	results := make(chan result, 16)
	for i := 0; i < 16; i++ {
		challenge, refresh, browser := newSession("mfa"), newSession("refresh"), newSession("wfs_test")
		if err := store.CreateAuthSession(ctx, challenge); err != nil {
			t.Fatal(err)
		}
		go func() {
			next := enabled
			next.TOTPLastStep = 101
			err := stores[i%len(stores)].CompleteMFALogin(ctx, challenge.SessionHash, &next, &enabled, refresh, browser)
			results <- result{err, challenge, refresh, browser}
		}()
	}
	wins, conflicts := 0, 0
	for range 16 {
		got := <-results
		if got.err == nil {
			wins++
		} else if errors.Is(got.err, ErrMFAChanged) {
			conflicts++
		} else {
			t.Fatal(got.err)
		}
		challenge, err := store.GetAuthSessionByHash(ctx, got.challenge.SessionHash)
		if err != nil {
			t.Fatal(err)
		}
		if (challenge.RevokedAt != nil) != (got.err == nil) {
			t.Fatal("challenge consumption disagrees with transaction")
		}
		for _, session := range []*models.AuthSession{got.refresh, got.browser} {
			_, err := store.GetAuthSessionByHash(ctx, session.SessionHash)
			if (err == nil) != (got.err == nil) {
				t.Fatal("failed step race left a session")
			}
		}
	}
	if wins != 1 || conflicts != 15 {
		t.Fatalf("same step/different challenges: wins=%d conflicts=%d", wins, conflicts)
	}
	current, err := store.GetUserByID(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.TOTPLastStep != 101 || current.CredentialVersion != enabled.CredentialVersion || current.TOTPRecoveryCodes != enabled.TOTPRecoveryCodes {
		t.Fatal("step consumption changed unrelated credential state")
	}
	rollback := *current
	rollback.TOTPLastStep = 100
	if err := store.UpdateUserMFA(ctx, &rollback, current); !errors.Is(err, ErrMFAChanged) {
		t.Fatalf("step rollback allowed: %v", err)
	}
	changed := *current
	changed.PasswordHash = "replacement hash"
	if err := store.UpdateUser(ctx, &changed); err != nil {
		t.Fatal(err)
	}
	if changed.CredentialVersion != current.CredentialVersion+1 {
		t.Fatal("password change did not advance credential version")
	}
	oldRole := *current
	oldRole.Role = models.RoleAdmin
	if err := store.UpdateUser(ctx, &oldRole); !errors.Is(err, ErrUserChanged) {
		t.Fatalf("stale role update restored a password: %v", err)
	}
	challenge, refresh, browser := newSession("mfa"), newSession("refresh"), newSession("wfs_test")
	if err := store.CreateAuthSession(ctx, challenge); err != nil {
		t.Fatal(err)
	}
	next := *current
	next.TOTPLastStep = 102
	if err := store.CompleteMFALogin(ctx, challenge.SessionHash, &next, current, refresh, browser); !errors.Is(err, ErrMFAChanged) {
		t.Fatalf("stale password exchange succeeded: %v", err)
	}
	changed.Role = models.RoleAdmin
	version := changed.CredentialVersion
	if err := store.UpdateUser(ctx, &changed); err != nil {
		t.Fatal(err)
	}
	if changed.CredentialVersion != version {
		t.Fatal("role-only change invalidated MFA challenges")
	}
	return user.ID
}

func TestMFAReplayMigrationPostgres(t *testing.T) {
	s := newClusterPostgresStore(t)
	testMFAReplayMigration(t, s, s.db)
}
func testMFAReplayMigration(t *testing.T, s Store, database *sqlx.DB) {
	t.Helper()
	ctx := t.Context()
	user := &models.User{ID: uuid.NewString(), Email: uuid.NewString() + "@example.test", PasswordHash: "unchanged"}
	if err := s.CreateUser(ctx, user); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `UPDATE users SET totp_secret='legacy secret',totp_enabled=TRUE,totp_recovery_codes='["legacy code"]'`); err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"credential_version", "mfa_revision", "totp_last_step"} {
		if _, err := database.ExecContext(ctx, "ALTER TABLE users DROP COLUMN "+column); err != nil {
			t.Fatal(err)
		}
	}
	floor := time.Now().Unix()/30 + 1
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	migrated, err := s.GetUserByID(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if migrated.CredentialVersion != 0 || migrated.MFARevision != 0 || (migrated.TOTPLastStep < floor || migrated.TOTPLastStep > time.Now().Unix()/30+1) || migrated.TOTPSecret != "legacy secret" || !migrated.TOTPEnabled || migrated.TOTPRecoveryCodes != `["legacy code"]` {
		t.Fatal("migration lost enrollment or initialized unsafe replay state")
	}
	next := *migrated
	next.TOTPLastStep = migrated.TOTPLastStep + 1
	if err := s.UpdateUserMFA(ctx, &next, migrated); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	again, err := s.GetUserByID(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again.TOTPLastStep != next.TOTPLastStep || again.MFARevision != next.MFARevision {
		t.Fatal("migration rerun reset consumed TOTP state")
	}
}
