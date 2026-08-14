package engine

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/models"
)

// skipPrefix / fixPrefix are the only decision tokens the orchestrator
// treats as a deliberate per-finding verdict.
const (
	skipPrefix = "SKIP:"
	fixPrefix  = "FIX:"
)

// Placeholders replaced when rendering an operator-edited template.
const (
	FindingPlaceholder      = "{{finding}}"
	FindingsFilePlaceholder = "{{findings_file}}"
	ToolPlaceholder         = "{{tool}}"
	CountPlaceholder        = "{{count}}"
)

// Setting keys for editable instruction templates (org-wide settings KV).
const (
	SettingPromptInitial  = "fixer_prompt_initial"
	SettingPromptFollowup = "fixer_prompt_followup"
)

// HandsOffRules is appended to every rendered prompt, including operator
// overrides in Settings. High-level product rules — not a path list.
const HandsOffRules = `Hands-off:
- Do not break Helm/packaging: do not delete vendored chart tarballs; do not bump Chart.yaml/Chart.lock without the matching chart.
- Do not rewrite OpenAPI/Swagger API contracts.
- Version bumps: smallest upgrade that lands the fix (1.25.1 → 1.25.2, not 1.26.1). A minor only if a vuln has no patch. No Go / Postgres / Redis / frontend majors as a side effect. Do not mute Renovate.`

// DefaultInitialInstructions is the first-loop prompt: one scanner, findings
// grouped by source file. Open the file and fix what is real. Operators can
// override it in Settings; reset restores this.
const DefaultInitialInstructions = `You are Wolf's fixer for scanner ` + ToolPlaceholder + `. Open ` + FindingsFilePlaceholder + ` (` + CountPlaceholder + ` findings, grouped by source file).

Open each file and fix the real issues. Do the edits yourself. Do not spend the turn planning, classifying, or fanning out workers. Medium and low count when they are genuine.

Smallest change. Match style. Do not delete tests or touch unrelated files. Leave generated / vendor / lockfile alone unless it is a clean version bump. Leave false positives / scanner noise alone.
Do not break Helm or packaging: do not delete vendored chart tarballs, and do not bump Chart.yaml/Chart.lock without adding the matching chart. Do not rewrite OpenAPI/Swagger API contracts.
Version bumps: take the smallest upgrade that lands the fix (1.25.1 → 1.25.2, not 1.26.1). A minor only if a vuln has no patch. Do not jump Go, Postgres, Redis, or frontend majors as a side effect. Do not mute Renovate or skip a safe patch.

Optional, as their own lines:
` + "`SKIP: <finding-id> false positive — <reason>`" + `
` + "`FIX: <finding-id> <what you changed>`" + `

Stop when the files are saved. Next scanner is a later turn.
`

// DefaultFollowupInstructions is used on loop 2+ after a rescan. Same
// file-first posture on whatever is still open.
const DefaultFollowupInstructions = `You are Wolf's fixer on a FOLLOW-UP pass for scanner ` + ToolPlaceholder + `. Leftovers are in ` + FindingsFilePlaceholder + ` (` + CountPlaceholder + `), grouped by source file.

Open each leftover file and fix what is still real. Do the edits yourself. Do not plan or fan out workers. Medium and low count. Do not invent work.

Smallest change. Do not weaken tests. Leave false positives / scanner noise and generated / vendor / lockfile alone.
Do not break Helm/packaging or rewrite OpenAPI contracts. No drive-by Go / infra / frontend majors.
Version bumps: smallest upgrade that lands the fix (patch before minor; no major unless a vuln has no smaller fix). Do not mute Renovate.

Optional, as their own lines:
` + "`SKIP: <finding-id> false positive — <reason>`" + `
` + "`FIX: <finding-id> <what you changed>`" + `

Stop when the files are saved.
`

// DefaultClassifyInstructions is unused by the orchestrator (one edit turn
// per tool). Kept so Settings reset / older docs still resolve.
const DefaultClassifyInstructions = `You are Wolf's fixer CLASSIFYING scanner ` + ToolPlaceholder + `. The findings review file is IN THIS REPO at ` + FindingsFilePlaceholder + ` (` + CountPlaceholder + `). Open that file and read it.

## Goal
Do not edit any files. Do not run formatters or builds. Decide which findings are real local line-of-code fixes.

Use the task tool. Fan out at most 4 task workers. One owner per file for reading only. Parent reprints every ` + "`FIX:`" + ` and ` + "`SKIP:`" + ` line.

Print exactly ` + "`SKIP: <finding-id> <one-line reason>`" + ` for false positives / scanner noise (use those words so Wolf mutes them), generated/vendor/lockfile, or empty path.
Print exactly ` + "`FIX: <finding-id> <one-line what you would change>`" + ` for genuine local code issues you will edit in the next turn.

The parent MUST reprint every token as its own line. If you print no FIX lines, Wolf will still run an edit turn on every finding that has a file path.
`

