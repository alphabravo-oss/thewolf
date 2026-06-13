package gates

import (
	"encoding/json"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestDefaultPolicyFailsCriticalHighSecurityAndSecrets(t *testing.T) {
	findings := []models.Finding{
		{ID: "critical", Severity: models.SeverityCritical, Category: models.CategorySAST},
		{ID: "high", Severity: models.SeverityHigh, Category: models.CategorySCA},
		{ID: "secret", Severity: models.SeverityLow, Category: models.CategorySecrets},
		{ID: "suppressed", Severity: models.SeverityCritical, Category: models.CategorySAST, Suppressed: true},
	}

	eval := Evaluate(DefaultPolicy(), findings)
	if eval.Status != StatusFail {
		t.Fatalf("status = %q, want fail", eval.Status)
	}
	if eval.Summary.FailCount != 3 {
		t.Fatalf("fail count = %d, want 3", eval.Summary.FailCount)
	}
}

func TestDefaultPolicyWarnsMediumSecurity(t *testing.T) {
	eval := Evaluate(DefaultPolicy(), []models.Finding{
		{ID: "medium", Severity: models.SeverityMedium, Category: models.CategorySAST},
	})
	if eval.Status != StatusWarn {
		t.Fatalf("status = %q, want warn", eval.Status)
	}
	if eval.Summary.WarnCount != 1 {
		t.Fatalf("warn count = %d, want 1", eval.Summary.WarnCount)
	}
}

func TestParsePolicyAndConfidenceMinimum(t *testing.T) {
	rules := []Rule{{
		ID:            "high-confidence-medium",
		Severity:      []models.Severity{models.SeverityMedium},
		ConfidenceMin: "high",
		Action:        ActionWarn,
	}}
	data, _ := json.Marshal(rules)
	policy, err := ParsePolicy("custom", "warn", string(data))
	if err != nil {
		t.Fatalf("ParsePolicy: %v", err)
	}

	eval := Evaluate(policy, []models.Finding{
		{ID: "low-conf", Severity: models.SeverityMedium, Confidence: "medium"},
		{ID: "high-conf", Severity: models.SeverityMedium, Confidence: "high"},
	})
	if eval.Status != StatusWarn || eval.Summary.WarnCount != 1 {
		t.Fatalf("unexpected evaluation: %+v", eval)
	}
	if len(eval.MatchedRules) != 1 || eval.MatchedRules[0].FindingIDs[0] != "high-conf" {
		t.Fatalf("unexpected matched rules: %+v", eval.MatchedRules)
	}
}
