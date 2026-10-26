package gates

import (
	"encoding/json"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestScanGateRequiresCompleteEvidence(t *testing.T) {
	for _, tc := range []struct {
		name                              string
		status                            models.ScanStatus
		selected, completed, failed, want string
	}{
		{"complete", models.ScanStatusCompleted, `["bandit"]`, `["bandit"]`, `[]`, StatusPass},
		{"failed", models.ScanStatusFailed, `["bandit"]`, `[]`, `["bandit"]`, StatusIncomplete},
		{"cancelled", models.ScanStatusCancelled, `["bandit"]`, `[]`, `[]`, StatusIncomplete},
		{"pending", models.ScanStatusPending, `["bandit"]`, `[]`, `[]`, StatusPending},
		{"running", models.ScanStatusRunning, `["bandit"]`, `[]`, `[]`, StatusPending},
		{"missing", models.ScanStatusCompleted, `["bandit","gosec"]`, `["bandit"]`, `[]`, StatusIncomplete},
		{"wrong tool", models.ScanStatusCompleted, `["bandit"]`, `["gosec"]`, `[]`, StatusIncomplete},
		{"tool failed", models.ScanStatusCompleted, `["bandit"]`, `["bandit"]`, `["bandit"]`, StatusIncomplete},
		{"none", models.ScanStatusCompleted, `[]`, `[]`, `[]`, StatusIncomplete},
		{"invalid", models.ScanStatusCompleted, `{}`, `["bandit"]`, `[]`, StatusIncomplete},
	} {
		t.Run(tc.name, func(t *testing.T) {
			eval := EvaluateScan(DefaultPolicy(), &models.Scan{Status: tc.status, ToolsSelected: tc.selected, ToolsCompleted: tc.completed, ToolsFailed: tc.failed}, nil)
			if eval.Status != tc.want || eval.Summary.Status != tc.want {
				t.Fatalf("got %+v; want %s", eval, tc.want)
			}
			if tc.want != StatusPass && eval.Reason == "" {
				t.Fatal("missing explanation")
			}
		})
	}
}

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

func TestFastPRPolicyGatesNewHighButNotExisting(t *testing.T) {
	policy := FastPRPolicy()
	if policy.Name != "fast-pr-gate" {
		t.Fatalf("name = %q, want fast-pr-gate", policy.Name)
	}

	existingHigh := Evaluate(policy, []models.Finding{
		{ID: "existing-high", Severity: models.SeverityHigh, Category: models.CategorySAST, BaselineState: "existing"},
	})
	if existingHigh.Status == StatusFail {
		t.Fatalf("existing high SAST should not fail: %+v", existingHigh)
	}

	newHigh := Evaluate(policy, []models.Finding{
		{ID: "new-high", Severity: models.SeverityHigh, Category: models.CategorySAST, BaselineState: "new"},
	})
	if newHigh.Status != StatusFail {
		t.Fatalf("new high SAST should fail: %+v", newHigh)
	}

	existingSecret := Evaluate(policy, []models.Finding{
		{ID: "secret", Severity: models.SeverityLow, Category: models.CategorySecrets, BaselineState: "existing"},
	})
	if existingSecret.Status != StatusFail {
		t.Fatalf("unsuppressed secret should fail regardless of existing: %+v", existingSecret)
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

func TestUnknownBaselineDoesNotMatchNewOrPassGate(t *testing.T) {
	finding := models.Finding{ID: "unknown", Severity: models.SeverityHigh, Category: models.CategorySAST}
	eval := Evaluate(FastPRPolicy(), []models.Finding{finding})
	if eval.Status != StatusIncomplete || eval.Summary.FailCount != 0 || len(eval.MatchedRules) != 0 {
		t.Fatalf("unknown baseline asserted a new finding or passed: %+v", eval)
	}
	finding.Suppressed = true
	eval = Evaluate(FastPRPolicy(), []models.Finding{finding})
	if eval.Status != StatusPass {
		t.Fatalf("irrelevant suppressed finding blocked policy: %+v", eval)
	}
}
