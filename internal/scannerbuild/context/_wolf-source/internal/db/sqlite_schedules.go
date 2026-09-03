package db

import (
	"context"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func (s *SQLiteStore) CreateScanSchedule(ctx context.Context, schedule *models.ScanSchedule) error {
	now := time.Now().UTC()
	schedule.CreatedAt = now
	schedule.UpdatedAt = now
	_, err := s.db.NamedExecContext(ctx,
		`INSERT INTO scan_schedules (id, user_id, repo_id, collection_id, interval_minutes, branch, profile,
		 quiet_start, quiet_end, enabled, last_run_at, last_sha, created_at, updated_at)
		 VALUES (:id, :user_id, :repo_id, :collection_id, :interval_minutes, :branch, :profile,
		 :quiet_start, :quiet_end, :enabled, :last_run_at, :last_sha, :created_at, :updated_at)`,
		schedule)
	return err
}

func (s *SQLiteStore) GetScanScheduleByID(ctx context.Context, id string) (*models.ScanSchedule, error) {
	var schedule models.ScanSchedule
	err := s.db.GetContext(ctx, &schedule, "SELECT * FROM scan_schedules WHERE id = ?", id)
	if err != nil {
		return nil, err
	}
	return &schedule, nil
}

func (s *SQLiteStore) ListScanSchedulesByUser(ctx context.Context, userID string) ([]models.ScanSchedule, error) {
	var rows []models.ScanSchedule
	err := s.db.SelectContext(ctx, &rows,
		"SELECT * FROM scan_schedules WHERE user_id = ? ORDER BY created_at DESC", userID)
	return rows, err
}

func (s *SQLiteStore) ListEnabledScanSchedules(ctx context.Context) ([]models.ScanSchedule, error) {
	var rows []models.ScanSchedule
	err := s.db.SelectContext(ctx, &rows, "SELECT * FROM scan_schedules WHERE enabled = 1")
	return rows, err
}

func (s *SQLiteStore) UpdateScanSchedule(ctx context.Context, schedule *models.ScanSchedule) error {
	schedule.UpdatedAt = time.Now().UTC()
	_, err := s.db.NamedExecContext(ctx,
		`UPDATE scan_schedules SET repo_id=:repo_id, collection_id=:collection_id, interval_minutes=:interval_minutes,
		 branch=:branch, profile=:profile, quiet_start=:quiet_start, quiet_end=:quiet_end, enabled=:enabled,
		 last_run_at=:last_run_at, last_sha=:last_sha, updated_at=:updated_at WHERE id=:id`,
		schedule)
	return err
}

func (s *SQLiteStore) DeleteScanSchedule(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM scan_schedules WHERE id = ?", id)
	return err
}
