package knowledge

import "strings"

// Categorize returns the (fineCategory, fixStrategyID) pair for a finding.
// It tries, in order:
//
//  1. Exact (tool, ruleID) lookup in the curated registry.
//  2. Tool-specific heuristics (e.g. trivy CVEs → "vulnerable-dependency",
//     semgrep namespace prefixes → CategorizeBySemgrepPrefix).
//  3. Empty strings — the caller should leave fields blank and the finding
//     surfaces under "Uncategorized" in fix docs.
//
// Keeping this in one function means new fallback rules land in one place
// and stay testable without touching the runner.
func Categorize(tool, ruleID string) (fineCategory, fixStrategy string) {
	if e, ok := Lookup(tool, ruleID); ok {
		return e.FineCategory, e.FixStrategy
	}

	switch tool {
	case "trivy":
		// Trivy's vulnerability findings carry CVE-#### as ruleID; map
		// them all to a single "vulnerable-dependency" category.
		if strings.HasPrefix(strings.ToUpper(ruleID), "CVE-") {
			return "vulnerable-dependency", "update-vulnerable-dependency"
		}
		if strings.HasPrefix(ruleID, "GHSA-") {
			return "vulnerable-dependency", "update-vulnerable-dependency"
		}
	case "semgrep":
		if fc, fs := CategorizeBySemgrepPrefix(ruleID); fc != "" {
			return fc, fs
		}
	}
	return "", ""
}
