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

		// --- Test fixtures and testdata.
		//
		// testdata/, fixtures/, test-fixtures/ are by convention storage
		// for inputs scanners are *supposed* to flag (anti-patterns,
		// fake secrets, exploit payloads). Suppress ALL findings in
		// these directories — they are noise in 99% of cases.
		{PathGlob: "**/testdata/**", Reason: "default:testdata"},
		{PathGlob: "**/test-fixtures/**", Reason: "default:test-fixtures"},
		{PathGlob: "**/fixtures/**", Reason: "default:fixtures"},
		// Test source files: only suppress findings that are commonly
		// FPs in test contexts (hardcoded fake creds, intentional unsafe
		// constructs). Real bugs in test code can still indicate a real
		// problem so we don't blanket-suppress.
		{PathGlob: "**/*_test.go", Categories: []string{"hardcoded-secret"}, Reason: "default:test-file-secrets"},
		{PathGlob: "**/test_*.py", Categories: []string{"hardcoded-secret"}, Reason: "default:test-file-secrets"},
		{PathGlob: "**/*_test.py", Categories: []string{"hardcoded-secret"}, Reason: "default:test-file-secrets"},
		{PathGlob: "**/*.test.ts", Categories: []string{"hardcoded-secret"}, Reason: "default:test-file-secrets"},
		{PathGlob: "**/*.test.tsx", Categories: []string{"hardcoded-secret"}, Reason: "default:test-file-secrets"},
		{PathGlob: "**/*.test.js", Categories: []string{"hardcoded-secret"}, Reason: "default:test-file-secrets"},
		{PathGlob: "**/*.test.jsx", Categories: []string{"hardcoded-secret"}, Reason: "default:test-file-secrets"},
		{PathGlob: "**/*.spec.ts", Categories: []string{"hardcoded-secret"}, Reason: "default:test-file-secrets"},
		{PathGlob: "**/*.spec.tsx", Categories: []string{"hardcoded-secret"}, Reason: "default:test-file-secrets"},
		{PathGlob: "**/*.spec.js", Categories: []string{"hardcoded-secret"}, Reason: "default:test-file-secrets"},
		{PathGlob: "**/__tests__/**", Categories: []string{"hardcoded-secret"}, Reason: "default:test-file-secrets"},
		{PathGlob: "**/e2e/**", Categories: []string{"hardcoded-secret"}, Reason: "default:e2e-secrets"},
		// Some projects have *_test.* JSON/text fixture files alongside
		// the test source — same suppression intent.
		{PathGlob: "**/*_output.json", Reason: "default:scanner-fixture"},
		{PathGlob: "**/*_output.jsonl", Reason: "default:scanner-fixture"},

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
