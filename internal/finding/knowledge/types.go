// Package knowledge holds the deterministic rule → category → fix-strategy
// mappings used to enrich and group findings without AI. Each scanner has
// its own data file (data_<tool>.go) that registers entries via init().
// Fix-strategy templates are markdown blocks keyed by strategy ID.
package knowledge

// Entry maps one tool rule to its fine-grained category and fix strategy.
// References are CWE/OWASP/docs links surfaced in fix docs.
type Entry struct {
	Tool         string   // e.g. "gosec"
	RuleID       string   // e.g. "G201"
	FineCategory string   // e.g. "sql-injection"
	FixStrategy  string   // e.g. "parameterize-query"
	References   []string // e.g. ["CWE-89", "OWASP A03:2021"]
}

// Strategy is a fix-strategy template. The body is markdown surfaced as a
// section heading in FIX-*.md docs (rendered once per category).
type Strategy struct {
	ID         string   // e.g. "parameterize-query"
	Title      string   // e.g. "Use parameterized queries"
	AppliesTo  []string // fine_categories this strategy applies to
	Body       string   // markdown body (no front matter); rendered verbatim
	References []string // extra refs surfaced under "References:"
}
