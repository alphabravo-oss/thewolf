package models

import "time"

// Loop represents a scan→fix→re-scan cycle.
type Loop struct {
	ID                     string         `json:"id" db:"id"`
	UserID                 string         `json:"user_id" db:"user_id"`
	RepoID                 string         `json:"repo_id" db:"repo_id"`
	CollectionID           *string        `json:"collection_id,omitempty" db:"collection_id"`
	Status                 LoopStatus     `json:"status" db:"status"`
	MaxIterations          int            `json:"max_iterations" db:"max_iterations"`
	CurrentIteration       int            `json:"current_iteration" db:"current_iteration"`
	SeverityFilter         string         `json:"severity_filter" db:"severity_filter"`
	RescanStrategy         RescanStrategy `json:"rescan_strategy" db:"rescan_strategy"`
	TotalFindingsInitial   int            `json:"total_findings_initial" db:"total_findings_initial"`
	TotalFindingsFixed     int            `json:"total_findings_fixed" db:"total_findings_fixed"`
	TotalFindingsNew       int            `json:"total_findings_new" db:"total_findings_new"`
	TotalFindingsRemaining int            `json:"total_findings_remaining" db:"total_findings_remaining"`
	GuardrailWarnings      string         `json:"guardrail_warnings" db:"guardrail_warnings"`
	StartedAt              *time.Time     `json:"started_at,omitempty" db:"started_at"`
	CompletedAt            *time.Time     `json:"completed_at,omitempty" db:"completed_at"`
	CreatedAt              time.Time      `json:"created_at" db:"created_at"`
	UpdatedAt              time.Time      `json:"updated_at" db:"updated_at"`
}
