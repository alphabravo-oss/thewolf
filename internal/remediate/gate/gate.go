// Package gate decides where a remediation session stops for human approval.
// Gates are Wolf's own checkpoints; they are independent of OpenCode's
// permission rules, which hold in every mode.
package gate

// Decision is whether a session may continue past a checkpoint.
type Decision string

const (
	DecisionProceed Decision = "proceed"
	DecisionHold    Decision = "hold"
)

// Policy is a session's gate configuration.
type Policy struct {
	PlanGate  bool
	PatchGate bool
}

// AfterPlan reports whether to hold after the triage run.
func (p Policy) AfterPlan() Decision {
	if p.PlanGate {
		return DecisionHold
	}
	return DecisionProceed
}

// AfterPatch reports whether to hold before patches land.
func (p Policy) AfterPatch() Decision {
	if p.PatchGate {
		return DecisionHold
	}
	return DecisionProceed
}

// IsYolo reports whether both gates are disabled. Yolo disables Wolf's
// approval checkpoints; the hard deny list still applies.
func (p Policy) IsYolo() bool { return !p.PlanGate && !p.PatchGate }
