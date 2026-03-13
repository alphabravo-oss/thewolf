package models

import "time"

// ScanArtifact represents a file artifact produced by a scan.
type ScanArtifact struct {
	ID           string       `json:"id" db:"id"`
	ScanID       string       `json:"scan_id" db:"scan_id"`
	ArtifactType ArtifactType `json:"artifact_type" db:"artifact_type"`
	FilePath     string       `json:"file_path" db:"file_path"`
	FileSize     int64        `json:"file_size" db:"file_size"`
	CreatedAt    time.Time    `json:"created_at" db:"created_at"`
	UpdatedAt    time.Time    `json:"updated_at" db:"updated_at"`
}
