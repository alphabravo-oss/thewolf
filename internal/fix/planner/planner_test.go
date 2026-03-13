package planner

import (
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestPlan_GroupsByCategory(t *testing.T) {
	findings := []models.Finding{
		{ID: "1", Category: models.CategorySAST, Severity: models.SeverityHigh, CompositeScore: 80},
		{ID: "2", Category: models.CategorySCA, Severity: models.SeverityMedium, CompositeScore: 50},
		{ID: "3", Category: models.CategorySAST, Severity: models.SeverityCritical, CompositeScore: 95},
		{ID: "4", Category: models.CategorySecrets, Severity: models.SeverityHigh, CompositeScore: 70},
	}

	plan := Plan(findings, nil)

	if plan.TotalCount != 4 {
		t.Errorf("expected 4 total, got %d", plan.TotalCount)
	}
	if len(plan.Groups) != 3 {
		t.Fatalf("expected 3 groups, got %d", len(plan.Groups))
	}
	// SAST group should be first (has highest score 95)
	if plan.Groups[0].Category != models.CategorySAST {
		t.Errorf("expected first group to be sast, got %s", plan.Groups[0].Category)
	}
	if len(plan.Groups[0].Findings) != 2 {
		t.Errorf("expected 2 sast findings, got %d", len(plan.Groups[0].Findings))
	}
}

func TestPlan_FiltersBySeverity(t *testing.T) {
	findings := []models.Finding{
		{ID: "1", Severity: models.SeverityCritical, CompositeScore: 90},
		{ID: "2", Severity: models.SeverityHigh, CompositeScore: 70},
		{ID: "3", Severity: models.SeverityLow, CompositeScore: 10},
		{ID: "4", Severity: models.SeverityInfo, CompositeScore: 5},
	}

	plan := Plan(findings, []models.Severity{models.SeverityCritical, models.SeverityHigh})

	if plan.TotalCount != 2 {
		t.Errorf("expected 2, got %d", plan.TotalCount)
	}
}

func TestPlan_PrioritizesByCompositeScore(t *testing.T) {
	findings := []models.Finding{
		{ID: "1", Category: models.CategorySAST, Severity: models.SeverityLow, CompositeScore: 10},
		{ID: "2", Category: models.CategorySAST, Severity: models.SeverityCritical, CompositeScore: 95},
		{ID: "3", Category: models.CategorySAST, Severity: models.SeverityHigh, CompositeScore: 80},
	}

	plan := Plan(findings, nil)
	group := plan.Groups[0]

	// Within the group, findings should be sorted by composite score desc
	if group.Findings[0].ID != "2" {
		t.Errorf("expected finding 2 first, got %s", group.Findings[0].ID)
	}
	if group.Findings[1].ID != "3" {
		t.Errorf("expected finding 3 second, got %s", group.Findings[1].ID)
	}
}

func TestPlan_EmptyFindings(t *testing.T) {
	plan := Plan(nil, nil)
	if plan.TotalCount != 0 {
		t.Errorf("expected 0, got %d", plan.TotalCount)
	}
	if len(plan.Groups) != 0 {
		t.Errorf("expected 0 groups, got %d", len(plan.Groups))
	}
}
