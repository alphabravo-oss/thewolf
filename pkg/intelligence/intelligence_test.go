package intelligence

import (
	"context"
	"testing"
)

func TestConsensusPathCitesEvidenceOnly(t *testing.T) {
	p, err := (Consensus{}).AttackPath(context.Background(), Input{
		VulnerabilityID: "v1",
		Evidence: []EvidenceRef{
			{FindingID: "f1", Tool: "semgrep", File: "a.go", Line: 10},
			{FindingID: "f2", Tool: "gosec", File: "a.go", Line: 10},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Nodes) != 2 || len(p.Citations) != 2 || len(p.Edges) != 1 {
		t.Fatalf("path = %+v", p)
	}
	if p.Edges[0].Reason != "same identity cluster" {
		t.Fatalf("edge = %+v", p.Edges[0])
	}
}

func TestConsensusInvestigateDoesNotInvent(t *testing.T) {
	inv, err := (Consensus{}).Investigate(context.Background(), Input{
		Title:    "xss",
		Evidence: []EvidenceRef{{FindingID: "f1", Tool: "semgrep"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Citations) != 1 || inv.Citations[0].FindingID != "f1" {
		t.Fatalf("inv = %+v", inv)
	}
}
