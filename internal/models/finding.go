package models

import "time"

// Finding represents a single issue found by a static analysis tool.
type Finding struct {
	ID                string   `json:"id" db:"id"`
	ScanID            string   `json:"scan_id" db:"scan_id"`
	RepoID            string   `json:"repo_id" db:"repo_id"`
	Fingerprint       string   `json:"fingerprint" db:"fingerprint"`
	ToolName          string   `json:"tool_name" db:"tool_name"`
	Category          Category `json:"category" db:"category"`
	Severity          Severity `json:"severity" db:"severity"`
	Title             string   `json:"title" db:"title"`
	Description       string   `json:"description" db:"description"`
	FilePath          string   `json:"file_path" db:"file_path"`
	LineStart         int      `json:"line_start" db:"line_start"`
	LineEnd           int      `json:"line_end" db:"line_end"`
	CodeSnippet       string   `json:"code_snippet" db:"code_snippet"`
	CWEID             string   `json:"cwe_id,omitempty" db:"cwe_id"`
	RuleID            string   `json:"rule_id,omitempty" db:"rule_id"`
	ToolSeverityScore float64  `json:"tool_severity_score" db:"tool_severity_score"`
	LocationWeight    float64  `json:"location_weight" db:"location_weight"`
	AIContextScore    float64  `json:"ai_context_score" db:"ai_context_score"`
	CompositeScore    float64  `json:"composite_score" db:"composite_score"`
	AIFixSuggestion   string   `json:"ai_fix_suggestion,omitempty" db:"ai_fix_suggestion"`
	ModuleName        string   `json:"module_name" db:"module_name"`
	FunctionName      string   `json:"function_name" db:"function_name"`
	SymbolKind        string   `json:"symbol_kind" db:"symbol_kind"`
	FilePurpose       string   `json:"file_purpose" db:"file_purpose"`
	DependentsJSON    string   `json:"dependents_json" db:"dependents_json"`
	Status            Status   `json:"status" db:"status"`
	SARIFData         string   `json:"sarif_data,omitempty" db:"sarif_data"`
	CreatedAt         time.Time `json:"created_at" db:"created_at"`
	UpdatedAt         time.Time `json:"updated_at" db:"updated_at"`
}
