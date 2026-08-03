// Package enrich turns wolf findings into AI-ready remediation prompts.
//
// The default path is fully deterministic: BuildPrompt assembles a fixed
// five-section prompt from data wolf already has on the finding — no AI
// call, no cost, no variability. An optional AI-authored guidance layer
// (see guidance.go) sits on top when a provider is configured.
//
// Core principle: enrichment never invents findings. It only describes
// findings wolf already reported.
package enrich

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/models"
)

// BuildPrompt produces the deterministic remediation prompt for a finding.
//
// The output has five fixed sections — Problem, Location, Repo context,
// Task, Acceptance criteria — and is a pure function of the finding:
// the same finding always yields the same prompt. Missing optional fields
// are omitted rather than rendered blank.
func BuildPrompt(f models.Finding) string {
	var b strings.Builder

	// ── Problem ───────────────────────────────────────────────────
	b.WriteString("## Problem\n")
	fmt.Fprintf(&b, "%s severity %s finding from %s: %s\n",
		titleCase(string(f.Severity)),
		categoryLabel(f),
		orNone(f.ToolName),
		strings.TrimSpace(f.Title))
	if d := strings.TrimSpace(f.Description); d != "" {
		b.WriteString("\n")
		b.WriteString(d)
		b.WriteString("\n")
	}

	// ── Location ──────────────────────────────────────────────────
	b.WriteString("\n## Location\n")
	fmt.Fprintf(&b, "File: %s%s\n", orNone(f.FilePath), lineRange(f))
	if f.FunctionName != "" {
		kind := f.SymbolKind
		if kind == "" {
			kind = "symbol"
		}
		fmt.Fprintf(&b, "Function: %s (%s)\n", f.FunctionName, kind)
	}
	if f.ModuleName != "" {
		fmt.Fprintf(&b, "Module: %s\n", f.ModuleName)
	}
	if snippet := strings.TrimRight(f.CodeSnippet, "\n"); snippet != "" {
		b.WriteString("\n```\n")
		b.WriteString(snippet)
		b.WriteString("\n```\n")
	}

	// ── Repo context ──────────────────────────────────────────────
	ctx := repoContextLines(f)
	if len(ctx) > 0 {
		b.WriteString("\n## Repo context\n")
		for _, line := range ctx {
			b.WriteString(line)
			b.WriteString("\n")
		}
	}

	// ── Task ──────────────────────────────────────────────────────
	b.WriteString("\n## Task\n")
	fmt.Fprintf(&b, "Remediate the %s issue described above. ", categoryLabel(f))
	b.WriteString("Make the smallest change that resolves the issue without altering unrelated behavior. ")
	b.WriteString("If the finding is a false positive (e.g. test fixture, sample data, intentional pattern), explain why instead of changing code.\n")

	// ── Acceptance criteria ───────────────────────────────────────
	b.WriteString("\n## Acceptance criteria\n")
	loc := orNone(f.FilePath) + lineRange(f)
	fmt.Fprintf(&b, "- The issue at %s is resolved or proven to be a false positive.\n", loc)
	b.WriteString("- Existing behavior and tests still pass.\n")
	b.WriteString("- No new findings are introduced by the change.\n")
	if f.RuleID != "" {
		fmt.Fprintf(&b, "- A re-scan no longer reports rule %s at this location.\n", f.RuleID)
	}

	return b.String()
}

// repoContextLines collects the optional context bullets, in a stable order.
func repoContextLines(f models.Finding) []string {
	var lines []string
	if f.FilePurpose != "" {
		lines = append(lines, "- File purpose: "+f.FilePurpose)
	}
	if f.CWEID != "" {
		lines = append(lines, "- CWE: "+f.CWEID)
	}
	if f.RuleID != "" {
		lines = append(lines, "- Rule: "+f.RuleID)
	}
	if f.FineCategory != "" {
		lines = append(lines, "- Issue class: "+f.FineCategory)
	}
	if len(f.CorroboratedBy) > 0 {
		tools := append([]string(nil), f.CorroboratedBy...)
		sort.Strings(tools)
		lines = append(lines, "- Corroborated by: "+strings.Join(tools, ", "))
	}
	if deps := formatDependents(f.DependentsJSON); deps != "" {
		lines = append(lines, "- Dependents: "+deps)
	}
	return lines
}

// formatDependents renders the dependents graph JSON as a compact, stable
// string. Accepts either a JSON array of strings or a JSON object whose
// keys are dependent paths. Returns "" when the JSON is empty or invalid.
func formatDependents(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" || raw == "[]" || raw == "{}" {
		return ""
	}
	var arr []string
	if err := json.Unmarshal([]byte(raw), &arr); err == nil {
		if len(arr) == 0 {
			return ""
		}
		sort.Strings(arr)
		return strings.Join(arr, ", ")
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &obj); err == nil {
		keys := make([]string, 0, len(obj))
		for k := range obj {
			keys = append(keys, k)
		}
		if len(keys) == 0 {
			return ""
		}
		sort.Strings(keys)
		return strings.Join(keys, ", ")
	}
	return ""
}

// categoryLabel prefers the fine-grained category, falling back to the
// coarse category, then a generic word.
func categoryLabel(f models.Finding) string {
	if f.FineCategory != "" {
		return f.FineCategory
	}
	if f.Category != "" {
		return string(f.Category)
	}
	return "code-quality"
}

func lineRange(f models.Finding) string {
	switch {
	case f.LineStart <= 0:
		return ""
	case f.LineEnd > f.LineStart:
		return fmt.Sprintf(":%d-%d", f.LineStart, f.LineEnd)
	default:
		return fmt.Sprintf(":%d", f.LineStart)
	}
}

func titleCase(s string) string {
	if s == "" {
		return "Unspecified"
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(unknown)"
	}
	return s
}
