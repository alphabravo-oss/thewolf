package models

import (
	"slices"
	"time"
)

// Finding represents a single issue found by a static analysis tool.
type Finding struct {
	// ClusterID identifies the persisted primary; empty means provisional or legacy.
	// Members retain their original IDs and scanner evidence for existing references.
	ClusterID           string   `json:"cluster_id,omitempty" db:"cluster_id"`
	ID                  string   `json:"id" db:"id"`
	ScanID              string   `json:"scan_id" db:"scan_id"`
	RepoID              string   `json:"repo_id" db:"repo_id"`
	Fingerprint         string   `json:"fingerprint" db:"fingerprint"`
	StableFingerprint   string   `json:"stable_fingerprint,omitempty" db:"stable_fingerprint"`
	LocationFingerprint string   `json:"location_fingerprint,omitempty" db:"location_fingerprint"`
	SemanticFingerprint string   `json:"semantic_fingerprint,omitempty" db:"semantic_fingerprint"`
	EvidenceFingerprint string   `json:"evidence_fingerprint,omitempty" db:"evidence_fingerprint"`
	IdentityVersion     int      `json:"identity_version,omitempty" db:"identity_version"`
	ToolName            string   `json:"tool_name" db:"tool_name"`
	Category            Category `json:"category" db:"category"`
	Severity            Severity `json:"severity" db:"severity"`
	Title               string   `json:"title" db:"title"`
	Description         string   `json:"description" db:"description"`
	FilePath            string   `json:"file_path" db:"file_path"`
	LineStart           int      `json:"line_start" db:"line_start"`
	LineEnd             int      `json:"line_end" db:"line_end"`
	CodeSnippet         string   `json:"code_snippet" db:"code_snippet"`
	CWEID               string   `json:"cwe_id,omitempty" db:"cwe_id"`
	RuleID              string   `json:"rule_id,omitempty" db:"rule_id"`
	// Dependency identity is separate from display text and source-code module enrichment.
	PackageName       string    `json:"package_name,omitempty" db:"package_name"`
	PackageVersion    string    `json:"package_version,omitempty" db:"package_version"`
	PackageEcosystem  string    `json:"package_ecosystem,omitempty" db:"package_ecosystem"`
	PackageURL        string    `json:"package_url,omitempty" db:"package_url"`
	AdvisoryID        string    `json:"advisory_id,omitempty" db:"advisory_id"`
	ToolSeverityScore float64   `json:"tool_severity_score" db:"tool_severity_score"`
	LocationWeight    float64   `json:"location_weight" db:"location_weight"`
	AIContextScore    float64   `json:"ai_context_score" db:"ai_context_score"`
	CompositeScore    float64   `json:"composite_score" db:"composite_score"`
	AIFixSuggestion   string    `json:"ai_fix_suggestion,omitempty" db:"ai_fix_suggestion"`
	ModuleName        string    `json:"module_name" db:"module_name"`
	FunctionName      string    `json:"function_name" db:"function_name"`
	SymbolKind        string    `json:"symbol_kind" db:"symbol_kind"`
	FilePurpose       string    `json:"file_purpose" db:"file_purpose"`
	DependentsJSON    string    `json:"dependents_json" db:"dependents_json"`
	Status            Status    `json:"status" db:"status"`
	SARIFData         string    `json:"sarif_data,omitempty" db:"sarif_data"`
	CreatedAt         time.Time `json:"created_at" db:"created_at"`
	UpdatedAt         time.Time `json:"updated_at" db:"updated_at"`

	// --- Deterministic scan-quality fields. ---

	// FineCategory is the canonical fine-grained issue category derived
	// from a (tool, rule_id) knowledge-base lookup. Examples:
	// "sql-injection", "weak-crypto", "hardcoded-secret". Empty when the
	// knowledge base has no entry for the rule.
	FineCategory string `json:"fine_category,omitempty" db:"fine_category"`

	// FixStrategyID points at a fix-strategy markdown template (e.g.
	// "parameterize-query"). The renderer joins this against the
	// strategy registry to produce a single "how to fix" block per
	// category. Empty when uncategorized.
	FixStrategyID string `json:"fix_strategy_id,omitempty" db:"fix_strategy_id"`

	// Confidence is "high" | "medium" | "low" — derived deterministically
	// from cross-tool agreement at dedupe time. Three or more tools at
	// the same location → high; two → medium; one → low. Manual grouping
	// clears confidence because association does not prove scanner agreement.
	Confidence string `json:"confidence,omitempty" db:"confidence"`

	// CorroboratedByJSON is the durable form of CorroboratedBy.
	CorroboratedByJSON string `json:"-" db:"corroborated_by_json"`

	// CorroboratedBy lists every tool that reported a finding matching
	// this one's automatic key, or manually associated evidence. The primary record's
	// ToolName is always included.
	CorroboratedBy []string `json:"corroborated_by,omitempty" db:"-"`

	// Suppressed is true when a default rule or .wolfignore entry matched
	// the finding's file path / rule / category. Suppressed findings
	// still appear in findings.json (for audit) but are excluded from
	// FIX-HIGH.md and from the visible portion of FIX-ALL.md.
	Suppressed bool `json:"suppressed,omitempty" db:"suppressed"`

	// SuppressionID references a durable suppression record when a
	// server-side suppression matched. Empty for default/.wolfignore
	// suppression rules.
	SuppressionID string `json:"suppression_id,omitempty" db:"suppression_id"`

	// SuppressedReason is a short, human-readable explanation of which
	// rule fired (e.g. "default:vendor", "default:test-file",
	// ".wolfignore:**/legacy/**"). Empty when Suppressed is false.
	// SuppressedReason is persisted when a server-side suppression rule
	// fires, so the UI can explain why a finding was hidden without
	// re-running suppression logic. Default/.wolfignore rules still set
	// this field at scan time even though SuppressionID stays empty.
	SuppressedReason string `json:"suppressed_reason,omitempty" db:"suppressed_reason"`

	// --- Baseline diff fields (scan quality platform). ---

	// BaselineState is assigned by baseline/diff comparison. Expected
	// values include "new", "existing", "fixed", "resurfaced",
	// "suppressed", "accepted_risk", and "ignored_by_policy".
	// BaselineIdentityKey binds derived history to the primary identity that supplied it.
	BaselineIdentityKey string `json:"-" db:"baseline_identity_key"`
	BaselineState       string `json:"baseline_state,omitempty" db:"baseline_state"`

	FirstSeenAt        *time.Time `json:"first_seen_at,omitempty" db:"first_seen_at"`
	LastSeenAt         *time.Time `json:"last_seen_at,omitempty" db:"last_seen_at"`
	IntroducedInScanID string     `json:"introduced_in_scan_id,omitempty" db:"introduced_in_scan_id"`
	ResolvedInScanID   string     `json:"resolved_in_scan_id,omitempty" db:"resolved_in_scan_id"`
	SourceKind         string     `json:"source_kind,omitempty" db:"source_kind"`
	SourceRef          string     `json:"source_ref,omitempty" db:"source_ref"`

	// --- AI integration fields (in-memory / on-disk only). ---

	// AIFixPrompt is the remediation prompt produced by `wolf enrich` —
	// a ready-to-hand-to-an-AI-agent description of the finding and how
	// to fix it. Deterministically templated by default; optionally
	// AI-authored. Empty until the finding is enriched.
	AIFixPrompt string `json:"ai_fix_prompt,omitempty" db:"-"`

	// TriagedBy records who set the current Status: "" (untriaged),
	// "ai" (AI auto-remediation triage), or "human". Lets the UI flag
	// AI-dismissed findings for human review.
	TriagedBy string `json:"triaged_by,omitempty" db:"-"`

	// AIUnfixable is true when the auto-fix loop exhausted a finding's
	// per-finding fix budget without clearing it.
	AIUnfixable bool `json:"ai_unfixable,omitempty" db:"-"`
}

// ReportingTools returns distinct persisted contributors, including legacy rows
// that only have ToolName. Tool facets overlap; they do not sum to issue count.
func (f Finding) ReportingTools() []string {
	tools := append(slices.Clone(f.CorroboratedBy), f.ToolName)
	slices.Sort(tools)
	tools = slices.Compact(tools)
	return slices.DeleteFunc(tools, func(tool string) bool { return tool == "" })
}
