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
	Model        string  // optional model override (default: claude-sonnet-4-20250514)
	MaxBudgetUSD float64 // max cost per finding in USD (0 = unlimited)
	MaxTurns     int     // max agentic turns per finding (0 = default 20)
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

	maxTurns := c.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 20
	}

	args := []string{
		"-p", prompt,
		"--output-format", "json",
		"--max-turns", fmt.Sprintf("%d", maxTurns),
		"--permission-mode", "bypassPermissions",
		"--no-session-persistence",
	}
	if c.Model != "" {
		args = append(args, "--model", c.Model)
	}
	if c.MaxBudgetUSD > 0 {
		args = append(args, "--max-budget-usd", fmt.Sprintf("%.2f", c.MaxBudgetUSD))
	}

	cmd := exec.CommandContext(ctx, "claude", args...)
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

	prompt := buildPrompt(req.Finding)

	cmd := exec.CommandContext(ctx, "codex",
		"exec",
		"--full-auto",
		"--json",
		"--ephemeral",
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

	return parseCodexOutput(output), nil
}

// Custom implements SubprocessEngine using a user-provided CLI command.
// This supports cc-mirror forks (kimi, glm, etc.) or any Claude Code-compatible
// CLI that accepts the same flags.
//
// The Command field is the binary name (e.g., "kimi", "glm", "aider").
// The Args field provides extra arguments inserted before the prompt.
// The Mode field controls how the prompt is passed:
//   - "claude" (default): uses Claude Code flags (--permission-mode bypassPermissions, -p, etc.)
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
			"--permission-mode", "bypassPermissions",
			"--output-format", "json",
			"--max-turns", "20",
			"--no-session-persistence",
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

	cmd := exec.CommandContext(ctx, c.Command, args...)
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

	sb.WriteString("# Security Finding Fix Request\n\n")

	// Location
	sb.WriteString("## Location\n")
	fmt.Fprintf(&sb, "- **File**: `%s`\n", f.FilePath)
	if f.LineEnd > 0 && f.LineEnd != f.LineStart {
		fmt.Fprintf(&sb, "- **Lines**: %d-%d\n", f.LineStart, f.LineEnd)
	} else {
		fmt.Fprintf(&sb, "- **Line**: %d\n", f.LineStart)
	}
	if f.FunctionName != "" {
		fmt.Fprintf(&sb, "- **Function**: `%s`\n", f.FunctionName)
	}
	if f.ModuleName != "" {
		fmt.Fprintf(&sb, "- **Module**: `%s`\n", f.ModuleName)
	}

	// Issue details
	sb.WriteString("\n## Issue\n")
	fmt.Fprintf(&sb, "- **Title**: %s\n", f.Title)
	fmt.Fprintf(&sb, "- **Severity**: %s\n", f.Severity)
	fmt.Fprintf(&sb, "- **Category**: %s\n", f.Category)
	if f.ToolName != "" {
		fmt.Fprintf(&sb, "- **Tool**: %s\n", f.ToolName)
	}
	if f.RuleID != "" {
		fmt.Fprintf(&sb, "- **Rule**: %s\n", f.RuleID)
	}
	if f.CWEID != "" {
		fmt.Fprintf(&sb, "- **CWE**: %s\n", f.CWEID)
	}

	// Description
	sb.WriteString("\n## Description\n")
	fmt.Fprintf(&sb, "%s\n", f.Description)

	// Code context
	if f.CodeSnippet != "" {
		sb.WriteString("\n## Current Code\n```\n")
		sb.WriteString(f.CodeSnippet)
		sb.WriteString("\n```\n")
	}

	// AI suggestion
	if f.AIFixSuggestion != "" {
		sb.WriteString("\n## Suggested Fix\n")
		fmt.Fprintf(&sb, "%s\n", f.AIFixSuggestion)
	}

	// Constraints
	sb.WriteString("\n## Instructions\n")
	sb.WriteString("1. Read the file at the specified location.\n")
	sb.WriteString("2. Apply the minimum change necessary to fix this specific issue.\n")
	sb.WriteString("3. Do NOT refactor surrounding code, add comments, or make unrelated changes.\n")
	sb.WriteString("4. Do NOT modify files that are not directly related to this finding.\n")
	sb.WriteString("5. Do NOT run tests, linters, or build commands.\n")

	return sb.String()
}

