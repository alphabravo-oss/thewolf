package remediate

import (
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func f(id string) models.Finding { return models.Finding{ID: id} }

func TestComputeDelta(t *testing.T) {
	before := []models.Finding{f("a"), f("b"), f("c")}
	after := []models.Finding{f("b"), f("d")}

	d := ComputeDelta(before, after)

	if len(d.Fixed) != 2 {
		t.Errorf("Fixed = %d, want 2 (a, c)", len(d.Fixed))
	}
	if len(d.Remaining) != 1 || d.Remaining[0].ID != "b" {
		t.Errorf("Remaining = %v, want [b]", d.Remaining)
	}
	if len(d.New) != 1 || d.New[0].ID != "d" {
		t.Errorf("New = %v, want [d]", d.New)
	}
}

func TestRegressedWhenNetWorse(t *testing.T) {
	before := []models.Finding{f("a")}
	after := []models.Finding{f("a"), f("b"), f("c")}

	if !ComputeDelta(before, after).Regressed() {
		t.Fatal("Regressed() = false, want true — remediation added findings")
	}
}

func TestNotRegressedWhenNetBetter(t *testing.T) {
	before := []models.Finding{f("a"), f("b"), f("c")}
	after := []models.Finding{f("d")}

	if ComputeDelta(before, after).Regressed() {
		t.Fatal("Regressed() = true, want false — net improvement")
	}
}

func TestNotRegressedWhenNeutral(t *testing.T) {
	before := []models.Finding{f("a"), f("b")}
	after := []models.Finding{f("b"), f("c")}

	// Fixed=1 (a), Remaining=1 (b), New=1 (c): trading one finding for another.
	// Not regressed because we fixed as many as we added.
	if ComputeDelta(before, after).Regressed() {
		t.Fatal("Regressed() = true, want false — neutral trade (fixed=new)")
	}
}
