package orchestrator

import (
	"fmt"
	"sort"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/models"
)

// ToolReport is the per-scanner rollup shown on the agent page.
type ToolReport struct {
	Tool      string         `json:"tool"`
	Total     int            `json:"total"`
	Kept      int            `json:"kept"`
	Open      int            `json:"open"`
	Unfixable int            `json:"unfixable"`
	Muted     int            `json:"muted"`
	Deferred  int            `json:"deferred"`
	Reasons   map[string]int `json:"reasons,omitempty"`
}

// FindingNote is one finding's final disposition for the human review report.
type FindingNote struct {
	ID       string `json:"id"`
	Tool     string `json:"tool"`
	Severity string `json:"severity,omitempty"`
	File     string `json:"file,omitempty"`
	Title    string `json:"title,omitempty"`
	Outcome  string `json:"outcome"` // kept | open | unfixable | muted | deferred
	Reason   string `json:"reason"`
}

const maxListedOpen = 80

type jobReport struct {
	notes map[string]FindingNote
}

func newJobReport() *jobReport {
	return &jobReport{notes: map[string]FindingNote{}}
}

func (r *jobReport) note(f models.Finding, outcome, reason string) {
	if r == nil || f.ID == "" {
		return
	}
	r.notes[f.ID] = FindingNote{
		ID:       f.ID,
		Tool:     strings.TrimSpace(f.ToolName),
		Severity: strings.TrimSpace(string(f.Severity)),
		File:     strings.TrimSpace(f.FilePath),
		Title:    strings.TrimSpace(f.Title),
		Outcome:  outcome,
		Reason:   reason,
	}
}

func (r *jobReport) noteID(id, tool, outcome, reason string) {
	if r == nil || id == "" {
		return
	}
	n := r.notes[id]
	n.ID = id
	if tool != "" {
		n.Tool = tool
	}
	n.Outcome = outcome
	n.Reason = reason
	r.notes[id] = n
}

func (r *jobReport) tools() []ToolReport {
	if r == nil {
		return nil
	}
	index := map[string]int{}
	var out []ToolReport
	for _, n := range r.notes {
		tool := n.Tool
		if tool == "" {
			tool = "unknown"
		}
		i, ok := index[tool]
		if !ok {
			i = len(out)
			index[tool] = i
			out = append(out, ToolReport{Tool: tool, Reasons: map[string]int{}})
		}
		out[i].Total++
		switch n.Outcome {
		case models.FixOutcomeKept:
			out[i].Kept++
		case models.FixOutcomeUnfixable:
			out[i].Unfixable++
		case models.FixOutcomeMuted:
			out[i].Muted++
		case "deferred":
			out[i].Deferred++
		default:
			out[i].Open++
		}
		if n.Reason != "" && n.Outcome != models.FixOutcomeKept {
			out[i].Reasons[n.Reason]++
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Kept != out[j].Kept {
			return out[i].Kept > out[j].Kept
		}
		return out[i].Tool < out[j].Tool
	})
	return out
}

func (r *jobReport) openList() []FindingNote {
	if r == nil {
		return nil
	}
	var out []FindingNote
	for _, n := range r.notes {
		if n.Outcome == models.FixOutcomeKept {
			continue
		}
		out = append(out, n)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if ra, rb := severityRank(models.Severity(out[i].Severity)), severityRank(models.Severity(out[j].Severity)); ra != rb {
			return ra > rb
		}
		if out[i].Tool != out[j].Tool {
			return out[i].Tool < out[j].Tool
		}
		return out[i].File < out[j].File
	})
	if len(out) > maxListedOpen {
		return out[:maxListedOpen]
	}
	return out
}

func (r *jobReport) markdown() string {
	tools := r.tools()
	var b strings.Builder
	b.WriteString("## Fix report\n\n")
	b.WriteString("| Tool | Total | Fixed | Open | Skipped | Muted | Deferred |\n")
	b.WriteString("|---|---:|---:|---:|---:|---:|---:|\n")
	for _, t := range tools {
		fmt.Fprintf(&b, "| %s | %d | %d | %d | %d | %d | %d |\n",
			t.Tool, t.Total, t.Kept, t.Open, t.Unfixable, t.Muted, t.Deferred)
	}
	b.WriteString("\n### Why not fixed\n\n")
	reasonCounts := map[string]int{}
	for _, n := range r.notes {
		if n.Outcome == models.FixOutcomeKept || n.Reason == "" {
			continue
		}
		reasonCounts[n.Reason]++
	}
	type kv struct {
		k string
		v int
	}
	var reasons []kv
	for k, v := range reasonCounts {
		reasons = append(reasons, kv{k, v})
	}
	sort.Slice(reasons, func(i, j int) bool {
		if reasons[i].v != reasons[j].v {
			return reasons[i].v > reasons[j].v
		}
		return reasons[i].k < reasons[j].k
	})
	for _, rsn := range reasons {
		fmt.Fprintf(&b, "- %s (%d)\n", rsn.k, rsn.v)
	}
	listed := r.openList()
	if len(listed) == 0 {
		return b.String()
	}
	b.WriteString("\nHighest-severity leftovers:\n\n")
	for _, n := range listed {
		label := n.Title
		if label == "" {
			label = n.ID
		}
		file := n.File
		if file == "" {
			file = "(no file)"
		}
		fmt.Fprintf(&b, "- [%s] %s %s — %s — %s\n", n.Severity, n.Tool, file, label, n.Reason)
	}
	return b.String()
}
