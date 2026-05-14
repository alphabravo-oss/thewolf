package knowledge

// semgrep mappings cover the most frequently emitted rules in the
// default security + correctness rulesets.

func init() {
	for _, e := range semgrepEntries() {
		RegisterEntry(e)
	}
}

func semgrepEntries() []Entry {
	const t = "semgrep"
	return []Entry{
		// Python — Flask
		{Tool: t, RuleID: "python.flask.security.audit.xss.flask-no-escape-template", FineCategory: "xss", FixStrategy: "escape-html-output"},
		{Tool: t, RuleID: "python.flask.security.injection.tainted-sql-string", FineCategory: "sql-injection", FixStrategy: "parameterize-query"},
		{Tool: t, RuleID: "python.flask.security.audit.debug-enabled", FineCategory: "debug-exposure", FixStrategy: "remove-debug-endpoints"},

		// Python — Django
		{Tool: t, RuleID: "python.django.security.audit.unsafe-template-use", FineCategory: "xss", FixStrategy: "escape-html-output"},
		{Tool: t, RuleID: "python.django.security.audit.raw-query", FineCategory: "sql-injection", FixStrategy: "parameterize-query"},
		{Tool: t, RuleID: "python.django.security.audit.csrf-exempt", FineCategory: "csrf", FixStrategy: "tool-docs-fallback"},

		// Python — generic
		{Tool: t, RuleID: "python.lang.security.audit.dangerous-subprocess-use.dangerous-subprocess-use", FineCategory: "command-injection", FixStrategy: "use-arg-list-no-shell"},
		{Tool: t, RuleID: "python.lang.security.audit.exec-detected.exec-detected", FineCategory: "code-injection", FixStrategy: "tool-docs-fallback"},
		{Tool: t, RuleID: "python.lang.security.audit.eval-detected.eval-detected", FineCategory: "code-injection", FixStrategy: "tool-docs-fallback"},
		{Tool: t, RuleID: "python.lang.security.audit.dangerous-system-call.dangerous-system-call", FineCategory: "command-injection", FixStrategy: "use-arg-list-no-shell"},
		{Tool: t, RuleID: "python.lang.security.audit.insecure-hash-algorithms-md5.insecure-hash-algorithm-md5", FineCategory: "weak-crypto", FixStrategy: "replace-weak-hash"},
		{Tool: t, RuleID: "python.lang.security.audit.insecure-hash-algorithms-sha1.insecure-hash-algorithm-sha1", FineCategory: "weak-crypto", FixStrategy: "replace-weak-hash"},

		// JavaScript / TypeScript
		{Tool: t, RuleID: "javascript.lang.security.audit.code-string-concat.code-string-concat", FineCategory: "code-injection", FixStrategy: "tool-docs-fallback"},
		{Tool: t, RuleID: "javascript.lang.security.audit.detect-non-literal-regexp.detect-non-literal-regexp", FineCategory: "redos", FixStrategy: "validate-input"},
		{Tool: t, RuleID: "javascript.lang.security.audit.detect-eval-with-expression.detect-eval-with-expression", FineCategory: "code-injection", FixStrategy: "tool-docs-fallback"},
		{Tool: t, RuleID: "javascript.express.security.audit.express-cookie-session-no-secure.express-cookie-session-no-secure", FineCategory: "insecure-cookie", FixStrategy: "set-secure-cookie-flags"},
		{Tool: t, RuleID: "javascript.express.security.audit.express-cookie-session-no-httponly.express-cookie-session-no-httponly", FineCategory: "insecure-cookie", FixStrategy: "set-secure-cookie-flags"},

		// Go
		{Tool: t, RuleID: "go.lang.security.audit.crypto.use-of-md5.use-of-md5", FineCategory: "weak-crypto", FixStrategy: "replace-weak-hash"},
		{Tool: t, RuleID: "go.lang.security.audit.crypto.use-of-sha1.use-of-sha1", FineCategory: "weak-crypto", FixStrategy: "replace-weak-hash"},
		{Tool: t, RuleID: "go.lang.security.audit.crypto.math-random.math-random-used", FineCategory: "insecure-randomness", FixStrategy: "use-crypto-rand"},
		{Tool: t, RuleID: "go.lang.security.audit.dangerous-exec-command.dangerous-exec-command", FineCategory: "command-injection", FixStrategy: "use-arg-list-no-shell"},
		{Tool: t, RuleID: "go.lang.security.audit.database.string-formatted-query.string-formatted-query", FineCategory: "sql-injection", FixStrategy: "parameterize-query"},

		// Generic secrets
		{Tool: t, RuleID: "generic.secrets.security.detected-aws-account-id.detected-aws-account-id", FineCategory: "hardcoded-secret", FixStrategy: "rotate-and-remove-secret"},
		{Tool: t, RuleID: "generic.secrets.security.detected-private-key.detected-private-key", FineCategory: "hardcoded-secret", FixStrategy: "rotate-and-remove-secret"},
	}
}

// CategorizeBySemgrepPrefix is a fallback for semgrep rule_ids we haven't
// individually mapped. It returns a (category, strategy) pair derived from
// the rule's namespace path. Empty strings mean "no inferred mapping".
func CategorizeBySemgrepPrefix(ruleID string) (fineCat, strategy string) {
	switch {
	case containsPath(ruleID, "sqli", "tainted-sql"):
		return "sql-injection", "parameterize-query"
	case containsPath(ruleID, "xss"):
		return "xss", "escape-html-output"
	case containsPath(ruleID, "command-injection", "subprocess-shell", "shell-injection"):
		return "command-injection", "use-arg-list-no-shell"
	case containsPath(ruleID, "md5", "sha1", "weak-hash"):
		return "weak-crypto", "replace-weak-hash"
	case containsPath(ruleID, "secret", "private-key"):
		return "hardcoded-secret", "rotate-and-remove-secret"
	case containsPath(ruleID, "open-redirect", "untrusted-redirect"):
		return "open-redirect", "validate-redirect-target"
	case containsPath(ruleID, "path-traversal", "tainted-path"):
		return "path-traversal", "validate-input"
	case containsPath(ruleID, "deserialization", "yaml.load"):
		return "unsafe-deserialization", "use-safer-yaml-load"
	}
	return "", ""
}

// containsPath checks whether any of needles appears anywhere in the
// dot-separated rule_id. The substring match is intentional — semgrep
// includes the family name at varying depths.
func containsPath(ruleID string, needles ...string) bool {
	for _, n := range needles {
		if indexOf(ruleID, n) >= 0 {
			return true
		}
	}
	return false
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
