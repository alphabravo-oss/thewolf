package db

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func (s *PostgresStore) EnqueueFixerConsole(ctx context.Context, c *models.FixerConsole) error {
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

func (s *PostgresStore) GetFixerConsoleByID(ctx context.Context, id string) (*models.FixerConsole, error) {
	var c models.FixerConsole
	if err := s.db.GetContext(ctx, &c, "SELECT * FROM fixer_consoles WHERE id = $1", id); err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *PostgresStore) ClaimNextFixerConsole(ctx context.Context, workerID string) (*models.FixerConsole, error) {
	var c models.FixerConsole
	now := time.Now().UTC()
	err := s.db.GetContext(ctx, &c,
		`UPDATE fixer_consoles SET status = $1, claimed_by = $2, claimed_at = $3, heartbeat_at = $3, updated_at = $3
		 WHERE id = (
		   SELECT id FROM fixer_consoles WHERE status = $4 ORDER BY created_at LIMIT 1 FOR UPDATE SKIP LOCKED
		 )
		 RETURNING *`,
		models.FixerConsoleClaimed, workerID, now, models.FixerConsoleQueued)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *PostgresStore) UpdateFixerConsole(ctx context.Context, c *models.FixerConsole) error {
	return updateFixerConsole(ctx, s.db, c)
}

func (s *PostgresStore) HeartbeatFixerConsole(ctx context.Context, id, worker string, claimedAt time.Time) error {
	return heartbeatFixerConsole(ctx, s.db, id, worker, claimedAt)
}

func (s *PostgresStore) ReclaimStaleConsoles(ctx context.Context, cutoff time.Time) (int, error) {
	return reclaimStaleConsoles(ctx, s.db, cutoff)
}

func (s *PostgresStore) AppendFixerConsoleStdin(ctx context.Context, consoleID, data string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO fixer_console_stdin (id, console_id, data, created_at) VALUES ($1, $2, $3, $4)`,
		uuid.NewString(), consoleID, data, time.Now().UTC())
	return err
}

func (s *PostgresStore) ListActiveFixerConsoles(ctx context.Context) ([]models.FixerConsole, error) {
	var out []models.FixerConsole
	err := s.db.SelectContext(ctx, &out,
		`SELECT * FROM fixer_consoles WHERE status IN ($1, $2) ORDER BY created_at ASC`,
		models.FixerConsoleClaimed, models.FixerConsoleRunning)
	return out, err
}

func (s *PostgresStore) DrainFixerConsoleStdin(ctx context.Context, cons *models.FixerConsole) ([]string, error) {
	return drainFixerConsoleStdin(ctx, s.db, cons)
}
