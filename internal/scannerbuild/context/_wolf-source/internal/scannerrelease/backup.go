package scannerrelease

import "time"

const (
	BackupFormat        = "wolf.scanner-release-backup"
	BackupFormatVersion = 1
	BackupMigration     = 46
	RestoreConfirmation = "RESTORE_SCANNER_RELEASE_STATE"
)

// BackupCell keeps database values portable between SQLite and PostgreSQL
// without allowing JSON number coercion to change immutable identities.
type BackupCell struct {
	Kind  string `json:"kind"`
	Value string `json:"value,omitempty"`
}

type BackupTable struct {
	Name     string         `json:"name"`
	Columns  []string       `json:"columns"`
	Rows     [][]BackupCell `json:"rows"`
	RowCount int            `json:"row_count"`
	Digest   string         `json:"sha256"`
}

// ReleaseBackup contains the complete release-management database graph. It
// deliberately excludes application secrets and recovery-infrastructure
// bookkeeping. Registry and signer secret/key references remain opaque.
type ReleaseBackup struct {
	Format            string        `json:"format"`
	Version           int           `json:"version"`
	RequiredMigration int           `json:"required_migration"`
	Complete          bool          `json:"complete"`
	SourceBackend     string        `json:"source_backend"`
	CreatedAt         time.Time     `json:"created_at"`
	SchemaFingerprint string        `json:"schema_fingerprint"`
	SanitizedFields   []string      `json:"sanitized_fields"`
	Tables            []BackupTable `json:"tables"`
	PayloadDigest     string        `json:"payload_sha256"`
}

type BackupPreflight struct {
	Valid             bool           `json:"valid"`
	Format            string         `json:"format"`
	Version           int            `json:"version"`
	PayloadDigest     string         `json:"payload_sha256"`
	SchemaFingerprint string         `json:"schema_fingerprint"`
	SourceBackend     string         `json:"source_backend"`
	TargetBackend     string         `json:"target_backend"`
	TableCounts       map[string]int `json:"table_counts"`
	TargetEmpty       bool           `json:"target_empty"`
	Restorable        bool           `json:"restorable"`
	Reasons           []string       `json:"reasons,omitempty"`
}

type BackupCommand struct {
	Actor                   string
	Reason                  string
	IdempotencyKey          string
	MaintenanceConfirmation string
}

type BackupRestoreResult struct {
	OperationID   string         `json:"operation_id"`
	State         string         `json:"state"`
	PayloadDigest string         `json:"payload_sha256"`
	TableCounts   map[string]int `json:"table_counts"`
	RestoredAt    time.Time      `json:"restored_at"`
	Idempotent    bool           `json:"idempotent"`
}

type BackupOperation struct {
	ID              string     `db:"id" json:"id"`
	OperationType   string     `db:"operation_type" json:"operation_type"`
	State           string     `db:"state" json:"state"`
	Actor           string     `db:"actor" json:"actor"`
	Reason          string     `db:"reason" json:"reason"`
	IdempotencyKey  string     `db:"idempotency_key" json:"idempotency_key"`
	PayloadDigest   string     `db:"payload_digest" json:"payload_sha256"`
	FormatVersion   int        `db:"format_version" json:"format_version"`
	TableCountsJSON string     `db:"table_counts_json" json:"table_counts"`
	ErrorDetail     string     `db:"error_detail" json:"error_detail,omitempty"`
	StartedAt       time.Time  `db:"started_at" json:"started_at"`
	CompletedAt     *time.Time `db:"completed_at" json:"completed_at,omitempty"`
}

type MaintenanceStatus struct {
	ID             string     `db:"id" json:"id"`
	Mode           string     `db:"mode" json:"mode"`
	Owner          string     `db:"owner" json:"owner,omitempty"`
	LeaseToken     string     `db:"lease_token" json:"-"`
	LeaseExpiresAt *time.Time `db:"lease_expires_at" json:"lease_expires_at,omitempty"`
	Version        int64      `db:"version" json:"version"`
	UpdatedAt      time.Time  `db:"updated_at" json:"updated_at"`
}

// RestoreActive reports whether an exclusive restore lease is currently
// authoritative. An expired lease is not allowed to strand readiness.
func (status MaintenanceStatus) RestoreActive(now time.Time) bool {
	if status.Mode != "restore" {
		return false
	}
	return status.LeaseExpiresAt == nil || status.LeaseExpiresAt.After(now.UTC())
}
