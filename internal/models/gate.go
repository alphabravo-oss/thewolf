package models

import "time"

type QualityPolicy struct {
	ID        string    `json:"id" db:"id"`
	Name      string    `json:"name" db:"name"`
	Scope     string    `json:"scope" db:"scope"`
	ScopeID   string    `json:"scope_id" db:"scope_id"`
	Mode      string    `json:"mode" db:"mode"`
	RulesJSON string    `json:"rules_json" db:"rules_json"`
	Enabled   bool      `json:"enabled" db:"enabled"`
	CreatedBy string    `json:"created_by" db:"created_by"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

type QualityGateResult struct {
	ID               string    `json:"id" db:"id"`
	ScanID           string    `json:"scan_id" db:"scan_id"`
	PolicyID         string    `json:"policy_id" db:"policy_id"`
	Status           string    `json:"status" db:"status"`
	SummaryJSON      string    `json:"summary_json" db:"summary_json"`
	MatchedRulesJSON string    `json:"matched_rules_json" db:"matched_rules_json"`
	EvaluatedAt      time.Time `json:"evaluated_at" db:"evaluated_at"`
	CreatedAt        time.Time `json:"created_at" db:"created_at"`
	UpdatedAt        time.Time `json:"updated_at" db:"updated_at"`
}
