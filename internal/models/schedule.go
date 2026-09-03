package models

import "time"

// ScanSchedule is a recurring scan for a repo or a collection.
type ScanSchedule struct {
	ID               string     `json:"id" db:"id"`
	UserID           string     `json:"user_id" db:"user_id"`
	RepoID           string     `json:"repo_id,omitempty" db:"repo_id"`
	CollectionID     string     `json:"collection_id,omitempty" db:"collection_id"`
	IntervalMinutes  int        `json:"interval_minutes" db:"interval_minutes"`
	Branch           string     `json:"branch,omitempty" db:"branch"`
	Profile          string     `json:"profile,omitempty" db:"profile"`
	QuietStart       string     `json:"quiet_start,omitempty" db:"quiet_start"`
	QuietEnd         string     `json:"quiet_end,omitempty" db:"quiet_end"`
	Enabled          bool       `json:"enabled" db:"enabled"`
	LastRunAt        *time.Time `json:"last_run_at,omitempty" db:"last_run_at"`
	LastSHA          string     `json:"last_sha,omitempty" db:"last_sha"`
	CreatedAt        time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at" db:"updated_at"`
}
