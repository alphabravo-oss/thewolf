// Package report — fix_doc.go renders the curated FIX-HIGH.md and FIX-ALL.md
// documents from a deduped/categorized finding set. These are the artifacts
// engineers (or downstream AI agents) actually read; RAW.md remains the
// "everything verbatim" report.
package report

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/alphabravocompany/thewolf/internal/finding/knowledge"
	"github.com/alphabravocompany/thewolf/internal/models"
)

// FixDocConfig is the input to the fix-doc renderers. It is intentionally
// smaller than ReportConfig — the fix docs only need findings + metadata,
// not AI summaries or tool breakdowns (those live in RAW.md).
type FixDocConfig struct {
	ScanID      string
	RepoName    string
	RepoPath    string
	Branch      string
	Commit      string
	Languages   []string
	ScannersRun []string
	GeneratedAt time.Time
	Findings    []models.Finding

	// RawTotal is the count of findings before dedup, displayed in the
	// header so the reader can see how much noise was filtered. Optional.
	RawTotal int
}

// isHighPriority returns true for findings that belong in FIX-HIGH.md:
// severity ∈ {critical, high}. Confidence (cross-tool agreement) is shown
// in the rendered output but is intentionally not used to *exclude*
// findings — single-tool detections from specialty scanners (gitleaks
// secrets, trivy CVEs) are real signal even without corroboration.
func isHighPriority(f models.Finding) bool {
	return f.Severity == models.SeverityCritical || f.Severity == models.SeverityHigh
}

// RenderFixHigh builds the FIX-HIGH.md markdown body. Findings without a
// fine_category, or marked Suppressed, are excluded from the HIGH doc —
// by definition we can't tell engineers what to do about uncategorized
// findings, and suppressed findings have been deemed noise.
func RenderFixHigh(cfg FixDocConfig) string {
	var picked []models.Finding
	for _, f := range cfg.Findings {
		if f.Suppressed {
			continue
		}
		if isHighPriority(f) && f.FineCategory != "" {
			picked = append(picked, f)
		}
	}
	return renderFixDoc(cfg, picked, "High Priority",
		"Findings with severity ∈ {critical, high} that match a curated fix strategy. Confidence (cross-tool corroboration) is shown per location. Suppressed findings are excluded; see findings.json for the full audit trail.",
		false)
}

// RenderFixAll builds the FIX-ALL.md markdown body. The main body contains
// every *non-suppressed* finding (categorized + uncategorized). Suppressed
// findings are listed in a collapsed appendix at the bottom so reviewers can
// audit what was filtered without the noise hijacking the doc.
func RenderFixAll(cfg FixDocConfig) string {
	visible := make([]models.Finding, 0, len(cfg.Findings))
	suppressed := make([]models.Finding, 0)
	for _, f := range cfg.Findings {
		if f.Suppressed {
			suppressed = append(suppressed, f)
			continue
		}
		visible = append(visible, f)
	}
	body := renderFixDoc(cfg, visible, "All Findings",
		"Every finding that passed deduplication. Suppressed findings (vendored code, generated files, test fixtures) are listed at the end.",
		true)
	if len(suppressed) > 0 {
		body += renderSuppressedAppendix(suppressed)
	}
	return body
}

// renderSuppressedAppendix renders the per-rule grouped list of suppressed
// findings in a collapsed <details> block. Markdown viewers render the
// block but hide the content until the reader expands it.
func renderSuppressedAppendix(in []models.Finding) string {
	var b strings.Builder
	b.WriteString("\n---\n\n")
	fmt.Fprintf(&b, "<details>\n<summary><strong>Suppressed (%d findings)</strong> — click to expand</summary>\n\n",
		len(in))
	b.WriteString("These findings were filtered by built-in defaults or `.wolfignore`. They\n")
	b.WriteString("remain in `findings.json` for audit, but are hidden from the main report.\n\n")

	// Group by suppressed_reason so the reader can see which rule fired.
	groups := make(map[string][]models.Finding)
	for _, f := range in {
		reason := f.SuppressedReason
		if reason == "" {
			reason = "(no reason)"
		}
		groups[reason] = append(groups[reason], f)
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "**%s** (%d)\n", k, len(groups[k]))
		for _, f := range groups[k] {
			loc := f.FilePath
			if f.LineStart > 0 {
				loc = fmt.Sprintf("%s:%d", f.FilePath, f.LineStart)
			}
			fmt.Fprintf(&b, "- `%s` — %s — %s\n", loc, f.ToolName, f.Title)
		}
		b.WriteString("\n")
	}
	b.WriteString("</details>\n")
	return b.String()
}

