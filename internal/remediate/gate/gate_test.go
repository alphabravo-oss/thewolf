package gate

import "testing"

func TestPolicyMatrix(t *testing.T) {
	tests := []struct {
		name                string
		planGate, patchGate bool
		wantPlan, wantPatch Decision
		wantYolo            bool
	}{
		{"both on", true, true, DecisionHold, DecisionHold, false},
		{"plan only", true, false, DecisionHold, DecisionProceed, false},
		{"patch only", false, true, DecisionProceed, DecisionHold, false},
		{"yolo", false, false, DecisionProceed, DecisionProceed, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := Policy{PlanGate: tt.planGate, PatchGate: tt.patchGate}
			if got := p.AfterPlan(); got != tt.wantPlan {
				t.Errorf("AfterPlan() = %v, want %v", got, tt.wantPlan)
			}
			if got := p.AfterPatch(); got != tt.wantPatch {
				t.Errorf("AfterPatch() = %v, want %v", got, tt.wantPatch)
			}
			if got := p.IsYolo(); got != tt.wantYolo {
				t.Errorf("IsYolo() = %v, want %v", got, tt.wantYolo)
			}
		})
	}
}
