package policyapi

// Evaluator is the public quality-gate contract. Community uses
// internal/finding/gates. Enterprise may register additional evaluators.
type Evaluator interface {
	Name() string
	Evaluate(findings []map[string]any) (status string, err error)
}

type Nop struct{}

func (Nop) Name() string { return "nop" }

func (Nop) Evaluate([]map[string]any) (string, error) { return "skip", nil }
