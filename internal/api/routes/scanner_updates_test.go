package routes

import (
	"testing"
	"time"
)

func TestScannerUpdatesKeepFirstSeenUntilSetChanges(t *testing.T) {
	t0 := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	recordScannerUpdates([]string{"b:stable", "a:stable"}, t0)
	recordScannerUpdates([]string{"a:stable", "b:stable"}, t0.Add(6*time.Hour))
	if n, since := pendingScannerUpdates(); n != 2 || !since.Equal(t0) {
		t.Fatalf("unchanged set must keep first-seen time: %d %v", n, since)
	}
	recordScannerUpdates([]string{"a:stable"}, t0.Add(12*time.Hour))
	if n, since := pendingScannerUpdates(); n != 1 || !since.Equal(t0.Add(12*time.Hour)) {
		t.Fatalf("changed set must restart first-seen time: %d %v", n, since)
	}
	recordScannerUpdates(nil, t0.Add(18*time.Hour))
	if n, _ := pendingScannerUpdates(); n != 0 {
		t.Fatalf("pulling updates must clear the notice, got %d", n)
	}
}
