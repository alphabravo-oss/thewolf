package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/wolflog"
)

// FixRequest holds the information needed to fix a single finding.
type FixRequest struct {
	Finding  models.Finding
	RepoPath string // working directory (worktree path)
	Timeout  time.Duration
}

// FixResult holds the outcome of a fix attempt.
type FixResult struct {
	Success      bool
	FilesChanged []string
	Diff         string
	Output       string
	Error        string
}

// SubprocessEngine defines the interface for AI-powered fix engines.
type SubprocessEngine interface {
	Name() string
	Fix(ctx context.Context, req FixRequest) (*FixResult, error)
	Available() bool
}

// DefaultTimeout is the per-finding fix timeout.
const DefaultTimeout = 5 * time.Minute

// ClaudeCode implements SubprocessEngine using the Claude Code CLI.
type ClaudeCode struct {
	Model string // optional model override (default: claude-sonnet-4-20250514)
}

func (c *ClaudeCode) Name() string { return "claude-code" }

func (c *ClaudeCode) Available() bool {
	_, err := exec.LookPath("claude")
	return err == nil
}

func (c *ClaudeCode) Fix(ctx context.Context, req FixRequest) (*FixResult, error) {
	timeout := req.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	prompt := buildPrompt(req.Finding)

	model := c.Model
	if model == "" {
		model = "claude-sonnet-4-20250514"
	}

	cmd := exec.CommandContext(ctx, "claude", // #nosec G204 -- command is a configured tool name; args from internal config, not user input
		"--dangerously-skip-permissions",
		"--output-format", "json",
		"--max-turns", "20",
		"--model", model,
		"-p", prompt,
	)
	cmd.Dir = req.RepoPath

	output, err := cmd.CombinedOutput()
	if err != nil {
		return &FixResult{
			Success: false,
			Output:  string(output),
			Error:   err.Error(),
		}, nil
	}

	return parseClaudeOutput(output)
}

// Codex implements SubprocessEngine using the Codex CLI.
type Codex struct{}

func (c *Codex) Name() string { return "codex" }

func (c *Codex) Available() bool {
	_, err := exec.LookPath("codex")
	return err == nil
}

func (c *Codex) Fix(ctx context.Context, req FixRequest) (*FixResult, error) {
	timeout := req.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	prompt := fmt.Sprintf("Fix: %s in %s:%d. %s",
		req.Finding.Title,
		req.Finding.FilePath,
		req.Finding.LineStart,
		req.Finding.Description,
	)

	cmd := exec.CommandContext(ctx, "codex", // #nosec G204 -- command is a configured tool name; args from internal config, not user input
		"--approval-mode", "full-auto",
		"--quiet",
		prompt,
	)
	cmd.Dir = req.RepoPath

	output, err := cmd.CombinedOutput()
	if err != nil {
		return &FixResult{
			Success: false,
			Output:  string(output),
			Error:   err.Error(),
		}, nil
	}

	return &FixResult{
		Success: true,
		Output:  string(output),
	}, nil
}

// Custom implements SubprocessEngine using a user-provided CLI command.
// This supports cc-mirror forks (kimi, glm, etc.) or any Claude Code-compatible
// CLI that accepts the same flags.
//
// The Command field is the binary name (e.g., "kimi", "glm", "aider").
// The Args field provides extra arguments inserted before the prompt.
// The Mode field controls how the prompt is passed:
//   - "claude" (default): uses Claude Code flags (--dangerously-skip-permissions, -p, etc.)
//   - "raw": passes only the extra args + prompt as the last argument
type Custom struct {
	Command string   // binary name (e.g., "kimi", "glm")
	Args    []string // extra args to pass before the prompt
	Model   string   // model override (optional)
	Mode    string   // "claude" (default) or "raw"
}

func (c *Custom) Name() string { return "custom:" + c.Command }

func (c *Custom) Available() bool {
	_, err := exec.LookPath(c.Command)
	return err == nil
}

func (c *Custom) Fix(ctx context.Context, req FixRequest) (*FixResult, error) {
	timeout := req.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	prompt := buildPrompt(req.Finding)

	var args []string
	mode := c.Mode
	if mode == "" {
		mode = "claude"
	}

	switch mode {
	case "claude":
		// Claude Code-compatible flags (works for cc-mirror forks like kimi, glm)
		args = append(args,
			"--dangerously-skip-permissions",
			"--output-format", "json",
			"--max-turns", "20",
		)
		if c.Model != "" {
			args = append(args, "--model", c.Model)
		}
		args = append(args, c.Args...)
		args = append(args, "-p", prompt)
	case "raw":
		// Raw mode: just extra args + prompt
		args = append(args, c.Args...)
		args = append(args, prompt)
	default:
		return nil, fmt.Errorf("unknown custom engine mode: %s", mode)
	}

	// #nosec G204 -- command is a configured tool name (docker / claude / codex / scanner binary); args sourced from internal config, not user input
	// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
	cmd := exec.CommandContext(ctx, c.Command, args...) // #nosec G204 -- command is a configured tool name; args sourced from internal config
	cmd.Dir = req.RepoPath

	output, err := cmd.CombinedOutput()
	if err != nil {
		return &FixResult{
			Success: false,
			Output:  string(output),
			Error:   err.Error(),
		}, nil
	}

	if mode == "claude" {
		return parseClaudeOutput(output)
	}

	return &FixResult{
		Success: true,
		Output:  string(output),
	}, nil
}

