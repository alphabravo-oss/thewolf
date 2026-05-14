package knowledge

// gosec G-series rule mappings. Source: https://github.com/securego/gosec
// Coverage target: all G-series rules emitted by the version pinned in
// scanners/versions.env. New rule IDs from gosec upgrades should land here.

func init() {
	for _, e := range gosecEntries() {
		RegisterEntry(e)
	}
}

func gosecEntries() []Entry {
	const t = "gosec"
	return []Entry{
		// Crypto / TLS
		{Tool: t, RuleID: "G101", FineCategory: "hardcoded-secret", FixStrategy: "rotate-and-remove-secret", References: []string{"CWE-798"}},
		{Tool: t, RuleID: "G102", FineCategory: "insecure-network-bind", FixStrategy: "tool-docs-fallback"},
		{Tool: t, RuleID: "G103", FineCategory: "unsafe-pointer-use", FixStrategy: "tool-docs-fallback"},
		{Tool: t, RuleID: "G104", FineCategory: "unchecked-error", FixStrategy: "tool-docs-fallback"},
		{Tool: t, RuleID: "G106", FineCategory: "insecure-ssh", FixStrategy: "tool-docs-fallback"},
		{Tool: t, RuleID: "G107", FineCategory: "ssrf", FixStrategy: "validate-input"},
		{Tool: t, RuleID: "G108", FineCategory: "debug-exposure", FixStrategy: "remove-debug-endpoints"},
		{Tool: t, RuleID: "G109", FineCategory: "integer-overflow", FixStrategy: "validate-input"},
		{Tool: t, RuleID: "G110", FineCategory: "decompression-bomb", FixStrategy: "validate-input"},

		// File system / path
		{Tool: t, RuleID: "G201", FineCategory: "sql-injection", FixStrategy: "parameterize-query", References: []string{"CWE-89"}},
		{Tool: t, RuleID: "G202", FineCategory: "sql-injection", FixStrategy: "parameterize-query", References: []string{"CWE-89"}},
		{Tool: t, RuleID: "G203", FineCategory: "xss", FixStrategy: "escape-html-output"},
		{Tool: t, RuleID: "G204", FineCategory: "command-injection", FixStrategy: "use-arg-list-no-shell"},

		// File-system permissions
		{Tool: t, RuleID: "G301", FineCategory: "insecure-permissions", FixStrategy: "tool-docs-fallback"},
		{Tool: t, RuleID: "G302", FineCategory: "insecure-permissions", FixStrategy: "tool-docs-fallback"},
		{Tool: t, RuleID: "G303", FineCategory: "tempfile-creation", FixStrategy: "tool-docs-fallback"},
		{Tool: t, RuleID: "G304", FineCategory: "path-traversal", FixStrategy: "validate-input", References: []string{"CWE-22"}},
		{Tool: t, RuleID: "G305", FineCategory: "path-traversal", FixStrategy: "validate-input"},
		{Tool: t, RuleID: "G306", FineCategory: "insecure-permissions", FixStrategy: "tool-docs-fallback"},
		{Tool: t, RuleID: "G307", FineCategory: "unchecked-close", FixStrategy: "tool-docs-fallback"},

		// Crypto
		{Tool: t, RuleID: "G401", FineCategory: "weak-crypto", FixStrategy: "replace-weak-hash", References: []string{"CWE-327"}},
		{Tool: t, RuleID: "G402", FineCategory: "insecure-transport", FixStrategy: "use-https-and-tls12-plus"},
		{Tool: t, RuleID: "G403", FineCategory: "weak-crypto", FixStrategy: "tool-docs-fallback"},
		{Tool: t, RuleID: "G404", FineCategory: "insecure-randomness", FixStrategy: "use-crypto-rand", References: []string{"CWE-338"}},
		{Tool: t, RuleID: "G405", FineCategory: "weak-crypto", FixStrategy: "tool-docs-fallback"},
		{Tool: t, RuleID: "G406", FineCategory: "weak-crypto", FixStrategy: "replace-weak-hash"},

		// Blocklisted imports
		{Tool: t, RuleID: "G501", FineCategory: "weak-crypto", FixStrategy: "replace-weak-hash"},
		{Tool: t, RuleID: "G502", FineCategory: "weak-crypto", FixStrategy: "replace-weak-hash"},
		{Tool: t, RuleID: "G503", FineCategory: "weak-crypto", FixStrategy: "replace-weak-hash"},
		{Tool: t, RuleID: "G504", FineCategory: "insecure-transport", FixStrategy: "use-https-and-tls12-plus"},
		{Tool: t, RuleID: "G505", FineCategory: "weak-crypto", FixStrategy: "replace-weak-hash"},

		// Misc
		{Tool: t, RuleID: "G601", FineCategory: "implicit-memory-aliasing", FixStrategy: "tool-docs-fallback"},
		{Tool: t, RuleID: "G602", FineCategory: "slice-bounds", FixStrategy: "validate-input"},
	}
}
