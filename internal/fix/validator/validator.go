package validator

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Result holds the outcome of a validation run.
type Result struct {
	Pass   bool
	Output string
}

// ToolCommand maps tool names to their validation commands.
var ToolCommand = map[string][]string{
	"bandit":      {"bandit", "-r"},
	"ruff":        {"ruff", "check"},
	"mypy":        {"mypy"},
	"eslint":      {"eslint"},
	"gosec":       {"gosec"},
	"staticcheck": {"staticcheck"},
	"clippy":      {"cargo", "clippy", "--"},
	"semgrep":     {"semgrep", "scan", "--quiet"},
	"trivy":       {"trivy", "fs", "--severity", "CRITICAL,HIGH"},
}

// Validator re-runs the relevant tool on changed files to verify a fix.
type Validator struct {
	Timeout time.Duration
}

// NewValidator creates a validator with default timeout.
func NewValidator() *Validator {
	return &Validator{Timeout: 2 * time.Minute}
}

// Validate re-runs the tool that found the original issue on the changed files.
func (v *Validator) Validate(ctx context.Context, toolName string, repoPath string, files []string) (*Result, error) {
	if len(files) == 0 {
		return &Result{Pass: true, Output: "no files changed"}, nil
	}

	cmdParts, ok := ToolCommand[toolName]
	if !ok {
		// Unknown tool — skip validation, assume pass
		return &Result{
			Pass:   true,
			Output: fmt.Sprintf("no validation command for tool %q, skipping", toolName),
		}, nil
	}

	timeout := v.Timeout
	if timeout == 0 {
		timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Build command with files appended
	args := make([]string, len(cmdParts)-1, len(cmdParts)-1+len(files))
	copy(args, cmdParts[1:])
	args = append(args, files...)

	// #nosec G204 -- command is a configured tool name (docker / claude / codex / scanner binary); args sourced from internal config, not user input
	// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
	cmd := exec.CommandContext(ctx, cmdParts[0], args...)
	cmd.Dir = repoPath
	output, err := cmd.CombinedOutput()

	if err != nil {
		// Non-zero exit means the tool found issues
		return &Result{
			Pass:   false,
			Output: string(output),
		}, nil
	}

	return &Result{
		Pass:   true,
		Output: string(output),
	}, nil
}

// ValidateWithCommand runs a custom validation command.
func (v *Validator) ValidateWithCommand(ctx context.Context, repoPath string, command string) (*Result, error) {
	timeout := v.Timeout
	if timeout == 0 {
		timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	parts := strings.Fields(command)
	if len(parts) == 0 {
		return &Result{Pass: true, Output: "empty command"}, nil
	}

	// #nosec G204 -- command is a configured tool name (docker / claude / codex / scanner binary); args sourced from internal config, not user input
	// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
	cmd := exec.CommandContext(ctx, parts[0], parts[1:]...)
	cmd.Dir = repoPath
	output, err := cmd.CombinedOutput()

	if err != nil {
		return &Result{
			Pass:   false,
			Output: string(output),
		}, nil
	}

	return &Result{
		Pass:   true,
		Output: string(output),
	}, nil
}
