package db

import (
	"context"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func (s *SQLiteStore) CreateFindingSuppression(ctx context.Context, suppression *models.FindingSuppression) error {
	return createFindingSuppression(ctx, s.db, suppression)
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
			"SELECT * FROM finding_suppressions WHERE repo_id = ? ORDER BY created_at DESC, id", repoID)
	} else {
		err = s.db.SelectContext(ctx, &suppressions,
			"SELECT * FROM finding_suppressions WHERE repo_id = ? AND status = ? ORDER BY created_at DESC, id",
			repoID, models.SuppressionStatusActive)
	}
	return suppressions, err
}

func (s *SQLiteStore) RevokeFindingSuppression(ctx context.Context, id, actorID string) error {
	return revokeFindingSuppression(ctx, s.db, id, actorID)
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

func (s *SQLiteStore) ExpireFindingSuppressions(ctx context.Context, now time.Time) (int, error) {
	return expireFindingSuppressions(ctx, s.db, now)
}
