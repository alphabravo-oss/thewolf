package db

import (
	"context"
	"time"

	"github.com/alphabravocompany/thewolf/internal/auth/apikey"
	"github.com/alphabravocompany/thewolf/internal/models"
)

// --- API Tokens ---

func (s *SQLiteStore) CreateAPIToken(ctx context.Context, t *models.APIToken) error {
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now().UTC()
	}
	t.Scopes = apikey.ScopeSet(t.ScopeList).Encode()
	_, err := s.db.NamedExecContext(ctx,
		`INSERT INTO api_tokens (id, user_id, name, token_hash, token_prefix, scopes, created_at, last_used_at, expires_at, revoked_at)
		 VALUES (:id, :user_id, :name, :token_hash, :token_prefix, :scopes, :created_at, :last_used_at, :expires_at, :revoked_at)`, t)
	return err
}

func (s *SQLiteStore) GetAPITokenByHash(ctx context.Context, hash string) (*models.APIToken, error) {
	var t models.APIToken
	if err := s.db.GetContext(ctx, &t, "SELECT * FROM api_tokens WHERE token_hash = ?", hash); err != nil {
		return nil, err
	}
	t.ScopeList = apikey.DecodeScopes(t.Scopes)
	return &t, nil
}

func (s *SQLiteStore) GetAPITokenByID(ctx context.Context, id string) (*models.APIToken, error) {
	var t models.APIToken
	if err := s.db.GetContext(ctx, &t, "SELECT * FROM api_tokens WHERE id = ?", id); err != nil {
		return nil, err
	}
	t.ScopeList = apikey.DecodeScopes(t.Scopes)
	return &t, nil
}

func (s *SQLiteStore) ListAPITokensByUser(ctx context.Context, userID string) ([]models.APIToken, error) {
	var ts []models.APIToken
	err := s.db.SelectContext(ctx, &ts, "SELECT * FROM api_tokens WHERE user_id = ? ORDER BY created_at DESC", userID)
	for i := range ts {
		ts[i].ScopeList = apikey.DecodeScopes(ts[i].Scopes)
	}
	return ts, err
}

// ListAllAPITokens returns every user's tokens (admin oversight). Tokens are
// hash-only — no plaintext is ever stored or returned, just metadata.
func (s *SQLiteStore) ListAllAPITokens(ctx context.Context) ([]models.APIToken, error) {
	var ts []models.APIToken
	err := s.db.SelectContext(ctx, &ts, "SELECT * FROM api_tokens ORDER BY created_at DESC")
	for i := range ts {
		ts[i].ScopeList = apikey.DecodeScopes(ts[i].Scopes)
	}
	return ts, err
}

func (s *SQLiteStore) RevokeAPIToken(ctx context.Context, id string) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, "UPDATE api_tokens SET revoked_at = ? WHERE id = ?", now, id)
	return err
}

func (s *SQLiteStore) RevokeAPITokensByUser(ctx context.Context, userID string, exceptTokenID string) error {
	now := time.Now().UTC()
	if exceptTokenID != "" {
		_, err := s.db.ExecContext(ctx,
			"UPDATE api_tokens SET revoked_at = ? WHERE user_id = ? AND id <> ? AND revoked_at IS NULL",
			now, userID, exceptTokenID)
		return err
	}
	_, err := s.db.ExecContext(ctx,
		"UPDATE api_tokens SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL",
		now, userID)
	return err
}

func (s *SQLiteStore) TouchAPIToken(ctx context.Context, id string) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, "UPDATE api_tokens SET last_used_at = ? WHERE id = ?", now, id)
	return err
}

// --- Audit Log ---

func (s *SQLiteStore) AppendAuditLog(ctx context.Context, e *models.AuditLogEntry) error {
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.NamedExecContext(ctx,
		`INSERT INTO audit_log (id, token_id, user_id, action, method, path, resource_id, status_code, event_type, category, severity, ip, user_agent, created_at)
		 VALUES (:id, :token_id, :user_id, :action, :method, :path, :resource_id, :status_code, :event_type, :category, :severity, :ip, :user_agent, :created_at)`, e)
	return err
}

func (s *SQLiteStore) ListAuditLog(ctx context.Context, limit int) ([]models.AuditLogEntry, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	var es []models.AuditLogEntry
	err := s.db.SelectContext(ctx, &es, "SELECT * FROM audit_log ORDER BY created_at DESC LIMIT ?", limit)
	return es, err
}
