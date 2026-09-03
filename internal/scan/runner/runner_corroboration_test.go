package runner

import (
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

// Cross-tool matches at the same (file, line, fine_category) should collapse
// into one record annotated with the contributing tools and a confidence
// derived from how many distinct tools flagged it.
func TestDeduplicate_CrossToolCorroboration_HighConfidence(t *testing.T) {
	in := []models.Finding{
		{ToolName: "gosec", FilePath: "a.go", LineStart: 10,
			FineCategory: "sql-injection", Severity: models.SeverityHigh, RuleID: "G201"},
		{ToolName: "semgrep", FilePath: "a.go", LineStart: 10,
			FineCategory: "sql-injection", Severity: models.SeverityCritical, RuleID: "x"},
		{ToolName: "snyk", FilePath: "a.go", LineStart: 10,
			FineCategory: "sql-injection", Severity: models.SeverityMedium, RuleID: "y"},
	}
	out := Deduplicate(in)
	if len(out) != 1 {
		t.Fatalf("expected 1 deduped finding, got %d", len(out))
	}
	if out[0].Severity != models.SeverityCritical {
		t.Errorf("primary severity = %s, want critical (highest of inputs)", out[0].Severity)
	}
	if out[0].Confidence != "high" {
		t.Errorf("Confidence = %q, want high (3 tools)", out[0].Confidence)
	}
	if got := len(out[0].CorroboratedBy); got != 3 {
		t.Errorf("CorroboratedBy len = %d, want 3", got)
	}
}

func TestDeduplicate_TwoToolsIsMedium(t *testing.T) {
	in := []models.Finding{
		{ToolName: "gosec", FilePath: "a.go", LineStart: 5,
			FineCategory: "weak-crypto", Severity: models.SeverityHigh},
		{ToolName: "semgrep", FilePath: "a.go", LineStart: 5,
			FineCategory: "weak-crypto", Severity: models.SeverityHigh},
	}
	out := Deduplicate(in)
	if len(out) != 1 || out[0].Confidence != "medium" {
		t.Errorf("expected single finding with medium confidence, got %+v", out)
	}
}

func TestDeduplicate_SingleToolIsLow(t *testing.T) {
	in := []models.Finding{
		{ToolName: "gosec", FilePath: "a.go", LineStart: 5,
			FineCategory: "weak-crypto", Severity: models.SeverityHigh},
	}
	out := Deduplicate(in)
	if len(out) != 1 || out[0].Confidence != "low" {
		t.Errorf("single tool should yield low confidence, got %+v", out)
	}
}

// When fine_category is absent the legacy rule_id/title key is used so that
// pre-knowledge findings still dedupe the way they did before Phase 2.
func TestDeduplicate_NoFineCategory_FallbackKey(t *testing.T) {
	in := []models.Finding{
		{ToolName: "X", FilePath: "f", LineStart: 1, RuleID: "R1", Severity: models.SeverityLow},
		{ToolName: "X", FilePath: "f", LineStart: 1, RuleID: "R1", Severity: models.SeverityHigh},
		{ToolName: "X", FilePath: "f", LineStart: 1, RuleID: "R2", Severity: models.SeverityLow},
	}
	out := Deduplicate(in)
	if len(out) != 2 {
		t.Fatalf("expected 2 deduped (R1 collapsed, R2 separate), got %d", len(out))
	}
}

// Findings from different files must NOT collapse even with the same category.
func TestDeduplicate_DifferentFilesStaySeparate(t *testing.T) {
	in := []models.Finding{
		{ToolName: "gosec", FilePath: "a.go", LineStart: 1, FineCategory: "sql-injection", Severity: models.SeverityHigh},
		{ToolName: "gosec", FilePath: "b.go", LineStart: 1, FineCategory: "sql-injection", Severity: models.SeverityHigh},
	}
	out := Deduplicate(in)
	if len(out) != 2 {
		t.Errorf("different files should not collapse: got %d", len(out))
	}
}

func TestDeduplicate_SCADistinctCVEsStaySeparate(t *testing.T) {
	in := []models.Finding{
		{ToolName: "trivy", Category: models.CategorySCA, FilePath: "go.mod", LineStart: 0,
			FineCategory: "vulnerable-dependency", RuleID: "CVE-1", Severity: models.SeverityHigh},
		{ToolName: "trivy", Category: models.CategorySCA, FilePath: "go.mod", LineStart: 0,
			FineCategory: "vulnerable-dependency", RuleID: "CVE-2", Severity: models.SeverityHigh},
	}
	out := Deduplicate(in)
	if len(out) != 2 {
		t.Fatalf("expected 2 SCA findings (distinct CVEs), got %d", len(out))
	}
}

func TestDeduplicate_SCASameCVECrossToolCollapses(t *testing.T) {
	in := []models.Finding{
		{ToolName: "trivy", Category: models.CategorySCA, FilePath: "go.mod", LineStart: 0,
			FineCategory: "vulnerable-dependency", RuleID: "CVE-1", Severity: models.SeverityHigh},
		{ToolName: "grype", Category: models.CategorySCA, FilePath: "go.mod", LineStart: 0,
			FineCategory: "vulnerable-dependency", RuleID: "CVE-1", Severity: models.SeverityHigh},
	}
	out := Deduplicate(in)
	if len(out) != 1 {
		t.Fatalf("expected 1 corroborated SCA finding, got %d", len(out))
	}
	if got := len(out[0].CorroboratedBy); got != 2 {
		t.Errorf("CorroboratedBy len = %d, want 2 (%v)", got, out[0].CorroboratedBy)
	}
	seen := map[string]bool{}
	for _, name := range out[0].CorroboratedBy {
		seen[name] = true
	}
	if !seen["trivy"] || !seen["grype"] {
		t.Errorf("CorroboratedBy = %v, want trivy and grype", out[0].CorroboratedBy)
	}
}

func TestApplyKnowledge_GosecG201(t *testing.T) {
	f := models.Finding{ToolName: "gosec", RuleID: "G201"}
	if !applyKnowledge(&f) {
		t.Fatal("applyKnowledge returned false for known rule")
	}
	if f.FineCategory != "sql-injection" {
		t.Errorf("FineCategory = %q", f.FineCategory)
	}
	if f.FixStrategyID != "parameterize-query" {
		t.Errorf("FixStrategyID = %q", f.FixStrategyID)
	}
}

func TestApplyKnowledge_UnknownLeavesEmpty(t *testing.T) {
	f := models.Finding{ToolName: "no-such-tool", RuleID: "no-such-rule"}
	if applyKnowledge(&f) {
		t.Error("applyKnowledge should report false for unknown rule")
	}
	if f.FineCategory != "" {
		t.Error("FineCategory should remain empty for unknown rule")
	}
}
