package qualification

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRunBaseExercisesFallbackAndFailureContracts(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("WOLF_FIXER_VARIANT", "base")
	report, err := Run(context.Background(), "base", "none", t.TempDir())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.SchemaVersion != SchemaVersion || report.Variant != "base" || report.AuthMode != "none" {
		t.Fatalf("unexpected report: %+v", report)
	}
	if len(report.InstalledCLIs) != 0 || len(report.SelectedTiers) != 1 || report.SelectedTiers[0] != "api" {
		t.Fatalf("unexpected runtime boundary: %+v", report)
	}
	if len(report.CompletedChecks) != 9 {
		t.Fatalf("completed checks = %v", report.CompletedChecks)
	}
}

func TestRunValidatesVariantAuthAndCLIBoundary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("WOLF_FIXER_VARIANT", "base")
	if _, err := Run(context.Background(), "base", "api-key", t.TempDir()); err == nil {
		t.Fatal("expected auth-mode mismatch")
	}
	if _, err := Run(context.Background(), "api", "api-key", t.TempDir()); err == nil {
		t.Fatal("expected runtime variant mismatch")
	}

	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	if _, err := Run(context.Background(), "base", "none", t.TempDir()); err == nil {
		t.Fatal("expected unexpected-CLI rejection")
	}
}
