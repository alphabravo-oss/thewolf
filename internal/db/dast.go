package db

import (
	"context"
	"database/sql"
	"errors"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/jmoiron/sqlx"
)

var ErrDASTRevision = errors.New("DAST settings changed; reload before saving or approving")

func (s *PostgresStore) GetDASTTarget(ctx context.Context, repoID string) (*models.DASTTarget, error) {
	return getDASTTarget(ctx, s.db, repoID)
}
func getDASTTarget(ctx context.Context, database *sqlx.DB, repoID string) (*models.DASTTarget, error) {
	t := &models.DASTTarget{RepoID: repoID, Mode: "baseline", APIFormat: "openapi"}
	err := database.GetContext(ctx, t, database.Rebind("SELECT * FROM repo_dast_targets WHERE repo_id=?"), repoID)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return t, err
}

func (s *PostgresStore) SaveDASTTarget(ctx context.Context, t *models.DASTTarget, approve bool) error {
	return saveDASTTarget(ctx, s.db, t, approve)
}
func saveDASTTarget(ctx context.Context, database *sqlx.DB, t *models.DASTTarget, approve bool) error {
	var result sql.Result
	var err error
	if approve {
		result, err = database.ExecContext(ctx, database.Rebind(`UPDATE repo_dast_targets SET approved_by=?, allow_private=?, active_approved=?, revision=revision+1 WHERE repo_id=? AND revision=?`), t.ApprovedBy, t.AllowPrivate, t.ActiveApproved, t.RepoID, t.Revision)
	} else {
		// Every edit revokes approval, including spec/credential changes. Generic
		// repository updates cannot restore approval from a stale Repo snapshot.
		t.ApprovedBy, t.AllowPrivate, t.ActiveApproved = "", false, false
		if t.Revision == 0 {
			result, err = database.ExecContext(ctx, database.Rebind(`INSERT INTO repo_dast_targets (repo_id,url,mode,api_spec_url,api_format,auth_secret_id) VALUES (?,?,?,?,?,?) ON CONFLICT(repo_id) DO NOTHING`), t.RepoID, t.URL, t.Mode, t.APISpecURL, t.APIFormat, t.AuthSecretID)
		} else {
			result, err = database.ExecContext(ctx, database.Rebind(`UPDATE repo_dast_targets SET url=?, mode=?, api_spec_url=?, api_format=?, auth_secret_id=?, approved_by='', allow_private=FALSE, active_approved=FALSE, revision=revision+1 WHERE repo_id=? AND revision=?`), t.URL, t.Mode, t.APISpecURL, t.APIFormat, t.AuthSecretID, t.RepoID, t.Revision)
		}
	}
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrDASTRevision
	}
	t.Revision++
	return nil
}
