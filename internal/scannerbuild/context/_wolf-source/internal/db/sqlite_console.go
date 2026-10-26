package db

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func (s *SQLiteStore) EnqueueFixerConsole(ctx context.Context, c *models.FixerConsole) error {
	now := time.Now().UTC()
	if c.CreatedAt.IsZero() {
		c.CreatedAt = now
	}
	c.UpdatedAt = now
	if c.Status == "" {
		c.Status = models.FixerConsoleQueued
	}
	_, err := s.db.NamedExecContext(ctx,
		`INSERT INTO fixer_consoles
		 (id, user_id, kind, engine, status, claimed_by, last_url, error,
		  claimed_at, started_at, finished_at, heartbeat_at, created_at, updated_at)
		 VALUES
		 (:id, :user_id, :kind, :engine, :status, :claimed_by, :last_url, :error,
		  :claimed_at, :started_at, :finished_at, :heartbeat_at, :created_at, :updated_at)`, c)
	return err
}

func (s *SQLiteStore) GetFixerConsoleByID(ctx context.Context, id string) (*models.FixerConsole, error) {
	var c models.FixerConsole
	if err := s.db.GetContext(ctx, &c, "SELECT * FROM fixer_consoles WHERE id = ?", id); err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *SQLiteStore) ClaimNextFixerConsole(ctx context.Context, workerID string) (*models.FixerConsole, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var id string
	if err := tx.QueryRowxContext(ctx,
		`SELECT id FROM fixer_consoles WHERE status = ? ORDER BY created_at LIMIT 1`,
		models.FixerConsoleQueued).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx,
		`UPDATE fixer_consoles SET status = ?, claimed_by = ?, claimed_at = ?, heartbeat_at = ?, updated_at = ?
		 WHERE id = ? AND status = ?`,
		models.FixerConsoleClaimed, workerID, now, now, now, id, models.FixerConsoleQueued); err != nil {
		return nil, err
	}
	var claimed models.FixerConsole
	if err := tx.GetContext(ctx, &claimed, `SELECT * FROM fixer_consoles WHERE id=?`, id); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &claimed, nil
}

func (s *SQLiteStore) UpdateFixerConsole(ctx context.Context, c *models.FixerConsole) error {
	return updateFixerConsole(ctx, s.db, c)
}

func (s *SQLiteStore) HeartbeatFixerConsole(ctx context.Context, id, worker string, claimedAt time.Time) error {
	return heartbeatFixerConsole(ctx, s.db, id, worker, claimedAt)
}

func (s *SQLiteStore) ReclaimStaleConsoles(ctx context.Context, cutoff time.Time) (int, error) {
	return reclaimStaleConsoles(ctx, s.db, cutoff)
}

func (s *SQLiteStore) AppendFixerConsoleStdin(ctx context.Context, consoleID, data string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO fixer_console_stdin (id, console_id, data, created_at) VALUES (?, ?, ?, ?)`,
		uuid.NewString(), consoleID, data, time.Now().UTC())
	return err
}

func (s *SQLiteStore) ListActiveFixerConsoles(ctx context.Context) ([]models.FixerConsole, error) {
	var out []models.FixerConsole
	err := s.db.SelectContext(ctx, &out,
		`SELECT * FROM fixer_consoles WHERE status IN (?, ?) ORDER BY created_at ASC`,
		models.FixerConsoleClaimed, models.FixerConsoleRunning)
	return out, err
}

func (s *SQLiteStore) DrainFixerConsoleStdin(ctx context.Context, cons *models.FixerConsole) ([]string, error) {
	return drainFixerConsoleStdin(ctx, s.db, cons)
}
