package db

import (
	"context"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func (s *SQLiteStore) CreateAuthSession(ctx context.Context, session *models.AuthSession) error {
	if session.CreatedAt.IsZero() {
		session.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.NamedExecContext(ctx,
		`INSERT INTO auth_sessions (id, user_id, session_hash, session_prefix, created_at, last_used_at, expires_at, revoked_at)
		 VALUES (:id, :user_id, :session_hash, :session_prefix, :created_at, :last_used_at, :expires_at, :revoked_at)`, session)
	return err
}

func (s *SQLiteStore) GetAuthSessionByHash(ctx context.Context, hash string) (*models.AuthSession, error) {
	var session models.AuthSession
	if err := s.db.GetContext(ctx, &session, "SELECT * FROM auth_sessions WHERE session_hash = ?", hash); err != nil {
		return nil, err
	}
	return &session, nil
}

func (s *SQLiteStore) RevokeAuthSessionByHash(ctx context.Context, hash string) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, "UPDATE auth_sessions SET revoked_at = ? WHERE session_hash = ?", now, hash)
	return err
}

func (s *SQLiteStore) RevokeAuthSessionsByUser(ctx context.Context, userID string, exceptSessionID string) error {
	now := time.Now().UTC()
	if exceptSessionID != "" {
		_, err := s.db.ExecContext(ctx,
			"UPDATE auth_sessions SET revoked_at = ? WHERE user_id = ? AND id <> ? AND revoked_at IS NULL",
			now, userID, exceptSessionID)
		return err
	}
	_, err := s.db.ExecContext(ctx,
		"UPDATE auth_sessions SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL",
		now, userID)
	return err
}

func (s *SQLiteStore) TouchAuthSession(ctx context.Context, id string) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, "UPDATE auth_sessions SET last_used_at = ? WHERE id = ?", now, id)
	return err
}

// RotateAuthSession consumes one refresh credential and persists its successor
// in a single transaction, so concurrent requests cannot both succeed.
func (s *SQLiteStore) RotateAuthSession(ctx context.Context, hash string, next *models.AuthSession) (bool, error) {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	now := time.Now().UTC()
	result, err := tx.ExecContext(ctx, "UPDATE auth_sessions SET revoked_at = ? WHERE session_hash = ? AND revoked_at IS NULL AND expires_at > ?", now, hash, now)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		return false, err
	}
	_, err = tx.NamedExecContext(ctx, `INSERT INTO auth_sessions (id, user_id, session_hash, session_prefix, created_at, expires_at)
 VALUES (:id, :user_id, :session_hash, :session_prefix, :created_at, :expires_at)`, next)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
