package hygiene

import (
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/models"
)

// SuppressionWriter persists a Wolf suppression so the next scan UI matches
// the in-repo ignore.
type SuppressionWriter interface {
	CreateFindingSuppression(s *models.FindingSuppression) error
}

// Mute applies an in-repo scanner ignore and a durable Wolf suppression so
// accepted noise does not come back on the next scan.
func Mute(repoPath string, job *models.FixJob, f models.Finding, reason string, write SuppressionWriter) (files []string, err error) {
	if repoPath != "" && strings.TrimSpace(f.FilePath) != "" && f.LineStart > 0 {
		if _, werr := addIgnoreComment(join(repoPath, f.FilePath), f); werr == nil {
			files = append(files, f.FilePath)
		}
	}
	if write == nil || job == nil {
		return files, nil
	}
	scopeType := models.SuppressionScopeFingerprint
	scopeVal := f.Fingerprint
	if scopeVal == "" {
		scopeVal = f.StableFingerprint
		scopeType = models.SuppressionScopeStableFingerprint
	}
	if scopeVal == "" && f.RuleID != "" {
		scopeType = models.SuppressionScopeRule
		scopeVal = f.RuleID
	}
	if scopeVal == "" {
		return files, nil
	}
	s := &models.FindingSuppression{
		ID:         uuid.NewString(),
		RepoID:     job.RepoID,
		CreatedBy:  job.UserID,
		ScopeType:  scopeType,
		ScopeValue: scopeVal,
		Reason:     "wolf fixer: " + reason,
		Status:     models.SuppressionStatusActive,
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	return files, write.CreateFindingSuppression(s)
}

// MuteRule writes one Wolf rule-level suppression so a noisy scanner rule
// does not come back as thousands of rows.
func MuteRule(job *models.FixJob, tool, rule, reason string, write SuppressionWriter) error {
	if write == nil || job == nil || strings.TrimSpace(rule) == "" {
		return nil
	}
	_ = tool
	s := &models.FindingSuppression{
		ID:         uuid.NewString(),
		RepoID:     job.RepoID,
		CreatedBy:  job.UserID,
		ScopeType:  models.SuppressionScopeRule,
		ScopeValue: rule,
		Reason:     "wolf fixer: " + reason,
		Status:     models.SuppressionStatusActive,
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	return write.CreateFindingSuppression(s)
}

func join(root, rel string) string {
	if root == "" {
		return rel
	}
	if strings.HasPrefix(rel, "/") {
		return rel
	}
	return strings.TrimRight(root, "/") + "/" + rel
}

func addIgnoreComment(abs string, f models.Finding) (string, error) {
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	comment := ignoreComment(f)
	if comment == "" {
		return "", nil
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	idx := f.LineStart - 1
	if idx < 0 || idx >= len(lines) {
		return "", nil
	}
	if strings.Contains(lines[idx], strings.TrimSpace(comment)) {
		return "", nil
	}
	ext := strings.ToLower(abs)
	if strings.HasSuffix(ext, ".go") || strings.HasSuffix(ext, ".js") ||
		strings.HasSuffix(ext, ".ts") || strings.HasSuffix(ext, ".tsx") {
		lines[idx] = strings.TrimRight(lines[idx], " \t") + " " + comment
	} else {
		indent := leadingWS(lines[idx])
		out := make([]string, 0, len(lines)+1)
		out = append(out, lines[:idx]...)
		out = append(out, indent+comment)
		out = append(out, lines[idx:]...)
		lines = out
	}
	mode := os.FileMode(0o644)
	if info, err := os.Stat(abs); err == nil {
		mode = info.Mode()
	}
	return abs, os.WriteFile(abs, []byte(strings.Join(lines, "\n")), mode)
}

func ignoreComment(f models.Finding) string {
	tool := strings.ToLower(strings.TrimSpace(f.ToolName))
	rule := strings.TrimSpace(f.RuleID)
	switch tool {
	case "gosec", "staticcheck", "gokart":
		if rule == "" {
			return "//nosec"
		}
		return "//nosec " + rule
	case "semgrep":
		if rule == "" {
			return "// nosemgrep"
		}
		return "// nosemgrep: " + rule
	case "eslint":
		return "// eslint-disable-next-line"
	case "yamllint":
		return "# yamllint disable-line"
	case "shellcheck":
		if rule == "" {
			return "# shellcheck disable"
		}
		code := rule
		if !strings.HasPrefix(strings.ToUpper(code), "SC") {
			code = "SC" + strings.TrimPrefix(code, "SC")
		}
		return "# shellcheck disable=" + code
	case "checkov":
		if rule == "" {
			return "# checkov:skip=CKV"
		}
		return "# checkov:skip=" + rule
	default:
		if strings.HasSuffix(strings.ToLower(f.FilePath), ".go") {
			return "//nolint"
		}
		return ""
	}
}

func leadingWS(s string) string {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return s[:i]
}

// NoiseReason reports whether a SKIP note is scanner noise (mute it) versus
// "too hard / later".
func NoiseReason(note string) bool {
	s := strings.ToLower(note)
	for _, k := range []string{
		"false positive", "scanner noise", "does not match", "generated",
		"vendor", "lockfile", "not a local", "empty path", "test fixture",
		"noise", "docs/", ".md", "example", "not a line-of-code",
		"style only", "best practice noise",
	} {
		if strings.Contains(s, k) {
			return true
		}
	}
	return false
}
