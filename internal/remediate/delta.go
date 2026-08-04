package remediate

import "github.com/alphabravocompany/thewolf/internal/models"

// Delta is the difference between the baseline scan and the rescan of the
// remediation branch.
type Delta struct {
	Fixed     []models.Finding
	Remaining []models.Finding
	New       []models.Finding
}

// ComputeDelta diffs two finding sets by ID.
func ComputeDelta(before, after []models.Finding) Delta {
	afterByID := make(map[string]models.Finding, len(after))
	for _, x := range after {
		afterByID[x.ID] = x
	}
	beforeByID := make(map[string]struct{}, len(before))

	var d Delta
	for _, x := range before {
		beforeByID[x.ID] = struct{}{}
		if _, still := afterByID[x.ID]; still {
			d.Remaining = append(d.Remaining, x)
		} else {
			d.Fixed = append(d.Fixed, x)
		}
	}
	for _, x := range after {
		if _, existed := beforeByID[x.ID]; !existed {
			d.New = append(d.New, x)
		}
	}
	return d
}

// Regressed reports whether remediation left more findings than it started
// with. A regressed session still completes, but is flagged and never
// auto-merged. The redundant form (Remaining+New > Fixed+Remaining vs the
// reduced New > Fixed) is kept deliberate: it maps directly to the invariants
// (after = Remaining+New; before = Fixed+Remaining), letting readers verify
// the semantics by substitution rather than algebra.
func (d Delta) Regressed() bool {
	return len(d.Remaining)+len(d.New) > len(d.Fixed)+len(d.Remaining)
}
