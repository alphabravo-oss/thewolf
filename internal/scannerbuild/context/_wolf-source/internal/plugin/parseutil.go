package plugin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Skipf emits a standardized skip message through an OnOutput callback.
// Format: [SKIP] toolName: message
func Skipf(onOutput func(string), toolName, format string, args ...any) {
	if onOutput != nil {
		msg := fmt.Sprintf(format, args...)
		onOutput(fmt.Sprintf("[SKIP] %s: %s", toolName, msg))
	}
}

// Infof emits a standardized info message through an OnOutput callback.
// Format: [INFO] toolName: message
func Infof(onOutput func(string), toolName, format string, args ...any) {
	if onOutput != nil {
		msg := fmt.Sprintf(format, args...)
		onOutput(fmt.Sprintf("[INFO] %s: %s", toolName, msg))
	}
}

// EmitDiagnostic sends structured [FIX] messages through an OnOutput callback
// so they appear in tool logs and SSE streams with actionable guidance.
func EmitDiagnostic(onOutput func(string), diag *ToolDiagnostic) {
	if onOutput == nil || diag == nil {
		return
	}
	onOutput(fmt.Sprintf("[FIX] %s: %s", diag.Tool, diag.Problem))
	for _, cmd := range diag.FixCommands {
		onOutput(fmt.Sprintf("[FIX] %s: → Run: %s", diag.Tool, cmd))
	}
	if diag.DocURL != "" {
		onOutput(fmt.Sprintf("[FIX] %s: → Docs: %s", diag.Tool, diag.DocURL))
	}
}

// HasFilesWithExtension walks the directory tree looking for any file with
// one of the given extensions. It returns as soon as the first match is found,
// so it's fast even on large repos. Skips common vendored/generated dirs.
func HasFilesWithExtension(dir string, exts ...string) bool {
	// Build a set for O(1) lookup
	extSet := make(map[string]struct{}, len(exts))
	for _, ext := range exts {
		extSet["."+ext] = struct{}{}
	}

	skipDirs := map[string]struct{}{
		"node_modules": {}, "vendor": {}, ".git": {},
		"__pycache__": {}, ".tox": {}, ".venv": {},
		"dist": {}, "build": {}, ".next": {},
	}

	found := false
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if d.IsDir() {
			if _, skip := skipDirs[d.Name()]; skip {
				return filepath.SkipDir
			}
			return nil
		}
		if _, ok := extSet[filepath.Ext(d.Name())]; ok {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// HasFile checks if a specific file exists in the directory.
func HasFile(dir string, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}

// HasDir reports whether name is a directory under dir.
func HasDir(dir, name string) bool {
	info, err := os.Stat(filepath.Join(dir, name))
	return err == nil && info.IsDir()
}

// HasZizmorInputs is true when the tree has GitHub Actions, Dependabot,
// composite actions, or pre-commit config that zizmor knows how to audit.
func HasZizmorInputs(dir string) bool {
	return HasDir(dir, filepath.Join(".github", "workflows")) ||
		HasFile(dir, filepath.Join(".github", "dependabot.yml")) ||
		HasFile(dir, filepath.Join(".github", "dependabot.yaml")) ||
		HasFile(dir, "action.yml") ||
		HasFile(dir, "action.yaml") ||
		HasFile(dir, ".pre-commit-config.yaml")
}

// HasPipelineConfig is true when the tree has CI workflow files that
// poutine can analyze (GitHub Actions, GitLab CI, Azure DevOps, Tekton).
func HasPipelineConfig(dir string) bool {
	return HasDir(dir, filepath.Join(".github", "workflows")) ||
		HasFile(dir, ".gitlab-ci.yml") ||
		HasFile(dir, "azure-pipelines.yml") ||
		HasDir(dir, ".azure-pipelines") ||
		HasDir(dir, ".tekton")
}

// FindFile searches for a file by name in dir and its immediate subdirectories
// (one level deep). Returns the directory containing the file, or "" if not found.
// This handles monorepos where e.g. go.mod is in a subdirectory.
func FindFile(dir string, name string) string {
	// Check root first.
	if HasFile(dir, name) {
		return dir
	}

	// Check one level of subdirectories.
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}

	skipDirs := map[string]struct{}{
		"node_modules": {}, "vendor": {}, ".git": {},
		"__pycache__": {}, ".tox": {}, ".venv": {},
		"dist": {}, ".next": {}, ".terraform": {},
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, skip := skipDirs[entry.Name()]; skip {
			continue
		}
		subdir := filepath.Join(dir, entry.Name())
		if HasFile(subdir, name) {
			return subdir
		}
	}

	return ""
}

// WrapExecError creates a detailed error message from an exec failure,
// including truncated stderr if available.
func WrapExecError(toolName string, err error) error {
	if exitErr, ok := err.(*exec.ExitError); ok && len(exitErr.Stderr) > 0 {
		stderr := strings.TrimSpace(string(exitErr.Stderr))
		if len(stderr) > 500 {
			stderr = "..." + stderr[len(stderr)-500:]
		}
		return fmt.Errorf("%s execution failed: %w\n%s", toolName, err, stderr)
	}
	return fmt.Errorf("%s execution failed: %w", toolName, err)
}

// ExtractJSON finds the first JSON object or array in data, stripping any
// non-JSON prefix (warnings, version banners, debug output) that tools may
// write to stdout before their structured output. Returns the original data
// unchanged if it already starts with valid JSON.
func ExtractJSON(data []byte) []byte {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return data
	}

	if json.Valid(trimmed) {
		return trimmed
	}

	// Validate candidate starts instead of assuming the first bracket begins
	// JSON. Several scanners emit prefixes such as "[main] INFO" and
	// Kubernetes combines stdout/stderr into one log stream. Bound the number
	// of full-suffix validations so hostile output cannot force unbounded
	// quadratic work.
	const maxCandidates = 64
	candidates := 0
	for index, character := range trimmed {
		if character != '{' && character != '[' {
			continue
		}
		candidate := bytes.TrimSpace(trimmed[index:])
		candidates++
		if json.Valid(candidate) {
			return candidate
		}
		if candidates == maxCandidates {
			break
		}
	}

	// No JSON found — return original so callers get a clear parse error
	return data
}

// ExtractXML finds the first XML declaration (<?xml) or root element (<) in
// data, stripping any non-XML prefix that tools may write to stdout.
func ExtractXML(data []byte) []byte {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return data
	}

	if trimmed[0] == '<' {
		return trimmed
	}

	idx := bytes.Index(trimmed, []byte("<?xml"))
	if idx < 0 {
		idx = bytes.IndexByte(trimmed, '<')
	}
	if idx >= 0 {
		return trimmed[idx:]
	}

	return data
}
