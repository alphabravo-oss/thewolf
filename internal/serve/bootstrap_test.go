package serve

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/models"
)

type bootstrapReadFailure struct{ db.Store }

func (s bootstrapReadFailure) GetUserByEmail(context.Context, string) (*models.User, error) {
	return nil, errors.New("fixture unavailable")
}

func TestBootstrapAdminValidationAndNoRepromotion(t *testing.T) {
	store, err := db.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	t.Setenv("WOLF_ADMIN_EMAIL", "")
	t.Setenv("WOLF_ADMIN_PASSWORD", "")
	if err := bootstrapAdmin(t.Context(), store); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ email, password string }{
		{"", "long-test-password"}, {"admin@example.test", ""},
		{"invalid@@example.test", "long-test-password"},
		{"Display <admin@example.test>", "long-test-password"},
		{"admin@example.test", "short"}, {"admin@example.test", strings.Repeat("a", 129)},
	} {
		t.Setenv("WOLF_ADMIN_EMAIL", tc.email)
		t.Setenv("WOLF_ADMIN_PASSWORD", tc.password)
		if err := bootstrapAdmin(t.Context(), store); err == nil {
			t.Fatal("invalid bootstrap accepted")
		}
	}
	t.Setenv("WOLF_ADMIN_EMAIL", " ADMIN@example.test ")
	t.Setenv("WOLF_ADMIN_PASSWORD", "long-test-password")
	if err := bootstrapAdmin(t.Context(), bootstrapReadFailure{store}); err == nil {
		t.Fatal("failed lookup allowed bootstrap")
	}
	if err := bootstrapAdmin(t.Context(), store); err != nil {
		t.Fatal(err)
	}
	u, err := store.GetUserByEmail(t.Context(), "admin@example.test")
	if err != nil || u.Role != models.RoleAdmin {
		t.Fatalf("bootstrap: %+v %v", u, err)
	}
	if ok, err := auth.VerifyPassword("long-test-password", u.PasswordHash); err != nil || !ok {
		t.Fatal("bootstrap password is unusable")
	}
	oldHash := u.PasswordHash
	u.Role = models.RoleUser
	if err := store.UpdateUser(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WOLF_ADMIN_PASSWORD", "different-valid-password")
	if err := bootstrapAdmin(t.Context(), store); err != nil {
		t.Fatal(err)
	}
	u, err = store.GetUserByEmail(t.Context(), u.Email)
	if err != nil || u.Role != models.RoleUser || u.PasswordHash != oldHash {
		t.Fatal("restart repromoted or reset an existing account")
	}
}
