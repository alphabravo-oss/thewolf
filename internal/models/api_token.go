package models

import "time"

// APIToken is a non-interactive credential for CLI / CI / AI-agent access.
// The plaintext secret is never persisted — only TokenHash (SHA-256) and
// TokenPrefix (first 8 chars, for display) are stored.
type APIToken struct {
	ID          string     `json:"id" db:"id"`
	UserID      string     `json:"user_id" db:"user_id"`
	Name        string     `json:"name" db:"name"`
	TokenHash   string     `json:"-" db:"token_hash"`
	TokenPrefix string     `json:"token_prefix" db:"token_prefix"`
	Scopes      string     `json:"-" db:"scopes"`       // JSON-encoded []string as stored
	ScopeList   []string   `json:"scopes" db:"-"`       // decoded form for API responses
	CreatedAt   time.Time  `json:"created_at" db:"created_at"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty" db:"last_used_at"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty" db:"expires_at"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty" db:"revoked_at"`
}

// AuditLogEntry records a single mutating API request. TokenID is nil when
// the request was authenticated with a JWT (the UI) rather than an API token.
type AuditLogEntry struct {
	ID         string    `json:"id" db:"id"`
	TokenID    *string   `json:"token_id,omitempty" db:"token_id"`
	UserID     string    `json:"user_id" db:"user_id"`
	Action     string    `json:"action" db:"action"`
	Method     string    `json:"method" db:"method"`
	Path       string    `json:"path" db:"path"`
	ResourceID string    `json:"resource_id,omitempty" db:"resource_id"`
	StatusCode int       `json:"status_code" db:"status_code"`
	CreatedAt  time.Time `json:"created_at" db:"created_at"`
}
