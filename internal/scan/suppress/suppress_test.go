package suppress

import (
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestPathMatch_Doublestar(t *testing.T) {
	cases := []struct {
		pat, name string
		want      bool
	}{
		{"**/vendor/**", "vendor/github.com/foo/bar.go", true},
		{"**/vendor/**", "internal/vendor/x.go", true},
		{"**/vendor/**", "internal/x.go", false},
		{"**/*.min.js", "ui-next/dist/assets/app.min.js", true},
		{"**/*.min.js", "ui-next/dist/assets/app.js", false},
		{"**/node_modules/**", "node_modules/foo/bar.js", true},
		{"**/.next/**", "ui/.next/cache/x", true},
		{"**/.next/**", "ui-next/dist/x", false},
		{"**/*_test.go", "internal/foo/bar_test.go", true},
		{"**/*_test.go", "internal/foo/bar.go", false},
	}
	for _, tc := range cases {
		got := pathMatch(tc.pat, tc.name)
		if got != tc.want {
			t.Errorf("pathMatch(%q, %q) = %v, want %v", tc.pat, tc.name, got, tc.want)
		}
	}
}

func TestApply_VendorSuppressesEverything(t *testing.T) {
	findings := []models.Finding{
		{FilePath: "vendor/github.com/foo/bar.go", FineCategory: "sql-injection"},
		{FilePath: "internal/db/users.go", FineCategory: "sql-injection"},
	}
	out, n := Apply(findings, DefaultRules())
	if n != 1 {
		t.Errorf("expected 1 suppression, got %d", n)
	}
	if !out[0].Suppressed {
		t.Error("vendor finding should be suppressed")
	}
	if out[1].Suppressed {
		t.Error("non-vendor finding should NOT be suppressed")
	}
	if !strings.Contains(out[0].SuppressedReason, "vendor") {
		t.Errorf("Reason = %q, expected to contain 'vendor'", out[0].SuppressedReason)
	}
}

func TestApply_TestFileSuppressesOnlySecrets(t *testing.T) {
	findings := []models.Finding{
		{FilePath: "internal/foo/bar_test.go", FineCategory: "hardcoded-secret"},
		{FilePath: "internal/foo/bar_test.go", FineCategory: "sql-injection"},
	}
	out, n := Apply(findings, DefaultRules())
	if n != 1 {
		t.Fatalf("expected 1 suppression, got %d", n)
	}
	if !out[0].Suppressed || out[1].Suppressed {
		t.Errorf("expected only the secret finding to be suppressed: %+v", out)
	}
}

func TestParseWolfIgnore(t *testing.T) {
	src := `
# a comment
**/legacy/**
**/testdata/** category=hardcoded-secret
* rule=semgrep.foo.bar
**/internal/** category=xss,sql-injection rule=R1,R2
`
	rs, err := ParseWolfIgnore(strings.NewReader(src), ".wolfignore")
	if err != nil {
		t.Fatal(err)
	}
	if got := len(rs.Rules); got != 4 {
		t.Fatalf("expected 4 rules, got %d", got)
	}
	if rs.Rules[0].PathGlob != "**/legacy/**" {
		t.Errorf("Rule[0] path = %q", rs.Rules[0].PathGlob)
	}
	if got := rs.Rules[1].Categories; len(got) != 1 || got[0] != "hardcoded-secret" {
		t.Errorf("Rule[1] categories = %v", got)
	}
	if got := rs.Rules[2].RuleIDs; len(got) != 1 || got[0] != "semgrep.foo.bar" {
		t.Errorf("Rule[2] rule_ids = %v", got)
	}
	if got := rs.Rules[3].Categories; len(got) != 2 {
		t.Errorf("Rule[3] categories = %v", got)
	}
}

func TestParseWolfIgnoreFile_MissingIsNoError(t *testing.T) {
	rs, err := ParseWolfIgnoreFile("/nonexistent/.wolfignore")
	if err != nil {
		t.Errorf("missing file should be silently empty, got err: %v", err)
	}
	if len(rs.Rules) != 0 {
		t.Errorf("expected empty ruleset, got %d", len(rs.Rules))
	}
}

func TestCombine_FirstMatchWins(t *testing.T) {
	a := RuleSet{Rules: []Rule{{PathGlob: "**/foo.go", Reason: "a"}}}
	b := RuleSet{Rules: []Rule{{PathGlob: "**/foo.go", Reason: "b"}}}
	combined := Combine(a, b)
	r, ok := combined.Match(models.Finding{FilePath: "x/foo.go"})
	if !ok || r.Reason != "a" {
		t.Errorf("expected first-wins, got %+v ok=%v", r, ok)
	}
}