// InstructionForLoop picks the first-pass or follow-up template. Empty
// overrides fall back to the shipped defaults.
func InstructionForLoop(loop int, initial, followup string) string {
	if loop >= 2 {
		if s := strings.TrimSpace(followup); s != "" {
			return s
		}
		return DefaultFollowupInstructions
	}
	if s := strings.TrimSpace(initial); s != "" {
		return s
	}
	return DefaultInitialInstructions
}

func buildPrompt(f models.Finding) string {
	return RenderPrompt("", FixRequest{Finding: f})
}

// RenderPrompt applies an instruction template and injects tool / file /
// count / finding placeholders. If the template omits {{findings_file}}
// and {{finding}}, the review-file path (or a single finding block) is
// appended so a truncated edit cannot drop the issue details.
func RenderPrompt(instructions string, req FixRequest) string {
	batch := req.Batch()
	tmpl := strings.TrimSpace(instructions)
	if tmpl == "" {
		tmpl = DefaultInitialInstructions
	}
	tool := strings.TrimSpace(req.Tool)
	if tool == "" && len(batch) > 0 {
		tool = strings.TrimSpace(batch[0].ToolName)
	}
	if tool == "" {
		tool = "unknown"
	}
	file := strings.TrimSpace(req.FindingsFile)
	tmpl = strings.ReplaceAll(tmpl, ToolPlaceholder, tool)
	tmpl = strings.ReplaceAll(tmpl, FindingsFilePlaceholder, file)
	tmpl = strings.ReplaceAll(tmpl, CountPlaceholder, fmt.Sprintf("%d", len(batch)))
	tmpl = appendHandsOff(tmpl)
	body := ""
	if file != "" {
		body = fmt.Sprintf("Findings file (in this repo): %s\nTool: %s\nCount: %d\n\n%s",
			file, tool, len(batch), inventoryBlock(batch))
	} else if len(batch) == 1 {
		body = findingBlock(batch[0])
	} else if len(batch) > 1 {
		body = FormatFindingsFile(tool, batch)
	}
	if strings.Contains(tmpl, FindingPlaceholder) {
		if body == "" {
			body = findingBlock(models.Finding{})
		}
		return strings.ReplaceAll(tmpl, FindingPlaceholder, body)
	}
	if body == "" {
		return tmpl
	}
	return tmpl + "\n\n" + body
}

func appendHandsOff(tmpl string) string {
	if strings.Contains(tmpl, HandsOffRules) {
		return tmpl
	}
	return strings.TrimRight(tmpl, "\n") + "\n\n" + HandsOffRules
}

