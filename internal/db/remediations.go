package db

import (
	"context"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/jmoiron/sqlx"
)

func updateRemediation(ctx context.Context, database *sqlx.DB, rem *models.Remediation) error {
	rem.UpdatedAt = time.Now().UTC()
	result, err := database.NamedExecContext(ctx,
		`UPDATE remediations SET
		   repo_id=:repo_id, branch=:branch, workspace_path=:workspace_path, state=:state,
		   published_sha=:published_sha, published_at=:published_at, updated_at=:updated_at
		 WHERE id=:id AND (state='open' OR
		 (state=:state AND workspace_path=:workspace_path AND branch=:branch AND repo_id=:repo_id))`, rem)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrFixFrozen
	}
	return nil
}
