package scannerrelease

import "strings"

// DiscoveryEligibleForCandidate reports whether a discovery is a complete,
// fail-closed snapshot that may be used as candidate input. Completed runs
// with partial source coverage remain useful operational evidence, but they
// must never silently drive the weekly release proposal path.
func DiscoveryEligibleForCandidate(run *DiscoveryRun) bool {
	if run == nil || run.State != DiscoveryCompleted || strings.TrimSpace(run.ErrorClass) != "" {
		return false
	}
	if run.UnreachableCount != 0 || run.UnsupportedCount != 0 || run.UnknownCount != 0 {
		return false
	}
	// Older/imported empty snapshots do not have coverage counters. Preserve
	// their compatibility while requiring full coverage whenever a run reports
	// a non-empty discovery population.
	if run.TotalCount == 0 {
		return true
	}
	return run.Coverage >= 1 && run.CoveredCount == run.TotalCount
}
