package models

import "time"

// AuthSession is a browser login session. The plaintext cookie value is
// never persisted; only SessionHash is stored.
type AuthSession struct {
	ID            string     `json:"id" db:"id"`
	UserID        string     `json:"user_id" db:"user_id"`
	SessionHash   string     `json:"-" db:"session_hash"`
	SessionPrefix string     `json:"session_prefix" db:"session_prefix"`
	CreatedAt     time.Time  `json:"created_at" db:"created_at"`
	LastUsedAt    *time.Time `json:"last_used_at,omitempty" db:"last_used_at"`
	ExpiresAt     time.Time  `json:"expires_at" db:"expires_at"`
	RevokedAt     *time.Time `json:"revoked_at,omitempty" db:"revoked_at"`
}
