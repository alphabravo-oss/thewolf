package enricher

import (
	"sort"

	"github.com/alphabravocompany/thewolf/internal/scan/mapper"
)

// ResolveEnclosingSymbol finds the nearest symbol declaration at or above the given
// line in the same file. Returns (name, kind). Prefers function/method over class.
func ResolveEnclosingSymbol(symbols []mapper.Symbol, filePath string, line int) (string, string) {
	if len(symbols) == 0 || line <= 0 {
		return "", ""
	}

	// Filter to same file and sort by line descending
	var candidates []mapper.Symbol
	for _, s := range symbols {
		if s.FilePath == filePath && s.Line <= line {
			candidates = append(candidates, s)
		}
	}

	if len(candidates) == 0 {
		return "", ""
	}

	// Sort by line descending (closest first)
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Line > candidates[j].Line
	})

	// Prefer function/method over class for the closest match
	best := candidates[0]
	if isClassKind(best.Kind) {
		// Prefer the closest function/method if available
		for _, c := range candidates {
			if isFunctionKind(c.Kind) {
				best = c
				break
			}
		}
	}

	return best.Name, best.Kind
}

func isFunctionKind(kind string) bool {
	switch kind {
	case "function", "method", "func", "def", "fn":
		return true
	}
	return false
}

func isClassKind(kind string) bool {
	switch kind {
	case "class", "struct", "type", "interface", "trait", "enum":
		return true
	}
	return false
}
