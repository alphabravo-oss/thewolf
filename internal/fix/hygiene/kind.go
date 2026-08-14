// Package hygiene classifies fixer work into lint, dependency bumps,
// org-policy, and line-of-code agents — and applies the mechanical
// passes that should not go through OpenCode.
package hygiene

import (
	"os"
	"path/filepath"
	"strings"
)

// Kind is how Wolf should handle a scanner's findings.
type Kind string

const (
	KindCode   Kind = "code"
	KindLint   Kind = "lint"
	KindBump   Kind = "bump"
	KindPolicy Kind = "policy"
)

// Classify returns the handling kind for a scanner name.
func Classify(tool string) Kind {
	switch strings.ToLower(strings.TrimSpace(tool)) {
	case "yamllint", "markdownlint", "hadolint", "eslint", "prettier", "ruff", "rubocop", "shellcheck":
		return KindLint
	case "trivy", "grype", "osv", "osv-scanner", "govulncheck", "npm-audit", "renovate":
		return KindBump
	case "scorecard":
		return KindPolicy
	default:
		return KindCode
	}
}

// KindRank orders a loop: lint/format first, then bumps, then policy,
// then the code agent. Lower runs earlier.
func KindRank(tool string) int {
	switch Classify(tool) {
	case KindLint:
		return 0
	case KindBump:
		return 1
	case KindPolicy:
		return 2
	default:
		return 3
	}
}

// Result is the outcome of a mechanical hygiene pass.
type Result struct {
	Kept    map[string]string // finding id → what we did
	Muted   map[string]string // finding id → why we muted it
	Files   []string
	Message string
}

func emptyResult() Result {
	return Result{Kept: map[string]string{}, Muted: map[string]string{}}
}

func looksLikeRepo(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	for _, n := range []string{".git", "go.mod", "package.json", ".github"} {
		if _, err := os.Stat(filepath.Join(path, n)); err == nil {
			return true
		}
	}
	return false
}