// FormatFindingsFile is the review document handed to the agent for one
// scanner, grouped by source file so the job is "open this file and fix it".
func FormatFindingsFile(tool string, findings []models.Finding) string {
	groups := groupFindingsByFile(findings)
	var sb strings.Builder
	fmt.Fprintf(&sb, "# %s findings (%d) in %d files\n\n", tool, len(findings), len(groups))
	sb.WriteString("Open each file. Fix what is real. Skip junk.\n\n")
	n := 0
	for _, g := range groups {
		label := g.path
		if label == "" {
			label = "(no file)"
		}
		fmt.Fprintf(&sb, "## %s\n", label)
		for _, f := range g.findings {
			n++
			fmt.Fprintf(&sb, "### %d. %s\n", n, FindingLabel(f))
			fmt.Fprintf(&sb, "- id: %s\n", f.ID)
			if f.LineStart > 0 {
				fmt.Fprintf(&sb, "- line: %d\n", f.LineStart)
			}
			if f.RuleID != "" {
				fmt.Fprintf(&sb, "- rule: %s\n", f.RuleID)
			}
			if f.Confidence != "" {
				fmt.Fprintf(&sb, "- confidence: %s\n", f.Confidence)
			}
			if f.CWEID != "" {
				fmt.Fprintf(&sb, "- cwe: %s\n", f.CWEID)
			}
			if desc := compactText(f.Description, 400); desc != "" {
				fmt.Fprintf(&sb, "\n%s\n", desc)
			}
			if snip := compactText(f.CodeSnippet, 500); snip != "" {
				fmt.Fprintf(&sb, "\n```\n%s\n```\n", snip)
			}
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

type fileGroup struct {
	path     string
	findings []models.Finding
}

func groupFindingsByFile(findings []models.Finding) []fileGroup {
	order := make([]string, 0)
	by := map[string][]models.Finding{}
	for _, f := range findings {
		p := strings.TrimSpace(f.FilePath)
		if _, ok := by[p]; !ok {
			order = append(order, p)
		}
		by[p] = append(by[p], f)
	}
	rank := func(p string) int {
		best := 0
		for _, f := range by[p] {
			if r := fileSeverityRank(f.Severity); r > best {
				best = r
			}
		}
		return best
	}
	sort.SliceStable(order, func(i, j int) bool {
		if order[i] == "" {
			return false
		}
		if order[j] == "" {
			return true
		}
		ri, rj := rank(order[i]), rank(order[j])
		if ri != rj {
			return ri > rj
		}
		return order[i] < order[j]
	})
	out := make([]fileGroup, 0, len(order))
	for _, p := range order {
		out = append(out, fileGroup{path: p, findings: by[p]})
	}
	return out
}

func inventoryBlock(findings []models.Finding) string {
	const head = 30
	var sb strings.Builder
	sb.WriteString("## Inventory (start here; full detail is in the review file)\n")
	n := len(findings)
	if n > head {
		n = head
	}
	for i := 0; i < n; i++ {
		fmt.Fprintf(&sb, "%d. %s\n   id: %s\n", i+1, FindingLabel(findings[i]), findings[i].ID)
	}
	if len(findings) > head {
		fmt.Fprintf(&sb, "… and %d more in the review file.\n", len(findings)-head)
	}
	return sb.String()
}

// FindingLabel is the one-line identity used in logs and the review file.
func FindingLabel(f models.Finding) string {
	title := strings.TrimSpace(f.Title)
	if title == "" {
		title = f.RuleID
	}
	if title == "" {
		title = f.ID
	}
	file := f.FilePath
	if f.LineStart > 0 {
		file = fmt.Sprintf("%s:%d", f.FilePath, f.LineStart)
	}
	tool := strings.TrimSpace(f.ToolName)
	sev := strings.TrimSpace(string(f.Severity))
	switch {
	case tool != "" && sev != "":
		return fmt.Sprintf("[%s] %s %s — %s", sev, tool, file, title)
	case sev != "":
		return fmt.Sprintf("[%s] %s — %s", sev, file, title)
	default:
		return fmt.Sprintf("%s — %s", file, title)
	}
}

func fileSeverityRank(s models.Severity) int {
	switch s {
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
	default:
		return 0
	}
}

func compactText(s string, max int) string {
	s = strings.TrimSpace(s)
	if max > 0 && len(s) > max {
		return s[:max] + "…"
	}
	return s
}

// Decision is one SKIP/FIX line parsed from engine output.
type Decision struct {
	Kind string // skip | fix
	ID   string
	Note string
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

func stripANSI(s string) string {
	return ansiRe.ReplaceAllString(s, "")
}

// ParseDecisions extracts SKIP:/FIX: tokens from engine output. Tokens may
// sit on their own line, several on one line, or inside OpenCode JSON
// `text` / `say` fields. The id may be a full UUID or an 8-character prefix.
func ParseDecisions(output string) []Decision {
	raw := flattenEngineOutput(output)
	var out []Decision
	seen := map[string]int{}
	for _, d := range scanDecisionTokens(raw) {
		key := d.Kind + "|" + d.ID
		if d.ID == "" {
			key = d.Kind + "|" + d.Note
		}
		if i, ok := seen[key]; ok {
			out[i] = d
			continue
		}
		seen[key] = len(out)
		out = append(out, d)
	}
	return out
}

func flattenEngineOutput(s string) string {
	s = stripANSI(s)
	var extra []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var v any
		if json.Unmarshal([]byte(line), &v) != nil {
			continue
		}
		walkJSONStrings(v, &extra)
	}
	if len(extra) == 0 {
		return s
	}
	return s + "\n" + strings.Join(extra, "\n")
}

func walkJSONStrings(v any, out *[]string) {
	switch t := v.(type) {
	case string:
		if strings.Contains(t, skipPrefix) || strings.Contains(t, fixPrefix) {
			*out = append(*out, t)
		}
	case []any:
		for _, x := range t {
			walkJSONStrings(x, out)
		}
	case map[string]any:
		for _, x := range t {
			walkJSONStrings(x, out)
		}
	}
}

func scanDecisionTokens(s string) []Decision {
	var out []Decision
	for i := 0; i < len(s); {
		skipAt := indexFrom(s, skipPrefix, i)
		fixAt := indexFrom(s, fixPrefix, i)
		var next int
		var kind string
		var tokLen int
		switch {
		case skipAt >= 0 && (fixAt < 0 || skipAt < fixAt):
			next, kind, tokLen = skipAt, "skip", len(skipPrefix)
		case fixAt >= 0:
			next, kind, tokLen = fixAt, "fix", len(fixPrefix)
		default:
			return out
		}
		start := next + tokLen
		end := len(s)
		if n := indexFrom(s, skipPrefix, start); n >= 0 && n < end {
			end = n
		}
		if n := indexFrom(s, fixPrefix, start); n >= 0 && n < end {
			end = n
		}
		rest := strings.TrimSpace(s[start:end])
		if cut := strings.IndexAny(rest, "\n\r"); cut >= 0 {
			rest = rest[:cut]
		}
		rest = strings.Trim(rest, " \t\"'`")
		id, note := splitDecision(rest)
		if id == "" && (strings.Contains(rest, "<finding-id>") || strings.Contains(rest, "<what you")) {
			i = end
			continue
		}
		if id != "" && !looksLikeFindingID(id) {
			i = end
			continue
		}
		if note == "" && kind == "skip" {
			note = "model skipped this finding"
		}
		if id == "" && note == "" {
			i = end
			continue
		}
		out = append(out, Decision{Kind: kind, ID: id, Note: note})
		i = end
	}
	return out
}

func indexFrom(s, sub string, from int) int {
	if from < 0 || from > len(s) {
		return -1
	}
	n := strings.Index(s[from:], sub)
	if n < 0 {
		return -1
	}
	return from + n
}

func splitDecision(rest string) (id, note string) {
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return "", ""
	}
	fields := strings.Fields(rest)
	if looksLikeFindingID(fields[0]) {
		return fields[0], strings.TrimSpace(strings.TrimPrefix(rest, fields[0]))
	}
	return "", rest
}

func looksLikeFindingID(s string) bool {
	if len(s) < 8 {
		return false
	}
	for _, r := range s {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F') || r == '-' {
			continue
		}
		return false
	}
	return true
}

// MatchDecisionID maps a (possibly short) id from the model onto a finding.
func MatchDecisionID(raw string, findings []models.Finding) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	for _, f := range findings {
		if f.ID == raw || strings.HasPrefix(f.ID, raw) || (len(raw) >= 8 && strings.HasPrefix(raw, f.ID[:8])) {
			return f.ID
		}
	}
	return ""
}

