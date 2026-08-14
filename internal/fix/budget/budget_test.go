package budget

import (
	"strings"
	"testing"
	"time"
)

func TestCeilings_Validate(t *testing.T) {
	if err := (Ceilings{MaxIterations: 5}).Validate(); err != nil {
		t.Errorf("valid ceilings rejected: %v", err)
	}
	if err := (Ceilings{}).Validate(); err == nil {
		t.Error("MaxIterations=0 must be rejected")
	}
}

func TestTracker_MaxIterations(t *testing.T) {
	tr := New(Ceilings{MaxIterations: 3})
	for i := 0; i < 3; i++ {
		if r := tr.StopReason(); r != "" {
			t.Fatalf("iteration %d: unexpected stop %q", i, r)
		}
		tr.StartIteration()
	}
	if r := tr.StopReason(); !strings.Contains(r, "max-iterations") {
		t.Errorf("expected max-iterations stop, got %q", r)
	}
}

func TestTracker_CostBudget(t *testing.T) {
	tr := New(Ceilings{MaxIterations: 100, MaxCostUSD: 5.0})
	tr.StartIteration()
	tr.AddCost(4.80)
	if r := tr.StopReason(); r != "" {
		t.Fatalf("under budget should continue, got %q", r)
	}
	tr.AddCost(0.50) // now $5.30
	r := tr.StopReason()
	if !strings.Contains(r, "cost budget") {
		t.Errorf("expected cost-budget stop, got %q", r)
	}
}

func TestTracker_WallClock(t *testing.T) {
	tr := New(Ceilings{MaxIterations: 100, WallClock: 1 * time.Millisecond})
	time.Sleep(3 * time.Millisecond)
	if r := tr.StopReason(); !strings.Contains(r, "wall-clock") {
		t.Errorf("expected wall-clock stop, got %q", r)
	}
}

func TestTracker_NoOptionalCeilings(t *testing.T) {
	tr := New(Ceilings{MaxIterations: 2})
	tr.StartIteration()
	tr.AddCost(9999)
	if r := tr.StopReason(); r != "" {
		t.Errorf("with no cost ceiling, spend should not stop the run, got %q", r)
	}
}

func TestTracker_InvocationTimeout(t *testing.T) {
	tr := New(Ceilings{MaxIterations: 1, PerInvocation: 30 * time.Second})
	if tr.InvocationTimeout() != 30*time.Second {
		t.Errorf("InvocationTimeout = %v", tr.InvocationTimeout())
	}
	if New(Ceilings{MaxIterations: 1}).InvocationTimeout() != 0 {
		t.Error("unset PerInvocation should be 0")
	}
}
