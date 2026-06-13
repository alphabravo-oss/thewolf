package models

import "time"

type ScanBaseline struct {
	ID        string    `json:"id" db:"id"`
	RepoID    string    `json:"repo_id" db:"repo_id"`
	Branch    string    `json:"branch" db:"branch"`
	Name      string    `json:"name" db:"name"`
	ScanID    string    `json:"scan_id" db:"scan_id"`
	Strategy  string    `json:"strategy" db:"strategy"`
	CreatedBy string    `json:"created_by" db:"created_by"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

type ScanComparison struct {
	ID             string    `json:"id" db:"id"`
	RepoID         string    `json:"repo_id" db:"repo_id"`
	BaselineScanID string    `json:"baseline_scan_id" db:"baseline_scan_id"`
	CurrentScanID  string    `json:"current_scan_id" db:"current_scan_id"`
	SummaryJSON    string    `json:"summary_json" db:"summary_json"`
	CreatedAt      time.Time `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time `json:"updated_at" db:"updated_at"`
}
