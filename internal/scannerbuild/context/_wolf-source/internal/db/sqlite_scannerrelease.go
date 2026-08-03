package db

// ScannerReleases returns the SQLite-backed scanner release repositories.
func (s *SQLiteStore) ScannerReleases() ScannerReleasePersistence {
	return newScannerReleaseRepository(s.db)
}
