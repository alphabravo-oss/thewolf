package models

import "time"

type TestCoverage struct {
	ScanID     string    `db:"scan_id"`
	CommitSHA  string    `db:"commit_sha"`
	ReportJSON string    `db:"report_json"`
	ImportedBy string    `db:"imported_by"`
	ImportedAt time.Time `db:"imported_at"`
}
