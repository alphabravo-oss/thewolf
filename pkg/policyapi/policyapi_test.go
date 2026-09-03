package policyapi

import "testing"

func TestNopEvaluator(t *testing.T) {
	var e Evaluator = Nop{}
	st, err := e.Evaluate(nil)
	if err != nil || st != "skip" || e.Name() == "" {
		t.Fatalf("%q %v", st, err)
	}
}
