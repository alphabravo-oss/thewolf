package security

import (
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestPluginMetadata(t *testing.T) {
	plugins := []struct {
		plugin   models.Plugin
		name     string
		category models.Category
	}{
		{&DetectSecretsPlugin{}, "detect-secrets", models.CategorySecrets},
		{&NucleiPlugin{}, "nuclei", models.CategoryDAST},
		{&OSVScannerPlugin{}, "osv-scanner", models.CategorySCA},
	}

	for _, tc := range plugins {
		t.Run(tc.name, func(t *testing.T) {
			if tc.plugin.Name() != tc.name {
				t.Errorf("expected name %s, got %s", tc.name, tc.plugin.Name())
			}
			if tc.plugin.Category() != tc.category {
				t.Errorf("expected category %s, got %s", tc.category, tc.plugin.Category())
			}
			if langs := tc.plugin.Languages(); len(langs) != 0 {
				t.Errorf("expected empty languages, got %v", langs)
			}
		})
	}
}

func TestParseDetectSecretsOutput(t *testing.T) {
	// Fixture mixes a high-noise KeywordDetector hit (filtered) and a
	// pattern-based AWS key hit (kept). The parser drops KeywordDetector
	// because its signal-to-noise on real codebases is too poor; see the
	// detectSecretsNoiseTypes map.
	data := []byte(`{
		"results": {
			"config/settings.py": [
				{
					"type": "Secret Keyword",
					"line_number": 15,
					"hashed_secret": "abc123"
				},
				{
					"type": "AWS Access Key",
					"line_number": 20,
					"hashed_secret": "def456"
				}
			]
		}
	}`)

	findings, err := parseDetectSecretsOutput(data)
	if err != nil {
		t.Fatalf("parseDetectSecretsOutput returned error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding (Secret Keyword filtered, AWS Access Key kept), got %d", len(findings))
	}
	if findings[0].Title != `Potential AWS Access Key detected` {
		t.Errorf("kept finding should be AWS Access Key, got title=%q", findings[0].Title)
	}

	for _, f := range findings {
		if f.ToolName != "detect-secrets" {
			t.Errorf("expected tool name detect-secrets, got %s", f.ToolName)
		}
		if f.Category != models.CategorySecrets {
			t.Errorf("expected category secrets, got %s", f.Category)
		}
		if f.Severity != models.SeverityHigh {
			t.Errorf("expected severity high, got %s", f.Severity)
		}
		if f.FilePath != "config/settings.py" {
			t.Errorf("expected file path config/settings.py, got %s", f.FilePath)
		}
	}
}

func TestParseNucleiOutput(t *testing.T) {
	data := []byte(`{"template-id":"cve-2021-44228","info":{"name":"Log4j RCE","description":"Remote code execution","severity":"critical","tags":["cve"],"reference":[],"cwe":["CWE-502"]},"matcher-name":"log4j","host":"http://example.com","matched-at":"http://example.com/api"}
{"template-id":"http-missing-security-headers","info":{"name":"Missing Headers","description":"Security headers missing","severity":"info","tags":["headers"],"reference":[],"cwe":[]},"matcher-name":"","host":"http://example.com","matched-at":"http://example.com/"}
`)

	findings, err := parseNucleiOutput(data)
	if err != nil {
		t.Fatalf("parseNucleiOutput returned error: %v", err)
	}
	if len(findings) != 2 {
		t.Fatalf("expected 2 findings, got %d", len(findings))
	}

	f := findings[0]
	if f.ToolName != "nuclei" {
		t.Errorf("expected tool name nuclei, got %s", f.ToolName)
	}
	if f.Category != models.CategoryDAST {
		t.Errorf("expected category dast, got %s", f.Category)
	}
	if f.Severity != models.SeverityCritical {
		t.Errorf("expected severity critical, got %s", f.Severity)
	}
	if f.RuleID != "cve-2021-44228" {
		t.Errorf("expected rule ID cve-2021-44228, got %s", f.RuleID)
	}
	if f.CWEID != "CWE-502" {
		t.Errorf("expected CWE-502, got %s", f.CWEID)
	}

	if findings[1].Severity != models.SeverityInfo {
		t.Errorf("expected severity info for second finding, got %s", findings[1].Severity)
	}
}

func TestParseBearerOutput(t *testing.T) {
	findings, err := parseBearerOutput([]byte(`{"high":[{"id":"ruby_rails_sql_injection","title":"SQL injection","description":"Untrusted query","cwe_ids":["89"],"filename":"app/models/user.rb","line_number":12,"snippet_end_line":13,"data_type":{"name":"SQL"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].ToolName != "bearer" || findings[0].RuleID != "ruby_rails_sql_injection" || findings[0].CWEID != "CWE-89" || findings[0].Severity != models.SeverityHigh {
		t.Fatalf("bearer finding = %#v", findings)
	}
}

func TestParseScorecardOutput(t *testing.T) {
	findings, err := parseScorecardOutput([]byte(`{"checks":[{"name":"Branch-Protection","score":2,"reason":"Protection is incomplete","documentation":{"url":"https://example.invalid/docs"},"details":["fixture"]},{"name":"Maintained","score":10}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].ToolName != "scorecard" || findings[0].RuleID != "Branch-Protection" || findings[0].Severity != models.SeverityCritical {
		t.Fatalf("scorecard finding = %#v", findings)
	}
}

func TestMapNucleiSeverity(t *testing.T) {
	tests := []struct {
		input    string
		expected models.Severity
	}{
		{"critical", models.SeverityCritical},
		{"high", models.SeverityHigh},
		{"medium", models.SeverityMedium},
		{"low", models.SeverityLow},
		{"info", models.SeverityInfo},
		{"unknown", models.SeverityInfo},
	}

	for _, tc := range tests {
		got := mapNucleiSeverity(tc.input)
		if got != tc.expected {
			t.Errorf("mapNucleiSeverity(%q) = %s, want %s", tc.input, got, tc.expected)
		}
	}
}

func TestParseOSVScannerOutput(t *testing.T) {
	data := []byte(`{
		"results": [
			{
				"source": {"path": "package.json", "type": "lockfile"},
				"packages": [
					{
						"package": {"name": "lodash", "version": "4.17.20", "ecosystem": "npm"},
						"vulnerabilities": [
							{
								"id": "GHSA-xxxx-yyyy",
								"summary": "Prototype pollution",
								"details": "Allows prototype pollution",
								"severity": [{"type": "CVSS_V3", "score": "7.5"}],
								"aliases": ["CVE-2021-23337"]
							}
						]
					}
				]
			}
		]
	}`)

	findings, err := parseOSVScannerOutput(data)
	if err != nil {
		t.Fatalf("parseOSVScannerOutput returned error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}

	f := findings[0]
	if f.ToolName != "osv-scanner" {
		t.Errorf("expected tool name osv-scanner, got %s", f.ToolName)
	}
	if f.Category != models.CategorySCA {
		t.Errorf("expected category sca, got %s", f.Category)
	}
	if f.RuleID != "GHSA-xxxx-yyyy" {
		t.Errorf("expected rule ID GHSA-xxxx-yyyy, got %s", f.RuleID)
	}
	if f.CWEID != "CVE-2021-23337" {
		t.Errorf("expected CVE alias, got %s", f.CWEID)
	}
	if f.FilePath != "package.json" {
		t.Errorf("expected file path package.json, got %s", f.FilePath)
	}
}
