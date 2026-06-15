package models

import "time"

// Repo represents a repository managed by wolf.
type Repo struct {
	ID                 string     `json:"id" db:"id"`
	UserID             string     `json:"user_id" db:"user_id"`
	Name               string     `json:"name" db:"name"`
	SourceType         SourceType `json:"source_type" db:"source_type"`
	SourcePath         string     `json:"source_path" db:"source_path"`
	RemoteNodeID       *string    `json:"remote_node_id,omitempty" db:"remote_node_id"`
	RemotePath         string     `json:"remote_path,omitempty" db:"remote_path"`
	LastCommitSHA      string     `json:"last_commit_sha,omitempty" db:"last_commit_sha"`
	LastDirtyState     string     `json:"last_dirty_state,omitempty" db:"last_dirty_state"`
	DefaultBranch      string     `json:"default_branch" db:"default_branch"`
	DetectedLanguages  string     `json:"detected_languages" db:"detected_languages"`
	DetectedFrameworks string     `json:"detected_frameworks" db:"detected_frameworks"`
	DetectedAt         *time.Time `json:"detected_at" db:"detected_at"`
	CreatedAt          time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at" db:"updated_at"`

	// Fixable is the derived writability verdict for the autonomous fix engine:
	// can wolf actually write a fix branch to this repo's source? It is never
	// persisted (db:"-") — it is computed on demand by the writability
	// preflight (GET /repos/{id}/fixable) and surfaced in the UI so the Fix
	// action can be disabled with a clear reason instead of failing mid-job.
	Fixable *RepoFixable `json:"fixable,omitempty" db:"-"`
}

// RepoFixable is the derived "can we write a fix here?" indicator attached to a
// Repo. It mirrors writability.Result; it lives in models so the Repo struct can
// carry it without a models→fix/writability import cycle.
type RepoFixable struct {
	Writable bool   `json:"writable"`
	Reason   string `json:"reason"`
}

// Collection represents a group of repositories.
type Collection struct {
	ID          string    `json:"id" db:"id"`
	UserID      string    `json:"user_id" db:"user_id"`
	Name        string    `json:"name" db:"name"`
	Description string    `json:"description" db:"description"`
	ScanConfig  string    `json:"scan_config" db:"scan_config"`
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time `json:"updated_at" db:"updated_at"`
	RepoCount   int       `json:"repo_count" db:"repo_count"`
}

// CollectionRepo represents the junction between collections and repos.
type CollectionRepo struct {
	CollectionID string `json:"collection_id" db:"collection_id"`
	RepoID       string `json:"repo_id" db:"repo_id"`
}
