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
