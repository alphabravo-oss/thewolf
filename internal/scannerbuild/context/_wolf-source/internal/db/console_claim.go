package db

import (
	"context"
	"errors"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/alphabravocompany/thewolf/internal/models"
)

var ErrFixerConsoleConflict = errors.New("console claim or status changed")

func updateFixerConsole(ctx context.Context, database *sqlx.DB, c *models.FixerConsole) error {
	c.UpdatedAt = time.Now().UTC()
	result, err := database.NamedExecContext(ctx, `UPDATE fixer_consoles SET
		status=:status, last_url=:last_url, error=:error, started_at=:started_at,
		finished_at=:finished_at, updated_at=:updated_at
		WHERE id=:id AND claimed_by=:claimed_by AND claimed_at IS NOT DISTINCT FROM :claimed_at
		AND (:status='cancelled' OR claimed_at IS NOT NULL)
		AND status IN ('queued', 'claimed', 'running')`, c)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil {
		return err
	} else if n != 1 {
		return ErrFixerConsoleConflict
	}
	return nil
}

func heartbeatFixerConsole(ctx context.Context, database *sqlx.DB, id, worker string, claimedAt time.Time) error {
	result, err := database.ExecContext(ctx, database.Rebind(`UPDATE fixer_consoles SET heartbeat_at=?
		WHERE id=? AND claimed_by=? AND claimed_at=? AND status IN ('claimed', 'running')`),
		time.Now().UTC(), id, worker, claimedAt)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil {
		return err
	} else if n != 1 {
		return ErrFixerConsoleConflict
	}
	return nil
}

func reclaimStaleConsoles(ctx context.Context, database *sqlx.DB, cutoff time.Time) (int, error) {
	// A login/shell cannot resume its lost terminal state. Replaying its queued
	// keystrokes in a new process can execute different commands or credentials.
	now := time.Now().UTC()
	result, err := database.ExecContext(ctx, database.Rebind(`UPDATE fixer_consoles
		SET status='exited', finished_at=?, updated_at=?, error='Worker heartbeat expired; start a new console.'
		WHERE status IN ('claimed', 'running') AND (heartbeat_at IS NULL OR heartbeat_at < ?)`), now, now, cutoff)
	if err != nil {
		return 0, err
	}
	n, err := result.RowsAffected()
	return int(n), err
}

func drainFixerConsoleStdin(ctx context.Context, database *sqlx.DB, c *models.FixerConsole) ([]string, error) {
	tx, err := database.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	query := `SELECT * FROM fixer_consoles WHERE id=?`
	if database.DriverName() == "postgres" {
		query += " FOR UPDATE"
	}
	var current models.FixerConsole
	if err := tx.GetContext(ctx, &current, database.Rebind(query), c.ID); err != nil {
		return nil, err
	}
	if c.ClaimedAt == nil || current.ClaimedAt == nil || !c.ClaimedAt.Equal(*current.ClaimedAt) ||
		c.ClaimedBy != current.ClaimedBy ||
		(current.Status != models.FixerConsoleClaimed && current.Status != models.FixerConsoleRunning) {
		return nil, ErrFixerConsoleConflict
	}
	var rows []struct {
		ID   string `db:"id"`
		Data string `db:"data"`
	}
	if err := tx.SelectContext(ctx, &rows, database.Rebind(`SELECT id, data FROM fixer_console_stdin WHERE console_id=? ORDER BY created_at, id`), c.ID); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, tx.Commit()
	}
	ids, out := make([]string, len(rows)), make([]string, len(rows))
	for i, row := range rows {
		ids[i], out[i] = row.ID, row.Data
	}
	query, args, err := sqlx.In(`DELETE FROM fixer_console_stdin WHERE id IN (?)`, ids)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, database.Rebind(query), args...); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}
