// Package identity computes versioned finding fingerprints used by
// deduplication, baselines, suppressions, and future quality gates.
package identity

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/models"
)

const Version = 1

type Fingerprints struct {
	Stable   string
	Location string
	Semantic string
	Evidence string
	Version  int
}

var whitespaceRE = regexp.MustCompile(`\s+`)

// NormalizePath converts scanner/container-specific paths into a stable,
// repo-relative representation where possible.
func NormalizePath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.TrimPrefix(path, "file://")
	path = strings.ReplaceAll(path, `\`, "/")
	path = filepath.ToSlash(path)
	path = strings.TrimPrefix(path, "./")

	for _, prefix := range []string{"/scan/", "/workspace/", "/repo/", "/src/"} {
		if strings.HasPrefix(path, prefix) {
			path = strings.TrimPrefix(path, prefix)
			break
		}
	}

	path = strings.TrimPrefix(path, "/scan")
	path = strings.TrimPrefix(path, "/workspace")
	path = strings.TrimPrefix(path, "/repo")
	path = strings.TrimPrefix(path, "/src")
	path = strings.TrimPrefix(path, "/")
	path = filepath.ToSlash(filepath.Clean(path))
	if path == "." {
		return ""
	}
	return path
}

func Digest(parts ...string) string {
	h := sha256.New()
	for i, part := range parts {
		if i > 0 {
			h.Write([]byte{0})
		}
		h.Write([]byte(strings.TrimSpace(strings.ToLower(part))))
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func Build(f models.Finding) Fingerprints {
	path := NormalizePath(f.FilePath)
	ruleKey := firstNonEmpty(f.FineCategory, f.RuleID, f.Title, string(f.Category))
	if f.Category == models.CategorySCA {
		ruleKey = firstNonEmpty(f.RuleID, f.CWEID, f.Title, f.FineCategory)
	}
	symbolKey := firstNonEmpty(f.FunctionName, f.ModuleName, f.SymbolKind)
	snippetHash := normalizedSnippetHash(f.CodeSnippet)
	line := fmt.Sprintf("%d:%d", f.LineStart, f.LineEnd)

	location := Digest("location", path, line, ruleKey, f.ToolName)
	semantic := Digest("semantic", path, ruleKey, symbolKey, snippetHash)
	evidence := Digest("evidence", f.ToolName, f.RuleID, f.Title, path, line, snippetHash)

	stable := semantic
	if path == "" || ruleKey == "" {
		stable = location
	}

	return Fingerprints{
		Stable:   stable,
		Location: location,
		Semantic: semantic,
		Evidence: evidence,
		Version:  Version,
	}
}

func Apply(f *models.Finding) {
	fps := Build(*f)
	f.FilePath = NormalizePath(f.FilePath)
	if f.StableFingerprint == "" {
		f.StableFingerprint = fps.Stable
	}
	if f.LocationFingerprint == "" {
		f.LocationFingerprint = fps.Location
	}
	if f.SemanticFingerprint == "" {
		f.SemanticFingerprint = fps.Semantic
	}
	if f.EvidenceFingerprint == "" {
		f.EvidenceFingerprint = fps.Evidence
	}
	if f.IdentityVersion == 0 {
		f.IdentityVersion = fps.Version
	}
	if f.Fingerprint == "" {
		f.Fingerprint = fps.Stable
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func normalizedSnippetHash(snippet string) string {
	snippet = strings.TrimSpace(snippet)
	if snippet == "" {
		return ""
	}
	snippet = whitespaceRE.ReplaceAllString(snippet, " ")
	return Digest("snippet", snippet)
}
