package models

import "time"

type ScannerRunRecord struct {
	ID                    string     `json:"id" db:"id"`
	ScanID                string     `json:"scan_id" db:"scan_id"`
	ToolName              string     `json:"tool_name" db:"tool_name"`
	Status                string     `json:"status" db:"status"`
	Category              string     `json:"category,omitempty" db:"category"`
	Image                 string     `json:"image,omitempty" db:"image"`
	ImageDigest           string     `json:"image_digest,omitempty" db:"image_digest"`
	ScannerReleaseID      string     `json:"scanner_release_id,omitempty" db:"scanner_release_id"`
	ReleaseManifestDigest string     `json:"release_manifest_digest,omitempty" db:"release_manifest_digest"`
	Version               string     `json:"version,omitempty" db:"version"`
	CommandJSON           string     `json:"command_json" db:"command_json"`
	ExitCode              int        `json:"exit_code" db:"exit_code"`
	DurationMS            int64      `json:"duration_ms" db:"duration_ms"`
	FindingCount          int        `json:"finding_count" db:"finding_count"`
	ErrorMessage          string     `json:"error_message,omitempty" db:"error_message"`
	ParserStatus          string     `json:"parser_status,omitempty" db:"parser_status"`
	ParserMessage         string     `json:"parser_message,omitempty" db:"parser_message"`
	RuntimeBackend        string     `json:"runtime_backend,omitempty" db:"runtime_backend"`
	RuntimeRef            string     `json:"runtime_ref,omitempty" db:"runtime_ref"`
	Attempt               int        `json:"attempt,omitempty" db:"attempt"`
	LeaseToken            string     `json:"-" db:"-"`
	CancelRequestedAt     *time.Time `json:"-" db:"cancel_requested_at"`
	RequestedScope        string     `json:"requested_scope,omitempty" db:"requested_scope"`
	EffectiveScope        string     `json:"effective_scope,omitempty" db:"effective_scope"`
	ScopeMessage          string     `json:"scope_message,omitempty" db:"scope_message"`
	StartedAt             *time.Time `json:"started_at,omitempty" db:"started_at"`
	FinishedAt            *time.Time `json:"finished_at,omitempty" db:"finished_at"`
	CreatedAt             time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at" db:"updated_at"`
}
