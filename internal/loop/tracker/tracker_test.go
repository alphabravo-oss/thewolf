package tracker

import (
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestTracker_UpdateAndDiff(t *testing.T) {
	tr := New()

	// Iteration 0: three findings.
	tr.Update(0, []models.Finding{
		{Fingerprint: "fp-a", Title: "Issue A", Severity: models.SeverityHigh},
		{Fingerprint: "fp-b", Title: "Issue B", Severity: models.SeverityMedium},
		{Fingerprint: "fp-c", Title: "Issue C", Severity: models.SeverityLow},
	})

	// Iteration 1: fp-a fixed, fp-d is new, fp-b and fp-c remain.
	tr.Update(1, []models.Finding{
		{Fingerprint: "fp-b", Title: "Issue B", Severity: models.SeverityMedium},
		{Fingerprint: "fp-c", Title: "Issue C", Severity: models.SeverityLow},
		{Fingerprint: "fp-d", Title: "Issue D", Severity: models.SeverityCritical},
	})

	diff := tr.Diff(0, 1)

	if diff.FixedCount != 1 {
		t.Errorf("expected 1 fixed, got %d", diff.FixedCount)
	}
	if diff.Fixed[0].Fingerprint != "fp-a" {
		t.Errorf("expected fp-a fixed, got %s", diff.Fixed[0].Fingerprint)
	}

	if diff.NewCount != 1 {
		t.Errorf("expected 1 new, got %d", diff.NewCount)
	}
	if diff.New[0].Fingerprint != "fp-d" {
		t.Errorf("expected fp-d new, got %s", diff.New[0].Fingerprint)
	}

	if diff.RemainingCount != 2 {
		t.Errorf("expected 2 remaining, got %d", diff.RemainingCount)
	}
}

func TestTracker_DiffNoChange(t *testing.T) {
	tr := New()

	findings := []models.Finding{
		{Fingerprint: "fp-1", Title: "Issue 1"},
		{Fingerprint: "fp-2", Title: "Issue 2"},
	}

	tr.Update(0, findings)
	tr.Update(1, findings)

	diff := tr.Diff(0, 1)

	if diff.FixedCount != 0 {
		t.Errorf("expected 0 fixed, got %d", diff.FixedCount)
	}
	if diff.NewCount != 0 {
		t.Errorf("expected 0 new, got %d", diff.NewCount)
	}
	if diff.RemainingCount != 2 {
		t.Errorf("expected 2 remaining, got %d", diff.RemainingCount)
	}
}

func TestTracker_DiffAllFixed(t *testing.T) {
	tr := New()

	tr.Update(0, []models.Finding{
		{Fingerprint: "fp-x", Title: "X"},
		{Fingerprint: "fp-y", Title: "Y"},
	})
	tr.Update(1, []models.Finding{})

	diff := tr.Diff(0, 1)

	if diff.FixedCount != 2 {
		t.Errorf("expected 2 fixed, got %d", diff.FixedCount)
	}
	if diff.NewCount != 0 {
		t.Errorf("expected 0 new, got %d", diff.NewCount)
	}
	if diff.RemainingCount != 0 {
		t.Errorf("expected 0 remaining, got %d", diff.RemainingCount)
	}
}

func TestTracker_DiffEmptyPrev(t *testing.T) {
	tr := New()

	tr.Update(0, []models.Finding{})
	tr.Update(1, []models.Finding{
		{Fingerprint: "fp-new", Title: "New Issue"},
	})

	diff := tr.Diff(0, 1)

	if diff.FixedCount != 0 {
		t.Errorf("expected 0 fixed, got %d", diff.FixedCount)
	}
	if diff.NewCount != 1 {
		t.Errorf("expected 1 new, got %d", diff.NewCount)
	}
}

func TestTracker_DiffMissingIteration(t *testing.T) {
	tr := New()

	tr.Update(0, []models.Finding{
		{Fingerprint: "fp-1", Title: "Issue 1"},
	})

	// Diff against a non-existent iteration should treat it as empty.
	diff := tr.Diff(0, 99)

	if diff.FixedCount != 1 {
		t.Errorf("expected 1 fixed (all gone), got %d", diff.FixedCount)
	}
	if diff.NewCount != 0 {
		t.Errorf("expected 0 new, got %d", diff.NewCount)
	}
}

func TestTracker_SkipsEmptyFingerprints(t *testing.T) {
	tr := New()

	tr.Update(0, []models.Finding{
		{Fingerprint: "fp-1", Title: "Has FP"},
		{Fingerprint: "", Title: "No FP"},
	})

	findings := tr.FindingsForIteration(0)
	if len(findings) != 1 {
		t.Errorf("expected 1 finding (skipping empty fingerprint), got %d", len(findings))
	}
}

func TestTracker_FindingsForIteration(t *testing.T) {
	tr := New()

	tr.Update(0, []models.Finding{
		{Fingerprint: "fp-a", Title: "A"},
		{Fingerprint: "fp-b", Title: "B"},
	})

	findings := tr.FindingsForIteration(0)
	if len(findings) != 2 {
		t.Errorf("expected 2 findings, got %d", len(findings))
	}

	// Non-existent iteration.
	findings = tr.FindingsForIteration(5)
	if len(findings) != 0 {
		t.Errorf("expected 0 findings for non-existent iteration, got %d", len(findings))
	}
}
