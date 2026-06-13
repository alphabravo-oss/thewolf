package db

import (
	"context"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func (s *SQLiteStore) CreateFindingSuppression(ctx context.Context, suppression *models.FindingSuppression) error {
	now := time.Now().UTC()
	suppression.CreatedAt = now
	suppression.UpdatedAt = now
	if suppression.Status == "" {
		suppression.Status = models.SuppressionStatusActive
	}
	_, err := s.db.NamedExecContext(ctx,
		`INSERT INTO finding_suppressions (id, repo_id, created_by, scope_type, scope_value, branch, reason, expires_at, status, created_at, updated_at)
		 VALUES (:id, :repo_id, :created_by, :scope_type, :scope_value, :branch, :reason, :expires_at, :status, :created_at, :updated_at)`,
		suppression)
	return err
}

func (s *SQLiteStore) GetFindingSuppressionByID(ctx context.Context, id string) (*models.FindingSuppression, error) {
	var suppression models.FindingSuppression
	err := s.db.GetContext(ctx, &suppression, "SELECT * FROM finding_suppressions WHERE id = ?", id)
	if err != nil {
		return nil, err
	}
	return &suppression, nil
}

func (s *SQLiteStore) ListFindingSuppressions(ctx context.Context, repoID string, includeInactive bool) ([]models.FindingSuppression, error) {
	var suppressions []models.FindingSuppression
	var err error
	if includeInactive {
		err = s.db.SelectContext(ctx, &suppressions,
			"SELECT * FROM finding_suppressions WHERE repo_id = ? ORDER BY created_at DESC", repoID)
	} else {
		err = s.db.SelectContext(ctx, &suppressions,
			"SELECT * FROM finding_suppressions WHERE repo_id = ? AND status = ? ORDER BY created_at DESC",
			repoID, models.SuppressionStatusActive)
	}
	return suppressions, err
}

func (s *SQLiteStore) RevokeFindingSuppression(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE finding_suppressions SET status = ?, updated_at = ? WHERE id = ?",
		models.SuppressionStatusRevoked, time.Now().UTC(), id)
	return err
}

func (s *SQLiteStore) CreateFindingSuppressionAudit(ctx context.Context, entry *models.FindingSuppressionAudit) error {
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.NamedExecContext(ctx,
		`INSERT INTO finding_suppression_audit (id, suppression_id, action, actor_id, details_json, created_at)
		 VALUES (:id, :suppression_id, :action, :actor_id, :details_json, :created_at)`,
		entry)
	return err
}
