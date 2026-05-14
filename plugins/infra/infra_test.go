package infra

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestPluginMetadata(t *testing.T) {
	plugins := []struct {
		plugin   models.Plugin
		name     string
		category models.Category
	}{
		{&KubeLinterPlugin{}, "kube-linter", models.CategoryInfra},
		{&KubescapePlugin{}, "kubescape", models.CategoryInfra},
		{&TFLintPlugin{}, "tflint", models.CategoryInfra},
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

func TestParseKubeLinterOutput(t *testing.T) {
	data := []byte(`{
		"Reports": [
			{
				"Check": "no-read-only-root-fs",
				"Description": "Container does not have a read-only root filesystem",
				"Remediation": "Set readOnlyRootFilesystem to true",
				"Diagnostic": {"Message": "container nginx does not have a read-only root file system"},
				"Object": {"K8sObject": {"FilePath": "deploy.yaml"}}
			}
		]
	}`)

	findings, err := parseKubeLinterOutput(data)
	if err != nil {
		t.Fatalf("parseKubeLinterOutput returned error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}

	f := findings[0]
	if f.ToolName != "kube-linter" {
		t.Errorf("expected tool name kube-linter, got %s", f.ToolName)
	}
	if f.Category != models.CategoryInfra {
		t.Errorf("expected category infra, got %s", f.Category)
	}
	if f.Severity != models.SeverityMedium {
		t.Errorf("expected severity medium, got %s", f.Severity)
	}
	if f.RuleID != "no-read-only-root-fs" {
		t.Errorf("expected rule ID no-read-only-root-fs, got %s", f.RuleID)
	}
	if f.FilePath != "deploy.yaml" {
		t.Errorf("expected file path deploy.yaml, got %s", f.FilePath)
	}
}

func TestParseKubescapeOutput(t *testing.T) {
	data := []byte(`{
		"results": [
			{
				"resourceID": "default/Deployment/nginx",
				"controls": [
					{
						"controlID": "C-0034",
						"name": "Automatic mapping of service account",
						"status": "failed",
						"severity": {"scoreFactor": 7.0}
					},
					{
						"controlID": "C-0001",
						"name": "Passed control",
						"status": "passed",
						"severity": {"scoreFactor": 3.0}
					}
				]
			}
		]
	}`)

	findings, err := parseKubescapeOutput(data)
	if err != nil {
		t.Fatalf("parseKubescapeOutput returned error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding (passed should be skipped), got %d", len(findings))
	}

	f := findings[0]
	if f.ToolName != "kubescape" {
		t.Errorf("expected tool name kubescape, got %s", f.ToolName)
	}
	if f.RuleID != "C-0034" {
		t.Errorf("expected rule ID C-0034, got %s", f.RuleID)
	}
	if f.Severity != models.SeverityHigh {
		t.Errorf("expected severity high for score 7.0, got %s", f.Severity)
	}
}

func TestMapKubescapeScore(t *testing.T) {
	tests := []struct {
		score    float64
		expected models.Severity
	}{
		{9.0, models.SeverityCritical},
		{8.0, models.SeverityCritical},
		{7.0, models.SeverityHigh},
		{6.0, models.SeverityHigh},
		{5.0, models.SeverityMedium},
		{4.0, models.SeverityMedium},
		{3.0, models.SeverityLow},
		{2.0, models.SeverityLow},
		{1.0, models.SeverityInfo},
		{0.0, models.SeverityInfo},
	}

	for _, tc := range tests {
		got := mapKubescapeScore(tc.score)
		if got != tc.expected {
			t.Errorf("mapKubescapeScore(%.1f) = %s, want %s", tc.score, got, tc.expected)
		}
	}
}

func TestParseTFLintOutput(t *testing.T) {
	data := []byte(`{
		"issues": [
			{
				"rule": {"name": "aws_instance_invalid_type", "severity": "error"},
				"message": "instance type is invalid",
				"range": {
					"filename": "main.tf",
					"start": {"line": 10, "column": 3},
					"end": {"line": 10}
				}
			},
			{
				"rule": {"name": "terraform_naming_convention", "severity": "warning"},
				"message": "naming convention violated",
				"range": {
					"filename": "vars.tf",
					"start": {"line": 5, "column": 1},
					"end": {"line": 5}
				}
			}
		]
	}`)

	findings, err := parseTFLintOutput(data)
	if err != nil {
		t.Fatalf("parseTFLintOutput returned error: %v", err)
	}
	if len(findings) != 2 {
		t.Fatalf("expected 2 findings, got %d", len(findings))
	}

	if findings[0].Severity != models.SeverityHigh {
		t.Errorf("expected severity high for error, got %s", findings[0].Severity)
	}
	if findings[0].LineStart != 10 {
		t.Errorf("expected line 10, got %d", findings[0].LineStart)
	}
	if findings[1].Severity != models.SeverityMedium {
		t.Errorf("expected severity medium for warning, got %s", findings[1].Severity)
	}
}

func TestMapTFLintSeverity(t *testing.T) {
	tests := []struct {
		input    string
		expected models.Severity
	}{
		{"error", models.SeverityHigh},
		{"warning", models.SeverityMedium},
		{"notice", models.SeverityLow},
		{"unknown", models.SeverityInfo},
	}

	for _, tc := range tests {
		got := mapTFLintSeverity(tc.input)
		if got != tc.expected {
			t.Errorf("mapTFLintSeverity(%q) = %s, want %s", tc.input, got, tc.expected)
		}
	}
}

func TestKICSLoadExcludeQueries(t *testing.T) {
	tmp := t.TempDir()
	cases := []struct {
		name     string
		yaml     string
		expected string
	}{
		{
			name:     "no file",
			yaml:     "",
			expected: "",
		},
		{
			name: "two uuids with comments",
			yaml: `# top-level comment
exclude-queries:
  # apt pin
  - 965a08d7-ef86-4f14-8792-4a3b2098937e
  - 2b6ebc63-a614-4dab-aebf-a4fdba2387a3  # apk pin
`,
			expected: "965a08d7-ef86-4f14-8792-4a3b2098937e,2b6ebc63-a614-4dab-aebf-a4fdba2387a3",
		},
		{
			name: "quoted uuids",
			yaml: `exclude-queries:
  - "965a08d7-ef86-4f14-8792-4a3b2098937e"
  - '2b6ebc63-a614-4dab-aebf-a4fdba2387a3'
`,
			expected: "965a08d7-ef86-4f14-8792-4a3b2098937e,2b6ebc63-a614-4dab-aebf-a4fdba2387a3",
		},
		{
			name: "list under different key is ignored",
			yaml: `other-list:
  - 965a08d7-ef86-4f14-8792-4a3b2098937e
exclude-queries:
  - 2b6ebc63-a614-4dab-aebf-a4fdba2387a3
`,
			expected: "2b6ebc63-a614-4dab-aebf-a4fdba2387a3",
		},
		{
			name:     "empty section",
			yaml:     "exclude-queries:\n",
			expected: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := filepath.Join(tmp, tc.name)
			if err := os.MkdirAll(repo, 0o750); err != nil {
				t.Fatal(err)
			}
			if tc.yaml != "" {
				if err := os.WriteFile(filepath.Join(repo, ".kics.yaml"), []byte(tc.yaml), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			got := kicsLoadExcludeQueries(repo)
			if got != tc.expected {
				t.Errorf("got %q, want %q", got, tc.expected)
			}
		})
	}
}
