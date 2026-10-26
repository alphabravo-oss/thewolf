package report

import (
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/finding/knowledge"
	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestFixHighRetainsSupportedToolsAndUnknownRules(t *testing.T) {
	cfg := FixDocConfig{GeneratedAt: time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)}
	for _, tc := range []struct{ tool, rule, category string }{
		{"grype", "CVE-2026-1234", "vulnerable-dependency"},
		{"osv-scanner", "PYSEC-2026-1", "vulnerable-dependency"},
		{"codeql", "go/sql-injection", "sql-injection"},
		{"checkov", "CKV_AWS_20", "iac-misconfiguration"},
		{"trufflehog", "AWS", "hardcoded-secret"},
		{"unknown-scanner", "new-rule", ""},
	} {
		category, strategy := knowledge.Categorize(tc.tool, tc.rule)
		if category != tc.category {
			t.Fatalf("%s:%s category %q, want %q", tc.tool, tc.rule, category, tc.category)
		}
		if category != "" {
			if entry, ok := knowledge.GetStrategy(strategy); !ok || entry.Body == "" {
				t.Fatalf("missing guidance for %s: %s", tc.tool, strategy)
			}
		}
		cfg.Findings = append(cfg.Findings, models.Finding{ToolName: tc.tool, RuleID: tc.rule, FilePath: tc.tool + ".txt", Severity: models.SeverityHigh, FineCategory: category, FixStrategyID: strategy})
	}
	cfg.Findings = append(cfg.Findings,
		models.Finding{FilePath: "suppressed.txt", Severity: models.SeverityCritical, Suppressed: true},
		models.Finding{FilePath: "low.txt", Severity: models.SeverityLow})
	body := RenderFixHigh(cfg)
	if body != RenderFixHigh(cfg) {
		t.Fatal("same input produced different guidance")
	}
	for _, f := range cfg.Findings[:6] {
		if !strings.Contains(body, f.FilePath) {
			t.Errorf("lost high-priority finding %s", f.FilePath)
		}
	}
	if !strings.Contains(body, "**Total surfaced:** 6") || !strings.Contains(body, "Uncategorized") || strings.Contains(body, "suppressed.txt") || strings.Contains(body, "low.txt") {
		t.Fatalf("incorrect high-priority report:\n%s", body)
	}
}
