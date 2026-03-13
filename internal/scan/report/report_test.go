package report

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func testConfig() ReportConfig {
	return ReportConfig{
		ScanID:   "scan-001",
		RepoName: "wolfcorp/example",
		Branch:   "main",
		Findings: []models.Finding{
			{
				ID:             "f1",
				ToolName:       "gosec",
				Category:       models.CategorySAST,
				Severity:       models.SeverityCritical,
				Title:          "SQL Injection",
				Description:    "Unsanitized input in SQL query",
				FilePath:       "pkg/db/query.go",
				LineStart:      42,
				LineEnd:        42,
				CWEID:          "CWE-89",
				RuleID:         "G201",
				CompositeScore: 9.5,
				Status:         models.StatusOpen,
			},
			{
				ID:             "f2",
				ToolName:       "gosec",
				Category:       models.CategorySAST,
				Severity:       models.SeverityHigh,
				Title:          "Hardcoded credentials",
				Description:    "Password stored in source code",
				FilePath:       "internal/auth/config.go",
				LineStart:      15,
				LineEnd:        15,
				RuleID:         "G101",
				CompositeScore: 8.2,
				Status:         models.StatusOpen,
			},
			{
				ID:             "f3",
				ToolName:       "semgrep",
				Category:       models.CategorySAST,
				Severity:       models.SeverityMedium,
				Title:          "Missing error check",
				Description:    "Return value of os.Remove is not checked",
				FilePath:       "cmd/clean.go",
				LineStart:      88,
				LineEnd:        88,
				RuleID:         "go.unchecked-error",
				CompositeScore: 5.0,
				Status:         models.StatusOpen,
			},
			{
				ID:             "f4",
				ToolName:       "trivy",
				Category:       models.CategorySCA,
				Severity:       models.SeverityLow,
				Title:          "Outdated dependency",
				Description:    "Package foo v1.2.3 has known low-severity vulnerability",
				FilePath:       "go.mod",
				LineStart:      10,
				LineEnd:        10,
				CompositeScore: 2.1,
				Status:         models.StatusOpen,
			},
			{
				ID:             "f5",
				ToolName:       "trivy",
				Category:       models.CategorySCA,
				Severity:       models.SeverityInfo,
				Title:          "License notice",
				Description:    "Dependency bar uses MIT license",
				FilePath:       "go.mod",
				LineStart:      20,
				LineEnd:        20,
				CompositeScore: 0.5,
				Status:         models.StatusOpen,
			},
		},
		Languages:   map[string]int{"Go": 42, "SQL": 3},
		Frameworks:  []string{"Chi", "GORM"},
		ToolsRun:    []string{"gosec", "semgrep", "trivy"},
		ToolsFailed: map[string]error{"trivy": fmt.Errorf("timeout after 60s")},
		Duration:    2*time.Minute + 30*time.Second,
		AISummary:   "The repository has a critical SQL injection vulnerability that should be addressed immediately.",
	}
}

func TestGenerateJSON_RoundTrip(t *testing.T) {
	cfg := testConfig()
	data, err := GenerateJSON(cfg)
	if err != nil {
		t.Fatalf("GenerateJSON failed: %v", err)
	}

	// Must be valid JSON.
	var rpt jsonReport
	if err := json.Unmarshal(data, &rpt); err != nil {
		t.Fatalf("JSON output is not valid: %v", err)
	}

	// Verify key fields.
	if rpt.ScanID != cfg.ScanID {
		t.Errorf("ScanID = %q, want %q", rpt.ScanID, cfg.ScanID)
	}
	if rpt.RepoName != cfg.RepoName {
		t.Errorf("RepoName = %q, want %q", rpt.RepoName, cfg.RepoName)
	}
	if rpt.Summary.Total != len(cfg.Findings) {
		t.Errorf("Total = %d, want %d", rpt.Summary.Total, len(cfg.Findings))
	}
	if len(rpt.Findings) != len(cfg.Findings) {
		t.Errorf("Findings count = %d, want %d", len(rpt.Findings), len(cfg.Findings))
	}
	if rpt.AISummary != cfg.AISummary {
		t.Errorf("AISummary = %q, want %q", rpt.AISummary, cfg.AISummary)
	}

	// Verify severity counts.
	if rpt.Summary.BySeverity["critical"] != 1 {
		t.Errorf("critical count = %d, want 1", rpt.Summary.BySeverity["critical"])
	}
	if rpt.Summary.BySeverity["high"] != 1 {
		t.Errorf("high count = %d, want 1", rpt.Summary.BySeverity["high"])
	}

	// Re-marshal and check it's still valid.
	data2, err := json.Marshal(rpt)
	if err != nil {
		t.Fatalf("re-marshal failed: %v", err)
	}
	var rpt2 jsonReport
	if err := json.Unmarshal(data2, &rpt2); err != nil {
		t.Fatalf("round-trip JSON invalid: %v", err)
	}
	if rpt2.ScanID != rpt.ScanID {
		t.Error("round-trip ScanID mismatch")
	}
}

func TestGenerateSARIF_ValidJSON(t *testing.T) {
	cfg := testConfig()
	data, err := GenerateSARIF(cfg)
	if err != nil {
		t.Fatalf("GenerateSARIF failed: %v", err)
	}

	var log sarifLog
	if err := json.Unmarshal(data, &log); err != nil {
		t.Fatalf("SARIF output is not valid JSON: %v", err)
	}

	// Verify schema and version.
	if log.Schema != sarifSchema {
		t.Errorf("schema = %q, want %q", log.Schema, sarifSchema)
	}
	if log.Version != sarifVersion {
		t.Errorf("version = %q, want %q", log.Version, sarifVersion)
	}
}

