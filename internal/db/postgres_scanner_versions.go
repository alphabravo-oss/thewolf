package db

import (
	"context"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func (s *PostgresStore) UpsertScannerVersionCheck(ctx context.Context, check *models.ScannerVersionCheck) error {
	_, err := s.db.NamedExecContext(ctx, `
		INSERT INTO scanner_version_checks (
			tool_name, pinned_version, latest_version, latest_reference, status,
			checked_at, error, source_type, source_url
		) VALUES (
			:tool_name, :pinned_version, :latest_version, :latest_reference, :status,
			:checked_at, :error, :source_type, :source_url
		)
		ON CONFLICT(tool_name) DO UPDATE SET
			pinned_version = excluded.pinned_version,
			latest_version = excluded.latest_version,
			latest_reference = excluded.latest_reference,
			status = excluded.status,
			checked_at = excluded.checked_at,
			error = excluded.error,
			source_type = excluded.source_type,
			source_url = excluded.source_url
	`, check)
	return err
}

func (s *PostgresStore) GetScannerVersionCheck(ctx context.Context, toolName string) (*models.ScannerVersionCheck, error) {
	var check models.ScannerVersionCheck
	if err := s.db.GetContext(ctx, &check, `SELECT * FROM scanner_version_checks WHERE tool_name = $1`, toolName); err != nil {
		return nil, err
	}
	return &check, nil
}

func (s *PostgresStore) ListScannerVersionChecks(ctx context.Context) ([]models.ScannerVersionCheck, error) {
	var checks []models.ScannerVersionCheck
	err := s.db.SelectContext(ctx, &checks, `SELECT * FROM scanner_version_checks ORDER BY tool_name ASC`)
	return checks, err
}
