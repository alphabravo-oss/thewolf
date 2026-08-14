package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

const maxScannerReleaseBackupBytes int64 = 512 << 20

func newScannerReleaseBackupCmd() *cobra.Command {
	command := &cobra.Command{
		Use:   "scanner-release-backup",
		Short: "Export, validate, and restore scanner release control-plane state",
	}
	command.AddCommand(
		newScannerReleaseBackupExportCmd(),
		newScannerReleaseBackupPreflightCmd(),
		newScannerReleaseBackupRestoreCmd(),
		newScannerReleaseBackupStatusCmd(),
	)
	return command
}

func newScannerReleaseBackupExportCmd() *cobra.Command {
	var output, actor, reason, idempotencyKey string
	command := &cobra.Command{
		Use:   "export",
		Short: "Create a checksummed scanner release backup",
		RunE: func(command *cobra.Command, _ []string) error {
			if strings.TrimSpace(output) == "" {
				return errors.New("--output is required")
			}
			store, err := openStore()
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()
			backup, err := store.ScannerReleases().ExportReleaseBackup(
				command.Context(),
				scannerrelease.BackupCommand{
					Actor: actor, Reason: reason, IdempotencyKey: idempotencyKey,
				},
			)
			if err != nil {
				return err
			}
			absolute, err := writeScannerReleaseBackup(output, backup)
			if err != nil {
				return err
			}
			return writeScannerReleaseJSON(command, map[string]any{
				"status":         "completed",
				"output":         absolute,
				"format":         backup.Format,
				"version":        backup.Version,
				"payload_sha256": backup.PayloadDigest,
				"table_counts":   scannerReleaseBackupCounts(backup),
			})
		},
	}
	command.Flags().StringVar(&output, "output", "", "new backup file path (must not already exist)")
	addScannerReleaseBackupAuditFlags(command, &actor, &reason, &idempotencyKey)
	return command
}

func newScannerReleaseBackupPreflightCmd() *cobra.Command {
	var input string
	command := &cobra.Command{
		Use:   "preflight",
		Short: "Validate a backup and the target database without changing either",
		RunE: func(command *cobra.Command, _ []string) error {
			backup, _, err := readScannerReleaseBackup(input)
			if err != nil {
				return err
			}
			store, err := openStore()
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()
			preflight, err := store.ScannerReleases().PreflightReleaseRestore(
				command.Context(), backup,
			)
			if err != nil {
				return err
			}
			if err := writeScannerReleaseJSON(command, preflight); err != nil {
				return err
			}
			if !preflight.Restorable {
				return errors.New("scanner release backup is valid but target is not restorable")
			}
			return nil
		},
	}
	command.Flags().StringVar(&input, "input", "", "backup file to validate")
	return command
}

func newScannerReleaseBackupRestoreCmd() *cobra.Command {
	var input, actor, reason, idempotencyKey, confirmation string
	command := &cobra.Command{
		Use:   "restore",
		Short: "Restore a validated backup into an empty scanner release domain",
		RunE: func(command *cobra.Command, _ []string) error {
			backup, absolute, err := readScannerReleaseBackup(input)
			if err != nil {
				return err
			}
			store, err := openStore()
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()
			result, err := store.ScannerReleases().RestoreReleaseBackup(
				command.Context(), backup,
				scannerrelease.BackupCommand{
					Actor: actor, Reason: reason, IdempotencyKey: idempotencyKey,
					MaintenanceConfirmation: confirmation,
				},
			)
			if err != nil {
				return err
			}
			return writeScannerReleaseJSON(command, map[string]any{
				"status":            result.State,
				"input":             absolute,
				"operation_id":      result.OperationID,
				"payload_sha256":    result.PayloadDigest,
				"table_counts":      result.TableCounts,
				"restored_at":       result.RestoredAt,
				"idempotent_replay": result.Idempotent,
			})
		},
	}
	command.Flags().StringVar(&input, "input", "", "backup file to restore")
	command.Flags().StringVar(
		&confirmation, "confirm", "",
		"exact destructive confirmation: "+scannerrelease.RestoreConfirmation,
	)
	addScannerReleaseBackupAuditFlags(command, &actor, &reason, &idempotencyKey)
	return command
}

