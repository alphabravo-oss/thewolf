package db

import (
	"context"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestScannerVersionChecksSQLite(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)

	check := &models.ScannerVersionCheck{
		ToolName:        "semgrep",
		PinnedVersion:   "1.92.0",
		LatestVersion:   "1.94.1",
		LatestReference: "semgrep/semgrep:1.94.1",
		Status:          models.ScannerVersionUpdateAvailable,
		CheckedAt:       now,
		SourceType:      "docker_registry",
		SourceURL:       "docker://semgrep/semgrep",
	}
	if err := store.UpsertScannerVersionCheck(ctx, check); err != nil {
		t.Fatalf("UpsertScannerVersionCheck insert: %v", err)
	}

	got, err := store.GetScannerVersionCheck(ctx, "semgrep")
	if err != nil {
		t.Fatalf("GetScannerVersionCheck: %v", err)
	}
	if got.LatestVersion != "1.94.1" || got.Status != models.ScannerVersionUpdateAvailable {
		t.Fatalf("unexpected inserted check: %#v", got)
	}

	check.LatestVersion = "1.92.0"
	check.LatestReference = "semgrep/semgrep:1.92.0"
	check.Status = models.ScannerVersionCurrent
	check.CheckedAt = now.Add(time.Hour)
	if err := store.UpsertScannerVersionCheck(ctx, check); err != nil {
		t.Fatalf("UpsertScannerVersionCheck update: %v", err)
	}

	list, err := store.ListScannerVersionChecks(ctx)
	if err != nil {
		t.Fatalf("ListScannerVersionChecks: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("checks len = %d, want 1", len(list))
	}
	if list[0].LatestVersion != "1.92.0" || list[0].Status != models.ScannerVersionCurrent {
		t.Fatalf("unexpected updated check: %#v", list[0])
	}
}
