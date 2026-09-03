// Package suppress implements deterministic path/category-based false-
// positive filtering for findings. It is a post-dedupe stage that flags
// findings as Suppressed (with a reason) rather than deleting them, so the
// raw artifact (findings.json) stays auditable while the curated docs
// (FIX-HIGH.md, FIX-ALL.md) hide the noise.
//
// There are two sources of suppression rules:
//
//  1. Built-in defaults (DefaultRules) — universal patterns: vendored code,
//     generated code, test fixtures with fake secrets.
//  2. Repo-local `.wolfignore` files parsed via ParseWolfIgnore.
//
// Rules are evaluated in order; the first match wins, and the matching rule
// becomes the finding's SuppressedReason.
package suppress

import (
	"path"
	"path/filepath"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/models"
)

// Rule is a single suppression directive. Empty Categories/Rules means
// "applies to all"; non-empty means "applies only when the finding's
// fine_category or rule_id matches one of these".
type Rule struct {
	// PathGlob is a doublestar-style glob (e.g. "**/vendor/**"). Matched
	// against the finding's repo-relative FilePath. An empty PathGlob
	// matches everything.
	PathGlob string

	// Categories restricts the rule to specific FineCategory values
	// (e.g. ["hardcoded-secret"]). Empty means all categories.
	Categories []string

	// RuleIDs restricts the rule to specific RuleID values. Empty means
	// all rule IDs.
	RuleIDs []string

	// Reason is a short label surfaced in SuppressedReason. The
	// convention is "<source>:<short-name>", e.g. "default:vendor".
	Reason string
}

// RuleSet is an ordered list of suppression rules. First match wins.
type RuleSet struct {
	Rules []Rule
}

// Match returns the first matching rule and true, or zero-value + false.
func (rs RuleSet) Match(f models.Finding) (Rule, bool) {
	for _, r := range rs.Rules {
		if matches(r, f) {
			return r, true
		}
	}
	return Rule{}, false
}

// Apply walks findings and sets Suppressed/SuppressedReason on each one
// that matches a rule. Returns the number of newly-suppressed findings.
// Findings already marked Suppressed are left alone.
func Apply(findings []models.Finding, rs RuleSet) ([]models.Finding, int) {
	count := 0
	for i := range findings {
		if findings[i].Suppressed {
			continue
		}
		if r, ok := rs.Match(findings[i]); ok {
			findings[i].Suppressed = true
			findings[i].SuppressedReason = r.Reason
			count++
		}
	}
	return findings, count
}

// matches checks whether r applies to f. All three predicates (path,
// category, rule_id) must hold; an empty predicate is "any".
func matches(r Rule, f models.Finding) bool {
	if strings.HasPrefix(r.Reason, "default:lockfile") && f.Category == models.CategorySCA {
		return false
	}
	if r.PathGlob != "" && !pathMatch(r.PathGlob, f.FilePath) {
		return false
	}
	if len(r.Categories) > 0 && !contains(r.Categories, f.FineCategory) {
		return false
	}
	if len(r.RuleIDs) > 0 && !contains(r.RuleIDs, f.RuleID) {
		return false
	}
	return true
}

// pathMatch evaluates a doublestar glob against a forward-slashed path.
// Supports:
//   - "**" matches any number of path segments (including zero)
//   - "*"  matches any characters within a single segment
//   - "?"  matches a single character within a segment
//
// We implement this directly rather than pulling in a glob dependency.
// Go's path.Match is too restrictive (no "**"); filepath.Match is OS-
// dependent (backslashes on Windows). This is the documented minimal
// gitignore-compatible behavior our rules rely on.
func pathMatch(pattern, name string) bool {
	pattern = filepath.ToSlash(pattern)
	name = filepath.ToSlash(name)

	// Strip leading "./"
	name = strings.TrimPrefix(name, "./")

	// If the pattern has no "**", fall back to path.Match for each
	// segment alignment.
	if !strings.Contains(pattern, "**") {
		ok, _ := path.Match(pattern, name)
		if ok {
			return true
		}
		// Allow a leading anchor-less pattern to match any prefix segment,
		// matching gitignore semantics for relative patterns.
		return matchAnyPrefix(pattern, name)
	}
	return doubleStarMatch(strings.Split(pattern, "/"), strings.Split(name, "/"))
}

// matchAnyPrefix tries path.Match against each suffix of name's segments,
// so "foo.txt" matches "a/b/foo.txt".
func matchAnyPrefix(pattern, name string) bool {
	parts := strings.Split(name, "/")
	for i := 0; i < len(parts); i++ {
		suffix := strings.Join(parts[i:], "/")
		if ok, _ := path.Match(pattern, suffix); ok {
			return true
		}
	}
	return false
}

// doubleStarMatch matches segments where "**" can absorb 0+ segments.
// Iterative with a recursion-via-loop approach to keep stack depth bounded.
func doubleStarMatch(pat, parts []string) bool {
	pi, ni := 0, 0
	// Snapshots used when ** absorbs more segments
	var ppStar, npStar = -1, -1

	for ni < len(parts) {
		if pi < len(pat) {
			p := pat[pi]
			if p == "**" {
				ppStar = pi
				npStar = ni
				pi++
				continue
			}
			if ok, _ := path.Match(p, parts[ni]); ok {
				pi++
				ni++
				continue
			}
		}
		if ppStar >= 0 {
			pi = ppStar + 1
			npStar++
			ni = npStar
			continue
		}
		return false
	}
	// Allow trailing "**" segments to absorb nothing.
	for pi < len(pat) && pat[pi] == "**" {
		pi++
	}
	return pi == len(pat)
}

func contains(haystack []string, needle string) bool {
	if needle == "" {
		return false
	}
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

// ApplyRepo is the one matcher for UI, artifacts, and gates: built-in
// defaults, repo `.wolfignore`, then gitignore. repoPath may be empty.
func ApplyRepo(findings []models.Finding, repoPath string) ([]models.Finding, int) {
	var wolf RuleSet
	if repoPath != "" {
		wolf, _ = ParseWolfIgnoreFile(filepath.Join(repoPath, ".wolfignore"))
	}
	findings, n := Apply(findings, Combine(DefaultRules(), wolf))
	n += ApplyGitignore(findings, repoPath)
	return findings, n
}

// Combine merges multiple RuleSets in order. Later sets are appended after
// earlier ones, preserving first-match semantics within Apply.
func Combine(sets ...RuleSet) RuleSet {
	var out RuleSet
	for _, s := range sets {
		out.Rules = append(out.Rules, s.Rules...)
	}
	return out
}
