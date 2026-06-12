package models

import "time"

// RemoteNode is a Linux host wolf can reach over SSH to inspect and scan repos
// before they are pushed back to a hosted git provider.
type RemoteNode struct {
	ID                 string     `json:"id" db:"id"`
	UserID             string     `json:"user_id" db:"user_id"`
	Name               string     `json:"name" db:"name"`
	Host               string     `json:"host" db:"host"`
	Port               int        `json:"port" db:"port"`
	Username           string     `json:"username" db:"username"`
	AuthType           string     `json:"auth_type" db:"auth_type"`
	CredentialSecretID *string    `json:"credential_secret_id,omitempty" db:"credential_secret_id"`
	KnownHosts         string     `json:"known_hosts,omitempty" db:"known_hosts"`
	BasePath           string     `json:"base_path,omitempty" db:"base_path"`
	Enabled            bool       `json:"enabled" db:"enabled"`
	LastCheckStatus    string     `json:"last_check_status,omitempty" db:"last_check_status"`
	LastCheckError     string     `json:"last_check_error,omitempty" db:"last_check_error"`
	LastCheckedAt      *time.Time `json:"last_checked_at,omitempty" db:"last_checked_at"`
	CreatedAt          time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at" db:"updated_at"`
}
