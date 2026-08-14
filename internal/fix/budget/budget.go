// Package budget enforces cost and time ceilings on a fixer agent run.
// max-iterations is the only mandatory bound; a per-invocation timeout,
// a total dollar budget, and an overall wall-clock cap are each optional.
package budget

import (
	"fmt"
	"time"
)

// Ceilings is the configured set of run bounds. Zero-valued optional
// fields mean "no limit".
type Ceilings struct {
	// MaxIterations is mandatory and must be > 0.
	MaxIterations int
	// PerInvocation caps a single AI invocation's wall-clock time.
	// Zero means no per-invocation timeout.
	PerInvocation time.Duration
	// WallClock caps the whole run's elapsed time. Zero means no cap.
	WallClock time.Duration
	// MaxCostUSD caps cumulative AI spend. Zero means no cap.
	MaxCostUSD float64
}

// Validate checks the mandatory invariant.
func (c Ceilings) Validate() error {
	if c.MaxIterations <= 0 {
		return fmt.Errorf("budget: MaxIterations must be > 0")
	}
	return nil
}

// Tracker accumulates progress against the configured ceilings.
// It is not safe for concurrent use; a fixer run is single-threaded.
type Tracker struct {
	ceilings   Ceilings
	started    time.Time
	iterations int
	costUSD    float64
}

// New returns a Tracker. The clock starts now.
func New(c Ceilings) *Tracker {
	return &Tracker{ceilings: c, started: time.Now()}
}

// StartIteration records that another iteration is beginning.
func (t *Tracker) StartIteration() { t.iterations++ }

// AddCost accumulates AI spend (USD) — typically from ai_logs cost_usd.
func (t *Tracker) AddCost(usd float64) {
	if usd > 0 {
		t.costUSD += usd
	}
}

// CostUSD returns cumulative recorded spend.
func (t *Tracker) CostUSD() float64 { return t.costUSD }

// Iterations returns how many iterations have started.
func (t *Tracker) Iterations() int { return t.iterations }

// InvocationTimeout returns the per-invocation timeout, or 0 if none.
func (t *Tracker) InvocationTimeout() time.Duration { return t.ceilings.PerInvocation }

// StopReason returns a non-empty reason when a ceiling has been crossed
// and the run must stop, or "" when it may continue. Checked before
// each iteration. The first-crossed ceiling wins; the order below is
// the tie-break.
func (t *Tracker) StopReason() string {
	if t.iterations >= t.ceilings.MaxIterations {
		return fmt.Sprintf("max-iterations reached (%d)", t.ceilings.MaxIterations)
	}
	if t.ceilings.WallClock > 0 && time.Since(t.started) >= t.ceilings.WallClock {
		return fmt.Sprintf("wall-clock cap reached (%s)", t.ceilings.WallClock)
	}
	if t.ceilings.MaxCostUSD > 0 && t.costUSD >= t.ceilings.MaxCostUSD {
		return fmt.Sprintf("cost budget reached ($%.2f of $%.2f)", t.costUSD, t.ceilings.MaxCostUSD)
	}
	return ""
}
