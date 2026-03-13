package plugin

import (
	"fmt"
	"regexp"
	"strings"
)

// ToolDiagnostic provides actionable guidance when a tool fails.
type ToolDiagnostic struct {
	Tool        string   // tool name
	Problem     string   // human-readable description of the issue
	FixCommands []string // commands the user can run to fix it
	DocURL      string   // optional link to documentation
}

// errorPattern maps a compiled regex to a diagnostic generator.
type errorPattern struct {
	tool    string // empty = matches any tool
	re      *regexp.Regexp
	diagnose func(toolName string, matches []string) *ToolDiagnostic
}

var errorPatterns = []errorPattern{
	// CodeQL: missing query packs
	{
		tool: "codeql",
		re:   regexp.MustCompile(`(?i)query pack (\S+) cannot be found`),
		diagnose: func(toolName string, matches []string) *ToolDiagnostic {
			pack := matches[1]
			return &ToolDiagnostic{
				Tool:        toolName,
				Problem:     fmt.Sprintf("Query pack %s not installed", pack),
				FixCommands: []string{fmt.Sprintf("codeql pack download %s", pack)},
				DocURL:      "https://docs.github.com/en/code-security/codeql-cli/using-the-advanced-functionality-of-the-codeql-cli/publishing-and-using-codeql-packs",
			}
		},
	},
	// CodeQL: database create failed (build errors)
	{
		tool: "codeql",
		re:   regexp.MustCompile(`(?i)database create failed`),
		diagnose: func(toolName string, _ []string) *ToolDiagnostic {
			return &ToolDiagnostic{
				Tool:    toolName,
				Problem: "CodeQL database creation failed — the project may need build tools or a build command",
				FixCommands: []string{
					"Ensure build tools for your project are installed (e.g. npm, mvn, dotnet)",
					"For compiled languages, try: codeql database create --command='<your-build-command>'",
				},
			}
		},
	},
	// ESLint: missing config
	{
		tool: "eslint",
		re:   regexp.MustCompile(`(?i)ESLint couldn't find.*config`),
		diagnose: func(toolName string, _ []string) *ToolDiagnostic {
			return &ToolDiagnostic{
				Tool:        toolName,
				Problem:     "No ESLint configuration found in the project",
				FixCommands: []string{"npm init @eslint/config"},
			}
		},
	},
	// pip-audit: not installed properly
	{
		tool: "pip-audit",
		re:   regexp.MustCompile(`(?i)No module named pip_audit`),
		diagnose: func(toolName string, _ []string) *ToolDiagnostic {
			return &ToolDiagnostic{
				Tool:        toolName,
				Problem:     "pip-audit module not found — broken or missing installation",
				FixCommands: []string{"pip install pip-audit"},
			}
		},
	},
	// npm-audit: no lockfile
	{
		tool: "npm-audit",
		re:   regexp.MustCompile(`(?i)ELOCKFILENOTFOUND`),
		diagnose: func(toolName string, _ []string) *ToolDiagnostic {
			return &ToolDiagnostic{
				Tool:        toolName,
				Problem:     "No package-lock.json found — npm audit requires a lockfile",
				FixCommands: []string{"npm install"},
			}
		},
	},
	// mypy: missing type stubs
	{
		tool: "mypy",
		re:   regexp.MustCompile(`(?i)Cannot find implementation or library stub`),
		diagnose: func(toolName string, _ []string) *ToolDiagnostic {
			return &ToolDiagnostic{
				Tool:        toolName,
				Problem:     "Missing type stubs for one or more dependencies",
				FixCommands: []string{"mypy --install-types"},
			}
		},
	},
	// semgrep: invalid config
	{
		tool: "semgrep",
		re:   regexp.MustCompile(`(?i)invalid configuration file`),
		diagnose: func(toolName string, _ []string) *ToolDiagnostic {
			return &ToolDiagnostic{
				Tool:    toolName,
				Problem: "Invalid Semgrep configuration file",
				FixCommands: []string{
					"Check .semgrep.yml or .semgrep/ for syntax errors",
					"semgrep --validate --config .semgrep.yml",
				},
			}
		},
	},
	// Generic: command not found / not in PATH
	{
		re: regexp.MustCompile(`(?i)(command not found|not found in PATH|executable file not found)`),
		diagnose: func(toolName string, _ []string) *ToolDiagnostic {
			return &ToolDiagnostic{
				Tool:        toolName,
				Problem:     fmt.Sprintf("%s is not installed or not in PATH", toolName),
				FixCommands: []string{fmt.Sprintf("Install %s — run: wolf setup", toolName)},
			}
		},
	},
}

// DiagnoseExecError examines a tool execution error and returns
// an actionable diagnostic if the error matches a known pattern.
// Returns nil if the error isn't recognizable.
func DiagnoseExecError(toolName string, err error, stderr string) *ToolDiagnostic {
	if err == nil {
		return nil
	}

	// Combine error message and stderr for pattern matching.
	combined := err.Error() + "\n" + stderr

	for _, p := range errorPatterns {
		// Skip patterns that are tool-specific and don't match.
		if p.tool != "" && !strings.EqualFold(p.tool, toolName) {
			continue
		}
		if matches := p.re.FindStringSubmatch(combined); matches != nil {
			return p.diagnose(toolName, matches)
		}
	}

	return nil
}
