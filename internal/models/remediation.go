package models

import "time"

// RemediationStatus is the state of an agentic remediation session.
type RemediationStatus string

const (
	RemediationPending     RemediationStatus = "pending"
	RemediationPlanning    RemediationStatus = "planning"
	RemediationPlanReview  RemediationStatus = "plan_review"
	RemediationExecuting   RemediationStatus = "executing"
	RemediationPatchReview RemediationStatus = "patch_review"
	RemediationApplying    RemediationStatus = "applying"
	RemediationRescanning  RemediationStatus = "rescanning"
	RemediationCompleted   RemediationStatus = "completed"
	RemediationFailed      RemediationStatus = "failed"
	RemediationCancelled   RemediationStatus = "cancelled"
	RemediationExhausted   RemediationStatus = "exhausted"
	RemediationRejected    RemediationStatus = "rejected"
)

// RemediationSession is one agentic remediation run over a scan's findings.
type RemediationSession struct {
	ID               string            `json:"id" db:"id"`
	UserID           string            `json:"user_id" db:"user_id"`
	RepoID           string            `json:"repo_id" db:"repo_id"`
	ScanID           string            `json:"scan_id" db:"scan_id"`
	LoopID           *string           `json:"loop_id,omitempty" db:"loop_id"`
	Status           RemediationStatus `json:"status" db:"status"`
	PlanGateEnabled  bool              `json:"plan_gate_enabled" db:"plan_gate_enabled"`
	PatchGateEnabled bool              `json:"patch_gate_enabled" db:"patch_gate_enabled"`
	// MaxTurns is the budget applied to EACH phase, not the session as a
	// whole — the plan run and the execute run each get up to this many
	// turns, which is why TurnsUsedPlan and TurnsUsedExecute are tracked
	// separately below rather than sharing one counter.
	MaxTurns         int        `json:"max_turns" db:"max_turns"`
	TurnsUsedPlan    int        `json:"turns_used_plan" db:"turns_used_plan"`
	TurnsUsedExecute int        `json:"turns_used_execute" db:"turns_used_execute"`
	TokensUsed       int64      `json:"tokens_used" db:"tokens_used"`
	CostUsed         float64    `json:"cost_used" db:"cost_used"`
	Provider         string     `json:"provider" db:"provider"`
	Model            string     `json:"model" db:"model"`
	BranchName       string     `json:"branch_name" db:"branch_name"`
	WorktreePath     string     `json:"-" db:"worktree_path"`
	PRURL            string     `json:"pr_url" db:"pr_url"`
	FailureReason    string     `json:"failure_reason" db:"failure_reason"`
	CreatedAt        time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at" db:"updated_at"`
	StartedAt        *time.Time `json:"started_at,omitempty" db:"started_at"`
	CompletedAt      *time.Time `json:"completed_at,omitempty" db:"completed_at"`
}

// RemediationPlan is a persisted triage plan awaiting or past its gate.
type RemediationPlan struct {
	ID             string     `json:"id" db:"id"`
	SessionID      string     `json:"session_id" db:"session_id"`
	PlanJSON       string     `json:"plan_json" db:"plan_json"`
	CreatedAt      time.Time  `json:"created_at" db:"created_at"`
	ApprovedBy     string     `json:"approved_by,omitempty" db:"approved_by"`
	ApprovedAt     *time.Time `json:"approved_at,omitempty" db:"approved_at"`
	RejectedReason string     `json:"rejected_reason,omitempty" db:"rejected_reason"`
}

// RemediationPatch is one commit produced by an execute run.
type RemediationPatch struct {
	ID        string `json:"id" db:"id"`
	SessionID string `json:"session_id" db:"session_id"`
	CommitSHA string `json:"commit_sha" db:"commit_sha"`
	// FilesChanged is a JSON array of repo-relative paths, e.g.
	// `["db/user.go","api/handler.go"]`. It is stored as text because the
	// column must work on SQLite and Postgres alike; JSON is the encoding
	// rather than a delimiter because paths may contain a comma or a space,
	// and because it round-trips with plan.Item.Files. Empty is "" or "[]".
	FilesChanged string `json:"files_changed" db:"files_changed"`
	// FindingIDs is a JSON array of finding IDs this commit addresses, e.g.
	// `["f-1","f-2"]`, encoded exactly as FilesChanged is.
	FindingIDs string     `json:"finding_ids" db:"finding_ids"`
	Message    string     `json:"message" db:"message"`
	CreatedAt  time.Time  `json:"created_at" db:"created_at"`
	ApprovedBy string     `json:"approved_by,omitempty" db:"approved_by"`
	ApprovedAt *time.Time `json:"approved_at,omitempty" db:"approved_at"`
}

// RemediationEvent is one redacted record from the agent's event stream.
type RemediationEvent struct {
	ID          string    `json:"id" db:"id"`
	SessionID   string    `json:"session_id" db:"session_id"`
	Seq         int       `json:"seq" db:"seq"`
	Type        string    `json:"type" db:"type"`
	PayloadJSON string    `json:"payload_json" db:"payload_json"`
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
}
