package plan

import "testing"

func TestParseValidPlan(t *testing.T) {
	data := []byte(`{
		"summary": "7 of 23 findings are actionable",
		"items": [
			{"finding_id": "f-1", "action": "fix", "rationale": "SQL injection in user query", "files": ["db/user.go"]},
			{"finding_id": "f-2", "action": "skip", "rationale": "test fixture, not shipped"}
		]
	}`)

	p, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(p.Items) != 2 {
		t.Fatalf("Items = %d, want 2", len(p.Items))
	}
	if got := p.Items[0].Action; got != ActionFix {
		t.Errorf("Items[0].Action = %q, want %q", got, ActionFix)
	}
	if got := p.FindingIDs(); len(got) != 1 || got[0] != "f-1" {
		t.Errorf("FindingIDs() = %v, want [f-1]", got)
	}
}

func TestParseRejectsInvalid(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{"empty items", `{"summary":"none","items":[]}`},
		{"unknown action", `{"summary":"s","items":[{"finding_id":"f-1","action":"delete","rationale":"r"}]}`},
		{"missing finding_id", `{"summary":"s","items":[{"action":"fix","rationale":"r"}]}`},
		{"missing rationale", `{"summary":"s","items":[{"finding_id":"f-1","action":"fix"}]}`},
		{"not json", `this is not json`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Parse([]byte(tt.data)); err == nil {
				t.Fatalf("Parse(%s) succeeded, want error", tt.data)
			}
		})
	}
}
