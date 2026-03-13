package models

import "time"

// Fix represents a fix operation for scan findings.
type Fix struct {
	ID                string    `json:"id" db:"id"`
	UserID            string    `json:"user_id" db:"user_id"`
	ScanID            string    `json:"scan_id" db:"scan_id"`
	LoopID            *string   `json:"loop_id,omitempty" db:"loop_id"`
	Status            FixStatus `json:"status" db:"status"`
	Mode              FixMode   `json:"mode" db:"mode"`
	Engine            string    `json:"engine" db:"engine"`
	SeverityFilter    string    `json:"severity_filter" db:"severity_filter"`
	BranchName        string    `json:"branch_name" db:"branch_name"`
	WorktreePath      string    `json:"worktree_path" db:"worktree_path"`
	FindingsAttempted int       `json:"findings_attempted" db:"findings_attempted"`
	FindingsFixed     int       `json:"findings_fixed" db:"findings_fixed"`
	FindingsFailed    int       `json:"findings_failed" db:"findings_failed"`
	PRURLs            string    `json:"pr_urls" db:"pr_urls"`
	StartedAt         *time.Time `json:"started_at,omitempty" db:"started_at"`
	CompletedAt       *time.Time `json:"completed_at,omitempty" db:"completed_at"`
	CreatedAt         time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at" db:"updated_at"`
}

// FixItem represents a single finding fix attempt.
type FixItem struct {
	ID               string           `json:"id" db:"id"`
	FixID            string           `json:"fix_id" db:"fix_id"`
	FindingID        string           `json:"finding_id" db:"finding_id"`
	Status           FixItemStatus    `json:"status" db:"status"`
	FilesChanged     string           `json:"files_changed" db:"files_changed"`
	Diff             string           `json:"diff" db:"diff"`
	ValidationResult ValidationResult `json:"validation_result" db:"validation_result"`
	ValidationOutput string           `json:"validation_output" db:"validation_output"`
	ErrorMessage     string           `json:"error_message,omitempty" db:"error_message"`
	CreatedAt        time.Time        `json:"created_at" db:"created_at"`
	UpdatedAt        time.Time        `json:"updated_at" db:"updated_at"`
}
