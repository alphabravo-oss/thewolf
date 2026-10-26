package db

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/google/uuid"
)

func TestMFAStateSQLite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mfa.db")
	first, err := NewSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	second, err := NewSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	testMFAState(t, []Store{first, second})
}
func TestMFAStatePostgres(t *testing.T) {
	s := newClusterPostgresStore(t)
	testMFAState(t, []Store{s})
}
func testMFAState(t *testing.T, stores []Store) {
	ctx := t.Context()
	s := stores[0]
	user := &models.User{ID: uuid.NewString(), Email: uuid.NewString() + "@example.test", PasswordHash: "unused"}
	if err := s.CreateUser(ctx, user); err != nil {
		t.Fatal(err)
	}
	before, err := s.GetUserByID(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	enabled := *before
	enabled.TOTPSecret = "encrypted-fixture"
	enabled.TOTPEnabled = true
	enabled.TOTPRecoveryCodes = `["a","b"]`
	if err := s.UpdateUserMFA(ctx, &enabled, before); err != nil {
		t.Fatal(err)
	}
	// Profile edits must neither overwrite MFA nor invalidate an MFA-only snapshot.
	profile := enabled
	profile.DisplayName = "updated profile"
	if err := s.UpdateUserProfile(ctx, &profile); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 16)
	for i := 0; i < 16; i++ {
		go func() {
			next := enabled
			next.TOTPRecoveryCodes = fmt.Sprintf(`["remaining-%d"]`, i)
			results <- stores[i%len(stores)].UpdateUserMFA(ctx, &next, &enabled)
		}()
	}
	successes, conflicts := 0, 0
	for range 16 {
		err := <-results
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrMFAChanged):
			conflicts++
		default:
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 15 {
		t.Fatalf("stale concurrent snapshots: success=%d conflict=%d", successes, conflicts)
	}
	current, err := s.GetUserByID(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.DisplayName != "updated profile" || current.TOTPRecoveryCodes == enabled.TOTPRecoveryCodes || current.TOTPSecret != enabled.TOTPSecret || !current.MFAEnabled() {
		t.Fatal("MFA update overwrote unrelated fields or lost its commit")
	}
	reset := *current
	reset.TOTPSecret = ""
	reset.TOTPEnabled = false
	reset.TOTPRecoveryCodes = ""
	if err := stores[len(stores)-1].UpdateUserMFA(ctx, &reset, current); err != nil {
		t.Fatal(err)
	}
	stale := *current
	stale.TOTPRecoveryCodes = `[]`
	if err := s.UpdateUserMFA(ctx, &stale, current); !errors.Is(err, ErrMFAChanged) {
		t.Fatalf("stale recovery write after reset: %v", err)
	}
	latest, err := s.GetUserByID(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if latest.MFAEnabled() || latest.TOTPSecret != "" || latest.TOTPRecoveryCodes != "" {
		t.Fatal("stale write restored reset enrollment")
	}
	// Invalid caller snapshots and canceled writes must not change any state.
	wrong := *latest
	wrong.ID = uuid.NewString()
	for _, previous := range []*models.User{nil, &wrong} {
		if err := s.UpdateUserMFA(ctx, latest, previous); !errors.Is(err, ErrMFAChanged) {
			t.Fatalf("invalid previous identity: %v", err)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	next := *latest
	next.TOTPRecoveryCodes = "changed"
	if err := s.UpdateUserMFA(canceled, &next, latest); err == nil {
		t.Fatal("canceled MFA write succeeded")
	}
	after, err := s.GetUserByID(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.TOTPSecret != latest.TOTPSecret || after.TOTPRecoveryCodes != latest.TOTPRecoveryCodes || after.TOTPEnabled != latest.TOTPEnabled {
		t.Fatal("failed write changed MFA state")
	}
}
