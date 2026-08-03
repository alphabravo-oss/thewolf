package driver

import (
	"context"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/remediate/meter"
	"github.com/alphabravocompany/thewolf/internal/remediate/plan"
)

func TestFakeReturnsPlanAndUsage(t *testing.T) {
	want := &plan.Plan{
		Summary: "1 actionable",
		Items:   []plan.Item{{FindingID: "f-1", Action: plan.ActionFix, Rationale: "sqli"}},
	}
	// step_finish is the turn signal the meter package actually counts —
	// see meter.turnSignal. "assistant" is not a real OpenCode event type.
	f := NewFake([]meter.Event{{Type: "step_finish"}, {Type: "step_finish"}}, want)

	got, usage, err := f.Plan(context.Background(), PlanRequest{
		WorktreePath: "/tmp/wt",
		MaxTurns:     10,
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if got.Summary != want.Summary {
		t.Errorf("Summary = %q, want %q", got.Summary, want.Summary)
	}
	if usage.Turns != 2 {
		t.Errorf("Usage.Turns = %d, want 2", usage.Turns)
	}
}

func TestFakeStopsAtTurnBudget(t *testing.T) {
	events := []meter.Event{{Type: "step_finish"}, {Type: "step_finish"}, {Type: "step_finish"}}
	f := NewFake(events, &plan.Plan{
		Summary: "s",
		Items:   []plan.Item{{FindingID: "f-1", Action: plan.ActionFix, Rationale: "r"}},
	})

	_, usage, err := f.Plan(context.Background(), PlanRequest{WorktreePath: "/tmp/wt", MaxTurns: 2})
	if err == nil {
		t.Fatal("Plan succeeded, want ErrBudgetExhausted")
	}
	if usage.Turns != 2 {
		t.Errorf("Usage.Turns = %d, want 2 (stopped at budget)", usage.Turns)
	}
}

// TestFakeZeroValueDoesNotReturnNilResultWithNilError is a regression test:
// Fake's fields are exported, so a caller can construct &Fake{} directly
// instead of going through NewFake and forget to set PlanOut/Series. Both
// methods must fail loudly rather than return a nil result with a nil
// error, which would nil-panic the first caller that touches the result.
func TestFakeZeroValueDoesNotReturnNilResultWithNilError(t *testing.T) {
	f := &Fake{}

	plan, _, err := f.Plan(context.Background(), PlanRequest{MaxTurns: 10})
	if plan != nil || err == nil {
		t.Errorf("Plan() = (%v, _, %v), want (nil, _, non-nil)", plan, err)
	}

	series, _, err := f.Execute(context.Background(), ExecuteRequest{MaxTurns: 10})
	if series != nil || err == nil {
		t.Errorf("Execute() = (%v, _, %v), want (nil, _, non-nil)", series, err)
	}
}
