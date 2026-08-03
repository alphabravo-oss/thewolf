package scannerrelease

import (
	"testing"
	"time"
)

func TestMaintenanceStatusRestoreActiveHonorsLeaseExpiry(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	future := now.Add(time.Minute)
	status := MaintenanceStatus{
		Mode: "restore", LeaseExpiresAt: &future,
	}
	if !status.RestoreActive(now) {
		t.Fatal("current restore lease was not active")
	}
	past := now.Add(-time.Second)
	status.LeaseExpiresAt = &past
	if status.RestoreActive(now) {
		t.Fatal("expired restore lease stranded readiness")
	}
	status.LeaseExpiresAt = nil
	if !status.RestoreActive(now) {
		t.Fatal("restore mode without expiry must fail closed")
	}
	status.Mode = "normal"
	if status.RestoreActive(now) {
		t.Fatal("normal mode reported active restore")
	}
}
