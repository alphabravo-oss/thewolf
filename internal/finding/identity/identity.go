// Package identity computes versioned finding fingerprints used by
// deduplication, baselines, suppressions, and future quality gates.
package identity

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
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
var cweRE = regexp.MustCompile(`(?i)^(?:CWE-)?([0-9]+)(?:$|\b)`)

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

	if path == "/scan" || path == "/workspace" || path == "/repo" || path == "/src" {
		return ""
	}
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
	if f.Category == models.CategorySecrets && f.ToolName == "detect-secrets" {
		return buildDetectSecret(f)
	}
	if f.Category == models.CategorySCA {
		return buildSCA(f)
	}
	return buildV1(f)
}

// detect-secrets supplies a SHA-1 fingerprint, not the secret itself. Keep
// that opaque upstream identity independent of category, array order and line.
// Unknown/legacy detector output falls back to its precise source location.
func buildDetectSecret(f models.Finding) Fingerprints {
	path := NormalizePath(f.FilePath)
	line := fmt.Sprintf("%d:%d", f.LineStart, f.LineEnd)
	parts := []string{"detect-secret-v3", f.RuleID}
	_, hash, found := strings.Cut(f.RuleID, ":")
	decoded, err := hex.DecodeString(hash)
	if !found || err != nil || len(decoded) != 20 {
		parts = append(parts, path, line, f.Title)
	}
	stable := Digest(parts...)
	return Fingerprints{Stable: stable, Semantic: stable,
		Location: Digest("detect-secret-location-v3", stable, path, line),
		Evidence: Digest("detect-secret-evidence-v3", stable, path, line, f.Title), Version: 3}
}

// Keep v1 available to fill missing legacy evidence without silently migrating
// its stable identity. Historical SCA backfill is a separate, explicit operation.
func buildV1(f models.Finding) Fingerprints {
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
	if f.IdentityVersion == 1 {
		fps = buildV1(*f)
	}
	f.FilePath = NormalizePath(f.FilePath)
	if match := cweRE.FindStringSubmatch(strings.TrimSpace(f.CWEID)); len(match) > 1 {
		if id, err := strconv.Atoi(match[1]); err == nil && id > 0 {
			f.CWEID = "CWE-" + strconv.Itoa(id)
		}
	}
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

// CanonicalAdvisory uses only explicit scanner aliases; it never guesses a CVE
// from descriptive prose. Keep the original RuleID for tool-specific evidence.
func CanonicalAdvisory(id string, aliases ...string) string {
	candidates := append([]string{strings.TrimSpace(id)}, aliases...)
	for _, prefix := range []string{"CVE-", "GHSA-"} {
		var matches []string
		for _, candidate := range candidates {
			candidate = strings.ToUpper(strings.TrimSpace(candidate))
			if strings.HasPrefix(candidate, prefix) {
				matches = append(matches, candidate)
			}
		}
		if len(matches) > 0 {
			sort.Strings(matches)
			return matches[0]
		}
	}
	return strings.TrimSpace(id)
}

func packageEcosystem(value string) string {
	switch value = strings.ToLower(strings.TrimSpace(value)); value {
	case "go", "gomod", "go-module":
		return "golang"
	case "python", "python-pkg", "pip":
		return "pypi"
	case "rust", "rust-crate":
		return "cargo"
	case "node-pkg", "nodejs":
		return "npm"
	case "java", "java-archive", "jar", "pom":
		return "maven"
	case "dotnet", "dotnet-deps":
		return "nuget"
	default:
		return value
	}
}

func buildSCA(f models.Finding) Fingerprints {
	path := NormalizePath(f.FilePath)
	advisory := CanonicalAdvisory(firstNonEmpty(f.AdvisoryID, f.RuleID))
	parts := []string{"sca-v2", path, packageEcosystem(f.PackageEcosystem), strings.TrimSpace(f.PackageName), strings.TrimSpace(f.PackageVersion), strings.TrimSpace(f.PackageURL), advisory}
	// Missing package/advisory data cannot substantiate cross-tool agreement.
	// Preserve conservative tool/display evidence rather than guessing identity.
	if (strings.TrimSpace(f.PackageName) == "" && strings.TrimSpace(f.PackageURL) == "") || advisory == "" {
		parts = append(parts, f.ToolName, f.Title, f.Description)
	}
	digest := func(parts []string) string {
		h := sha256.New()
		for _, part := range parts {
			_, _ = fmt.Fprintf(h, "%d:", len(part))
			_, _ = h.Write([]byte(part))
		}
		return fmt.Sprintf("%x", h.Sum(nil))
	}
	stable := digest(parts)
	location := digest(append(append([]string(nil), parts...), fmt.Sprintf("%d:%d", f.LineStart, f.LineEnd), f.ToolName))
	evidence := digest(append(append([]string(nil), parts...), f.ToolName, f.RuleID, f.Title, f.Description))
	return Fingerprints{Stable: stable, Semantic: stable, Location: location, Evidence: evidence, Version: 2}
}

// ClusterKey is shared by the runner and durable cluster reconciliation.
// Legacy non-SCA grouping remains file/line/category; typed semantic upgrades
// require the separate versioned identity migration.
func ClusterKey(f models.Finding) string {
	if f.Category == models.CategorySecrets && f.ToolName == "detect-secrets" {
		return "detect-secret:" + Build(f).Stable
	}
	if f.Category == models.CategorySCA {
		return "sca:" + Build(f).Stable
	}
	if f.FineCategory != "" {
		return fmt.Sprintf("%q:%q:%d:%q", f.Category, f.FilePath, f.LineStart, f.FineCategory)
	}
	identifier := f.RuleID
	if identifier == "" {
		identifier = f.Title
	}
	return fmt.Sprintf("%q:%q:%d:%q", f.Category, f.FilePath, f.LineStart, identifier)
}

// PreferPrimary keeps open, unsuppressed evidence visible, then chooses the
// higher severity. Equal candidates retain the caller's deterministic order.
func PreferPrimary(candidate, current models.Finding) bool {
	visible := !candidate.Suppressed && (candidate.Status == models.StatusOpen || candidate.Status == "")
	currentVisible := !current.Suppressed && (current.Status == models.StatusOpen || current.Status == "")
	return (visible && !currentVisible) || (visible == currentVisible && severityRank(candidate.Severity) > severityRank(current.Severity))
}

// severityRank returns a numeric rank for severity comparison.
// Higher rank means more severe.
func severityRank(s models.Severity) int {
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