func buildPrompt(f models.Finding) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Fix the following issue in this repository:\n\n")
	fmt.Fprintf(&sb, "Title: %s\n", f.Title)
	fmt.Fprintf(&sb, "File: %s:%d\n", f.FilePath, f.LineStart)
	fmt.Fprintf(&sb, "Severity: %s\n", f.Severity)
	fmt.Fprintf(&sb, "Description: %s\n", f.Description)
	if f.CodeSnippet != "" {
		fmt.Fprintf(&sb, "Code:\n%s\n", f.CodeSnippet)
	}
	if f.AIFixSuggestion != "" {
		fmt.Fprintf(&sb, "Suggestion: %s\n", f.AIFixSuggestion)
	}
	fmt.Fprintf(&sb, "\nApply the fix, then run the relevant linter/test to verify.\n")
	fmt.Fprintf(&sb, "Only modify files necessary to fix this specific issue.\n")
	fmt.Fprintf(&sb, "Do not make unrelated changes.")
	return sb.String()
}

func parseClaudeOutput(output []byte) (*FixResult, error) {
	// Claude Code JSON output contains a result field
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(output, &raw); err != nil {
		// If not valid JSON, treat as plain text success
		return &FixResult{
			Success: true,
			Output:  string(output),
		}, nil
	}

	result := &FixResult{
		Success: true,
		Output:  string(output),
	}

	// Try to extract files_changed from the output
	if filesRaw, ok := raw["files_changed"]; ok {
		var files []string
		if err := json.Unmarshal(filesRaw, &files); err == nil {
			result.FilesChanged = files
		}
	}

	return result, nil
}

// AutoEngine tries Claude Code first, then falls back to Codex.
type AutoEngine struct {
	engines []SubprocessEngine
}

// NewAutoEngine creates an engine that tries Claude Code first, then Codex.
func NewAutoEngine() *AutoEngine {
	return &AutoEngine{
		engines: []SubprocessEngine{&ClaudeCode{}, &Codex{}},
	}
}

func (a *AutoEngine) Name() string { return "auto" }

func (a *AutoEngine) Available() bool {
	for _, e := range a.engines {
		if e.Available() {
			return true
		}
	}
	return false
}

func (a *AutoEngine) Fix(ctx context.Context, req FixRequest) (*FixResult, error) {
	for _, e := range a.engines {
		if !e.Available() {
			wolflog.Debug().Str("engine", e.Name()).Msg("fix engine not available, trying next")
			continue
		}
		return e.Fix(ctx, req)
	}
	return nil, fmt.Errorf("no fix engine available (tried claude-code, codex)")
}

// NewEngine returns a SubprocessEngine by name.
// Supports built-in engines and custom commands via "custom:<command>" syntax.
// Examples:
//
//	"claude-code"          → Claude Code CLI
//	"codex"                → Codex CLI
//	"auto"                 → tries claude-code, then codex
//	"custom:kimi"          → cc-mirror with kimi backend (Claude Code-compatible flags)
//	"custom:glm"           → cc-mirror with glm backend
//	"custom:aider:raw"     → any CLI, raw mode (just passes prompt as last arg)
func NewEngine(name string) (SubprocessEngine, error) {
	switch {
	case name == "claude-code":
		return &ClaudeCode{}, nil
	case name == "codex":
		return &Codex{}, nil
	case name == "auto" || name == "":
		return NewAutoEngine(), nil
	case strings.HasPrefix(name, "custom:"):
		return parseCustomEngine(name)
	default:
		return nil, fmt.Errorf("unknown engine: %s (use 'custom:<command>' for custom CLI tools)", name)
	}
}

// parseCustomEngine parses "custom:<cmd>" or "custom:<cmd>:<mode>" syntax.
func parseCustomEngine(name string) (*Custom, error) {
	parts := strings.SplitN(name, ":", 3)
	if len(parts) < 2 || parts[1] == "" {
		return nil, fmt.Errorf("custom engine requires a command name: 'custom:<command>' or 'custom:<command>:<mode>'")
	}

	engine := &Custom{
		Command: parts[1],
		Mode:    "claude", // default to claude-compatible mode
	}

	if len(parts) == 3 && parts[2] != "" {
		engine.Mode = parts[2]
	}

	return engine, nil
}

// NewCustomEngine creates a Custom engine with explicit configuration.
func NewCustomEngine(command, model, mode string, extraArgs ...string) *Custom {
	if mode == "" {
		mode = "claude"
	}
	return &Custom{
		Command: command,
		Model:   model,
		Mode:    mode,
		Args:    extraArgs,
	}
}
