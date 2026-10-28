package serve

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestOpenStoreRejectsSQLiteAndGuardsUnmigratedData(t *testing.T) {
	t.Setenv("WOLF_DB_DRIVER", "sqlite")
	t.Setenv("WOLF_DB_DSN", "")
	if _, err := OpenStore(); err == nil || !strings.Contains(err.Error(), "migrate-from-sqlite") {
		t.Fatalf("sqlite driver: %v", err)
	}

	dsn := db.TestPostgresDSN(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("WOLF_DB_FILE", filepath.Join(home, "database.env"))
	t.Setenv("WOLF_DB_DRIVER", "postgres")
	t.Setenv("WOLF_DB_DSN", dsn)
	// No wolf.db: an empty PostgreSQL is a normal fresh install.
	store, err := OpenStore()
	if err != nil {
		t.Fatal(err)
	}
	_ = store.Close()

	// A leftover wolf.db with an empty PostgreSQL would look like a wiped install.
	if err := os.MkdirAll(filepath.Join(home, ".wolf"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".wolf", "wolf.db"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(); err == nil || !strings.Contains(err.Error(), "migrate-from-sqlite") {
		t.Fatalf("unmigrated wolf.db with empty PostgreSQL: %v", err)
	}

	// Once PostgreSQL has users the leftover file no longer blocks startup.
	seeded, err := db.NewPostgres(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := seeded.CreateUser(t.Context(), &models.User{ID: "u1", Email: "u1@example.test", PasswordHash: "hash"}); err != nil {
		t.Fatal(err)
	}
	_ = seeded.Close()
	store, err = OpenStore()
	if err != nil {
		t.Fatalf("migrated install: %v", err)
	}
	_ = store.Close()
}
