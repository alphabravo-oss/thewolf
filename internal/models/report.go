package models

import "time"

// ToolSummary holds a per-tool AI assessment summary for a scan.
type ToolSummary struct {
	ID             string    `json:"id" db:"id"`
	ScanID         string    `json:"scan_id" db:"scan_id"`
	ToolName       string    `json:"tool_name" db:"tool_name"`
	SummaryText    string    `json:"summary_text" db:"summary_text"`
	FindingCount   int       `json:"finding_count" db:"finding_count"`
	SeverityCounts string    `json:"severity_counts" db:"severity_counts"`
	CriticalIssues string    `json:"critical_issues" db:"critical_issues"`
	CreatedAt      time.Time `json:"created_at" db:"created_at"`
}

// ScanRecommendation holds a structured scan-level recommendation.
type ScanRecommendation struct {
	ID             string    `json:"id" db:"id"`
	ScanID         string    `json:"scan_id" db:"scan_id"`
	Priority       int       `json:"priority" db:"priority"`
	Category       string    `json:"category" db:"category"`
	Title          string    `json:"title" db:"title"`
	Description    string    `json:"description" db:"description"`
	AffectedTools  string    `json:"affected_tools" db:"affected_tools"`
	EffortEstimate string    `json:"effort_estimate" db:"effort_estimate"`
	CreatedAt      time.Time `json:"created_at" db:"created_at"`
}
