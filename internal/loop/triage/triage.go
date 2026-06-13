// Package triage uses an AI provider to classify findings as genuine or
// false positives before the auto-fix loop attempts remediation.
//
// Per the spec's Core Principle: triage NEVER removes a finding. It only
// sets a finding's Status to false_positive (with a reason) and marks
// TriagedBy="ai" so a human can review and override. Findings the AI
// dismisses are excluded from the loop's success counting.
//
// Fail-safe: any provider or parse error leaves every finding valid —
// triage never auto-dismisses on uncertainty.
package triage

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/ai"
	"github.com/alphabravocompany/thewolf/internal/models"
)

// Decision is the AI's verdict on one finding.
type Decision struct {
	FindingID string `json:"id"`
	Valid     bool   `json:"valid"`
	Reason    string `json:"reason"`
}

// Triage asks the provider to classify each finding. The returned slice
// has one Decision per input finding (in input order). On any provider
// or parse error, every finding is returned Valid=true and the error is
// surfaced so the caller can log it — the loop continues fail-safe.
func Triage(ctx context.Context, provider ai.Provider, findings []models.Finding) ([]Decision, error) {
	decisions := allValid(findings)
	if provider == nil || provider.Name() == "noop" || len(findings) == 0 {
		return decisions, nil
	}

	raw, err := provider.Complete(ctx, buildTriagePrompt(findings))
	if err != nil {
		return decisions, fmt.Errorf("triage provider %s: %w", provider.Name(), err)
	}

	parsed, perr := parseDecisions(raw)
	if perr != nil {
		return decisions, fmt.Errorf("triage parse: %w", perr)
	}

	// Merge parsed verdicts onto the fail-safe baseline by finding ID.
	byID := make(map[string]Decision, len(parsed))
	for _, d := range parsed {
		byID[d.FindingID] = d
	}
	for i := range decisions {
		if d, ok := byID[decisions[i].FindingID]; ok {
			decisions[i].Valid = d.Valid
			decisions[i].Reason = strings.TrimSpace(d.Reason)
		}
	}
	return decisions, nil
}

// Apply records the AI's false-positive verdicts on the findings slice:
// a finding the AI deems invalid gets Status=false_positive, the reason,
// and TriagedBy="ai". Genuine findings are left untouched. Returns the
// number of findings newly dismissed.
func Apply(findings []models.Finding, decisions []Decision) int {
	byID := make(map[string]Decision, len(decisions))
	for _, d := range decisions {
		byID[d.FindingID] = d
	}
	dismissed := 0
	for i := range findings {
		d, ok := byID[findings[i].ID]
		if !ok || d.Valid {
			continue
		}
		if findings[i].Status == models.StatusFalsePositive {
			continue // already dismissed
		}
		findings[i].Status = models.StatusFalsePositive
		findings[i].TriagedBy = "ai"
		if d.Reason != "" {
			findings[i].SuppressedReason = "ai-triage: " + d.Reason
		}
		dismissed++
	}
	return dismissed
}

// CountValid returns the findings NOT dismissed as false positives —
// the set the loop's success counting uses.
func CountValid(findings []models.Finding) int {
	n := 0
	for _, f := range findings {
		if f.Status != models.StatusFalsePositive {
			n++
		}
	}
	return n
}

func allValid(findings []models.Finding) []Decision {
	out := make([]Decision, len(findings))
	for i, f := range findings {
		out[i] = Decision{FindingID: f.ID, Valid: true}
	}
	return out
}

func buildTriagePrompt(findings []models.Finding) string {
	var b strings.Builder
	b.WriteString("You are triaging static-analysis findings. For each ")
	b.WriteString("finding decide whether it is a GENUINE issue or a FALSE ")
	b.WriteString("POSITIVE (e.g. test fixture, sample data, an intentional ")
	b.WriteString("pattern). Do NOT invent findings. Respond with ONLY a JSON ")
	b.WriteString("array: [{\"id\":\"...\",\"valid\":true|false,\"reason\":\"...\"}].\n\n")
	for _, f := range findings {
		fmt.Fprintf(&b, "- id=%s severity=%s tool=%s file=%s:%d title=%q\n",
			f.ID, f.Severity, f.ToolName, f.FilePath, f.LineStart, f.Title)
	}
	return b.String()
}

// parseDecisions extracts the JSON decision array from an LLM response,
// tolerating ``` fences and surrounding prose.
func parseDecisions(raw string) ([]Decision, error) {
	s := strings.TrimSpace(raw)
	if i := strings.Index(s, "```"); i >= 0 {
		rest := s[i+3:]
		if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
			rest = rest[nl+1:]
		}
		if end := strings.Index(rest, "```"); end >= 0 {
			s = strings.TrimSpace(rest[:end])
		}
	}
	start := strings.IndexByte(s, '[')
	end := strings.LastIndexByte(s, ']')
	if start < 0 || end < start {
		return nil, fmt.Errorf("no JSON array in response")
	}
	var decisions []Decision
	if err := json.Unmarshal([]byte(s[start:end+1]), &decisions); err != nil {
		return nil, err
	}
	return decisions, nil
}
