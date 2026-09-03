// Package intelligence is the public evidence contract for attack paths and
// cited investigation. Community schemas stay compatible; Enterprise registers
// a Correlator. Community corroboration is not replaced.
package intelligence

import (
	"context"
	"fmt"
)

type EvidenceRef struct {
	FindingID string `json:"finding_id"`
	Tool      string `json:"tool,omitempty"`
	Title     string `json:"title,omitempty"`
	File      string `json:"file,omitempty"`
	Line      int    `json:"line,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

type Citation struct {
	FindingID string `json:"finding_id"`
	Note      string `json:"note,omitempty"`
}

type Node struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Label string `json:"label"`
}

type Edge struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Reason string `json:"reason"`
}

type Path struct {
	VulnerabilityID string     `json:"vulnerability_id"`
	Summary         string     `json:"summary"`
	Nodes           []Node     `json:"nodes"`
	Edges           []Edge     `json:"edges,omitempty"`
	Citations       []Citation `json:"citations"`
}

type Investigation struct {
	VulnerabilityID string     `json:"vulnerability_id"`
	Answer          string     `json:"answer"`
	Citations       []Citation `json:"citations"`
}

type Input struct {
	VulnerabilityID string
	Title           string
	Evidence        []EvidenceRef
	Question        string
}

type Correlator interface {
	AttackPath(ctx context.Context, in Input) (Path, error)
	Investigate(ctx context.Context, in Input) (Investigation, error)
}

// Consensus builds a path only from supplied evidence. It does not invent
// runtime, cloud, or network hops.
type Consensus struct{}

func (Consensus) AttackPath(_ context.Context, in Input) (Path, error) {
	p := Path{
		VulnerabilityID: in.VulnerabilityID,
		Summary:         "Scanner consensus from clustered evidence; not a runtime attack path.",
	}
	if len(in.Evidence) == 0 {
		p.Summary = "No evidence members; no path."
		return p, nil
	}
	for i, e := range in.Evidence {
		id := e.FindingID
		if id == "" {
			id = fmt.Sprintf("e%d", i)
		}
		label := e.Tool
		if e.File != "" {
			if label != "" {
				label += " · "
			}
			label += e.File
			if e.Line > 0 {
				label += fmt.Sprintf(":%d", e.Line)
			}
		}
		if label == "" {
			label = e.Title
		}
		if label == "" {
			label = id
		}
		p.Nodes = append(p.Nodes, Node{ID: id, Kind: "evidence", Label: label})
		p.Citations = append(p.Citations, Citation{FindingID: e.FindingID, Note: e.Reason})
		if i > 0 {
			p.Edges = append(p.Edges, Edge{
				From:   p.Nodes[i-1].ID,
				To:     id,
				Reason: "same identity cluster",
			})
		}
	}
	return p, nil
}

func (Consensus) Investigate(_ context.Context, in Input) (Investigation, error) {
	inv := Investigation{
		VulnerabilityID: in.VulnerabilityID,
		Answer:          "Investigation is limited to cited scanner evidence. No additional hops were inferred.",
	}
	if in.Title != "" {
		inv.Answer = "Evidence for " + in.Title + ". " + inv.Answer
	}
	for _, e := range in.Evidence {
		inv.Citations = append(inv.Citations, Citation{FindingID: e.FindingID, Note: e.Tool})
	}
	if len(inv.Citations) == 0 {
		inv.Answer = "No evidence citations available."
	}
	return inv, nil
}
