package planner

import (
	"sort"

	"github.com/alphabravocompany/thewolf/internal/models"
)

// SeverityScore maps severity levels to numeric values for sorting.
var SeverityScore = map[models.Severity]float64{
	models.SeverityCritical: 10,
	models.SeverityHigh:     8,
	models.SeverityMedium:   5,
	models.SeverityLow:      2,
	models.SeverityInfo:     1,
}

// CategoryGroup holds findings grouped by their analysis category.
type CategoryGroup struct {
	Category models.Category
	Findings []models.Finding
}

// FixPlan represents an ordered plan of fix tasks grouped by category.
type FixPlan struct {
	Groups     []CategoryGroup
	TotalCount int
}

// Plan creates a fix plan from findings. It filters by severity (if provided),
// sorts by composite score descending, and groups by category.
func Plan(findings []models.Finding, severities []models.Severity) *FixPlan {
	filtered := filterBySeverity(findings, severities)
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].CompositeScore > filtered[j].CompositeScore
	})

	groups := groupByCategory(filtered)
	// Sort groups so categories with highest max score come first.
	sort.Slice(groups, func(i, j int) bool {
		return maxScore(groups[i].Findings) > maxScore(groups[j].Findings)
	})

	return &FixPlan{
		Groups:     groups,
		TotalCount: len(filtered),
	}
}

func filterBySeverity(findings []models.Finding, severities []models.Severity) []models.Finding {
	if len(severities) == 0 {
		result := make([]models.Finding, len(findings))
		copy(result, findings)
		return result
	}
	allowed := make(map[models.Severity]bool, len(severities))
	for _, s := range severities {
		allowed[s] = true
	}
	var result []models.Finding
	for _, f := range findings {
		if allowed[f.Severity] {
			result = append(result, f)
		}
	}
	return result
}

func groupByCategory(findings []models.Finding) []CategoryGroup {
	m := make(map[models.Category][]models.Finding)
	var order []models.Category
	for _, f := range findings {
		if _, seen := m[f.Category]; !seen {
			order = append(order, f.Category)
		}
		m[f.Category] = append(m[f.Category], f)
	}
	groups := make([]CategoryGroup, 0, len(m))
	for _, cat := range order {
		groups = append(groups, CategoryGroup{
			Category: cat,
			Findings: m[cat],
		})
	}
	return groups
}

func maxScore(findings []models.Finding) float64 {
	var max float64
	for _, f := range findings {
		if f.CompositeScore > max {
			max = f.CompositeScore
		}
	}
	return max
}
