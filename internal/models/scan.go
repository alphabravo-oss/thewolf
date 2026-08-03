package models

import "time"

// Scan represents a single scan operation.
type Scan struct {
	ID                     string     `json:"id" db:"id"`
	UserID                 string     `json:"user_id" db:"user_id"`
	RepoID                 string     `json:"repo_id" db:"repo_id"`
	CollectionID           *string    `json:"collection_id,omitempty" db:"collection_id"`
	LoopID                 *string    `json:"loop_id,omitempty" db:"loop_id"`
	Iteration              *int       `json:"iteration,omitempty" db:"iteration"`
	Branch                 string     `json:"branch" db:"branch"`
	SourceType             SourceType `json:"source_type,omitempty" db:"source_type"`
	RemoteNodeID           *string    `json:"remote_node_id,omitempty" db:"remote_node_id"`
	SourcePath             string     `json:"source_path,omitempty" db:"source_path"`
	CommitSHA              string     `json:"commit_sha,omitempty" db:"commit_sha"`
	TreeDigest             string     `json:"tree_digest,omitempty" db:"tree_digest"`
	DirtyState             string     `json:"dirty_state,omitempty" db:"dirty_state"`
	PreparedWorkspace      string     `json:"prepared_workspace,omitempty" db:"prepared_workspace"`
	RequestJSON            string     `json:"-" db:"request_json"`
	RequestDigest          string     `json:"-" db:"request_digest"`
	ClientReference        string     `json:"client_reference,omitempty" db:"client_reference"`
	IdempotencyKey         string     `json:"-" db:"idempotency_key"`
	Phase                  string     `json:"phase,omitempty" db:"phase"`
	ClaimedBy              string     `json:"-" db:"claimed_by"`
	LeaseToken             string     `json:"-" db:"lease_token"`
	LeaseExpiresAt         *time.Time `json:"-" db:"lease_expires_at"`
	HeartbeatAt            *time.Time `json:"-" db:"heartbeat_at"`
	Attempt                int        `json:"attempt,omitempty" db:"attempt"`
	MaxAttempts            int        `json:"-" db:"max_attempts"`
	CancelRequestedAt      *time.Time `json:"-" db:"cancel_requested_at"`
	FailureCode            string     `json:"failure_code,omitempty" db:"failure_code"`
	FailureMessage         string     `json:"failure_message,omitempty" db:"failure_message"`
	ExecutionBackend       string     `json:"execution_backend,omitempty" db:"execution_backend"`
	ScannerReleaseID       string     `json:"scanner_release_id,omitempty" db:"scanner_release_id"`
	ReleaseManifestDigest  string     `json:"release_manifest_digest,omitempty" db:"release_manifest_digest"`
	RescanOfScanID         string     `json:"rescan_of_scan_id,omitempty" db:"rescan_of_scan_id"`
	ReleaseSelectionReason string     `json:"release_selection_reason,omitempty" db:"release_selection_reason"`
	SourceFingerprint      string     `json:"source_fingerprint,omitempty" db:"source_fingerprint"`
	Profile                string     `json:"profile,omitempty" db:"profile"`
	Categories             string     `json:"categories,omitempty" db:"categories"`
	IncludePaths           string     `json:"include_paths,omitempty" db:"include_paths"`
	ExcludePaths           string     `json:"exclude_paths,omitempty" db:"exclude_paths"`
	Status                 ScanStatus `json:"status" db:"status"`
	ToolsSelected          string     `json:"tools_selected" db:"tools_selected"`
	ToolsCompleted         string     `json:"tools_completed" db:"tools_completed"`
	ToolsFailed            string     `json:"tools_failed" db:"tools_failed"`
	// ToolsErrors is a JSON-encoded map {toolName: errorMessage} so the
	// UI can surface *why* a given tool failed without digging into log
	// artifacts. Empty "{}" when no failures.
	ToolsErrors     string     `json:"tools_errors" db:"tools_errors"`
	FindingCount    int        `json:"finding_count" db:"finding_count"`
	CoverageSummary string     `json:"coverage_summary" db:"coverage_summary"`
	AIEnabled       bool       `json:"ai_enabled" db:"ai_enabled"`
	AISummary       string     `json:"ai_summary" db:"ai_summary"`
	StartedAt       *time.Time `json:"started_at,omitempty" db:"started_at"`
	CompletedAt     *time.Time `json:"completed_at,omitempty" db:"completed_at"`
	CreatedAt       time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at" db:"updated_at"`

	// Populated by API handlers, not stored in DB.
	Repo *Repo `json:"repo,omitempty" db:"-"`
}