// renderFixDoc is the shared core: group by fine_category, render the
// fix-strategy template once per group, then list locations.
func renderFixDoc(cfg FixDocConfig, in []models.Finding, title, subtitle string, includeUncategorized bool) string {
	var b strings.Builder

	// --- Header ---
	fmt.Fprintf(&b, "# Wolf Findings — %s\n\n", title)
	if cfg.RepoName != "" {
		commit := cfg.Commit
		if commit != "" {
			commit = " @ `" + commit + "`"
		}
		fmt.Fprintf(&b, "**Repo:** %s%s\n", cfg.RepoName, commit)
	}
	if !cfg.GeneratedAt.IsZero() {
		fmt.Fprintf(&b, "**Scan:** %s\n", cfg.GeneratedAt.UTC().Format(time.RFC3339))
	}
	if cfg.ScanID != "" {
		fmt.Fprintf(&b, "**Scan ID:** %s\n", cfg.ScanID)
	}
	if len(cfg.Languages) > 0 {
		fmt.Fprintf(&b, "**Languages:** %s\n", strings.Join(cfg.Languages, ", "))
	}
	if len(cfg.ScannersRun) > 0 {
		sorted := append([]string{}, cfg.ScannersRun...)
		sort.Strings(sorted)
		fmt.Fprintf(&b, "**Scanners run:** %s\n", strings.Join(sorted, ", "))
	}
	fmt.Fprintf(&b, "\n%s\n\n", subtitle)
	fmt.Fprintf(&b, "**Total surfaced:** %d", len(in))
	if cfg.RawTotal > 0 && cfg.RawTotal != len(in) {
		fmt.Fprintf(&b, " (of %d before deduplication)", cfg.RawTotal)
	}
	b.WriteString("\n\n---\n\n")

	if len(in) == 0 {
		b.WriteString("_No findings._\n")
		return b.String()
	}

	// --- Group by fine_category ---
	groups := make(map[string][]models.Finding)
	var uncategorized []models.Finding
	for _, f := range in {
		if f.FineCategory == "" {
			uncategorized = append(uncategorized, f)
			continue
		}
		groups[f.FineCategory] = append(groups[f.FineCategory], f)
	}

	// Order categories by (count desc, name asc) so the biggest pile of
	// related issues lands at the top — that's typically where the most
	// leverage is.
	type cat struct {
		name  string
		items []models.Finding
	}
	cats := make([]cat, 0, len(groups))
	for k, v := range groups {
		cats = append(cats, cat{k, v})
	}
	sort.Slice(cats, func(i, j int) bool {
		if len(cats[i].items) != len(cats[j].items) {
			return len(cats[i].items) > len(cats[j].items)
		}
		return cats[i].name < cats[j].name
	})

	// --- Render categories ---
	for i, c := range cats {
		fmt.Fprintf(&b, "## %d. %s — %d finding%s\n\n", i+1, humanCategoryTitle(c.name), len(c.items), plural(len(c.items)))

		// Resolve the fix strategy from the first finding that has one
		// (they should all share it within a category, but be defensive).
		stratID := ""
		for _, f := range c.items {
			if f.FixStrategyID != "" {
				stratID = f.FixStrategyID
				break
			}
		}
		if strat, ok := knowledge.GetStrategy(stratID); ok {
			fmt.Fprintf(&b, "### Fix strategy: %s\n\n%s\n\n", strat.Title, strat.Body)
			if len(strat.References) > 0 {
				fmt.Fprintf(&b, "**References:** %s\n\n", strings.Join(strat.References, ", "))
			}
		}

		b.WriteString("### Locations\n\n")
		// Sort locations by severity desc, then file, then line — so the
		// scariest finding in the category leads.
		items := append([]models.Finding{}, c.items...)
		sort.Slice(items, func(a, b int) bool {
			ra, rb := severityRankFinding(items[a]), severityRankFinding(items[b])
			if ra != rb {
				return ra > rb
			}
			if items[a].FilePath != items[b].FilePath {
				return items[a].FilePath < items[b].FilePath
			}
			return items[a].LineStart < items[b].LineStart
		})
		for _, f := range items {
			fmt.Fprintln(&b, renderLocation(f))
		}
		b.WriteString("\n")
	}

	// --- Uncategorized (FIX-ALL.md only) ---
	if includeUncategorized && len(uncategorized) > 0 {
		fmt.Fprintf(&b, "## %d. Uncategorized — %d finding%s\n\n",
			len(cats)+1, len(uncategorized), plural(len(uncategorized)))
		b.WriteString("These findings did not match any curated fix strategy yet. Open the\n")
		b.WriteString("scanner's documentation for guidance.\n\n")
		for _, f := range uncategorized {
			fmt.Fprintln(&b, renderLocation(f))
		}
		b.WriteString("\n")
	}

	return b.String()
}

