package hygiene

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/models"
)

const yamllintConfig = `extends: default

ignore: |
  **/vendor/**
  **/node_modules/**
  **/.git/**
  **/.wolf/**
  **/dist/**
  **/coverage/**

rules:
  line-length: disable
  document-start: disable
  truthy: disable
  comments: disable
  comments-indentation: disable
  braces:
    max-spaces-inside: 1
  brackets:
    max-spaces-inside: 1
  indentation:
    spaces: consistent
    indent-sequences: consistent
`

const markdownlintConfig = `{
  "MD013": false,
  "MD033": false,
  "MD041": false,
  "default": true
}
`

const hadolintConfig = `ignored:
  - DL3042
  - DL3059
`

// LintPass writes missing project lint config and runs mechanical
// formatters. Findings whose files actually changed are kept (fixed).
// Untouched leftovers stay open for the agent — they are not muted.
func LintPass(ctx context.Context, repoPath, tool string, findings []models.Finding) (Result, error) {
	res := emptyResult()
	if repoPath == "" {
		return res, nil
	}
	before := snapshotFindingFiles(repoPath, findings)
	switch strings.ToLower(strings.TrimSpace(tool)) {
	case "yamllint":
		if wrote, err := writeIfMissing(filepath.Join(repoPath, ".yamllint.yaml"), yamllintConfig); err != nil {
			return res, err
		} else if wrote {
			res.Files = append(res.Files, ".yamllint.yaml")
		}
		trimTrailingIn(repoPath, []string{".yml", ".yaml"})
		formatted := runOptional(ctx, repoPath, "yamlfmt", "-quiet", ".") ||
			runOptional(ctx, repoPath, "prettier", "--write", "**/*.{yml,yaml}")
		n := keepTouchedFindings(&res, findings, before, repoPath, "formatted YAML")
		if formatted && n > 0 {
			res.Message = "formatted YAML"
		} else if n > 0 {
			res.Message = "trimmed YAML whitespace"
		} else if len(res.Files) > 0 {
			res.Message = "wrote .yamllint.yaml; no finding files changed"
		} else {
			res.Message = "no YAML files changed"
		}
	case "markdownlint":
		if wrote, err := writeIfMissing(filepath.Join(repoPath, ".markdownlint.json"), markdownlintConfig); err != nil {
			return res, err
		} else if wrote {
			res.Files = append(res.Files, ".markdownlint.json")
		}
		_ = runOptional(ctx, repoPath, "markdownlint", "--fix", ".")
		n := keepTouchedFindings(&res, findings, before, repoPath, "markdownlint --fix")
		if n > 0 {
			res.Message = "fixed markdown"
		} else if len(res.Files) > 0 {
			res.Message = "wrote .markdownlint.json; no finding files changed"
		} else {
			res.Message = "no markdown files changed"
		}
	case "hadolint":
		if wrote, err := writeIfMissing(filepath.Join(repoPath, ".hadolint.yaml"), hadolintConfig); err != nil {
			return res, err
		} else if wrote {
			res.Files = append(res.Files, ".hadolint.yaml")
		}
		res.Message = "wrote .hadolint.yaml"
	case "eslint":
		_ = runOptional(ctx, repoPath, "eslint", ".", "--fix")
		n := keepTouchedFindings(&res, findings, before, repoPath, "eslint --fix")
		if n > 0 {
			res.Message = "eslint --fix changed finding files"
		} else {
			res.Message = "eslint --fix did not change finding files"
		}
	case "prettier":
		ok := runOptional(ctx, repoPath, "prettier", "--write", ".")
		n := keepTouchedFindings(&res, findings, before, repoPath, "prettier --write")
		switch {
		case !ok && n == 0:
			res.Message = "prettier not available on the fixer"
		case n > 0:
			res.Message = "prettier --write changed finding files"
		default:
			res.Message = "prettier did not change finding files"
		}
	case "ruff":
		_ = runOptional(ctx, repoPath, "ruff", "check", ".", "--fix")
		_ = runOptional(ctx, repoPath, "ruff", "format", ".")
		n := keepTouchedFindings(&res, findings, before, repoPath, "ruff --fix / format")
		if n > 0 {
			res.Message = "ruff changed finding files"
		} else {
			res.Message = "ruff did not change finding files"
		}
	case "rubocop":
		_ = runOptional(ctx, repoPath, "rubocop", "-A")
		n := keepTouchedFindings(&res, findings, before, repoPath, "rubocop -A")
		if n > 0 {
			res.Message = "rubocop changed finding files"
		} else {
			res.Message = "rubocop did not change finding files"
		}
	case "shellcheck":
		ok := runOptional(ctx, repoPath, "shfmt", "-w", ".")
		n := keepTouchedFindings(&res, findings, before, repoPath, "shfmt -w")
		switch {
		case !ok && n == 0:
			res.Message = "shfmt not available on the fixer"
		case n > 0:
			res.Message = "shfmt changed finding files"
		default:
			res.Message = "shfmt did not change finding files"
		}
	default:
		res.Message = "no mechanical lint pass for " + tool
	}
	if restored := RestoreProtected(ctx, repoPath); len(restored) > 0 {
		// Formatters walk the whole tree; drop contract/Helm rewrites
		// so they are not counted as fixes or committed.
		kept := res.Kept
		res.Kept = map[string]string{}
		for _, f := range findings {
			note, ok := kept[f.ID]
			if !ok || ProtectedRel(findingRelPath(f)) {
				continue
			}
			res.Kept[f.ID] = note
		}
	}
	return res, nil
}