func findingBlock(f models.Finding) string {
	var sb strings.Builder
	sb.WriteString("## Finding\n")
	fmt.Fprintf(&sb, "Title: %s\n", f.Title)
	fmt.Fprintf(&sb, "File: %s:%d\n", f.FilePath, f.LineStart)
	fmt.Fprintf(&sb, "Severity: %s\n", f.Severity)
	if f.Confidence != "" {
		fmt.Fprintf(&sb, "Confidence: %s\n", f.Confidence)
	}
	if f.ToolName != "" {
		fmt.Fprintf(&sb, "Tool: %s\n", f.ToolName)
	}
	if f.RuleID != "" {
		fmt.Fprintf(&sb, "Rule: %s\n", f.RuleID)
	}
	if f.CWEID != "" {
		fmt.Fprintf(&sb, "CWE: %s\n", f.CWEID)
	}
	if f.FineCategory != "" {
		fmt.Fprintf(&sb, "Category: %s\n", f.FineCategory)
	}
	if len(f.CorroboratedBy) > 0 {
		fmt.Fprintf(&sb, "Corroborated by: %s\n", strings.Join(f.CorroboratedBy, ", "))
	}
	fmt.Fprintf(&sb, "Description: %s\n", f.Description)
	if f.CodeSnippet != "" {
		fmt.Fprintf(&sb, "Code:\n%s\n", f.CodeSnippet)
	}
	if f.AIFixSuggestion != "" {
		fmt.Fprintf(&sb, "Suggestion: %s\n", f.AIFixSuggestion)
	}
	if f.AIFixPrompt != "" {
		fmt.Fprintf(&sb, "\nEnrichment:\n%s\n", f.AIFixPrompt)
	}
	return sb.String()
}

func detectSkip(output string) (string, bool) {
	for _, d := range ParseDecisions(output) {
		if d.Kind == "skip" {
			return d.Note, true
		}
	}
	return "", false
}

func applySkipVerdict(res *FixResult, ids []string) {
	if res == nil {
		return
	}
	decs := ParseDecisions(res.Output)
	if len(decs) == 0 {
		return
	}
	fixes, skips := 0, 0
	var firstSkip string
	for _, d := range decs {
		switch d.Kind {
		case "fix":
			fixes++
		case "skip":
			skips++
			if firstSkip == "" {
				firstSkip = d.Note
			}
		}
	}
	// A single-finding run that printed SKIP (and no FIX) is a whole-run skip.
	// A multi-finding run is only a whole-run skip when every line is SKIP
	// and nothing was marked FIX — the orchestrator still classifies leftovers.
	if fixes > 0 {
		return
	}
	if len(ids) > 1 && skips < len(ids) {
		return
	}
	if firstSkip == "" {
		return
	}
	res.Skipped = true
	res.SkipReason = firstSkip
	res.Success = false
	res.Error = skipPrefix + " " + firstSkip
}
