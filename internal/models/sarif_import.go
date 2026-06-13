package models

import "time"

type SARIFImport struct {
	ID             string    `json:"id" db:"id"`
	RepoID         string    `json:"repo_id" db:"repo_id"`
	ScanID         string    `json:"scan_id" db:"scan_id"`
	Source         string    `json:"source" db:"source"`
	ChecksumSHA256 string    `json:"checksum_sha256" db:"checksum_sha256"`
	ResultCount    int       `json:"result_count" db:"result_count"`
	ImportedCount  int       `json:"imported_count" db:"imported_count"`
	CreatedBy      string    `json:"created_by" db:"created_by"`
	CreatedAt      time.Time `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time `json:"updated_at" db:"updated_at"`
}
