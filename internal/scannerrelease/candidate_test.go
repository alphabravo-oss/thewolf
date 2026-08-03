package scannerrelease

import "testing"

func TestEffectiveDefinitionCommit(t *testing.T) {
	t.Parallel()
	base := "1111111111111111111111111111111111111111"
	proposed := "2222222222222222222222222222222222222222"
	tests := []struct {
		name      string
		candidate *Candidate
		want      string
	}{
		{name: "nil", candidate: nil, want: ""},
		{name: "base definition", candidate: &Candidate{DefinitionCommit: base}, want: base},
		{
			name: "proposal is authoritative",
			candidate: &Candidate{
				DefinitionCommit: base,
				ProposedCommit:   proposed,
			},
			want: proposed,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := EffectiveDefinitionCommit(test.candidate); got != test.want {
				t.Fatalf("EffectiveDefinitionCommit() = %q, want %q", got, test.want)
			}
		})
	}
}
