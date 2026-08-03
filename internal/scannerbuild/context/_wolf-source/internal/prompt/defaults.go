package prompt

// Prompt types.
const (
	TypeToolAssess     = "tool_assess"
	TypeExecSummary    = "executive_summary"
	SectionSystemCtx   = "system_context"
	SectionScoring     = "scoring_criteria"
	SectionOutputInstr = "output_instructions"
)

// ---------------------------------------------------------------------------
// Tool Assessment defaults
// ---------------------------------------------------------------------------

const DefaultToolAssessSystemContext = `You are a senior security engineer reviewing automated scan findings. Assess each finding for real-world exploitability, business impact, and false positive likelihood in the context of the repository's tech stack and architecture.`

const DefaultToolAssessScoringCriteria = `Score each finding 0-10 for real-world impact considering:
- Production code scores higher than test fixtures, build artifacts, or vendored dependencies
- Findings in public-facing services, authentication, or data handling score higher
- Known false positive patterns (e.g., secrets in test data, example configs) score lower
- Findings with clear exploit paths score higher than theoretical vulnerabilities
- Consider the file's role in the system (entry points, middleware, data models score higher)`

const DefaultToolAssessOutputInstructions = `Respond ONLY with valid JSON (no markdown fences, no extra text). Use this exact schema:
{
  "tool_summary": "brief summary of this tool's findings",
  "finding_scores": [{"index": 0, "context_score": 7}],
  "critical_issues": [{"finding_index": 0, "severity": "high", "title": "...", "impact": "...", "context_score": 9, "fix_suggestion": "..."}]
}

Score each finding 0-10 for real-world impact. Flag critical ones with fix suggestions.`

// ---------------------------------------------------------------------------
// Executive Summary defaults
// ---------------------------------------------------------------------------

const DefaultExecSummarySystemContext = `You are a principal security architect writing an executive security assessment. Synthesize tool-level findings into actionable intelligence for engineering leadership. Focus on systemic patterns, risk exposure, and prioritized remediation.`

const DefaultExecSummaryScoringCriteria = `Prioritize recommendations by:
- Immediate security risks (exposed secrets, critical CVEs) first
- Systemic patterns that indicate process gaps second
- Code quality and maintainability improvements third
- Consider effort-to-impact ratio when setting priority levels`

const DefaultExecSummaryOutputInstructions = `Respond ONLY with valid JSON (no markdown fences, no extra text). Use this exact schema:
{
  "summary": "2-4 paragraph executive summary in markdown",
  "recommendations": ["recommendation 1", "recommendation 2"],
  "structured_recommendations": [
    {"priority": 1, "category": "security", "title": "...", "description": "...", "affected_tools": ["tool1"], "effort_estimate": "low"}
  ]
}

Provide 3-5 structured recommendations with priority (1=highest to 5=lowest), category (security/quality/dependency/config/testing), and effort_estimate (low/medium/high).`

// defaults is the internal lookup table keyed by [promptType][section].
var defaults = map[string]map[string]string{
	TypeToolAssess: {
		SectionSystemCtx:   DefaultToolAssessSystemContext,
		SectionScoring:     DefaultToolAssessScoringCriteria,
		SectionOutputInstr: DefaultToolAssessOutputInstructions,
	},
	TypeExecSummary: {
		SectionSystemCtx:   DefaultExecSummarySystemContext,
		SectionScoring:     DefaultExecSummaryScoringCriteria,
		SectionOutputInstr: DefaultExecSummaryOutputInstructions,
	},
}

// GetDefault returns the hardcoded default for the given promptType and section.
// Returns an empty string if the combination is unknown.
func GetDefault(promptType, section string) string {
	if sections, ok := defaults[promptType]; ok {
		return sections[section]
	}
	return ""
}

// AllDefaults returns a copy of all hardcoded defaults keyed by promptType then section.
func AllDefaults() map[string]map[string]string {
	out := make(map[string]map[string]string, len(defaults))
	for pt, sections := range defaults {
		inner := make(map[string]string, len(sections))
		for k, v := range sections {
			inner[k] = v
		}
		out[pt] = inner
	}
	return out
}
