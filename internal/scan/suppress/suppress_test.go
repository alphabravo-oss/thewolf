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

func TestApply_TestFileSuppressesAllCategories(t *testing.T) {
	// Defaults now suppress ALL findings in test files, not just
	// hardcoded-secret. Test code is allowed to have intentional
	// unsafe constructs; a finding there is the test pattern, not
	// a real bug.
	findings := []models.Finding{
		{FilePath: "internal/foo/bar_test.go", FineCategory: "hardcoded-secret"},
		{FilePath: "internal/foo/bar_test.go", FineCategory: "sql-injection"},
		{FilePath: "internal/foo/bar.go", FineCategory: "sql-injection"}, // non-test, real
	}
	out, n := Apply(findings, DefaultRules())
	if n != 2 {
		t.Fatalf("expected 2 suppressions (both test-file findings), got %d", n)
	}
	if !out[0].Suppressed || !out[1].Suppressed {
		t.Errorf("expected both test-file findings suppressed: %+v", out)
	}
	if out[2].Suppressed {
		t.Errorf("non-test finding should NOT be suppressed: %+v", out[2])
	}
}

// TestApply_LanguageTestPatterns exercises the per-language test-file
// globs. Each row is a path that SHOULD match one of the default test-
// suppression rules. Catches regressions if a glob is dropped/renamed.
func TestApply_LanguageTestPatterns(t *testing.T) {
	cases := []struct {
		path      string
		descrLang string // for error messages
	}{
		// Go
		{"internal/foo/bar_test.go", "go test"},
		{"internal/foo/mock_db.go", "go mock"},
		{"internal/foo/db_mock.go", "go mock"},
		// Python
		{"app/test_users.py", "py test_*"},
		{"app/users_test.py", "py *_test"},
		{"tests/conftest.py", "py conftest (path)"},
		{"app/conftest.py", "py conftest (name)"},
		// JS/TS
		{"src/foo.test.ts", "ts test"},
		{"src/foo.test.tsx", "tsx test"},
		{"src/foo.test.mjs", "mjs test"},
		{"src/foo.spec.cjs", "cjs spec"},
		{"src/Button.stories.tsx", "storybook"},
		{"src/Story.stories.mdx", "storybook mdx"},
		// Java / Kotlin (Maven, Gradle)
		{"src/test/java/com/x/FooTest.java", "java test"},
		{"src/test/java/com/x/FooTests.java", "java tests"},
		{"src/test/java/com/x/PaymentIT.java", "java integration"},
		{"src/test/kotlin/com/x/FooSpec.kt", "kotlin spec"},
		{"src/test/kotlin/com/x/FooTest.kt", "kotlin test"},
		// Ruby (RSpec / Minitest)
		{"spec/models/user_spec.rb", "ruby spec"},
		{"test/models/user_test.rb", "ruby test"},
		// PHP (PHPUnit)
		{"tests/UserTest.php", "php test"},
		{"tests/UserTestCase.php", "php testcase"},
		// C# / .NET
		{"src/MyApp.Tests/UserTests.cs", ".net tests"},
		{"src/MyApp.Tests/UserTest.cs", ".net test"},
		// Swift (XCTest)
		{"Tests/MyAppTests/UserTests.swift", "swift tests"},
		// Rust (Cargo integration tests)
		{"tests/integration_users.rs", "rust integration dir"},
		// Universal test directories
		{"app/test/fixtures.txt", "test/ dir"},
		{"app/tests/spam.json", "tests/ dir"},
		{"app/spec/anything.rb", "spec/ dir"},
		{"app/Tests/X.cs", "Tests/ dir (case)"},
		{"app/__tests__/foo.js", "jest tests dir"},
		{"app/__snapshots__/foo.snap", "jest snapshots"},
		{"app/__mocks__/db.ts", "jest mocks"},
		{"app/__fixtures__/payload.json", "jest fixtures"},
		{"app/e2e/login.spec.ts", "e2e dir"},
		{"app/integrationTest/x.kt", "gradle integration"},
		// Test fixture / mock data dirs
		{"app/testdata/sample.json", "testdata"},
		{"app/test-fixtures/sample.json", "test-fixtures"},
		{"app/testFixtures/sample.json", "testFixtures"},
		{"app/fixtures/users.yml", "fixtures"},
		{"app/mock_data/x.json", "mock_data"},
		{"app/mock-data/x.json", "mock-data"},
		{"app/mocks/db.go", "mocks"},
		// Cypress / Playwright
		{"cypress/fixtures/login.json", "cypress fixtures"},
		{"cypress/screenshots/run.png", "cypress screenshots"},
		// Examples / demo / samples
		{"examples/quickstart.js", "examples"},
		{"example/x.js", "example"},
		{"demo/foo.py", "demo"},
		{"samples/payment.json", "samples"},
		{"app/payment.sample.json", "*.sample.*"},
		{"app/sample_payment.json", "sample_*"},
		// CI workflow YAML — both repo-root and nested submodule forms.
		{".github/workflows/ci.yml", "github workflows (root)"},
		{"submodules/proj/.github/workflows/release.yml", "github workflows (nested)"},
	}
	for _, tc := range cases {
		f := models.Finding{FilePath: tc.path, FineCategory: "sql-injection"}
		out, n := Apply([]models.Finding{f}, DefaultRules())
		if n != 1 || !out[0].Suppressed {
			t.Errorf("%s: %q should be suppressed by defaults, got %+v", tc.descrLang, tc.path, out[0])
		}
	}
}

// TestApply_RealCodeNotSuppressed locks down the inverse: a file path
// that *looks* like a test (e.g. has 'test' as a substring inside an
// otherwise normal package path) must NOT be suppressed.
func TestApply_RealCodeNotSuppressed(t *testing.T) {
	paths := []string{
		"internal/contestant/score.go", // 'test' substring, not test
		"app/protested/handler.py",     // same
		"src/Components/Button.tsx",    // normal source
		"cmd/wolf/main.go",
		"src/foo.ts", // not .test or .spec
		"src/api/users.java",
		"app/lib/encryption.rb",
	}
	for _, p := range paths {
		f := models.Finding{FilePath: p, FineCategory: "sql-injection"}
		out, _ := Apply([]models.Finding{f}, DefaultRules())
		if out[0].Suppressed {
			t.Errorf("%q should NOT be suppressed (reason was %q)", p, out[0].SuppressedReason)
		}
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
