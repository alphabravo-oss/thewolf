package db

import (
	"context"
	"database/sql"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/jmoiron/sqlx"
)

func saveTestCoverage(ctx context.Context, db *sqlx.DB, report *models.TestCoverage) error {
	result, err := db.NamedExecContext(ctx, `INSERT INTO scan_test_coverage
	(scan_id, commit_sha, report_json, imported_by, imported_at)
	SELECT id, :commit_sha, :report_json, :imported_by, :imported_at FROM scans
	WHERE id = :scan_id AND commit_sha = :commit_sha AND dirty_state IN ('', 'clean')
	ON CONFLICT (scan_id) DO UPDATE SET commit_sha = excluded.commit_sha,
	report_json = excluded.report_json, imported_by = excluded.imported_by, imported_at = excluded.imported_at`, report)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return sql.ErrNoRows
	}
	return err
}

func getTestCoverage(ctx context.Context, db *sqlx.DB, id string) (*models.TestCoverage, error) {
	var report models.TestCoverage
	err := db.GetContext(ctx, &report, db.Rebind("SELECT * FROM scan_test_coverage WHERE scan_id = ?"), id)
	return &report, err
}

func (s *SQLiteStore) SaveTestCoverage(ctx context.Context, r *models.TestCoverage) error {
	return saveTestCoverage(ctx, s.db, r)
}
func (s *PostgresStore) SaveTestCoverage(ctx context.Context, r *models.TestCoverage) error {
	return saveTestCoverage(ctx, s.db, r)
}
func (s *SQLiteStore) GetTestCoverage(ctx context.Context, id string) (*models.TestCoverage, error) {
	return getTestCoverage(ctx, s.db, id)
}
func (s *PostgresStore) GetTestCoverage(ctx context.Context, id string) (*models.TestCoverage, error) {
	return getTestCoverage(ctx, s.db, id)
}
