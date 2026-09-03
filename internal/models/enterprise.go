package models

import "time"

// EnterpriseRecord is a named Enterprise resource (workspace, role, mapping, …).
type EnterpriseRecord struct {
	Kind      string    `json:"kind" db:"kind"`
	ID        string    `json:"id" db:"id"`
	Name      string    `json:"name" db:"name"`
	Body      string    `json:"body" db:"body"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}
