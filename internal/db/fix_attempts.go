package db

import (
	"context"
	"errors"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/jmoiron/sqlx"
)

func (s *SQLiteStore) UpdateFixAttempt(ctx context.Context, attempt *models.FixAttempt) error {
	return finishFixAttempt(ctx, s.db, attempt)
}

func (s *PostgresStore) UpdateFixAttempt(ctx context.Context, attempt *models.FixAttempt) error {
	return finishFixAttempt(ctx, s.db, attempt)
}

// Only an unfinished attempt may acquire a verdict. Preserve its identity,
// start time and sequence number; a stale worker cannot rewrite a final result.
func finishFixAttempt(ctx context.Context, database *sqlx.DB, attempt *models.FixAttempt) error {
	if attempt == nil || attempt.Outcome == "" || attempt.Outcome == models.FixOutcomeRunning {
		return errors.New("fix attempt requires a final outcome")
	}
	result, err := database.NamedExecContext(ctx, `UPDATE fix_attempts SET
		model=:model, built=CAST(CAST(:built AS BOOLEAN) AS INTEGER), finding_cleared=CAST(CAST(:finding_cleared AS BOOLEAN) AS INTEGER),
		new_findings=:new_findings, outcome=:outcome, files_changed=:files_changed,
		diff_excerpt=:diff_excerpt, duration_ms=:duration_ms, cost_usd=:cost_usd,
		input_tokens=:input_tokens, output_tokens=:output_tokens
		WHERE id=:id AND job_id=:job_id AND finding_id=:finding_id AND attempt_no=:attempt_no AND outcome='running'`, attempt)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return errors.New("fix attempt changed or is already final")
	}
	return nil
}
