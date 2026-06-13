package models

import "time"

type SuppressionStatus string

const (
	SuppressionStatusActive  SuppressionStatus = "active"
	SuppressionStatusRevoked SuppressionStatus = "revoked"
	SuppressionStatusExpired SuppressionStatus = "expired"
)

type SuppressionScopeType string

const (
	SuppressionScopeFingerprint       SuppressionScopeType = "fingerprint"
	SuppressionScopeStableFingerprint SuppressionScopeType = "stable_fingerprint"
	SuppressionScopeRule              SuppressionScopeType = "rule"
	SuppressionScopeFineCategory      SuppressionScopeType = "fine_category"
	SuppressionScopePathGlob          SuppressionScopeType = "path_glob"
)

type FindingSuppression struct {
	ID         string               `json:"id" db:"id"`
	RepoID     string               `json:"repo_id" db:"repo_id"`
	CreatedBy  string               `json:"created_by" db:"created_by"`
	ScopeType  SuppressionScopeType `json:"scope_type" db:"scope_type"`
	ScopeValue string               `json:"scope_value" db:"scope_value"`
	Branch     string               `json:"branch,omitempty" db:"branch"`
	Reason     string               `json:"reason" db:"reason"`
	ExpiresAt  *time.Time           `json:"expires_at,omitempty" db:"expires_at"`
	Status     SuppressionStatus    `json:"status" db:"status"`
	CreatedAt  time.Time            `json:"created_at" db:"created_at"`
	UpdatedAt  time.Time            `json:"updated_at" db:"updated_at"`
}

type FindingSuppressionAudit struct {
	ID            string    `json:"id" db:"id"`
	SuppressionID string    `json:"suppression_id" db:"suppression_id"`
	Action        string    `json:"action" db:"action"`
	ActorID       string    `json:"actor_id" db:"actor_id"`
	DetailsJSON   string    `json:"details_json" db:"details_json"`
	CreatedAt     time.Time `json:"created_at" db:"created_at"`
}
