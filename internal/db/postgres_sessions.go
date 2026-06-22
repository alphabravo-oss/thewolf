package db

import (
	"context"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func (s *PostgresStore) CreateAuthSession(ctx context.Context, session *models.AuthSession) error {
	if session.CreatedAt.IsZero() {
		session.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.NamedExecContext(ctx,
		`INSERT INTO auth_sessions (id, user_id, session_hash, session_prefix, created_at, last_used_at, expires_at, revoked_at)
		 VALUES (:id, :user_id, :session_hash, :session_prefix, :created_at, :last_used_at, :expires_at, :revoked_at)`, session)
	return err
}

func (s *PostgresStore) GetAuthSessionByHash(ctx context.Context, hash string) (*models.AuthSession, error) {
	var session models.AuthSession
	if err := s.db.GetContext(ctx, &session, "SELECT * FROM auth_sessions WHERE session_hash = $1", hash); err != nil {
		return nil, err
	}
	return &session, nil
}

func (s *PostgresStore) RevokeAuthSessionByHash(ctx context.Context, hash string) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, "UPDATE auth_sessions SET revoked_at = $1 WHERE session_hash = $2", now, hash)
	return err
}

func (s *PostgresStore) RevokeAuthSessionsByUser(ctx context.Context, userID string, exceptSessionID string) error {
	now := time.Now().UTC()
	if exceptSessionID != "" {
		_, err := s.db.ExecContext(ctx,
			"UPDATE auth_sessions SET revoked_at = $1 WHERE user_id = $2 AND id <> $3 AND revoked_at IS NULL",
			now, userID, exceptSessionID)
		return err
	}
	_, err := s.db.ExecContext(ctx,
		"UPDATE auth_sessions SET revoked_at = $1 WHERE user_id = $2 AND revoked_at IS NULL",
		now, userID)
	return err
}

func (s *PostgresStore) TouchAuthSession(ctx context.Context, id string) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, "UPDATE auth_sessions SET last_used_at = $1 WHERE id = $2", now, id)
	return err
}
