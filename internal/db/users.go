package db

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/jmoiron/sqlx"
)

// ErrUserChanged requires a fresh read before changing a user's credentials.
var ErrUserChanged = errors.New("user credentials changed")

// updateUser prevents an old role/profile snapshot from restoring an earlier
// password, and invalidates password-step challenges when the password changes.
func updateUser(ctx context.Context, database *sqlx.DB, user *models.User) error {
	user.UpdatedAt = time.Now().UTC()
	if user.Role == "" {
		user.Role = models.RoleUser
	}
	query, args, err := sqlx.Named(`UPDATE users SET email=:email,
 credential_version=credential_version+CASE WHEN password_hash<>:password_hash THEN 1 ELSE 0 END,
 password_hash=:password_hash, role=:role, scanner_supply_chain_personas=:scanner_supply_chain_personas, updated_at=:updated_at
 WHERE id=:id AND credential_version=:credential_version RETURNING credential_version`, user)
	if err != nil {
		return err
	}
	var version int64
	if err := database.GetContext(ctx, &version, database.Rebind(query), args...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrUserChanged
		}
		return err
	}
	user.CredentialVersion = version
	return nil
}
