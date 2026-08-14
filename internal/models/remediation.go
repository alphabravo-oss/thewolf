package models

import "time"

// Remediation states. open = agents may attach; frozen = published/handed off;
// discarded = workspace and wolf-fix branch were deleted.
const (
	RemediationOpen      = "open"
	RemediationFrozen    = "frozen"
	RemediationDiscarded = "discarded"
)

// Remediation is the one-checkout session hung off an origin scan. Every
// agent run and child scan for that origin shares Branch + WorkspacePath
// until publish or discard.
type Remediation struct {
	ID             string     `json:"id" db:"id"`
	UserID         string     `json:"user_id" db:"user_id"`
	RepoID         string     `json:"repo_id" db:"repo_id"`
	OriginScanID   string     `json:"origin_scan_id" db:"origin_scan_id"`
	Branch         string     `json:"branch" db:"branch"`
	WorkspacePath  string     `json:"workspace_path,omitempty" db:"workspace_path"`
	State          string     `json:"state" db:"state"`
	PublishedSHA   string     `json:"published_sha,omitempty" db:"published_sha"`
	PublishedAt    *time.Time `json:"published_at,omitempty" db:"published_at"`
	CreatedAt      time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at" db:"updated_at"`
}

// RemediationBusy reports whether a job status means another agent is
// already using this remediation.
func RemediationBusy(status string) bool {
	switch status {
	case FixJobQueued, FixJobClaimed, FixJobRunning:
		return true
	default:
		return false
	}
}