func TestGenerateSARIF_GroupsByTool(t *testing.T) {
	cfg := testConfig()
	data, err := GenerateSARIF(cfg)
	if err != nil {
		t.Fatalf("GenerateSARIF failed: %v", err)
	}

	var log sarifLog
	if err := json.Unmarshal(data, &log); err != nil {
		t.Fatalf("SARIF parse failed: %v", err)
	}

	// Expect 3 runs: gosec, semgrep, trivy.
	if len(log.Runs) != 3 {
		t.Fatalf("runs = %d, want 3", len(log.Runs))
	}

	// Build a map of tool name -> result count.
	runMap := make(map[string]int)
	for _, run := range log.Runs {
		runMap[run.Tool.Driver.Name] = len(run.Results)
	}

	// gosec has 2 findings.
	if runMap["gosec"] != 2 {
		t.Errorf("gosec results = %d, want 2", runMap["gosec"])
	}
	// semgrep has 1 finding.
	if runMap["semgrep"] != 1 {
		t.Errorf("semgrep results = %d, want 1", runMap["semgrep"])
	}
	// trivy has 2 findings.
	if runMap["trivy"] != 2 {
		t.Errorf("trivy results = %d, want 2", runMap["trivy"])
	}
}

func TestGenerateSARIF_SeverityMapping(t *testing.T) {
	cfg := testConfig()
	data, err := GenerateSARIF(cfg)
	if err != nil {
		t.Fatalf("GenerateSARIF failed: %v", err)
	}

	var log sarifLog
	if err := json.Unmarshal(data, &log); err != nil {
		t.Fatalf("SARIF parse failed: %v", err)
	}

	// Find the gosec run and check levels.
	for _, run := range log.Runs {
		if run.Tool.Driver.Name != "gosec" {
			continue
		}
		for _, res := range run.Results {
			if res.Level != "error" {
				t.Errorf("gosec result level = %q, want 'error' (critical/high)", res.Level)
			}
		}
	}

	// Find semgrep run — medium maps to warning.
	for _, run := range log.Runs {
		if run.Tool.Driver.Name != "semgrep" {
			continue
		}
		for _, res := range run.Results {
			if res.Level != "warning" {
				t.Errorf("semgrep result level = %q, want 'warning' (medium)", res.Level)
			}
		}
	}

	// Find trivy run — low/info map to note.
	for _, run := range log.Runs {
		if run.Tool.Driver.Name != "trivy" {
			continue
		}
		for _, res := range run.Results {
			if res.Level != "note" {
				t.Errorf("trivy result level = %q, want 'note' (low/info)", res.Level)
			}
		}
	}
}

func TestGenerateMarkdown_ContainsAllSections(t *testing.T) {
	cfg := testConfig()
	md, err := GenerateMarkdown(cfg)
	if err != nil {
		t.Fatalf("GenerateMarkdown failed: %v", err)
	}

	requiredSections := []string{
		"# Wolf Scan Report",
		"**Scan ID:** scan-001",
		"**Branch:** main",
		"## Executive Summary",
		"SQL injection vulnerability",
		"## Overview",
		"**Total Findings:** 5",
		"**Critical:** 1",
		"**High:** 1",
		"**Medium:** 1",
		"**Low:** 1",
		"**Info:** 1",
		"## Findings by Severity",
		"### Critical (1)",
		"### High (1)",
		"### Medium (1)",
		"### Low (1)",
		"### Info (1)",
		"## Tool Breakdown",
		"gosec",
		"semgrep",
		"trivy",
	}

	for _, section := range requiredSections {
		if !strings.Contains(md, section) {
			t.Errorf("markdown missing expected content: %q", section)
		}
	}
}

func TestGenerateMarkdown_ToolBreakdownShowsFailure(t *testing.T) {
	cfg := testConfig()
	md, err := GenerateMarkdown(cfg)
	if err != nil {
		t.Fatalf("GenerateMarkdown failed: %v", err)
	}

	if !strings.Contains(md, "Failed") {
		t.Error("markdown should indicate trivy failure in tool breakdown")
	}
	if !strings.Contains(md, "timeout") {
		t.Error("markdown should contain the failure reason for trivy")
	}
}

func TestGenerateMarkdown_NoAISummaryFallback(t *testing.T) {
	cfg := testConfig()
	cfg.AISummary = ""

	md, err := GenerateMarkdown(cfg)
	if err != nil {
		t.Fatalf("GenerateMarkdown failed: %v", err)
	}

	if !strings.Contains(md, "Scan completed with") {
		t.Error("markdown should have auto-generated summary when AISummary is empty")
	}
}

func TestGenerateJSON_EmptyFindings(t *testing.T) {
	cfg := ReportConfig{
		ScanID:   "scan-empty",
		RepoName: "wolfcorp/empty",
		Branch:   "develop",
		Findings: nil,
		ToolsRun: []string{"gosec"},
		Duration: 5 * time.Second,
	}

	data, err := GenerateJSON(cfg)
	if err != nil {
		t.Fatalf("GenerateJSON failed: %v", err)
	}

	var rpt jsonReport
	if err := json.Unmarshal(data, &rpt); err != nil {
		t.Fatalf("JSON output is not valid: %v", err)
	}

	if rpt.Summary.Total != 0 {
		t.Errorf("Total = %d, want 0", rpt.Summary.Total)
	}
}
