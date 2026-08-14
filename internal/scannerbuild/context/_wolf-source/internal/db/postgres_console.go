package db

import (
	"context"
	"database/sql"
	"strconv"
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
	c.UpdatedAt = time.Now().UTC()
	_, err := s.db.NamedExecContext(ctx,
		`UPDATE fixer_consoles SET
		   kind=:kind, engine=:engine, status=:status, claimed_by=:claimed_by,
		   last_url=:last_url, error=:error, claimed_at=:claimed_at, started_at=:started_at,
		   finished_at=:finished_at, heartbeat_at=:heartbeat_at, updated_at=:updated_at
		 WHERE id=:id`, c)
	return err
}

func (s *PostgresStore) ReclaimStaleConsoles(ctx context.Context, cutoff time.Time) (int, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE fixer_consoles SET status = $1, claimed_by = '', updated_at = $2
		 WHERE status IN ($3, $4) AND (heartbeat_at IS NULL OR heartbeat_at < $5)`,
		models.FixerConsoleQueued, time.Now().UTC(),
		models.FixerConsoleClaimed, models.FixerConsoleRunning, cutoff)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
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

func (s *PostgresStore) DrainFixerConsoleStdin(ctx context.Context, consoleID string) ([]string, error) {
	var rows []struct {
		ID   string `db:"id"`
		Data string `db:"data"`
	}
	if err := s.db.SelectContext(ctx, &rows,
		`SELECT id, data FROM fixer_console_stdin WHERE console_id = $1 ORDER BY created_at ASC`, consoleID); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	ids := make([]any, 0, len(rows))
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
		out = append(out, r.Data)
	}
	q := "DELETE FROM fixer_console_stdin WHERE id IN ("
	for i := range ids {
		if i > 0 {
			q += ","
		}
		q += "$" + strconv.Itoa(i+1)
	}
	q += ")"
	if _, err := s.db.ExecContext(ctx, q, ids...); err != nil {
		return nil, err
	}
	return out, nil
}
