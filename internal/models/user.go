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
	ID           string    `json:"id" db:"id"`
	Email        string    `json:"email" db:"email"`
	PasswordHash string    `json:"-" db:"password_hash"`
	Role         string    `json:"role" db:"role"`
	CreatedAt    time.Time `json:"created_at" db:"created_at"`
	UpdatedAt    time.Time `json:"updated_at" db:"updated_at"`
}

// IsAdmin reports whether the user has the admin role.
func (u *User) IsAdmin() bool { return u != nil && u.Role == RoleAdmin }
