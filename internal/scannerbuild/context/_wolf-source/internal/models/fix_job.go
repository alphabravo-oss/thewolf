package models

import "time"

// FixJob status values.
const (
	FixJobQueued         = "queued"
	FixJobClaimed        = "claimed"
	FixJobRunning        = "running"
	FixJobAwaitingReview = "awaiting_review"
	FixJobAwaitingPush   = "awaiting_push"
	// PushFailed means the agent kept fixes on the branch, but GitHub
	// rejected the publish. Distinct from Failed (the agent itself blew up).
	FixJobPushFailed = "push_failed"
	FixJobSucceeded  = "succeeded"
	FixJobFailed     = "failed"
	FixJobCancelled  = "cancelled"
	FixJobSuperseded = "superseded"
)

// FixJob mode values.
const (
	// FixModeDryRun commits on an isolated fix branch and stops before push.
	// GitHub jobs with kept fixes land in awaiting_push so a human can push.
	FixModeDryRun = "dry_run"
	// FixModePush commits on the fix branch and pushes that branch (never the
	// default branch) when the loops finish.
	FixModePush = "push"
)

// Resume actions a paused job can take when re-queued.
const (
	FixResumeContinue = "continue"
	FixResumePush     = "push"
)

// FixAttempt outcome values.
const (
	FixOutcomeKept       = "kept"
	FixOutcomeRolledBack = "rolled_back"
	FixOutcomeUnfixable  = "unfixable"
	FixOutcomeMuted      = "muted"
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
	RemediationID  string     `json:"remediation_id,omitempty" db:"remediation_id"`
	FindingIDs     string     `json:"-" db:"finding_ids"` // JSON array as stored
	FindingIDList  []string   `json:"finding_ids" db:"-"` // decoded for API responses
	TargetBranch   string     `json:"target_branch" db:"target_branch"`
	Engine         string     `json:"engine" db:"engine"` // auto | claude-code | codex | opencode | api | custom
	Mode           string     `json:"mode" db:"mode"`     // dry_run | push
	SeverityFloor  string     `json:"severity_floor" db:"severity_floor"`
	MaxAttempts    int        `json:"max_attempts" db:"max_attempts"`
	MaxLoops       int        `json:"max_loops" db:"max_loops"`
	CurrentLoop    int        `json:"current_loop" db:"current_loop"`
	PlannedRuns    int        `json:"planned_runs" db:"planned_runs"`
	RunIndex       int        `json:"run_index" db:"run_index"`
	HumanInTheLoop bool       `json:"human_in_the_loop" db:"human_in_the_loop"`
	WorkspacePath  string     `json:"workspace_path,omitempty" db:"workspace_path"`
	BaseBranch     string     `json:"base_branch,omitempty" db:"base_branch"`
	Pushed         bool       `json:"pushed" db:"pushed"`
	PushSHA        string     `json:"push_sha,omitempty" db:"push_sha"`
	PauseReason    string     `json:"pause_reason,omitempty" db:"pause_reason"`
	ResumeAction   string     `json:"resume_action,omitempty" db:"resume_action"`
	Model          string     `json:"model,omitempty" db:"model"`
	Effort         string     `json:"effort,omitempty" db:"effort"`
	Variant        string     `json:"variant,omitempty" db:"variant"`
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
	// QueuedBehind is computed at API time for queued jobs; not stored.
	QueuedBehind *QueuedBehind `json:"queued_behind,omitempty" db:"-"`
}

// QueuedBehind identifies the job or fixer console holding the single worker.
type QueuedBehind struct {
	ID        string     `json:"id"`
	Kind      string     `json:"kind"` // job | console
	RepoID    string     `json:"repo_id,omitempty"`
	StartedAt *time.Time `json:"started_at,omitempty"`
}

// FixJobPaused reports whether the job is waiting on a human (review or push).
func FixJobPaused(status string) bool {
	return status == FixJobAwaitingReview || status == FixJobAwaitingPush || status == FixJobPushFailed
}

// FixJobTerminal reports whether a fix job has reached a final state.
func FixJobTerminal(status string) bool {
	switch status {
	case FixJobSucceeded, FixJobFailed, FixJobCancelled, FixJobSuperseded:
		return true
	default:
		return false
	}
}

// FixAttempt records one engine attempt at one finding and how the verification
// gate judged it. The audit trail behind every fix.
type FixAttempt struct {
	ID             string    `json:"id" db:"id"`
	JobID          string    `json:"job_id" db:"job_id"`
	FindingID      string    `json:"finding_id" db:"finding_id"`
	AttemptNo      int       `json:"attempt_no" db:"attempt_no"`
	EngineUsed     string    `json:"engine_used" db:"engine_used"` // cli:claude-code | cli:codex | cli:opencode | api | custom
	Model          string    `json:"model,omitempty" db:"model"`
	Built          bool      `json:"built" db:"built"`
	FindingCleared bool      `json:"finding_cleared" db:"finding_cleared"`
	NewFindings    int       `json:"new_findings" db:"new_findings"`
	Outcome        string    `json:"outcome" db:"outcome"` // kept | rolled_back | unfixable
	FilesChanged   string    `json:"files_changed" db:"files_changed"`
	DiffExcerpt    string    `json:"diff_excerpt,omitempty" db:"diff_excerpt"`
	DurationMS     int       `json:"duration_ms" db:"duration_ms"`
	CostUSD        float64   `json:"cost_usd" db:"cost_usd"`
	InputTokens    int64     `json:"input_tokens" db:"input_tokens"`
	OutputTokens   int64     `json:"output_tokens" db:"output_tokens"`
	CreatedAt      time.Time `json:"created_at" db:"created_at"`
	// ToolName / Title / FilePath / LineStart / Severity are the scanner
	// finding fields, filled by GET /fixes/{id}. Not stored on the attempt row.
	ToolName  string `json:"tool_name,omitempty" db:"-"`
	Title     string `json:"title,omitempty" db:"-"`
	FilePath  string `json:"file_path,omitempty" db:"-"`
	LineStart int    `json:"line_start,omitempty" db:"-"`
	Severity  string `json:"severity,omitempty" db:"-"`
	RuleID    string `json:"rule_id,omitempty" db:"-"`
}
