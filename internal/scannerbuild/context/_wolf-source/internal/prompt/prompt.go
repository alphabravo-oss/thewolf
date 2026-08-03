package prompt

import (
	"context"
	"fmt"
	"strings"
)

// ---------------------------------------------------------------------------
// Data types (local to avoid circular imports with internal/ai)
// ---------------------------------------------------------------------------

// FindingData holds the details of a single finding for prompt assembly.
type FindingData struct {
	Index        int
	Severity     string
	Title        string
	Description  string
	FilePath     string
	LineStart    int
	ModuleName   string
	FunctionName string
	FilePurpose  string
	Dependents   int
}

// ToolAssessData holds everything needed to build a tool-assessment prompt.
type ToolAssessData struct {
	ToolName   string
	RepoName   string
	Languages  map[string]int
	Frameworks []string
	Findings   []FindingData
}

// TopIssue represents a high-priority issue for inclusion in the executive summary.
type TopIssue struct {
	Severity     string
	ContextScore float64
	Title        string
	Impact       string
}

// ExecSummaryData holds everything needed to build an executive-summary prompt.
type ExecSummaryData struct {
	RepoName      string
	Languages     map[string]int
	Frameworks    []string
	TotalFindings int
	BySeverity    map[string]int
	ByTool        map[string]int
	ToolSummaries map[string]string
	TopIssues     []TopIssue
}

// ---------------------------------------------------------------------------
// PromptStore — interface so we don't import the db package
// ---------------------------------------------------------------------------

// PromptStore is the interface this package uses to look up user-customised
// prompt sections. Implementations live in internal/db.
type PromptStore interface {
	// ResolvePromptSection returns the prompt content for the given type,
	// section, and optional collectionID. It should implement the
	// collection -> global -> empty-string fallback chain. An empty return
	// means "use the hardcoded default".
	ResolvePromptSection(ctx context.Context, promptType, section, collectionID string) (string, error)
}

// ---------------------------------------------------------------------------
// Resolve
// ---------------------------------------------------------------------------

// Resolve looks up a prompt section following the resolution chain:
// collection-scoped -> global -> hardcoded default.
//
// store may be nil (returns the hardcoded default directly).
// collectionID may be empty (skips the collection-scoped lookup).
func Resolve(ctx context.Context, store PromptStore, promptType, section, collectionID string) string {
	if store != nil {
		if content, err := store.ResolvePromptSection(ctx, promptType, section, collectionID); err == nil && content != "" {
			return content
		}
	}
	return GetDefault(promptType, section)
}

// ---------------------------------------------------------------------------
// BuildToolAssess
// ---------------------------------------------------------------------------

// BuildToolAssess assembles a complete tool-assessment prompt from the three
// resolved template sections and the injected data. The layout is:
//
//  1. System context
//  2. Data injection (repo info + findings list)
//  3. Scoring criteria
//  4. Output instructions
func BuildToolAssess(systemCtx, scoringCriteria, outputInstructions string, data ToolAssessData) string {
	var b strings.Builder

	// 1. System context
	b.WriteString(systemCtx)
	b.WriteString("\n\n")

	// 2. Data injection — mirrors the format from ai.BuildToolAssessPrompt
	fmt.Fprintf(&b, "Assess %s findings for \"%s\".", data.ToolName, data.RepoName)

	if len(data.Languages) > 0 {
		b.WriteString(" Languages: ")
		parts := make([]string, 0, len(data.Languages))
		for lang, count := range data.Languages {
			parts = append(parts, fmt.Sprintf("%s(%d)", lang, count))
		}
		b.WriteString(strings.Join(parts, ", "))
	}

	if len(data.Frameworks) > 0 {
		b.WriteString(" Frameworks: ")
		b.WriteString(strings.Join(data.Frameworks, ", "))
	}

	b.WriteString("\n\nFindings:\n")

	for _, f := range data.Findings {
		desc := f.Description
		if len(desc) > 80 {
			desc = desc[:80]
		}
		fmt.Fprintf(&b, "%d. [%s] %s — %s:%d", f.Index, f.Severity, f.Title, f.FilePath, f.LineStart)
		if f.ModuleName != "" {
			fmt.Fprintf(&b, " [mod:%s", f.ModuleName)
			if f.FunctionName != "" {
				fmt.Fprintf(&b, " fn:%s", f.FunctionName)
			}
			if f.FilePurpose != "" {
				fmt.Fprintf(&b, " role:%s", f.FilePurpose)
			}
			if f.Dependents > 0 {
				fmt.Fprintf(&b, " deps:%d", f.Dependents)
			}
			b.WriteString("]")
		}
		if desc != "" && desc != f.Title {
			fmt.Fprintf(&b, " | %s", desc)
		}
		b.WriteString("\n")
	}

	// 3. Scoring criteria
	b.WriteString("\n")
	b.WriteString(scoringCriteria)
	b.WriteString("\n\n")

	// 4. Output instructions
	b.WriteString(outputInstructions)
	b.WriteString("\n")

	return b.String()
}

// ---------------------------------------------------------------------------
// BuildExecSummary
// ---------------------------------------------------------------------------

// BuildExecSummary assembles a complete executive-summary prompt from the three
// resolved template sections and the injected data. The layout is:
//
//  1. System context
//  2. Data injection (severity breakdown, tool summaries, top issues)
//  3. Scoring criteria
//  4. Output instructions
func BuildExecSummary(systemCtx, scoringCriteria, outputInstructions string, data ExecSummaryData) string {
	var b strings.Builder

	// 1. System context
	b.WriteString(systemCtx)
	b.WriteString("\n\n")

	// 2. Data injection — mirrors the format from ai.BuildFinalSummaryPrompt
	fmt.Fprintf(&b, "Executive summary for security scan of \"%s\". %d findings.\n", data.RepoName, data.TotalFindings)

	if len(data.Languages) > 0 {
		b.WriteString("Languages: ")
		parts := make([]string, 0, len(data.Languages))
		for lang, count := range data.Languages {
			parts = append(parts, fmt.Sprintf("%s(%d)", lang, count))
		}
		b.WriteString(strings.Join(parts, ", "))
		b.WriteString("\n")
	}

	if len(data.Frameworks) > 0 {
		b.WriteString("Frameworks: ")
		b.WriteString(strings.Join(data.Frameworks, ", "))
		b.WriteString("\n")
	}

	if len(data.BySeverity) > 0 {
		b.WriteString("Severity: ")
		parts := make([]string, 0, len(data.BySeverity))
		for sev, count := range data.BySeverity {
			parts = append(parts, fmt.Sprintf("%s=%d", sev, count))
		}
		b.WriteString(strings.Join(parts, ", "))
		b.WriteString("\n")
	}

	if len(data.ToolSummaries) > 0 {
		b.WriteString("\nTool results:\n")
		for tool, summary := range data.ToolSummaries {
			count := data.ByTool[tool]
			fmt.Fprintf(&b, "- %s (%d): %s\n", tool, count, summary)
		}
	}

	if len(data.TopIssues) > 0 {
		b.WriteString("\nTop issues:\n")
		for i, ci := range data.TopIssues {
			fmt.Fprintf(&b, "%d. [%s %.0f/10] %s — %s\n", i+1, ci.Severity, ci.ContextScore, ci.Title, ci.Impact)
		}
	}

	// 3. Scoring criteria
	b.WriteString("\n")
	b.WriteString(scoringCriteria)
	b.WriteString("\n\n")

	// 4. Output instructions
	b.WriteString(outputInstructions)
	b.WriteString("\n")

	return b.String()
}