// claudeJSONOutput represents the JSON structure returned by `claude -p --output-format json`.
type claudeJSONOutput struct {
	Result       string  `json:"result"`
	SessionID    string  `json:"session_id"`
	IsError      bool    `json:"is_error"`
	Subtype      string  `json:"subtype"`
	TotalCostUSD float64 `json:"total_cost_usd"`
	NumTurns     int     `json:"num_turns"`
	DurationMS   int     `json:"duration_ms"`
}

func parseClaudeOutput(output []byte) (*FixResult, error) {
	var parsed claudeJSONOutput
	if err := json.Unmarshal(output, &parsed); err != nil {
		// Non-JSON output — treat as text success
		return &FixResult{Success: true, Output: string(output)}, nil
	}
	// max-turns exits with is_error=true + subtype="error_max_turns",
	// but the CLI may have made partial progress — treat as success and
	// let the runner's CaptureDiff determine if changes were actually made.
	if parsed.IsError && parsed.Subtype != "error_max_turns" {
		return &FixResult{Success: false, Output: parsed.Result, Error: parsed.Result}, nil
	}
	return &FixResult{Success: true, Output: parsed.Result}, nil
}

// parseCodexOutput handles Codex --json newline-delimited JSON events.
// If the process exited successfully, the task completed.
func parseCodexOutput(output []byte) *FixResult {
	return &FixResult{
		Success: true,
		Output:  string(output),
	}
}

// EngineInfo describes a well-known fix engine and its availability.
type EngineInfo struct {
	Name      string `json:"name"`
	Label     string `json:"label"`
	Binary    string `json:"binary"`
	Mode      string `json:"mode"`
	Available bool   `json:"available"`
}

// WellKnownEngines lists the supported fix engines.
var WellKnownEngines = []EngineInfo{
	{Name: "claude-code", Label: "Claude Code", Binary: "claude", Mode: "claude"},
	{Name: "codex", Label: "OpenAI Codex CLI", Binary: "codex", Mode: ""},
	{Name: "auto", Label: "Auto (first available)", Binary: "", Mode: ""},
}

// ListAvailableEngines returns all well-known engines with availability status.
func ListAvailableEngines() []EngineInfo {
	result := make([]EngineInfo, len(WellKnownEngines))
	for i, e := range WellKnownEngines {
		info := e
		if info.Binary != "" {
			_, err := exec.LookPath(info.Binary)
			info.Available = err == nil
		} else {
			// "auto" is available if any engine is available
			info.Available = true
		}
		result[i] = info
	}
	return result
}

// SettingsApplier is optionally implemented by engines that accept runtime settings.
type SettingsApplier interface {
	ApplySettings(maxBudgetUSD float64, maxTurns int)
}

// ApplySettings sets the cost and turn limits on ClaudeCode engines.
func (c *ClaudeCode) ApplySettings(maxBudgetUSD float64, maxTurns int) {
	c.MaxBudgetUSD = maxBudgetUSD
	c.MaxTurns = maxTurns
}

// AutoEngine tries Claude Code first, then falls back to Codex.
type AutoEngine struct {
	engines []SubprocessEngine
}

// NewAutoEngine creates an engine that tries all known engines in priority order.
func NewAutoEngine() *AutoEngine {
	return &AutoEngine{
		engines: []SubprocessEngine{
			&ClaudeCode{}, &Codex{},
		},
	}
}

// ApplySettings propagates settings to all inner engines that support them.
func (a *AutoEngine) ApplySettings(maxBudgetUSD float64, maxTurns int) {
	for _, e := range a.engines {
		if sa, ok := e.(SettingsApplier); ok {
			sa.ApplySettings(maxBudgetUSD, maxTurns)
		}
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
//	"claude-code"          -> Claude Code CLI
//	"codex"                -> Codex CLI
//	"auto"                 -> tries claude-code, then codex
//	"custom:kimi"          -> cc-mirror with kimi backend (Claude Code-compatible flags)
//	"custom:aider:raw"     -> any CLI, raw mode (just passes prompt as last arg)
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
