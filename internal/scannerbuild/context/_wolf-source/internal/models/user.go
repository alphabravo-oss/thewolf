package models

import "time"

// Role values. Admins manage system settings, users, nodes, and scanner
// images, and can modify any resource. Regular users can only modify the
// resources they created and cannot reach the settings/admin surface.
const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

// User represents an authenticated user.
type User struct {
	ID           string `json:"id" db:"id"`
	Email        string `json:"email" db:"email"`
	PasswordHash string `json:"-" db:"password_hash"`
	Role         string `json:"role" db:"role"`
	// DisplayName is an optional human-friendly name shown in the UI in place
	// of the email. Empty falls back to the email.
	DisplayName string `json:"display_name" db:"display_name"`
	// ScannerSupplyChainPersonas is a JSON array of server-defined human
	// persona IDs. It is intentionally not serialized directly; API handlers
	// expose validated persona and effective-scope arrays instead.
	ScannerSupplyChainPersonas string `json:"-" db:"scanner_supply_chain_personas"`
	// TOTPSecret is the user's base32 TOTP secret, encrypted at rest with the
	// master key. Empty until enrollment. Never serialized to clients.
	TOTPSecret string `json:"-" db:"totp_secret"`
	// TOTPEnabled is true once the user has confirmed a code and activated MFA.
	TOTPEnabled bool `json:"totp_enabled" db:"totp_enabled"`
	// TOTPRecoveryCodes is a JSON array of SHA-256 hashes of unused one-time
	// recovery codes. Consumed (removed) as they are used. Never serialized.
	TOTPRecoveryCodes string    `json:"-" db:"totp_recovery_codes"`
	CreatedAt         time.Time `json:"created_at" db:"created_at"`
	UpdatedAt         time.Time `json:"updated_at" db:"updated_at"`
}

// IsAdmin reports whether the user has the admin role.
func (u *User) IsAdmin() bool { return u != nil && u.Role == RoleAdmin }

// MFAEnabled reports whether the user has an active TOTP second factor.
func (u *User) MFAEnabled() bool { return u != nil && u.TOTPEnabled }
