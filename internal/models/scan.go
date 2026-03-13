package models

import "time"

// Scan represents a single scan operation.
type Scan struct {
	ID              string     `json:"id" db:"id"`
	UserID          string     `json:"user_id" db:"user_id"`
	RepoID          string     `json:"repo_id" db:"repo_id"`
	CollectionID    *string    `json:"collection_id,omitempty" db:"collection_id"`
	LoopID          *string    `json:"loop_id,omitempty" db:"loop_id"`
	Iteration       *int       `json:"iteration,omitempty" db:"iteration"`
	Branch          string     `json:"branch" db:"branch"`
	Status          ScanStatus `json:"status" db:"status"`
	ToolsSelected   string     `json:"tools_selected" db:"tools_selected"`
	ToolsCompleted  string     `json:"tools_completed" db:"tools_completed"`
	ToolsFailed     string     `json:"tools_failed" db:"tools_failed"`
	FindingCount    int        `json:"finding_count" db:"finding_count"`
	CoverageSummary string     `json:"coverage_summary" db:"coverage_summary"`
	AIEnabled       bool       `json:"ai_enabled" db:"ai_enabled"`
	AISummary       string     `json:"ai_summary" db:"ai_summary"`
	StartedAt       *time.Time `json:"started_at,omitempty" db:"started_at"`
	CompletedAt     *time.Time `json:"completed_at,omitempty" db:"completed_at"`
	CreatedAt       time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at" db:"updated_at"`

	// Populated by API handlers, not stored in DB.
	Repo *Repo `json:"repo,omitempty" db:"-"`
}
