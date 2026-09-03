package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/google/uuid"
)

func TestInitWritesFiles(t *testing.T) {
	dir := t.TempDir()
	if err := writeWolfInit(dir, false); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{".wolf.yaml", filepath.Join(".github", "workflows", "wolf.yml")} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
	}
	if err := writeWolfInit(dir, false); err == nil {
		t.Fatal("expected refuse without --force")
	}
	if err := writeWolfInit(dir, true); err != nil {
		t.Fatalf("force: %v", err)
	}
}

func TestBackupRestoreRoundtrip(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "wolf.db")
	store, err := db.NewSQLite(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(); err != nil {
		t.Fatal(err)
	}
	email := "backup@example.com"
	if err := store.CreateUser(t.Context(), &models.User{
		ID: uuid.NewString(), Email: email, PasswordHash: "x", Role: models.RoleUser,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	tarPath := filepath.Join(dir, "backup.tar")
	if err := backupSQLite(src, tarPath, "", ""); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "restored.db")
	if err := restoreSQLite(tarPath, dest, "", false); err != nil {
		t.Fatal(err)
	}
	if err := restoreSQLite(tarPath, dest, "", false); err == nil {
		t.Fatal("expected refuse overwrite without --force")
	}
	opened, err := db.NewSQLite(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	if err := opened.Migrate(); err != nil {
		t.Fatal(err)
	}
	u, err := opened.GetUserByEmail(t.Context(), email)
	if err != nil || u == nil {
		t.Fatalf("restored user: %v", err)
	}

	encTar := filepath.Join(dir, "backup2.tar")
	if err := backupSQLite(src, encTar, "pw", ""); err != nil {
		t.Fatal(err)
	}
	encPath := encTar + ".enc"
	if _, err := os.Stat(encPath); err != nil {
		t.Fatalf("expected encrypted file: %v", err)
	}
	encDest := filepath.Join(dir, "restored-enc.db")
	if err := restoreSQLite(encPath, encDest, "pw", false); err != nil {
		t.Fatal(err)
	}
}
