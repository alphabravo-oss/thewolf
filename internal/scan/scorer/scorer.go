// Package scorer implements composite priority scoring for security findings.
//
// The composite score combines three dimensions:
//   - tool_severity: numeric weight derived from the finding's severity level
//   - location_weight: multiplier based on the file path's security sensitivity
//   - ai_context_score: externally provided contextual relevance (0-10)
//
// The final composite score is normalized to the 0-100 range.
package scorer

import (
	"encoding/json"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/models"
)

// maxRawScore is the theoretical maximum raw composite score (10 * 3 * 10).
const maxRawScore = 300.0

// severityScores maps severity levels to their numeric weights.
var severityScores = map[models.Severity]float64{
	models.SeverityCritical: 10,
	models.SeverityHigh:     8,
	models.SeverityMedium:   5,
	models.SeverityLow:      2,
	models.SeverityInfo:     1,
}

// highWeightPatterns trigger a 3x location multiplier.
var highWeightPatterns = []string{
	"auth",
	"payment",
	"billing",
	"api/routes",
	"middleware",
	"security",
	"crypto",
}

// mediumWeightPatterns trigger a 2x location multiplier.
var mediumWeightPatterns = []string{
	"controller",
	"service",
	"handler",
	"model",
	"core",
	"domain",
}

// lowWeightPatterns trigger a 0.5x location multiplier.
var lowWeightPatterns = []string{
	"vendor",
	"node_modules",
	"generated",
	"mock",
	"fixture",
	"testdata",
}

// SeverityScore returns the numeric weight for a given severity level.
// Unknown severities return 0.
func SeverityScore(s models.Severity) float64 {
	if score, ok := severityScores[s]; ok {
		return score
	}
	return 0
}

// LocationWeight returns a multiplier based on the file path's security sensitivity.
//
// Paths are checked against pattern groups in priority order:
//   - 3x: auth, payment, billing, api/routes, middleware, security, crypto
//   - 2x: controller, service, handler, model, core, domain
//   - 0.5x: vendor, node_modules, generated, mock, fixture, testdata
//   - 1x: default for all other paths
//
// The first matching group wins; patterns within a group are checked with
// case-insensitive substring matching.
func LocationWeight(filePath string) float64 {
	lower := strings.ToLower(filePath)

	for _, pattern := range highWeightPatterns {
		if strings.Contains(lower, pattern) {
			return 3.0
		}
	}

	for _, pattern := range mediumWeightPatterns {
		if strings.Contains(lower, pattern) {
			return 2.0
		}
	}

	for _, pattern := range lowWeightPatterns {
		if strings.Contains(lower, pattern) {
			return 0.5
		}
	}

	return 1.0
}

// CompositeScore calculates the normalized composite priority score.
//
// The raw score is toolSeverity * locationWeight * aiContext, normalized
// to the 0-100 range by dividing by the maximum possible raw score (300).
// The result is clamped to [0, 100].
func CompositeScore(toolSeverity, locationWeight, aiContext float64) float64 {
	raw := toolSeverity * locationWeight * aiContext
	normalized := (raw / maxRawScore) * 100.0

	if normalized < 0 {
		return 0
	}
	if normalized > 100 {
		return 100
	}
	return normalized
}

// DependencyBoost returns a multiplicative boost based on how many files
// depend on the finding's file. More dependents = higher impact.
func DependencyBoost(dependentsCount int) float64 {
	if dependentsCount >= 10 {
		return 1.5
	}
	if dependentsCount >= 5 {
		return 1.3
	}
	if dependentsCount >= 2 {
		return 1.1
	}
	return 1.0
}

// ScoreFinding computes and fills in all score fields on a Finding.
// If AIContextScore is zero, it defaults to 5.
// The Finding is modified in place and also returned for convenience.
func ScoreFinding(f *models.Finding) *models.Finding {
	f.ToolSeverityScore = SeverityScore(f.Severity)
	f.LocationWeight = LocationWeight(f.FilePath)

	if f.AIContextScore == 0 {
		f.AIContextScore = 5.0
	}

	f.CompositeScore = CompositeScore(f.ToolSeverityScore, f.LocationWeight, f.AIContextScore)

	// Apply dependency boost if enrichment data is available.
	if f.DependentsJSON != "" && f.DependentsJSON != "[]" {
		var deps []string
		if err := json.Unmarshal([]byte(f.DependentsJSON), &deps); err == nil {
			f.CompositeScore *= DependencyBoost(len(deps))
			if f.CompositeScore > 100 {
				f.CompositeScore = 100
			}
		}
	}

	return f
}

// ScoreFindings scores every finding in the slice and returns the modified slice.
func ScoreFindings(findings []models.Finding) []models.Finding {
	for i := range findings {
		ScoreFinding(&findings[i])
	}
	return findings
}
