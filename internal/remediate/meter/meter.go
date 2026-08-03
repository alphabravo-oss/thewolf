// Package meter bounds an OpenCode session. OpenCode's run command has no
// max-turns flag, so Wolf counts turns from the JSON event stream and stops
// the session itself.
package meter

// Event is one decoded record from `opencode run --format json`. Only the
// fields Wolf needs are modeled; the rest of the payload is ignored.
//
// Shape confirmed empirically against opencode-ai@1.18.11 — see
// docs/superpowers/specs/2026-08-03-opencode-spike-findings.md. Observed
// types are step_start, text, tool_use, and step_finish.
type Event struct {
	Type string `json:"type"`
	Part struct {
		// Reason is the step's stop reason, e.g. "stop".
		Reason string `json:"reason"`
		Tokens struct {
			Total     int64 `json:"total"`
			Input     int64 `json:"input"`
			Output    int64 `json:"output"`
			Reasoning int64 `json:"reasoning"`
		} `json:"tokens"`
		Cost float64 `json:"cost"`
	} `json:"part"`
}

// Usage is what a session spent. All three fields are populated by the turns
// meter: step_finish carries tokens and cost per step, so there is no reason
// to defer them to a separate meter.
type Usage struct {
	Turns  int
	Tokens int64
	Cost   float64
}

// Meter decides when a run has spent its budget.
type Meter interface {
	// Observe consumes one event and reports whether the budget is now spent.
	Observe(event Event) (exhausted bool)
	Usage() Usage
}

// turnSignal is the event type that marks a completed agent turn. Confirmed
// empirically against 1.18.11: a two-turn session emits step_start, tool_use,
// step_finish, step_start, text, step_finish. Counting step_finish counts
// turns. There is no "assistant" event type — a meter written against one
// counts zero turns forever and never stops the agent.
const turnSignal = "step_finish"

type turns struct {
	budget int
	count  int
	tokens int64
	cost   float64
}

// NewTurns returns a Meter that stops after budget turns. A budget <= 0 is
// treated as unbounded, which callers must not use in production — the
// session config clamps it before we get here.
func NewTurns(budget int) Meter { return &turns{budget: budget} }

func (t *turns) Observe(event Event) bool {
	if event.Type != turnSignal {
		return false
	}
	t.count++
	// step_finish reports the step's own token and cost totals, so accumulate
	// them here rather than deferring spend tracking to a separate meter.
	t.tokens += event.Part.Tokens.Total
	t.cost += event.Part.Cost
	return t.budget > 0 && t.count >= t.budget
}

func (t *turns) Usage() Usage {
	return Usage{Turns: t.count, Tokens: t.tokens, Cost: t.cost}
}
