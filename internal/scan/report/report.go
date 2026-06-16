package report

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
)

// ReportConfig holds all data needed to generate a scan report.
type ReportConfig struct {
	ScanID          string
	RepoName        string
	Branch          string
	Findings        []models.Finding
	Languages       map[string]int // language -> file count
	Frameworks      []string
	ToolsRun        []string
	ToolsFailed     map[string]error
	Duration        time.Duration
	AISummary       string
	ToolSummaries   []models.ToolSummary
	Recommendations []models.ScanRecommendation
}

type jsonToolSummary struct {
	ToolName       string          `json:"tool_name"`
	Summary        string          `json:"summary"`
	FindingCount   int             `json:"finding_count"`
	SeverityCounts map[string]int  `json:"severity_counts"`
	CriticalIssues json.RawMessage `json:"critical_issues"`
}

type jsonRecommendation struct {
	Priority       int             `json:"priority"`
	Category       string          `json:"category"`
	Title          string          `json:"title"`
	Description    string          `json:"description"`
	AffectedTools  json.RawMessage `json:"affected_tools"`
	EffortEstimate string          `json:"effort_estimate"`
}

// jsonReport is the top-level structure for JSON output.
type jsonReport struct {
	ScanID          string               `json:"scan_id"`
	RepoName        string               `json:"repo_name"`
	Branch          string               `json:"branch"`
	Date            string               `json:"date"`
	Duration        string               `json:"duration"`
	Summary         reportSummary        `json:"summary"`
	Findings        []jsonFinding        `json:"findings"`
	Tools           []toolEntry          `json:"tools"`
	AISummary       string               `json:"ai_summary,omitempty"`
	ToolSummaries   []jsonToolSummary    `json:"tool_summaries,omitempty"`
	Recommendations []jsonRecommendation `json:"recommendations,omitempty"`
}

type reportSummary struct {
	Total      int            `json:"total"`
	BySeverity map[string]int `json:"by_severity"`
	Languages  map[string]int `json:"languages"`
	Frameworks []string       `json:"frameworks"`
}

type jsonFinding struct {
	ID              string          `json:"id"`
	ToolName        string          `json:"tool_name"`
	Category        string          `json:"category"`
	Severity        string          `json:"severity"`
	Title           string          `json:"title"`
	Description     string          `json:"description"`
	FilePath        string          `json:"file_path"`
	LineStart       int             `json:"line_start"`
	LineEnd         int             `json:"line_end"`
	CodeSnippet     string          `json:"code_snippet,omitempty"`
	CWEID           string          `json:"cwe_id,omitempty"`
	RuleID          string          `json:"rule_id,omitempty"`
	CompositeScore  float64         `json:"composite_score"`
	AIFixSuggestion string          `json:"ai_fix_suggestion,omitempty"`
	ModuleName      string          `json:"module_name,omitempty"`
	FunctionName    string          `json:"function_name,omitempty"`
	SymbolKind      string          `json:"symbol_kind,omitempty"`
	FilePurpose     string          `json:"file_purpose,omitempty"`
	Dependents      json.RawMessage `json:"dependents,omitempty"`
	Status          string          `json:"status"`

	// Phase 2 / Phase 3 deterministic enrichment fields. Empty strings
	// are omitted via omitempty so older consumers still parse the file.
	FineCategory     string   `json:"fine_category,omitempty"`
	FixStrategyID    string   `json:"fix_strategy_id,omitempty"`
	Confidence       string   `json:"confidence,omitempty"`
	CorroboratedBy   []string `json:"corroborated_by,omitempty"`
	Suppressed       bool     `json:"suppressed,omitempty"`
	SuppressedReason string   `json:"suppressed_reason,omitempty"`
}

type toolEntry struct {
	Name     string `json:"name"`
	Findings int    `json:"findings"`
	Status   string `json:"status"`
}

// severityOrder defines the canonical ordering of severities for display.
var severityOrder = []models.Severity{
	models.SeverityCritical,
	models.SeverityHigh,
	models.SeverityMedium,
	models.SeverityLow,
	models.SeverityInfo,
}

// countBySeverity returns a map of severity string -> count.
func countBySeverity(findings []models.Finding) map[string]int {
	counts := make(map[string]int)
	for _, sev := range severityOrder {
		counts[string(sev)] = 0
	}
	for _, f := range findings {
		counts[string(f.Severity)]++
	}
	return counts
}

// findingsByTool groups findings by their ToolName.
func findingsByTool(findings []models.Finding) map[string][]models.Finding {
	m := make(map[string][]models.Finding)
	for _, f := range findings {
		m[f.ToolName] = append(m[f.ToolName], f)
	}
	return m
}

// filterBySeverity returns findings matching the given severity.
func filterBySeverity(findings []models.Finding, sev models.Severity) []models.Finding {
	var out []models.Finding
	for _, f := range findings {
		if f.Severity == sev {
			out = append(out, f)
		}
	}
	return out
}

