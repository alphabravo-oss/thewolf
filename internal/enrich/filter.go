package enrich

import (
	"path"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/models"
)

// Filter selects which findings an enrich run targets. A zero Filter
// (all fields empty) matches every finding. Set fields combine with AND
// semantics; within a field, values combine with OR.
type Filter struct {
	// Severities, when non-empty, restricts to these severities
	// (critical/high/medium/low/info).
	Severities []string
	// Categories, when non-empty, restricts to these coarse categories
	// (sast/sca/secrets/quality/docs/...).
	Categories []string
	// Tools, when non-empty, restricts to findings from these tools.
	Tools []string
	// IDs, when non-empty, restricts to these specific finding IDs.
	IDs []string
	// ExcludePaths is a list of doublestar-style globs; a finding whose
	// FilePath matches any of them is excluded.
	ExcludePaths []string
}

// Match reports whether the finding passes the filter.
func (f Filter) Match(fn models.Finding) bool {
	if len(f.Severities) > 0 && !containsFold(f.Severities, string(fn.Severity)) {
		return false
	}
	if len(f.Categories) > 0 && !containsFold(f.Categories, string(fn.Category)) {
		return false
	}
	if len(f.Tools) > 0 && !containsFold(f.Tools, fn.ToolName) {
		return false
	}
	if len(f.IDs) > 0 && !containsExact(f.IDs, fn.ID) {
		return false
	}
	for _, g := range f.ExcludePaths {
		if pathGlobMatch(g, fn.FilePath) {
			return false
		}
	}
	return true
}

// IsEmpty reports whether the filter targets everything (no constraints).
func (f Filter) IsEmpty() bool {
	return len(f.Severities) == 0 && len(f.Categories) == 0 &&
		len(f.Tools) == 0 && len(f.IDs) == 0 && len(f.ExcludePaths) == 0
}

func containsFold(haystack []string, needle string) bool {
	for _, h := range haystack {
		if strings.EqualFold(strings.TrimSpace(h), strings.TrimSpace(needle)) {
			return true
		}
	}
	return false
}

func containsExact(haystack []string, needle string) bool {
	for _, h := range haystack {
		if strings.TrimSpace(h) == needle {
			return true
		}
	}
	return false
}

// pathGlobMatch evaluates a doublestar-style glob against a forward-slashed
// path. Supports "**" (any number of segments, including zero), "*", "?".
// A pattern with no "**" also matches any path suffix, so "test/**" and
// "*.go" both behave like .gitignore-style relative patterns.
func pathGlobMatch(pattern, name string) bool {
	pattern = strings.TrimPrefix(strings.ReplaceAll(pattern, "\\", "/"), "./")
	name = strings.TrimPrefix(strings.ReplaceAll(name, "\\", "/"), "./")
	if pattern == "" || name == "" {
		return false
	}
	if strings.Contains(pattern, "**") {
		return doubleStarMatch(strings.Split(pattern, "/"), strings.Split(name, "/"))
	}
	if ok, _ := path.Match(pattern, name); ok {
		return true
	}
	parts := strings.Split(name, "/")
	for i := range parts {
		if ok, _ := path.Match(pattern, strings.Join(parts[i:], "/")); ok {
			return true
		}
	}
	return false
}

// doubleStarMatch matches path segments where "**" absorbs 0+ segments.
func doubleStarMatch(pat, parts []string) bool {
	pi, ni := 0, 0
	ppStar, npStar := -1, -1
	for ni < len(parts) {
		if pi < len(pat) {
			if pat[pi] == "**" {
				ppStar, npStar = pi, ni
				pi++
				continue
			}
			if ok, _ := path.Match(pat[pi], parts[ni]); ok {
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
	for pi < len(pat) && pat[pi] == "**" {
		pi++
	}
	return pi == len(pat)
}
