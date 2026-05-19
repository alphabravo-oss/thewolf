package suppress

// DefaultRules returns the built-in suppression ruleset applied before any
// repo-local `.wolfignore`. These rules encode universal FP patterns:
//
//   - Vendored / installed dependencies (the user doesn't own this code).
//   - Generated files and build output (regenerated; fixes don't stick).
//   - Test files and fixtures (intentional anti-patterns and fake secrets).
//
// Each rule's Reason follows the "default:<short-name>" convention so the
// renderer can render them grouped.
//
// To disable a default for a specific repo, add a `!`-prefixed entry to
// `.wolfignore` (negation overrides). To disable defaults globally, set
// `WOLF_DISABLE_DEFAULT_SUPPRESSIONS=1` in the runner config.
func DefaultRules() RuleSet {
	return RuleSet{Rules: []Rule{
		// --- Vendored / installed dependencies — all categories ---
		{PathGlob: "**/vendor/**", Reason: "default:vendor"},
		{PathGlob: "**/node_modules/**", Reason: "default:node_modules"},
		{PathGlob: "**/third_party/**", Reason: "default:third_party"},
		{PathGlob: "**/.git/**", Reason: "default:git-internal"},
		{PathGlob: "**/site-packages/**", Reason: "default:python-site-packages"},
		{PathGlob: "**/Pods/**", Reason: "default:cocoapods"},
		{PathGlob: "**/.bundle/**", Reason: "default:ruby-bundle"},

		// --- Framework / tooling caches ---
		{PathGlob: "**/.next/**", Reason: "default:nextjs-cache"},
		{PathGlob: "**/.nuxt/**", Reason: "default:nuxt-cache"},
		{PathGlob: "**/.svelte-kit/**", Reason: "default:sveltekit-cache"},
		{PathGlob: "**/.turbo/**", Reason: "default:turborepo-cache"},
		{PathGlob: "**/.cache/**", Reason: "default:generic-cache"},
		{PathGlob: "**/dist/**", Reason: "default:dist"},
		{PathGlob: "**/build/**", Reason: "default:build-output"},
		{PathGlob: "**/target/**", Reason: "default:target-output"},
		{PathGlob: "**/__pycache__/**", Reason: "default:python-cache"},
		{PathGlob: "**/.pytest_cache/**", Reason: "default:pytest-cache"},
		{PathGlob: "**/.mypy_cache/**", Reason: "default:mypy-cache"},
		{PathGlob: "**/.tox/**", Reason: "default:python-tox"},

		// --- Explicitly-named generated files ---
		{PathGlob: "**/*.generated.go", Reason: "default:generated"},
		{PathGlob: "**/*.gen.go", Reason: "default:generated"},
		{PathGlob: "**/*_generated.go", Reason: "default:generated"},
		{PathGlob: "**/*.pb.go", Reason: "default:protobuf-generated"},
		{PathGlob: "**/*_pb2.py", Reason: "default:protobuf-generated"},
		{PathGlob: "**/*.min.js", Reason: "default:minified"},
		{PathGlob: "**/*.min.css", Reason: "default:minified"},
		{PathGlob: "**/*.bundle.js", Reason: "default:bundled-asset"},

		// --- Test fixture / test-data directories — ALL findings suppressed.
		//
		// These directories are CONVENTIONAL storage for inputs scanners
		// are *supposed* to flag: anti-patterns, fake secrets, exploit
		// payloads, malformed JSON, etc. Real bugs here aren't real bugs;
		// they're the test corpus. Suppressing all findings (not just
		// hardcoded-secret) is the right default — if you really need to
		// scan a fixtures directory, negate via .wolfignore.
		{PathGlob: "**/testdata/**", Reason: "default:testdata"},          // go convention
		{PathGlob: "**/test-fixtures/**", Reason: "default:test-fixtures"},
		{PathGlob: "**/test_fixtures/**", Reason: "default:test-fixtures"},
		{PathGlob: "**/testFixtures/**", Reason: "default:test-fixtures"}, // gradle
		{PathGlob: "**/fixtures/**", Reason: "default:fixtures"},          // rails, cypress
		{PathGlob: "**/__fixtures__/**", Reason: "default:fixtures"},      // jest
		{PathGlob: "**/__snapshots__/**", Reason: "default:snapshots"},    // jest snapshots
		{PathGlob: "**/__mocks__/**", Reason: "default:mocks"},            // jest mocks
		{PathGlob: "**/mocks/**", Reason: "default:mocks"},
		{PathGlob: "**/mock_data/**", Reason: "default:mock-data"},
		{PathGlob: "**/mock-data/**", Reason: "default:mock-data"},
		{PathGlob: "**/cypress/fixtures/**", Reason: "default:cypress-fixtures"},
		{PathGlob: "**/cypress/screenshots/**", Reason: "default:cypress-screenshots"},
		{PathGlob: "**/cypress/videos/**", Reason: "default:cypress-videos"},
		{PathGlob: "**/playwright-report/**", Reason: "default:playwright-report"},

		// --- Test SOURCE directories (universal convention) — ALL findings.
		//
		// 'test/', 'tests/', 'spec/' as a top-level or per-module
		// directory is the standard convention across most ecosystems:
		//   - Java/Kotlin (Maven, Gradle): src/test/, src/integrationTest/
		//   - Ruby (RSpec, Minitest): spec/, test/
		//   - Rust (Cargo): tests/
		//   - PHP (PHPUnit): tests/
		//   - Python (pytest, unittest): tests/, test/
		//   - C# (.NET, NUnit, xUnit): Tests/, tests/, *.Tests/
		//   - Swift (XCTest): Tests/, *Tests/
		//   - C/C++: test/, tests/, gtest/
		// Same logic as fixtures: real bugs in test code are usually
		// test-only constructs (intentional unsafe inputs, fake creds).
		{PathGlob: "**/test/**", Reason: "default:test-dir"},
		{PathGlob: "**/tests/**", Reason: "default:tests-dir"},
		{PathGlob: "**/spec/**", Reason: "default:spec-dir"},
		{PathGlob: "**/Test/**", Reason: "default:test-dir-case"},   // C# / Swift / Java casing
		{PathGlob: "**/Tests/**", Reason: "default:tests-dir-case"}, // .NET, Swift convention
		{PathGlob: "**/e2e/**", Reason: "default:e2e"},
		{PathGlob: "**/integration/**", Reason: "default:integration-dir"},
		{PathGlob: "**/integrationTest/**", Reason: "default:gradle-integration"},
		{PathGlob: "**/src/test/**", Reason: "default:maven-test"},
		{PathGlob: "**/src/integrationTest/**", Reason: "default:gradle-integration"},
		{PathGlob: "**/__tests__/**", Reason: "default:jest-tests-dir"},

		// --- Test SOURCE FILES by name pattern — ALL findings.
		//
		// Each language's idiomatic filename suffix for a test file.
		// Listing as full-suppression (not category-restricted) because
		// scanners that flag e.g. exec.Command(args...) in a test will
		// be a test pattern, not a real injection bug.
		// Go
		{PathGlob: "**/*_test.go", Reason: "default:test-file:go"},
		{PathGlob: "**/*_mock.go", Reason: "default:mock-file:go"},
		{PathGlob: "**/mock_*.go", Reason: "default:mock-file:go"},
		// Python
		{PathGlob: "**/test_*.py", Reason: "default:test-file:py"},
		{PathGlob: "**/*_test.py", Reason: "default:test-file:py"},
		{PathGlob: "**/conftest.py", Reason: "default:pytest-conftest"},
		// JS/TS — handle every Node module extension
		{PathGlob: "**/*.test.ts", Reason: "default:test-file:ts"},
		{PathGlob: "**/*.test.tsx", Reason: "default:test-file:tsx"},
		{PathGlob: "**/*.test.js", Reason: "default:test-file:js"},
		{PathGlob: "**/*.test.jsx", Reason: "default:test-file:jsx"},
		{PathGlob: "**/*.test.mjs", Reason: "default:test-file:mjs"},
		{PathGlob: "**/*.test.cjs", Reason: "default:test-file:cjs"},
		{PathGlob: "**/*.spec.ts", Reason: "default:spec-file:ts"},
		{PathGlob: "**/*.spec.tsx", Reason: "default:spec-file:tsx"},
		{PathGlob: "**/*.spec.js", Reason: "default:spec-file:js"},
		{PathGlob: "**/*.spec.jsx", Reason: "default:spec-file:jsx"},
		{PathGlob: "**/*.spec.mjs", Reason: "default:spec-file:mjs"},
		{PathGlob: "**/*.spec.cjs", Reason: "default:spec-file:cjs"},
		{PathGlob: "**/*.stories.ts", Reason: "default:storybook"},
		{PathGlob: "**/*.stories.tsx", Reason: "default:storybook"},
		{PathGlob: "**/*.stories.js", Reason: "default:storybook"},
		{PathGlob: "**/*.stories.jsx", Reason: "default:storybook"},
		{PathGlob: "**/*.stories.mdx", Reason: "default:storybook"},
		// Java / Kotlin (JUnit / Spek)
		{PathGlob: "**/*Test.java", Reason: "default:test-file:java"},
		{PathGlob: "**/*Tests.java", Reason: "default:test-file:java"},
		{PathGlob: "**/*IT.java", Reason: "default:integration-file:java"},
		{PathGlob: "**/*ITCase.java", Reason: "default:integration-file:java"},
		{PathGlob: "**/*Test.kt", Reason: "default:test-file:kt"},
		{PathGlob: "**/*Tests.kt", Reason: "default:test-file:kt"},
		{PathGlob: "**/*Spec.kt", Reason: "default:spec-file:kt"},
		// Ruby (Minitest / RSpec)
		{PathGlob: "**/*_test.rb", Reason: "default:test-file:rb"},
		{PathGlob: "**/*_spec.rb", Reason: "default:spec-file:rb"},
		// PHP (PHPUnit)
		{PathGlob: "**/*Test.php", Reason: "default:test-file:php"},
		{PathGlob: "**/*TestCase.php", Reason: "default:test-file:php"},
		// C# / .NET
		{PathGlob: "**/*Tests.cs", Reason: "default:test-file:cs"},
		{PathGlob: "**/*Test.cs", Reason: "default:test-file:cs"},
		{PathGlob: "**/*Spec.cs", Reason: "default:spec-file:cs"},
		// Swift (XCTest)
		{PathGlob: "**/*Tests.swift", Reason: "default:test-file:swift"},
		{PathGlob: "**/*Spec.swift", Reason: "default:spec-file:swift"},
		// Rust
		{PathGlob: "**/tests.rs", Reason: "default:test-file:rs"},
		{PathGlob: "**/test.rs", Reason: "default:test-file:rs"},
		// Some projects have *_output.json fixture files alongside test
		// source — these are scanner-fixture inputs.
		{PathGlob: "**/*_output.json", Reason: "default:scanner-fixture"},
		{PathGlob: "**/*_output.jsonl", Reason: "default:scanner-fixture"},
		{PathGlob: "**/*.sample.*", Reason: "default:sample-file"},
		{PathGlob: "**/sample_*.json", Reason: "default:sample-file"},

		// --- Examples / demo / docs-only code.
		//
		// `examples/`, `example/`, `demo/`, `samples/`, `docs/` host
		// illustrative snippets. They're not the project's code path —
		// suppress all findings.
		{PathGlob: "**/examples/**", Reason: "default:examples"},
		{PathGlob: "**/example/**", Reason: "default:examples"},
		{PathGlob: "**/demo/**", Reason: "default:demo"},
		{PathGlob: "**/demos/**", Reason: "default:demo"},
		{PathGlob: "**/samples/**", Reason: "default:samples"},
		{PathGlob: "**/sample/**", Reason: "default:samples"},

		// --- CI / build automation pipelines.
		//
		// GitHub Actions workflow YAML hosts intentional patterns that
		// scanners read as bugs: `curl | bash` install steps, secret refs
		// like ${{ secrets.X }} that gitleaks treats as embedded creds,
		// shell snippets with unquoted expressions, etc. Same logic as
		// test code — these files aren't the project's production code
		// path. Limited to workflows/ on purpose; .github/ also holds
		// PR templates and CODEOWNERS which aren't noise sources.
		{PathGlob: ".github/workflows/**", Reason: "default:github-workflows"},
		{PathGlob: "**/.github/workflows/**", Reason: "default:github-workflows"},

		// --- Lockfiles ---
		{PathGlob: "**/package-lock.json", Reason: "default:lockfile"},
		{PathGlob: "**/pnpm-lock.yaml", Reason: "default:lockfile"},
		{PathGlob: "**/yarn.lock", Reason: "default:lockfile"},
		{PathGlob: "**/Gemfile.lock", Reason: "default:lockfile"},
		{PathGlob: "**/composer.lock", Reason: "default:lockfile"},
		{PathGlob: "**/Cargo.lock", Reason: "default:lockfile"},
		{PathGlob: "**/poetry.lock", Reason: "default:lockfile"},
		{PathGlob: "**/go.sum", Reason: "default:lockfile"},

		// --- Wolf's own outputs and scanner-config files ---
		// FINDINGS/ holds committed scan-result JSON exports; trufflehog
		// flags hex-like CVE IDs as "secrets" otherwise.
		{PathGlob: "FINDINGS/**", Reason: "default:wolf-self-output"},
		{PathGlob: "**/FINDINGS/**", Reason: "default:wolf-self-output"},
		// Scanner config files contain rule UUIDs (KICS), regex patterns,
		// and example payloads that detectors mistake for credentials.
		// No Categories restriction — these files are wolf's own configs;
		// any "finding" against them is meta-noise. The Categories
		// constraint we had previously only matched canonical
		// fine_category values, but trufflehog hits arrive with empty
		// fine_category and were never being suppressed.
		{PathGlob: ".kics.yaml", Reason: "default:scanner-config"},
		{PathGlob: ".kics-config.yaml", Reason: "default:scanner-config"},
		{PathGlob: ".semgrepignore", Reason: "default:scanner-config"},
		{PathGlob: ".trufflehog*", Reason: "default:scanner-config"},
		{PathGlob: ".sqlfluff", Reason: "default:scanner-config"},
		{PathGlob: ".yamllint", Reason: "default:scanner-config"},
		{PathGlob: ".markdownlint*", Reason: "default:scanner-config"},
		// FIXES.md / FIXES2.md etc. — wolf's own triage docs; they cite
		// CVE IDs, GHSA IDs, and KICS UUIDs.
		{PathGlob: "FIXES*.md", Reason: "default:wolf-triage-doc"},
	}}
}