func writeIfMissing(path, body string) (bool, error) {
	if _, err := os.Stat(path); err == nil {
		// Merge-not-overwrite: leave an existing project config alone.
		return false, nil
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

func findingRelPath(f models.Finding) string {
	p := strings.TrimSpace(f.FilePath)
	p = strings.TrimPrefix(p, "./")
	return filepath.ToSlash(p)
}

func isHygieneConfig(rel string) bool {
	base := strings.ToLower(filepath.Base(rel))
	switch base {
	case ".yamllint.yaml", ".yamllint.yml", ".yamllint",
		".markdownlint.json", ".markdownlint.yaml",
		".hadolint.yaml", ".hadolint.yml":
		return true
	default:
		return false
	}
}

func fileHash(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), true
}

func snapshotFindingFiles(root string, findings []models.Finding) map[string]string {
	out := make(map[string]string, len(findings))
	for _, f := range findings {
		rel := findingRelPath(f)
		if rel == "" || isHygieneConfig(rel) || ProtectedRel(rel) {
			continue
		}
		if sum, ok := fileHash(filepath.Join(root, filepath.FromSlash(rel))); ok {
			out[rel] = sum
		}
	}
	return out
}

func keepTouchedFindings(res *Result, findings []models.Finding, before map[string]string, root, note string) int {
	n := 0
	seenFile := map[string]bool{}
	for _, f := range findings {
		rel := findingRelPath(f)
		if rel == "" || isHygieneConfig(rel) || ProtectedRel(rel) {
			continue
		}
		sum, ok := fileHash(filepath.Join(root, filepath.FromSlash(rel)))
		if !ok || before[rel] == sum {
			continue
		}
		res.Kept[f.ID] = note
		n++
		if !seenFile[rel] {
			res.Files = append(res.Files, rel)
			seenFile[rel] = true
		}
	}
	return n
}

func trimTrailingIn(root string, exts []string) {
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			if info != nil && (info.Name() == ".git" || info.Name() == "node_modules" || info.Name() == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		ok := false
		for _, ext := range exts {
			if strings.HasSuffix(path, ext) {
				ok = true
				break
			}
		}
		if !ok {
			return nil
		}
		if rel, err := filepath.Rel(root, path); err == nil && ProtectedRel(rel) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		lines := strings.Split(string(data), "\n")
		changed := false
		for i, ln := range lines {
			trim := strings.TrimRight(ln, " \t")
			if trim != ln {
				lines[i] = trim
				changed = true
			}
		}
		if changed {
			_ = os.WriteFile(path, []byte(strings.Join(lines, "\n")), info.Mode())
		}
		return nil
	})
}

func runOptional(ctx context.Context, dir, name string, args ...string) bool {
	if _, err := exec.LookPath(name); err != nil {
		return false
	}
	cmd := exec.CommandContext(ctx, name, args...) // #nosec G204
	cmd.Dir = dir
	return cmd.Run() == nil
}
