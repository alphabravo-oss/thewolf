package db

import (
	"context"
	"errors"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/jmoiron/sqlx"
)

// migrateMFAReplay preserves enrollments while denying the entire currently
// valid window for legacy accounts whose previously used steps are unknown.
func migrateMFAReplay(database *sqlx.DB) error {
	if err := execAdditiveMigration(database, migration068SQL); err != nil {
		return err
	}
	_, err := database.Exec(database.Rebind(`UPDATE users SET totp_last_step = ? WHERE totp_enabled = TRUE AND totp_last_step = -1`), time.Now().Unix()/30+1)
	return err
}

// ErrMFAChanged means another operation changed MFA after the caller read it.
// Callers must not issue credentials or publish an uncommitted enrollment.
var ErrMFAChanged = errors.New("MFA state changed")

func updateUserMFA(ctx context.Context, database sqlx.ExtContext, user, previous *models.User) error {
	if user == nil || previous == nil || user.ID != previous.ID {
		return ErrMFAChanged
	}
	credentialChange := int64(0)
	if user.TOTPSecret != previous.TOTPSecret || user.TOTPEnabled != previous.TOTPEnabled {
		credentialChange = 1
	}
	if credentialChange == 0 && user.TOTPLastStep < previous.TOTPLastStep {
		return ErrMFAChanged
	}
	user.UpdatedAt = time.Now().UTC()
	result, err := sqlx.NamedExecContext(ctx, database, `UPDATE users SET
 totp_secret=:secret, totp_enabled=:enabled, totp_recovery_codes=:codes, totp_last_step=:last_step,
 credential_version=credential_version+:credential_change, mfa_revision=mfa_revision+1, updated_at=:updated_at
 WHERE id=:id AND totp_secret=:previous_secret AND totp_enabled=:previous_enabled
 AND totp_recovery_codes=:previous_codes AND totp_last_step=:previous_step
 AND credential_version=:previous_version AND mfa_revision=:previous_revision`, map[string]any{
		"id": user.ID, "secret": user.TOTPSecret, "enabled": user.TOTPEnabled, "codes": user.TOTPRecoveryCodes, "last_step": user.TOTPLastStep, "updated_at": user.UpdatedAt,
		"credential_change": credentialChange, "previous_version": previous.CredentialVersion, "previous_revision": previous.MFARevision,
		"previous_secret": previous.TOTPSecret, "previous_enabled": previous.TOTPEnabled, "previous_codes": previous.TOTPRecoveryCodes, "previous_step": previous.TOTPLastStep,
	})
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrMFAChanged
	}
	user.CredentialVersion = previous.CredentialVersion + credentialChange
	user.MFARevision = previous.MFARevision + 1
	return nil
}

// ErrMFAChallenge means the challenge is absent, expired, revoked or already used.
var ErrMFAChallenge = errors.New("invalid MFA challenge")

// CompleteMFALogin commits the verified factor, consumes the challenge and saves
// both credentials together. A failed transaction consumes neither factor nor
// challenge, so an otherwise valid exchange can be retried.
func (s *SQLiteStore) CompleteMFALogin(ctx context.Context, hash string, user, previous *models.User, refresh, browser *models.AuthSession) error {
	return completeMFALogin(ctx, s.db, hash, user, previous, refresh, browser)
}
func (s *PostgresStore) CompleteMFALogin(ctx context.Context, hash string, user, previous *models.User, refresh, browser *models.AuthSession) error {
	return completeMFALogin(ctx, s.db, hash, user, previous, refresh, browser)
}
func completeMFALogin(ctx context.Context, database *sqlx.DB, hash string, user, previous *models.User, refresh, browser *models.AuthSession) error {
	if user == nil || previous == nil || !previous.MFAEnabled() || refresh == nil || browser == nil || refresh.UserID != user.ID || browser.UserID != user.ID {
		return ErrMFAChanged
	}
	tx, err := database.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := updateUserMFA(ctx, tx, user, previous); err != nil {
		return err
	}
	now := time.Now().UTC()
	result, err := tx.ExecContext(ctx, tx.Rebind(`UPDATE auth_sessions SET revoked_at = ? WHERE session_hash = ? AND user_id = ? AND session_prefix = 'mfa' AND revoked_at IS NULL AND expires_at > ?`), now, hash, user.ID, now)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrMFAChallenge
	}
	for _, session := range []*models.AuthSession{refresh, browser} {
		if _, err := tx.NamedExecContext(ctx, `INSERT INTO auth_sessions (id, user_id, session_hash, session_prefix, created_at, expires_at) VALUES (:id, :user_id, :session_hash, :session_prefix, :created_at, :expires_at)`, session); err != nil {
			return err
		}
	}
	return tx.Commit()
}
