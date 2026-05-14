package knowledge

// bandit B-series rule mappings. Source: https://bandit.readthedocs.io/

func init() {
	for _, e := range banditEntries() {
		RegisterEntry(e)
	}
}

func banditEntries() []Entry {
	const t = "bandit"
	return []Entry{
		// Crypto
		{Tool: t, RuleID: "B303", FineCategory: "weak-crypto", FixStrategy: "replace-weak-hash"},
		{Tool: t, RuleID: "B304", FineCategory: "weak-crypto", FixStrategy: "replace-weak-hash"},
		{Tool: t, RuleID: "B305", FineCategory: "weak-crypto", FixStrategy: "tool-docs-fallback"},
		{Tool: t, RuleID: "B311", FineCategory: "insecure-randomness", FixStrategy: "use-crypto-rand"},
		{Tool: t, RuleID: "B324", FineCategory: "weak-crypto", FixStrategy: "replace-weak-hash"},

		// Shell/exec
		{Tool: t, RuleID: "B602", FineCategory: "command-injection", FixStrategy: "use-arg-list-no-shell"},
		{Tool: t, RuleID: "B603", FineCategory: "command-injection", FixStrategy: "use-arg-list-no-shell"},
		{Tool: t, RuleID: "B604", FineCategory: "command-injection", FixStrategy: "use-arg-list-no-shell"},
		{Tool: t, RuleID: "B605", FineCategory: "command-injection", FixStrategy: "use-arg-list-no-shell"},
		{Tool: t, RuleID: "B606", FineCategory: "command-injection", FixStrategy: "use-arg-list-no-shell"},
		{Tool: t, RuleID: "B607", FineCategory: "command-injection", FixStrategy: "use-arg-list-no-shell"},
		{Tool: t, RuleID: "B609", FineCategory: "command-injection", FixStrategy: "use-arg-list-no-shell"},

		// SQL
		{Tool: t, RuleID: "B608", FineCategory: "sql-injection", FixStrategy: "parameterize-query"},

		// Deserialization
		{Tool: t, RuleID: "B301", FineCategory: "unsafe-deserialization", FixStrategy: "tool-docs-fallback"},
		{Tool: t, RuleID: "B302", FineCategory: "unsafe-deserialization", FixStrategy: "tool-docs-fallback"},
		{Tool: t, RuleID: "B306", FineCategory: "unsafe-deserialization", FixStrategy: "tool-docs-fallback"},
		{Tool: t, RuleID: "B307", FineCategory: "command-injection", FixStrategy: "use-arg-list-no-shell"},
		{Tool: t, RuleID: "B506", FineCategory: "unsafe-deserialization", FixStrategy: "use-safer-yaml-load"},

		// Hardcoded secrets
		{Tool: t, RuleID: "B105", FineCategory: "hardcoded-secret", FixStrategy: "rotate-and-remove-secret"},
		{Tool: t, RuleID: "B106", FineCategory: "hardcoded-secret", FixStrategy: "rotate-and-remove-secret"},
		{Tool: t, RuleID: "B107", FineCategory: "hardcoded-secret", FixStrategy: "rotate-and-remove-secret"},

		// SSL/TLS
		{Tool: t, RuleID: "B501", FineCategory: "insecure-transport", FixStrategy: "use-https-and-tls12-plus"},
		{Tool: t, RuleID: "B502", FineCategory: "insecure-transport", FixStrategy: "use-https-and-tls12-plus"},
		{Tool: t, RuleID: "B503", FineCategory: "insecure-transport", FixStrategy: "use-https-and-tls12-plus"},
		{Tool: t, RuleID: "B504", FineCategory: "insecure-transport", FixStrategy: "use-https-and-tls12-plus"},
		{Tool: t, RuleID: "B505", FineCategory: "weak-crypto", FixStrategy: "tool-docs-fallback"},

		// XML / parsing
		{Tool: t, RuleID: "B313", FineCategory: "xxe", FixStrategy: "tool-docs-fallback"},
		{Tool: t, RuleID: "B314", FineCategory: "xxe", FixStrategy: "tool-docs-fallback"},
		{Tool: t, RuleID: "B315", FineCategory: "xxe", FixStrategy: "tool-docs-fallback"},
		{Tool: t, RuleID: "B316", FineCategory: "xxe", FixStrategy: "tool-docs-fallback"},
		{Tool: t, RuleID: "B317", FineCategory: "xxe", FixStrategy: "tool-docs-fallback"},
		{Tool: t, RuleID: "B318", FineCategory: "xxe", FixStrategy: "tool-docs-fallback"},
		{Tool: t, RuleID: "B319", FineCategory: "xxe", FixStrategy: "tool-docs-fallback"},
		{Tool: t, RuleID: "B320", FineCategory: "xxe", FixStrategy: "tool-docs-fallback"},

		// Misc
		{Tool: t, RuleID: "B102", FineCategory: "code-injection", FixStrategy: "tool-docs-fallback"},
		{Tool: t, RuleID: "B103", FineCategory: "insecure-permissions", FixStrategy: "tool-docs-fallback"},
		{Tool: t, RuleID: "B104", FineCategory: "insecure-network-bind", FixStrategy: "tool-docs-fallback"},
		{Tool: t, RuleID: "B108", FineCategory: "tempfile-creation", FixStrategy: "tool-docs-fallback"},
		{Tool: t, RuleID: "B110", FineCategory: "exception-swallowed", FixStrategy: "tool-docs-fallback"},
	}
}
