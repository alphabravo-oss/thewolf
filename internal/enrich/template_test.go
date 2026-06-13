package enrich

import (
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

// fullFinding returns a finding with every enrichment field populated.
func fullFinding() models.Finding {
	return models.Finding{
		ID:             "f1",
		ToolName:       "gosec",
		Category:       models.CategorySAST,
		Severity:       models.SeverityHigh,
		Title:          "SQL string concatenation",
		Description:    "User input is concatenated into a SQL query.",
		FilePath:       "internal/db/users.go",
		LineStart:      45,
		LineEnd:        48,
		CodeSnippet:    "query := \"SELECT * FROM users WHERE id=\" + id",
		CWEID:          "CWE-89",
		RuleID:         "G201",
		FunctionName:   "GetUser",
		SymbolKind:     "function",
		ModuleName:     "db",
		FilePurpose:    "database access layer",
		FineCategory:   "sql-injection",
		CorroboratedBy: []string{"semgrep", "gosec"},
		DependentsJSON: `["internal/api/users.go","internal/api/admin.go"]`,
	}
}

func TestBuildPrompt_AllSectionsPresent(t *testing.T) {
	got := BuildPrompt(fullFinding())
	for _, section := range []string{
		"## Problem", "## Location", "## Repo context",
		"## Task", "## Acceptance criteria",
	} {
		if !strings.Contains(got, section) {
			t.Errorf("missing section %q in prompt:\n%s", section, got)
		}
	}
	// Spot-check meaningful content lands in the right places.
	for _, want := range []string{
		"High severity sql-injection finding from gosec",
		"internal/db/users.go:45-48",
		"Function: GetUser (function)",
		"CWE: CWE-89",
		"Rule: G201",
		"Corroborated by: gosec, semgrep", // sorted
		"internal/api/admin.go, internal/api/users.go", // dependents sorted
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected prompt to contain %q\n---\n%s", want, got)
		}
	}
}

func TestBuildPrompt_Deterministic(t *testing.T) {
	f := fullFinding()
	a := BuildPrompt(f)
	b := BuildPrompt(f)
	if a != b {
		t.Fatal("BuildPrompt is not deterministic for the same finding")
	}
}

func TestBuildPrompt_MissingFieldsOmitted(t *testing.T) {
	// A bare finding — only the always-present fields set.
	bare := models.Finding{
		ToolName: "eslint",
		Category: models.CategoryQuality,
		Severity: models.SeverityLow,
		Title:    "unused variable",
		FilePath: "src/app.ts",
		LineStart: 10,
	}
	got := BuildPrompt(bare)

	// Optional sections/lines must NOT appear.
	for _, absent := range []string{
		"## Repo context", "Function:", "Module:", "```", "CWE:", "Rule:",
	} {
		if strings.Contains(got, absent) {
			t.Errorf("bare finding prompt should not contain %q\n---\n%s", absent, got)
		}
	}
	// Mandatory sections still present.
	for _, section := range []string{"## Problem", "## Location", "## Task", "## Acceptance criteria"} {
		if !strings.Contains(got, section) {
			t.Errorf("bare finding prompt missing %q", section)
		}
	}
	// Single-line location renders without a range.
	if !strings.Contains(got, "src/app.ts:10\n") {
		t.Errorf("expected single-line location, got:\n%s", got)
	}
}

func TestFormatDependents(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"array", `["b","a"]`, "a, b"},
		{"object", `{"z":1,"a":2}`, "a, z"},
		{"empty array", `[]`, ""},
		{"empty object", `{}`, ""},
		{"empty string", ``, ""},
		{"null", `null`, ""},
		{"garbage", `not json`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := formatDependents(c.in); got != c.want {
				t.Errorf("formatDependents(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
