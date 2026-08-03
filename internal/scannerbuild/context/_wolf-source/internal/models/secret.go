package models

import "time"

// Secret represents an encrypted secret stored in the database.
type Secret struct {
	ID             string    `json:"id" db:"id"`
	UserID         string    `json:"user_id" db:"user_id"`
	KeyType        KeyType   `json:"key_type" db:"key_type"`
	KeyName        string    `json:"key_name" db:"key_name"`
	EncryptedValue string    `json:"-" db:"encrypted_value"`
	AllowedHosts   string    `json:"allowed_hosts,omitempty" db:"allowed_hosts"`
	MetadataJSON   string    `json:"metadata,omitempty" db:"metadata_json"`
	CreatedAt      time.Time `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time `json:"updated_at" db:"updated_at"`
}
