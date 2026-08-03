package db

import _ "embed"

//go:embed migrations/030_scanner_release_management_postgres.sql
var migration030PostgresSQL string

// migration034SQL is embedded by sqlite.go because both database engines use
// the same additive discovery-execution DDL.

// ScannerReleases returns the PostgreSQL-backed scanner release repositories.
func (s *PostgresStore) ScannerReleases() ScannerReleasePersistence {
	return newScannerReleaseRepository(s.db)
}
