package db

import (
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

func TestUserRoleMigrationPostgres(t *testing.T) {
	s := newClusterPostgresStore(t)
	testUserRoleMigration(t, s, s.db)
}
func testUserRoleMigration(t *testing.T, store Store, database *sqlx.DB) {
	t.Helper()
	u := &models.User{ID: uuid.NewString(), Email: uuid.NewString() + "@example.test", PasswordHash: "unchanged", Role: models.RoleUser}
	if err := store.CreateUser(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetUserByID(t.Context(), u.ID)
	if err != nil || got.Role != models.RoleUser {
		t.Fatalf("startup promoted a user: %+v %v", got, err)
	}
	// Actual pre-role upgrades still bootstrap their original owner once.
	if _, err := database.Exec("ALTER TABLE users DROP COLUMN role"); err != nil {
		t.Fatal(err)
	}
	reconnectPostgresTestStore(store)
	if err := store.Migrate(); err != nil {
		t.Fatal(err)
	}
	got, err = store.GetUserByID(t.Context(), u.ID)
	if err != nil || got.Role != models.RoleAdmin {
		t.Fatalf("legacy owner not migrated: %+v %v", got, err)
	}
	got.Role = models.RoleUser
	if err := store.UpdateUser(t.Context(), got); err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(); err != nil {
		t.Fatal(err)
	}
	got, err = store.GetUserByID(t.Context(), u.ID)
	if err != nil || got.Role != models.RoleUser || got.PasswordHash != "unchanged" {
		t.Fatalf("migration undid demotion: %+v %v", got, err)
	}
}
