package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestScannerReleaseBackupCLIExportPreflightRestore(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "wolf.db")
	t.Setenv("WOLF_DB_DRIVER", "sqlite")
	t.Setenv("WOLF_DB_DSN", databasePath)
	backupPath := filepath.Join(t.TempDir(), "release-backup.json")

	export := newScannerReleaseBackupExportCmd()
	var exportOutput bytes.Buffer
	export.SetOut(&exportOutput)
	export.SetArgs([]string{
		"--output", backupPath,
		"--actor", "backup-test@example.test",
		"--reason", "CLI contract export",
		"--idempotency-key", "export-" + uuid.NewString(),
	})
	if err := export.Execute(); err != nil {
		t.Fatalf("export: %v", err)
	}
	if !strings.Contains(exportOutput.String(), `"payload_sha256"`) {
		t.Fatalf("export output = %s", exportOutput.String())
	}
	info, err := os.Stat(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("backup permissions = %o, want 600", info.Mode().Perm())
	}

	secondExport := newScannerReleaseBackupExportCmd()
	secondExport.SetArgs([]string{
		"--output", backupPath,
		"--actor", "backup-test@example.test",
		"--reason", "must not overwrite",
		"--idempotency-key", "export-" + uuid.NewString(),
	})
	if err := secondExport.Execute(); err == nil ||
		!strings.Contains(err.Error(), "without overwrite") {
		t.Fatalf("existing destination error = %v", err)
	}

	preflight := newScannerReleaseBackupPreflightCmd()
	var preflightOutput bytes.Buffer
	preflight.SetOut(&preflightOutput)
	preflight.SetArgs([]string{"--input", backupPath})
	if err := preflight.Execute(); err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if !strings.Contains(preflightOutput.String(), `"restorable": true`) {
		t.Fatalf("preflight output = %s", preflightOutput.String())
	}

	restore := newScannerReleaseBackupRestoreCmd()
	var restoreOutput bytes.Buffer
	restore.SetOut(&restoreOutput)
	restore.SetArgs([]string{
		"--input", backupPath,
		"--actor", "dr-test@example.test",
		"--reason", "CLI contract restore",
		"--idempotency-key", "restore-" + uuid.NewString(),
		"--confirm", "RESTORE_SCANNER_RELEASE_STATE",
	})
	if err := restore.Execute(); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if !strings.Contains(restoreOutput.String(), `"status": "completed"`) {
		t.Fatalf("restore output = %s", restoreOutput.String())
	}
}

func TestReadScannerReleaseBackupRejectsUnknownAndOversizedDocuments(t *testing.T) {
	unknown := filepath.Join(t.TempDir(), "unknown.json")
	if err := os.WriteFile(unknown, []byte(`{"unexpected":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readScannerReleaseBackup(unknown); err == nil ||
		!strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown field error = %v", err)
	}

	oversized := filepath.Join(t.TempDir(), "oversized.json")
	file, err := os.OpenFile(oversized, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxScannerReleaseBackupBytes + 1); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readScannerReleaseBackup(oversized); err == nil ||
		!strings.Contains(err.Error(), "size must be") {
		t.Fatalf("oversized error = %v", err)
	}
}