// renderLocation formats one finding as a markdown bullet with corroboration
// metadata when available.
func renderLocation(f models.Finding) string {
	var b strings.Builder
	loc := f.FilePath
	if f.LineStart > 0 {
		loc = fmt.Sprintf("%s:%d", f.FilePath, f.LineStart)
	}
	fmt.Fprintf(&b, "- `%s` — **%s** — %s", loc, strings.ToUpper(string(f.Severity)), f.ToolName)
	if len(f.CorroboratedBy) > 1 {
		// Show co-flagging tools (excluding the primary, which is f.ToolName).
		others := make([]string, 0, len(f.CorroboratedBy)-1)
		for _, t := range f.CorroboratedBy {
			if t != f.ToolName {
				others = append(others, t)
			}
		}
		if len(others) > 0 {
			fmt.Fprintf(&b, " _(corroborated by %s)_", strings.Join(others, ", "))
		}
	}
	if f.Title != "" {
		fmt.Fprintf(&b, " — %s", f.Title)
	}
	if f.RuleID != "" {
		fmt.Fprintf(&b, " `[%s]`", f.RuleID)
	}
	if f.CodeSnippet != "" {
		snip := strings.TrimSpace(f.CodeSnippet)
		if len(snip) > 240 {
			snip = snip[:240] + "..."
		}
		fmt.Fprintf(&b, "\n  ```\n  %s\n  ```", snip)
	}
	return b.String()
}

func severityRankFinding(f models.Finding) int {
	switch f.Severity {
	case models.SeverityCritical:
		return 5
	case models.SeverityHigh:
		return 4
	case models.SeverityMedium:
		return 3
	case models.SeverityLow:
		return 2
	case models.SeverityInfo:
		return 1
	}
	return 0
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// humanCategoryTitle turns "sql-injection" into "SQL Injection". Acronyms
// listed in the table get uppercased explicitly so "Sql Injection" doesn't
// land in the doc.
func humanCategoryTitle(s string) string {
	acronyms := map[string]string{
		"sql":  "SQL",
		"xss":  "XSS",
		"xxe":  "XXE",
		"csrf": "CSRF",
		"ssrf": "SSRF",
		"jwt":  "JWT",
		"cors": "CORS",
		"tls":  "TLS",
		"ssh":  "SSH",
		"iac":  "IaC",
		"redos": "ReDoS",
	}
	parts := strings.Split(s, "-")
	for i, p := range parts {
		if v, ok := acronyms[strings.ToLower(p)]; ok {
			parts[i] = v
			continue
		}
		if len(p) == 0 {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
}
