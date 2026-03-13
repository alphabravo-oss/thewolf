package ai

import (
	"context"
	"fmt"
	"strings"
)

// Provider defines the interface for AI-powered analysis of security findings.
type Provider interface {
	Name() string
	Analyze(ctx context.Context, req AnalyzeRequest) (*AnalyzeResponse, error)
	Score(ctx context.Context, req ScoreRequest) (*ScoreResponse, error)
	Summarize(ctx context.Context, req SummarizeRequest) (string, error)
	// Complete sends a free-form prompt and returns the raw text response.
	// It is used for tasks like semantic annotation that don't fit the
	// structured Analyze/Score/Summarize methods.
	Complete(ctx context.Context, prompt string) (string, error)
}

// AICallLog captures metadata from a single AI provider call for logging.
type AICallLog struct {
	Provider       string
	Model          string
	Prompt         string
	Response       string
	Error          string
	DurationMs     int64
	PromptTokens   int
	ResponseTokens int
	CostUSD        float64
}

// EstimateTokens approximates token count from string length (~4 chars/token).
func EstimateTokens(s string) int {
	return (len(s) + 3) / 4
}

// LogCallback is a function that receives AI call metadata after each provider call.
type LogCallback func(entry AICallLog)

// SetLogCallback configures the logging callback on providers that support it.
// Providers that don't support callbacks silently ignore this.
func SetLogCallback(p Provider, cb LogCallback) {
	type logCallbackSetter interface {
		SetLogCallback(LogCallback)
	}
	if s, ok := p.(logCallbackSetter); ok {
		s.SetLogCallback(cb)
	}
}

// AnalyzeRequest contains a single finding plus repository context for analysis.
type AnalyzeRequest struct {
	Finding     FindingContext
	RepoContext string // repo map summary
}

// FindingContext holds the details of a single security or quality finding.
type FindingContext struct {
	ToolName     string `json:"tool_name"`
	Severity     string `json:"severity"`
	Title        string `json:"title"`
	Description  string `json:"description"`
	FilePath     string `json:"file_path"`
	LineStart    int    `json:"line_start"`
	CodeSnippet  string `json:"code_snippet,omitempty"`
	ModuleName   string `json:"module_name,omitempty"`
	FunctionName string `json:"function_name,omitempty"`
	FilePurpose  string `json:"file_purpose,omitempty"`
	Dependents   int    `json:"dependents,omitempty"`
}

// AnalyzeResponse contains the AI's analysis of a single finding.
type AnalyzeResponse struct {
	FixSuggestion string  `json:"fix_suggestion"`
	ContextScore  float64 `json:"context_score"` // 0-10
	Explanation   string  `json:"explanation"`
}

// ScoreRequest contains multiple findings for batch scoring.
type ScoreRequest struct {
	Findings    []FindingContext
	RepoContext string
}

// ScoreResponse wraps the scored results for a batch of findings.
type ScoreResponse struct {
	Scores []FindingScore `json:"scores"`
}

// FindingScore represents the AI-assigned contextual score for a single finding.
type FindingScore struct {
	Index        int     `json:"index"`
	ContextScore float64 `json:"context_score"`
	Explanation  string  `json:"explanation"`
}

// NewProvider creates a Provider based on the engine name and API key.
// Supported engines: "anthropic", "openai" (require API key),
// "claude-code", "codex" (use local CLI, no key needed).
// Returns NoopProvider if the engine is unknown or a key-based engine has no key.
func NewProvider(engine, apiKey string) Provider {
	// CLI-based engines don't need an API key
	if IsCLIEngine(engine) {
		return NewCLIProvider(engine)
	}
	if apiKey == "" {
		return NewNoopProvider()
	}
	switch strings.ToLower(engine) {
	case "anthropic":
		return NewAnthropicProvider(apiKey)
	case "openai":
		return NewOpenAIProvider(apiKey)
	default:
		return NewNoopProvider()
	}
}

// SummarizeRequest holds all the data needed to produce a scan summary.
type SummarizeRequest struct {
	ScanID        string
	RepoName      string
	TotalFindings int
	BySeverity    map[string]int
	ByCategory    map[string]int
	ByTool        map[string]int
	TopFindings   []FindingContext
	Languages     map[string]int
	Frameworks    []string
}

// ToolAssessRequest contains one tool's findings for per-tool assessment.
type ToolAssessRequest struct {
	ToolName   string
	RepoName   string
	Languages  map[string]int
	Frameworks []string
	Findings   []FindingContext // only findings from this tool
}

// ToolAssessResponse is the AI's assessment of a single tool's findings.
type ToolAssessResponse struct {
	ToolSummary    string          `json:"tool_summary"`
	CriticalIssues []AssessedIssue `json:"critical_issues"`
	FindingScores  []FindingScore  `json:"finding_scores"`
}

