package db

import (
	"context"
	"database/sql"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func (s *SQLiteStore) ListEnterpriseRecords(ctx context.Context, kind string) ([]models.EnterpriseRecord, error) {
	var rows []models.EnterpriseRecord
	err := s.db.SelectContext(ctx, &rows,
		`SELECT kind, id, name, body, created_at, updated_at FROM enterprise_records WHERE kind = ? ORDER BY name, id`, kind)
	return rows, err
}

func (s *SQLiteStore) GetEnterpriseRecord(ctx context.Context, kind, id string) (*models.EnterpriseRecord, error) {
	var rec models.EnterpriseRecord
	err := s.db.GetContext(ctx, &rec,
		`SELECT kind, id, name, body, created_at, updated_at FROM enterprise_records WHERE kind = ? AND id = ?`, kind, id)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

func (s *SQLiteStore) PutEnterpriseRecord(ctx context.Context, rec *models.EnterpriseRecord) error {
	now := time.Now().UTC()
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = now
	}
	rec.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `
INSERT INTO enterprise_records (kind, id, name, body, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(kind, id) DO UPDATE SET name = excluded.name, body = excluded.body, updated_at = excluded.updated_at`,
		rec.Kind, rec.ID, rec.Name, rec.Body, rec.CreatedAt, rec.UpdatedAt)
	return err
}

func (s *SQLiteStore) DeleteEnterpriseRecord(ctx context.Context, kind, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM enterprise_records WHERE kind = ? AND id = ?`, kind, id)
	return err
}

func (s *PostgresStore) ListEnterpriseRecords(ctx context.Context, kind string) ([]models.EnterpriseRecord, error) {
	var rows []models.EnterpriseRecord
	err := s.db.SelectContext(ctx, &rows,
		`SELECT kind, id, name, body, created_at, updated_at FROM enterprise_records WHERE kind = $1 ORDER BY name, id`, kind)
	return rows, err
}

func (s *PostgresStore) GetEnterpriseRecord(ctx context.Context, kind, id string) (*models.EnterpriseRecord, error) {
	var rec models.EnterpriseRecord
	err := s.db.GetContext(ctx, &rec,
		`SELECT kind, id, name, body, created_at, updated_at FROM enterprise_records WHERE kind = $1 AND id = $2`, kind, id)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

func (s *PostgresStore) PutEnterpriseRecord(ctx context.Context, rec *models.EnterpriseRecord) error {
	now := time.Now().UTC()
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = now
	}
	rec.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `
INSERT INTO enterprise_records (kind, id, name, body, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT(kind, id) DO UPDATE SET name = excluded.name, body = excluded.body, updated_at = excluded.updated_at`,
		rec.Kind, rec.ID, rec.Name, rec.Body, rec.CreatedAt, rec.UpdatedAt)
	return err
}

func (s *PostgresStore) DeleteEnterpriseRecord(ctx context.Context, kind, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM enterprise_records WHERE kind = $1 AND id = $2`, kind, id)
	return err
}
