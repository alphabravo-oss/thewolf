package models

import "time"

// FixJob status values.
const (
	FixJobQueued    = "queued"
	FixJobClaimed   = "claimed"
	FixJobRunning   = "running"
	FixJobSucceeded = "succeeded"
	FixJobFailed    = "failed"
	FixJobCancelled = "cancelled"
)

// FixJob mode values. v1 only produces a branch + diff for review.
const (
	FixModeDryRun = "dry_run"
)

// FixAttempt outcome values.
const (
	FixOutcomeKept       = "kept"
	FixOutcomeRolledBack = "rolled_back"
	FixOutcomeUnfixable  = "unfixable"
)

// FixJob is one queued unit of autonomous remediation work. The wolf server
// enqueues it; a `wolf fixer` worker claims and runs it. Everything is gated
// by the autofix_enabled setting (default off).
type FixJob struct {
	ID             string     `json:"id" db:"id"`
	UserID         string     `json:"user_id" db:"user_id"`
	Type           string     `json:"type" db:"type"` // fix | loop
	RepoID         string     `json:"repo_id" db:"repo_id"`
	ScanID         string     `json:"scan_id,omitempty" db:"scan_id"`
	FindingIDs     string     `json:"-" db:"finding_ids"` // JSON array as stored
	FindingIDList  []string   `json:"finding_ids" db:"-"` // decoded for API responses
	TargetBranch   string     `json:"target_branch" db:"target_branch"`
	Engine         string     `json:"engine" db:"engine"` // auto | claude-code | codex | api | custom
	Mode           string     `json:"mode" db:"mode"`     // dry_run
	SeverityFloor  string     `json:"severity_floor" db:"severity_floor"`
	MaxAttempts    int        `json:"max_attempts" db:"max_attempts"`
	Status         string     `json:"status" db:"status"`
	ClaimedBy      string     `json:"claimed_by,omitempty" db:"claimed_by"`
	ResultBranch   string     `json:"result_branch,omitempty" db:"result_branch"`
	DiffArtifactID string     `json:"diff_artifact_id,omitempty" db:"diff_artifact_id"`
	Summary        string     `json:"summary" db:"summary"` // JSON
	Error          string     `json:"error,omitempty" db:"error"`
	ClaimedAt      *time.Time `json:"claimed_at,omitempty" db:"claimed_at"`
	StartedAt      *time.Time `json:"started_at,omitempty" db:"started_at"`
	FinishedAt     *time.Time `json:"finished_at,omitempty" db:"finished_at"`
	HeartbeatAt    *time.Time `json:"heartbeat_at,omitempty" db:"heartbeat_at"`
	CreatedAt      time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at" db:"updated_at"`
}

// FixAttempt records one engine attempt at one finding and how the verification
// gate judged it. The audit trail behind every fix.
type FixAttempt struct {
	ID             string    `json:"id" db:"id"`
	JobID          string    `json:"job_id" db:"job_id"`
	FindingID      string    `json:"finding_id" db:"finding_id"`
	AttemptNo      int       `json:"attempt_no" db:"attempt_no"`
	EngineUsed     string    `json:"engine_used" db:"engine_used"` // cli:claude-code | cli:codex | api | custom
	Model          string    `json:"model,omitempty" db:"model"`
	Built          bool      `json:"built" db:"built"`
	FindingCleared bool      `json:"finding_cleared" db:"finding_cleared"`
	NewFindings    int       `json:"new_findings" db:"new_findings"`
	Outcome        string    `json:"outcome" db:"outcome"` // kept | rolled_back | unfixable
	FilesChanged   string    `json:"files_changed" db:"files_changed"`
	DiffExcerpt    string    `json:"diff_excerpt,omitempty" db:"diff_excerpt"`
	DurationMS     int       `json:"duration_ms" db:"duration_ms"`
	CostUSD        float64   `json:"cost_usd" db:"cost_usd"`
	CreatedAt      time.Time `json:"created_at" db:"created_at"`
}
