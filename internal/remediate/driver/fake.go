package driver

import (
	"context"

	"github.com/alphabravocompany/thewolf/internal/remediate/meter"
	"github.com/alphabravocompany/thewolf/internal/remediate/plan"
)

// Fake replays a recorded event stream and returns a canned result. It drives
// the same meter as the exec driver, so budget behavior under test matches
// production.
type Fake struct {
	Events  []meter.Event
	PlanOut *plan.Plan
	Series  *PatchSeries
	// PlanErr and ExecErr, when set, are returned after the stream is
	// replayed — used to exercise orchestrator error paths.
	PlanErr error
	ExecErr error
}

// NewFake returns a Fake that replays events and yields p.
func NewFake(events []meter.Event, p *plan.Plan) *Fake {
	return &Fake{Events: events, PlanOut: p, Series: &PatchSeries{}}
}

func (f *Fake) replay(m meter.Meter, onEvent func(meter.Event)) bool {
	for _, e := range f.Events {
		if onEvent != nil {
			onEvent(e)
		}
		if m.Observe(e) {
			return true
		}
	}
	return false
}

func (f *Fake) Plan(_ context.Context, req PlanRequest) (*plan.Plan, meter.Usage, error) {
	m := meter.NewTurns(req.MaxTurns)
	if f.replay(m, req.OnEvent) {
		return nil, m.Usage(), ErrBudgetExhausted
	}
	if f.PlanErr != nil {
		return nil, m.Usage(), f.PlanErr
	}
	return f.PlanOut, m.Usage(), nil
}

func (f *Fake) Execute(_ context.Context, req ExecuteRequest) (*PatchSeries, meter.Usage, error) {
	m := meter.NewTurns(req.MaxTurns)
	if f.replay(m, req.OnEvent) {
		return nil, m.Usage(), ErrBudgetExhausted
	}
	if f.ExecErr != nil {
		return nil, m.Usage(), f.ExecErr
	}
	return f.Series, m.Usage(), nil
}