// AssessedIssue is a finding the AI flagged as requiring attention.
type AssessedIssue struct {
	FindingIndex  int     `json:"finding_index"`
	Title         string  `json:"title"`
	Severity      string  `json:"severity"`
	ContextScore  float64 `json:"context_score"`
	Impact        string  `json:"impact"`
	FixSuggestion string  `json:"fix_suggestion"`
}

// FinalSummaryRequest contains per-tool summaries for the final rollup.
type FinalSummaryRequest struct {
	RepoName      string
	Languages     map[string]int
	Frameworks    []string
	TotalFindings int
	BySeverity    map[string]int
	ByTool        map[string]int
	ToolSummaries map[string]string // tool name → AI-generated tool summary
	TopIssues     []AssessedIssue   // top critical issues across all tools
}

// StructuredRecommendation represents a structured, actionable recommendation.
type StructuredRecommendation struct {
	Priority       int      `json:"priority"`
	Category       string   `json:"category"`
	Title          string   `json:"title"`
	Description    string   `json:"description"`
	AffectedTools  []string `json:"affected_tools"`
	EffortEstimate string   `json:"effort_estimate"`
}

// FinalSummaryResponse is the overall scan summary.
type FinalSummaryResponse struct {
	Summary         string                     `json:"summary"`
	Recommendations []string                   `json:"recommendations"`
	StructuredRecs  []StructuredRecommendation `json:"structured_recommendations"`
}

// BuildToolAssessPrompt creates a compact prompt to assess one tool's findings.
func BuildToolAssessPrompt(req ToolAssessRequest) string {
	var b strings.Builder

	fmt.Fprintf(&b, "Assess %s findings for \"%s\".", req.ToolName, req.RepoName)

	if len(req.Languages) > 0 {
		b.WriteString(" Languages: ")
		parts := make([]string, 0, len(req.Languages))
		for lang, count := range req.Languages {
			parts = append(parts, fmt.Sprintf("%s(%d)", lang, count))
		}
		b.WriteString(strings.Join(parts, ", "))
	}
	b.WriteString("\n\nFindings:\n")

	for i, f := range req.Findings {
		desc := f.Description
		if len(desc) > 80 {
			desc = desc[:80]
		}
		fmt.Fprintf(&b, "%d. [%s] %s — %s:%d", i, f.Severity, f.Title, f.FilePath, f.LineStart)
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

	b.WriteString(`
Respond ONLY with valid JSON (no markdown fences, no extra text). Use this exact schema:
{
  "tool_summary": "brief summary of this tool's findings",
  "finding_scores": [{"index": 0, "context_score": 7}],
  "critical_issues": [{"finding_index": 0, "severity": "high", "title": "...", "impact": "...", "context_score": 9, "fix_suggestion": "..."}]
}

Score each finding 0-10 for real-world impact. Flag critical ones with fix suggestions.
`)

	return b.String()
}

// BuildFinalSummaryPrompt creates a compact prompt for the overall scan summary.
func BuildFinalSummaryPrompt(req FinalSummaryRequest) string {
	var b strings.Builder

	fmt.Fprintf(&b, "Executive summary for security scan of \"%s\". %d findings.\n", req.RepoName, req.TotalFindings)

	if len(req.BySeverity) > 0 {
		b.WriteString("Severity: ")
		parts := make([]string, 0, len(req.BySeverity))
		for sev, count := range req.BySeverity {
			parts = append(parts, fmt.Sprintf("%s=%d", sev, count))
		}
		b.WriteString(strings.Join(parts, ", "))
		b.WriteString("\n")
	}

	if len(req.ToolSummaries) > 0 {
		b.WriteString("\nTool results:\n")
		for tool, summary := range req.ToolSummaries {
			count := req.ByTool[tool]
			fmt.Fprintf(&b, "- %s (%d): %s\n", tool, count, summary)
		}
	}

	if len(req.TopIssues) > 0 {
		b.WriteString("\nTop issues:\n")
		for i, ci := range req.TopIssues {
			fmt.Fprintf(&b, "%d. [%s %.0f/10] %s — %s\n", i+1, ci.Severity, ci.ContextScore, ci.Title, ci.Impact)
		}
	}

	b.WriteString(`
Respond ONLY with valid JSON (no markdown fences, no extra text). Use this exact schema:
{
  "summary": "2-4 paragraph executive summary in markdown",
  "recommendations": ["recommendation 1", "recommendation 2"],
  "structured_recommendations": [
    {"priority": 1, "category": "security", "title": "...", "description": "...", "affected_tools": ["tool1"], "effort_estimate": "low"}
  ]
}

Provide 3-5 structured recommendations with priority (1=highest to 5=lowest), category (security/quality/dependency/config/testing), and effort_estimate (low/medium/high).
`)

	return b.String()
}


