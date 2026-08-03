package meter

import (
	"bufio"
	"encoding/json"
	"os"
	"testing"
)

func loadFixture(t *testing.T, name string) []Event {
	t.Helper()
	f, err := os.Open("testdata/" + name)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()

	var events []Event
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var e Event
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			t.Fatalf("unmarshal event: %v", err)
		}
		events = append(events, e)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan fixture: %v", err)
	}
	return events
}

func TestTurnsCountsFixtureStream(t *testing.T) {
	events := loadFixture(t, "session-basic.jsonl")
	m := NewTurns(100)
	for _, e := range events {
		if m.Observe(e) {
			t.Fatal("budget of 100 exhausted on the basic fixture")
		}
	}
	if got := m.Usage().Turns; got == 0 {
		t.Fatal("Usage().Turns = 0, want > 0 — turn signal not recognized")
	}
}

func TestTurnsStopsAtBudget(t *testing.T) {
	m := NewTurns(2)
	turn := Event{Type: "step_finish"}

	if m.Observe(turn) {
		t.Fatal("exhausted after turn 1, budget is 2")
	}
	if !m.Observe(turn) {
		t.Fatal("not exhausted after turn 2, budget is 2")
	}
	if got := m.Usage().Turns; got != 2 {
		t.Errorf("Usage().Turns = %d, want 2", got)
	}
}

func TestTurnsIgnoresNonTurnEvents(t *testing.T) {
	m := NewTurns(1)
	for _, notATurn := range []string{"step_start", "text", "tool_use"} {
		if m.Observe(Event{Type: notATurn}) {
			t.Fatalf("%s counted as a turn", notATurn)
		}
	}
	if got := m.Usage().Turns; got != 0 {
		t.Errorf("Usage().Turns = %d, want 0", got)
	}
}

// step_finish carries the step's own token and cost totals, so the turns
// meter accumulates spend as it goes — no separate cost meter is needed.
func TestUsageAccumulatesTokensAndCost(t *testing.T) {
	m := NewTurns(10)

	var e Event
	e.Type = "step_finish"
	e.Part.Tokens.Total = 34116
	e.Part.Cost = 0.25
	m.Observe(e)
	m.Observe(e)

	u := m.Usage()
	if u.Turns != 2 {
		t.Errorf("Turns = %d, want 2", u.Turns)
	}
	if u.Tokens != 68232 {
		t.Errorf("Tokens = %d, want 68232", u.Tokens)
	}
	if u.Cost != 0.5 {
		t.Errorf("Cost = %v, want 0.5", u.Cost)
	}
}

// The real fixture must report both turns and non-zero tokens — a meter that
// silently matched nothing would still pass a turns-only assertion.
func TestFixtureReportsTwoTurnsWithTokens(t *testing.T) {
	m := NewTurns(100)
	for _, e := range loadFixture(t, "session-basic.jsonl") {
		m.Observe(e)
	}
	u := m.Usage()
	if u.Turns != 2 {
		t.Errorf("Turns = %d, want 2 — the captured fixture has two step_finish events", u.Turns)
	}
	if u.Tokens == 0 {
		t.Error("Tokens = 0 — token totals were not read from step_finish")
	}
}
