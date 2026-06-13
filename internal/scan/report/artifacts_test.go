package report

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestWriteAll_WritesEveryArtifact(t *testing.T) {
	dir := t.TempDir()

	findings := []models.Finding{
		{
			ToolName: "gosec", Category: models.CategorySAST,
			Severity: models.SeverityHigh, Title: "G201", FilePath: "x.go",
			LineStart: 1, RuleID: "G201", Status: models.StatusOpen,
		},
		{
			ToolName: "gitleaks", Category: models.CategorySecrets,
			Severity: models.SeverityCritical, Title: "aws-key",
			FilePath: "y.go", LineStart: 5, Status: models.StatusOpen,
		},
	}
	rcfg := ReportConfig{
		ScanID:   "scan-abc",
		RepoName: "demo",
		Branch:   "main",
		Findings: findings,
		ToolsRun: []string{"gosec", "gitleaks"},
		Duration: 2 * time.Second,
	}
	m := Manifest{
		ScanID:      "scan-abc",
		RepoPath:    "/tmp/demo",
		Branch:      "main",
		StartedAt:   time.Unix(1700000000, 0).UTC(),
		FinishedAt:  time.Unix(1700000010, 0).UTC(),
		ScannersRun: []string{"gosec", "gitleaks"},
		ScannerPlan: &ScannerPlan{
			Run: []ScannerPlanDecision{
				{
					Tool:       "gosec",
					Selected:   true,
					ReasonCode: "language_match",
					Reason:     "scanner supports a detected language",
				},
			},
			Summary: ScannerPlanSummary{RunCount: 1, LanguageCount: 1},
		},
		Counts: CountFindings(0, findings),
	}

	res, err := WriteAll(dir, rcfg, m)
	if err != nil {
		t.Fatalf("WriteAll: %v", err)
	}

	// Every expected artifact landed on disk.
	for _, p := range []string{res.FindingsJSON, res.RawMarkdown, res.CombinedSARIF, res.Manifest} {
		if p == "" {
			t.Errorf("expected non-empty path for an artifact, got empty")
			continue
		}
		if _, err := os.Stat(p); err != nil {
			t.Errorf("artifact %s missing: %v", p, err)
		}
	}

	// findings.json round-trips through json.Unmarshal into a meaningful shape.
	data, err := os.ReadFile(filepath.Join(dir, "findings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("findings.json not valid JSON: %v", err)
	}
	if parsed["scan_id"] != "scan-abc" {
		t.Errorf("scan_id roundtrip: got %v", parsed["scan_id"])
	}

	// manifest.json counts.high_severity = 2 (critical+high).
	mdata, _ := os.ReadFile(res.Manifest)
	var mparsed map[string]any
	if err := json.Unmarshal(mdata, &mparsed); err != nil {
		t.Fatalf("manifest.json not valid JSON: %v", err)
	}
	counts, _ := mparsed["counts"].(map[string]any)
	if counts == nil || counts["high_severity"].(float64) != 2 {
		t.Errorf("expected high_severity=2, got %v", counts)
	}
	scannerPlan, _ := mparsed["scanner_plan"].(map[string]any)
	if scannerPlan == nil {
		t.Fatalf("manifest missing scanner_plan: %v", mparsed)
	}
	summary, _ := scannerPlan["summary"].(map[string]any)
	if summary == nil || summary["run_count"].(float64) != 1 {
		t.Errorf("scanner_plan.summary.run_count = %v", summary)
	}
}

// TestWriteAll_SuppressionIntegration exercises the full WriteAll path with
// findings that should be suppressed by the built-in default rules
// (vendored, .next cache, test-fixture secrets). It verifies:
//   - suppressed findings remain in findings.json (audit trail)
//   - they do NOT appear in FIX-HIGH.md body
//   - they appear under the collapsed "Suppressed" appendix in FIX-ALL.md
//   - manifest.json's counts.suppressed reflects what was filtered
func TestWriteAll_SuppressionIntegration(t *testing.T) {
	dir := t.TempDir()
	findings := []models.Finding{
		// Should land in FIX-HIGH.md
		{
			ToolName: "gosec", Severity: models.SeverityHigh,
			Title: "G201", FilePath: "internal/db/users.go", LineStart: 42,
			RuleID: "G201", FineCategory: "sql-injection", FixStrategyID: "parameterize-query",
			Status: models.StatusOpen,
		},
		// Should be suppressed by default:vendor
		{
			ToolName: "gosec", Severity: models.SeverityHigh,
			Title: "G401", FilePath: "vendor/github.com/foo/md5.go", LineStart: 10,
			RuleID: "G401", FineCategory: "weak-crypto", FixStrategyID: "replace-weak-hash",
			Status: models.StatusOpen,
		},
		// Should be suppressed by default:nextjs-cache
		{
			ToolName: "gitleaks", Severity: models.SeverityHigh,
			Title: "generic-api-key", FilePath: "ui/.next/cache/.previewinfo", LineStart: 1,
			RuleID: "generic-api-key", FineCategory: "hardcoded-secret", FixStrategyID: "rotate-and-remove-secret",
			Status: models.StatusOpen,
		},
		// Should be suppressed by default:test-file:go (catches all
		// findings in _test.go files; the category-scoped narrow rule
		// was replaced when we expanded test-file detection across
		// languages — see internal/scan/suppress/defaults.go).
		{
			ToolName: "gitleaks", Severity: models.SeverityHigh,
			Title: "aws-access-token", FilePath: "internal/foo/bar_test.go", LineStart: 5,
			RuleID: "aws-access-token", FineCategory: "hardcoded-secret", FixStrategyID: "rotate-and-remove-secret",
			Status: models.StatusOpen,
		},
		// SQL injection in a test file — also suppressed under the new
		// 'all categories in test files' rule. A real bug in a test file
		// indicates a test bug, not a production security issue; if you
		// genuinely need to scan a test, negate via .wolfignore.
		{
			ToolName: "gosec", Severity: models.SeverityHigh,
			Title: "G201", FilePath: "internal/foo/bar_test.go", LineStart: 7,
			RuleID: "G201", FineCategory: "sql-injection", FixStrategyID: "parameterize-query",
			Status: models.StatusOpen,
		},
	}
	rcfg := ReportConfig{
		ScanID:   "scan-suppress",
		RepoName: "demo",
		Findings: findings,
		ToolsRun: []string{"gosec", "gitleaks"},
	}
	m := Manifest{
		ScanID:      "scan-suppress",
		RepoPath:    "/nonexistent", // forces .wolfignore parse to no-op
		ScannersRun: []string{"gosec", "gitleaks"},
		Counts:      CountFindings(0, findings),
	}

	res, err := WriteAll(dir, rcfg, m)
	if err != nil {
		t.Fatalf("WriteAll: %v", err)
	}

	// findings.json contains all 5 with suppressed flags on 3.
	fjson, _ := os.ReadFile(res.FindingsJSON)
	if !strings.Contains(string(fjson), `"suppressed": true`) {
		t.Errorf("findings.json missing suppressed=true entries: %s", string(fjson))
	}

	// FIX-HIGH.md must contain only the 2 unsuppressed sql-injection findings,
	// and must NOT name the suppressed file paths.
	fhigh, _ := os.ReadFile(res.FixHigh)
	if !strings.Contains(string(fhigh), "SQL Injection") {
		t.Error("FIX-HIGH.md missing SQL Injection category")
	}
	if strings.Contains(string(fhigh), "vendor/") {
		t.Error("FIX-HIGH.md leaked vendor finding into main body")
	}
	if strings.Contains(string(fhigh), ".next/") {
		t.Error("FIX-HIGH.md leaked .next finding into main body")
	}

	// FIX-ALL.md must show the appendix and reference all suppressed reasons.
	// 4 suppressed: vendor, .next, two in *_test.go (secret + sql-injection).
	fall, _ := os.ReadFile(res.FixAll)
	if !strings.Contains(string(fall), "Suppressed (4 findings)") {
		t.Errorf("FIX-ALL.md missing suppressed appendix: %s", string(fall))
	}
	if !strings.Contains(string(fall), "default:vendor") ||
		!strings.Contains(string(fall), "default:nextjs-cache") {
		t.Error("FIX-ALL.md appendix missing expected reasons")
	}

	// manifest.json counts are correct: 4 suppressed, 1 visible.
	mdata, _ := os.ReadFile(res.Manifest)
	mtxt := string(mdata)
	if !strings.Contains(mtxt, `"suppressed": 4`) {
		t.Errorf("manifest counts.suppressed != 4, got: %s", mtxt)
	}
	if !strings.Contains(mtxt, `"visible": 1`) {
		t.Errorf("manifest counts.visible != 1, got: %s", mtxt)
	}
}

func TestScanDirName(t *testing.T) {
	ts := time.Date(2026, 5, 14, 8, 30, 0, 0, time.UTC)
	cases := []struct {
		repo, id, want string
	}{
		{"/Users/mj/mjcode/ab/thewolf", "abcd1234-5678-90ef-1234-567890abcdef", "thewolf_20260514-083000_abcd1234"},
		{"/tmp/my-app/", "shortid", "my-app_20260514-083000_shortid"},
		{"/tmp/weird name & chars", "", "weird-name---chars_20260514-083000_anon"},
		{"/", "x", "repo_20260514-083000_x"},
	}
	for _, tc := range cases {
		got := ScanDirName(tc.repo, ts, tc.id)
		if got != tc.want {
			t.Errorf("ScanDirName(%q,%q) = %q, want %q", tc.repo, tc.id, got, tc.want)
		}
	}
}

func TestCountFindings(t *testing.T) {
	findings := []models.Finding{
		{Severity: models.SeverityCritical},
		{Severity: models.SeverityHigh},
		{Severity: models.SeverityMedium},
		{Severity: models.SeverityLow},
		{Severity: models.SeverityInfo},
	}
	c := CountFindings(0, findings)
	if c.AfterDedupe != 5 {
		t.Errorf("AfterDedupe = %d, want 5", c.AfterDedupe)
	}
	if c.RawFindings != 5 {
		t.Errorf("RawFindings should default to AfterDedupe, got %d", c.RawFindings)
	}
	if c.HighSeverity != 2 {
		t.Errorf("HighSeverity = %d, want 2", c.HighSeverity)
	}

	c2 := CountFindings(10, findings)
	if c2.RawFindings != 10 {
		t.Errorf("explicit rawTotal not preserved: %d", c2.RawFindings)
	}
}
