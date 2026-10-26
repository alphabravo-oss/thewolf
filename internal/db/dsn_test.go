package db

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveDatabaseFileOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "database.env")
	t.Setenv("WOLF_DB_FILE", path)
	t.Setenv("WOLF_DB_DRIVER", "")
	t.Setenv("WOLF_DB_DSN", "")
	os.Unsetenv("WOLF_DB_DRIVER")
	os.Unsetenv("WOLF_DB_DSN")
	t.Setenv("WOLF_DB_FILE", path)

	if err := os.WriteFile(path, []byte("WOLF_DB_DRIVER=postgres\nWOLF_DB_DSN=postgres://wolf:s3cret@db.example:5432/wolf?sslmode=require\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := ResolveDatabase()
	if cfg.Driver != "postgres" || cfg.DSN == "" || !cfg.FileSet || cfg.EnvManaged {
		t.Fatalf("%+v", cfg)
	}
	v := cfg.View()
	if v.Host != "db.example:5432" || v.Database != "wolf" || v.User != "wolf" || v.SSLMode != "require" {
		t.Fatalf("%+v", v)
	}
	if v.Host != "db.example:5432" {
		t.Fatal("password must not appear in view")
	}
}

func TestResolveDatabaseEnvWins(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "database.env")
	t.Setenv("WOLF_DB_FILE", path)
	t.Setenv("WOLF_DB_DRIVER", "postgres")
	t.Setenv("WOLF_DB_DSN", "postgres://env@helm:5432/wolf")
	if err := os.WriteFile(path, []byte("WOLF_DB_DRIVER=postgres\nWOLF_DB_DSN=postgres://file@file:5432/wolf\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := ResolveDatabase()
	if !cfg.EnvManaged || cfg.DSN != "postgres://env@helm:5432/wolf" {
		t.Fatalf("%+v", cfg)
	}
}

func TestValidatePostgresDSN(t *testing.T) {
	if err := validatePostgresDSN("postgres://wolf@db:5432/wolf"); err != nil {
		t.Fatal(err)
	}
	if err := validatePostgresDSN("sqlite:///tmp/x"); err == nil {
		t.Fatal("expected error")
	}
}

func TestSavePostgresOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "database.env")
	t.Setenv("WOLF_DB_FILE", path)
	t.Setenv("WOLF_DB_DRIVER", "")
	t.Setenv("WOLF_DB_DSN", "")
	if err := SavePostgresOverride("postgres://wolf:pw@db:5432/wolf?sslmode=require"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WOLF_DB_DRIVER", "")
	t.Setenv("WOLF_DB_DSN", "")
	os.Unsetenv("WOLF_DB_DRIVER")
	os.Unsetenv("WOLF_DB_DSN")
	cfg := ResolveDatabase()
	if cfg.Driver != "postgres" || cfg.DSN != "postgres://wolf:pw@db:5432/wolf?sslmode=require" {
		t.Fatalf("%+v", cfg)
	}
}