// GenerateJSON produces a JSON-encoded report from the given config.
func GenerateJSON(cfg ReportConfig) ([]byte, error) {
	sevCounts := countBySeverity(cfg.Findings)
	byTool := findingsByTool(cfg.Findings)

	jFindings := make([]jsonFinding, 0, len(cfg.Findings))
	for _, f := range cfg.Findings {
		jf := jsonFinding{
			ID:               f.ID,
			ToolName:         f.ToolName,
			Category:         string(f.Category),
			Severity:         string(f.Severity),
			Title:            f.Title,
			Description:      f.Description,
			FilePath:         f.FilePath,
			LineStart:        f.LineStart,
			LineEnd:          f.LineEnd,
			CodeSnippet:      f.CodeSnippet,
			CWEID:            f.CWEID,
			RuleID:           f.RuleID,
			CompositeScore:   f.CompositeScore,
			AIFixSuggestion:  f.AIFixSuggestion,
			ModuleName:       f.ModuleName,
			FunctionName:     f.FunctionName,
			SymbolKind:       f.SymbolKind,
			FilePurpose:      f.FilePurpose,
			Status:           string(f.Status),
			FineCategory:     f.FineCategory,
			FixStrategyID:    f.FixStrategyID,
			Confidence:       f.Confidence,
			CorroboratedBy:   f.CorroboratedBy,
			Suppressed:       f.Suppressed,
			SuppressedReason: f.SuppressedReason,
		}
		if f.DependentsJSON != "" {
			jf.Dependents = json.RawMessage(f.DependentsJSON)
		}
		jFindings = append(jFindings, jf)
	}

	tools := buildToolEntries(cfg.ToolsRun, cfg.ToolsFailed, byTool)

	frameworks := cfg.Frameworks
	if frameworks == nil {
		frameworks = []string{}
	}

	// Map tool summaries
	var jToolSummaries []jsonToolSummary
	for _, ts := range cfg.ToolSummaries {
		jts := jsonToolSummary{
			ToolName:     ts.ToolName,
			Summary:      ts.SummaryText,
			FindingCount: ts.FindingCount,
		}
		if ts.SeverityCounts != "" {
			_ = json.Unmarshal([]byte(ts.SeverityCounts), &jts.SeverityCounts)
		}
		if jts.SeverityCounts == nil {
			jts.SeverityCounts = make(map[string]int)
		}
		if ts.CriticalIssues != "" {
			jts.CriticalIssues = json.RawMessage(ts.CriticalIssues)
		}
		jToolSummaries = append(jToolSummaries, jts)
	}

	// Map recommendations
	var jRecs []jsonRecommendation
	for _, rec := range cfg.Recommendations {
		jr := jsonRecommendation{
			Priority:       rec.Priority,
			Category:       rec.Category,
			Title:          rec.Title,
			Description:    rec.Description,
			EffortEstimate: rec.EffortEstimate,
		}
		if rec.AffectedTools != "" {
			jr.AffectedTools = json.RawMessage(rec.AffectedTools)
		}
		jRecs = append(jRecs, jr)
	}

	rpt := jsonReport{
		ScanID:   cfg.ScanID,
		RepoName: cfg.RepoName,
		Branch:   cfg.Branch,
		Date:     time.Now().UTC().Format(time.RFC3339),
		Duration: cfg.Duration.String(),
		Summary: reportSummary{
			Total:      len(cfg.Findings),
			BySeverity: sevCounts,
			Languages:  cfg.Languages,
			Frameworks: frameworks,
		},
		Findings:        jFindings,
		Tools:           tools,
		AISummary:       cfg.AISummary,
		ToolSummaries:   jToolSummaries,
		Recommendations: jRecs,
	}

	return json.MarshalIndent(rpt, "", "  ")
}

// GenerateMarkdown produces a Markdown report string.
func GenerateMarkdown(cfg ReportConfig) (string, error) {
	return renderMarkdown(cfg)
}

// GenerateSARIF produces SARIF v2.1.0 JSON output.
func GenerateSARIF(cfg ReportConfig) ([]byte, error) {
	return renderSARIF(cfg)
}

// buildToolEntries creates the tool status list for the report.
func buildToolEntries(toolsRun []string, toolsFailed map[string]error, byTool map[string][]models.Finding) []toolEntry {
	seen := make(map[string]bool)
	var entries []toolEntry

	// Deterministic order: sort tool names.
	names := make([]string, 0, len(toolsRun))
	names = append(names, toolsRun...)
	sort.Strings(names)

	for _, name := range names {
		if seen[name] {
			continue
		}
		seen[name] = true

		status := "success"
		if toolsFailed != nil {
			if _, failed := toolsFailed[name]; failed {
				status = fmt.Sprintf("failed: %v", toolsFailed[name])
			}
		}

		entries = append(entries, toolEntry{
			Name:     name,
			Findings: len(byTool[name]),
			Status:   status,
		})
	}

	return entries
}
