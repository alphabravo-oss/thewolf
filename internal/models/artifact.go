package models

import "time"

// ScanArtifact represents a file artifact produced by a scan.
type ScanArtifact struct {
	ID             string       `json:"id" db:"id"`
	ScanID         string       `json:"scan_id" db:"scan_id"`
	ArtifactType   ArtifactType `json:"artifact_type" db:"artifact_type"`
	FilePath       string       `json:"file_path" db:"file_path"`
	FileSize       int64        `json:"file_size" db:"file_size"`
	ChecksumSHA256 string       `json:"checksum_sha256,omitempty" db:"checksum_sha256"`
	RedactionLevel string       `json:"redaction_level,omitempty" db:"redaction_level"`
	CreatedAt      time.Time    `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time    `json:"updated_at" db:"updated_at"`
}