func newScannerReleaseBackupStatusCmd() *cobra.Command {
	var limit int
	command := &cobra.Command{
		Use:   "status",
		Short: "Show restore maintenance and backup audit evidence",
		RunE: func(command *cobra.Command, _ []string) error {
			store, err := openStore()
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()
			maintenance, err := store.ScannerReleases().GetReleaseMaintenanceStatus(command.Context())
			if err != nil {
				return err
			}
			operations, err := store.ScannerReleases().ListBackupOperations(command.Context(), limit)
			if err != nil {
				return err
			}
			return writeScannerReleaseJSON(command, map[string]any{
				"maintenance":    maintenance,
				"restore_active": maintenance.RestoreActive(time.Now()),
				"operations":     operations,
			})
		},
	}
	command.Flags().IntVar(&limit, "limit", 50, "maximum audit operations to return (1-200)")
	return command
}

func addScannerReleaseBackupAuditFlags(
	command *cobra.Command,
	actor, reason, idempotencyKey *string,
) {
	command.Flags().StringVar(actor, "actor", "", "operator or automation identity")
	command.Flags().StringVar(reason, "reason", "", "change-ticket or recovery reason")
	command.Flags().StringVar(idempotencyKey, "idempotency-key", "", "unique operation key")
}

func readScannerReleaseBackup(path string) (*scannerrelease.ReleaseBackup, string, error) {
	if strings.TrimSpace(path) == "" {
		return nil, "", errors.New("--input is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, "", err
	}
	file, err := os.Open(absolute)
	if err != nil {
		return nil, "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, "", err
	}
	if !info.Mode().IsRegular() {
		return nil, "", errors.New("scanner release backup must be a regular file")
	}
	if info.Size() <= 0 || info.Size() > maxScannerReleaseBackupBytes {
		return nil, "", fmt.Errorf(
			"scanner release backup size must be between 1 and %d bytes",
			maxScannerReleaseBackupBytes,
		)
	}
	decoder := json.NewDecoder(io.LimitReader(file, maxScannerReleaseBackupBytes+1))
	decoder.DisallowUnknownFields()
	var backup scannerrelease.ReleaseBackup
	if err := decoder.Decode(&backup); err != nil {
		return nil, "", fmt.Errorf("decode scanner release backup: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON documents")
		}
		return nil, "", fmt.Errorf("decode scanner release backup: %w", err)
	}
	return &backup, absolute, nil
}

func writeScannerReleaseBackup(
	path string,
	backup *scannerrelease.ReleaseBackup,
) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	directory := filepath.Dir(absolute)
	temp, err := os.CreateTemp(directory, ".scanner-release-backup-*.tmp")
	if err != nil {
		return "", err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return "", err
	}
	bounded := &scannerReleaseBackupWriter{
		writer: temp, remaining: maxScannerReleaseBackupBytes,
	}
	encoder := json.NewEncoder(bounded)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(backup); err != nil {
		_ = temp.Close()
		return "", err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return "", err
	}
	if err := temp.Close(); err != nil {
		return "", err
	}
	// A hard link publishes the fully synced inode atomically and fails if the
	// requested destination already exists; operators never overwrite the
	// only known-good backup by accident.
	if err := os.Link(tempName, absolute); err != nil {
		return "", fmt.Errorf("publish backup without overwrite: %w", err)
	}
	if directoryHandle, openErr := os.Open(directory); openErr == nil {
		_ = directoryHandle.Sync()
		_ = directoryHandle.Close()
	}
	return absolute, nil
}

type scannerReleaseBackupWriter struct {
	writer    io.Writer
	remaining int64
}

func (writer *scannerReleaseBackupWriter) Write(payload []byte) (int, error) {
	if int64(len(payload)) > writer.remaining {
		allowed := writer.remaining
		if allowed > 0 {
			written, err := writer.writer.Write(payload[:allowed])
			writer.remaining -= int64(written)
			if err != nil {
				return written, err
			}
		}
		return int(allowed), fmt.Errorf(
			"scanner release backup exceeds maximum size %d",
			maxScannerReleaseBackupBytes,
		)
	}
	written, err := writer.writer.Write(payload)
	writer.remaining -= int64(written)
	return written, err
}

func scannerReleaseBackupCounts(backup *scannerrelease.ReleaseBackup) map[string]int {
	counts := make(map[string]int, len(backup.Tables))
	for _, table := range backup.Tables {
		counts[table.Name] = table.RowCount
	}
	return counts
}

func writeScannerReleaseJSON(command *cobra.Command, value any) error {
	encoder := json.NewEncoder(command.OutOrStdout())
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
