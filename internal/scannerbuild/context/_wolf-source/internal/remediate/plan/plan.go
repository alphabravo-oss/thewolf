// Package plan defines the triage plan an OpenCode session emits before any
// code is written. Parsing is strict: a plan that drives a write run must be
// well-formed, so malformed output fails here rather than downstream.
package plan

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Action is what the agent intends to do with a finding.
type Action string

const (
	ActionFix  Action = "fix"
	ActionSkip Action = "skip"
)

// Item is one finding's disposition.
type Item struct {
	FindingID string   `json:"finding_id"`
	Action    Action   `json:"action"`
	Rationale string   `json:"rationale"`
	Files     []string `json:"files,omitempty"`
}

// Plan is the triage run's output.
type Plan struct {
	Summary string `json:"summary"`
	Items   []Item `json:"items"`
}

// Parse decodes and validates a plan. Unknown fields are rejected so a
// schema drift in OpenCode's output surfaces as an error, not a silent
// partial plan.
func Parse(data []byte) (*Plan, error) {
	var p Plan
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("decode plan: %w", err)
	}
	if len(p.Items) == 0 {
		return nil, fmt.Errorf("plan has no items")
	}
	for i, item := range p.Items {
		if item.FindingID == "" {
			return nil, fmt.Errorf("item %d: missing finding_id", i)
		}
		if item.Rationale == "" {
			return nil, fmt.Errorf("item %d: missing rationale", i)
		}
		switch item.Action {
		case ActionFix, ActionSkip:
		default:
			return nil, fmt.Errorf("item %d: unknown action %q", i, item.Action)
		}
	}
	return &p, nil
}

// FindingIDs returns the IDs the plan intends to fix, in plan order.
func (p *Plan) FindingIDs() []string {
	ids := make([]string, 0, len(p.Items))
	for _, item := range p.Items {
		if item.Action == ActionFix {
			ids = append(ids, item.FindingID)
		}
	}
	return ids
}
