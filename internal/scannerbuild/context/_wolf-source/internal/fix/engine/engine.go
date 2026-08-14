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

// FixRequest holds the information needed to fix one scanner's findings.
type FixRequest struct {
	Finding  models.Finding
	Findings []models.Finding
	// FindingsFile is an absolute path to the review document listing every
	// finding from this tool. The agent reads that file; it is not in the repo.
	FindingsFile string
	Tool         string
	RepoPath     string // working directory (worktree path)
	Timeout      time.Duration
	// Env is extra KEY=value pairs merged onto the CLI process (API keys).
	Env []string
	// Model / Effort / Variant are the t3code-style dials forwarded to the CLI.
	Model   string
	Effort  string
	Variant string
	// Instructions is the operator-editable template for this loop
	// (first pass vs follow-up). Empty uses the shipped default.
	Instructions string
	// Progress, if set, is called with a short human line as the engine
	// works (tool calls, steps). Used to stream the agent log live.
	Progress func(string)
	// Phase is "classify" (tokens only, no edits) or "fix" (default).
	Phase string
}

// Batch returns the findings this request covers (multi-finding tool run, or
// the single Finding for older callers).
func (r FixRequest) Batch() []models.Finding {
	if len(r.Findings) > 0 {
		return r.Findings
	}
	if r.Finding.ID != "" || r.Finding.FilePath != "" || r.Finding.Title != "" {
		return []models.Finding{r.Finding}
	}
	return nil
}

func requestIDs(req FixRequest) []string {
	batch := req.Batch()
	ids := make([]string, 0, len(batch))
	for _, f := range batch {
		if f.ID != "" {
			ids = append(ids, f.ID)
		}
	}
	return ids
}

// Usage is token/cost telemetry parsed from a harness (subscription usage
// still has no dollar cost; CostUSD is 0 in that case).
type Usage struct {
	Model        string
	InputTokens  int64
	OutputTokens int64
	TotalTokens  int64
	CostUSD      float64
	Turns        int
}

// FixResult holds the outcome of a fix attempt.
//
// EditsInPlace records how the change was delivered, which the orchestrator
// needs to know how to handle the result:
//
//   - true  (CLI engines: claude-code, codex, custom, config) — the engine
//     already edited the files in the worktree; the diff is informational and
//     the orchestrator should verify what landed on disk.
//   - false (the API engine) — the engine did NOT touch the filesystem; it
//     returned a unified diff in Diff that the orchestrator must apply
//     (git apply) before verifying.
type FixResult struct {
	Success      bool
	FilesChanged []string
	Diff         string
	Output       string
	Error        string
	// EditsInPlace is true when the engine mutated the worktree directly.
	// It is false for diff-returning engines (the API engine) whose Diff the
	// caller must apply itself.
	EditsInPlace bool
	Usage        Usage
	// Skipped means the engine judged the finding not worth fixing
	// (false positive, out of scope). Do not escalate to another engine.
	Skipped    bool
	SkipReason string
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

	prompt := RenderPrompt(req.Instructions, req)

	model := firstNonEmpty(req.Model, c.Model, "sonnet")
	args := []string{
		"--dangerously-skip-permissions",
		"--output-format", "json",
		"--max-turns", "20",
		"--model", model,
	}
	if effort := strings.TrimSpace(req.Effort); effort != "" && effort != "medium" {
		args = append(args, "--effort", effort)
	}
	args = append(args, "-p", prompt)

	cmd := exec.CommandContext(ctx, "claude", args...) // #nosec G204 -- command is a configured tool name; args from internal config, not user input
	cmd.Dir = req.RepoPath
	applyEngineEnv(cmd, req.Env)

	output, err := cmd.CombinedOutput()
	if err != nil {
		res := &FixResult{
			Success: false,
			Output:  string(output),
			Error:   err.Error(),
		}
		applySkipVerdict(res, requestIDs(req))
		return res, nil
	}

	res, err := parseClaudeOutput(output)
	if res != nil {
		applySkipVerdict(res, requestIDs(req))
	}
	return res, err
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

	args := []string{"--approval-mode", "full-auto", "--quiet"}
	if model := firstNonEmpty(req.Model); model != "" {
		args = append(args, "-m", model)
	}
	if effort := strings.TrimSpace(req.Effort); effort != "" {
		args = append(args, "-c", "model_reasoning_effort="+effort)
	}
	args = append(args, prompt)

	cmd := exec.CommandContext(ctx, "codex", args...) // #nosec G204 -- command is a configured tool name; args from internal config, not user input
	cmd.Dir = req.RepoPath
	applyEngineEnv(cmd, req.Env)
	// Judge by the worktree, not Codex's exit: an empty successful run is
	// rolled back by the verify gate.
	output, err := cmd.CombinedOutput()
	if err != nil {
		return &FixResult{
			Success: false,
			Output:  string(output),
			Error:   err.Error(),
		}, nil
	}

	changed := changedFiles(req.RepoPath)
	return &FixResult{
		Success:      true,
		FilesChanged: changed,
		Output:       string(output),
		EditsInPlace: true,
	}, nil
}

func applyEngineEnv(cmd *exec.Cmd, extra []string) {
	if len(extra) == 0 {
		return
	}
	cmd.Env = append(cmd.Environ(), extra...)
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

	prompt := RenderPrompt(req.Instructions, req)

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
	applyEngineEnv(cmd, req.Env)

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
		Success:      true,
		Output:       string(output),
		EditsInPlace: true,
	}, nil
}

func parseClaudeOutput(output []byte) (*FixResult, error) {
	// Claude Code JSON output contains a result field
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(output, &raw); err != nil {
		// Claude was invoked with --output-format=json. Accepting arbitrary or
		// empty stdout as success would let a protocol/auth regression bypass
		// the engine contract and defer discovery until after mutation.
		return &FixResult{
			Success: false,
			Output:  string(output),
			Error:   "claude returned malformed JSON output",
		}, nil
	}

	result := &FixResult{
		Success:      true,
		Output:       string(output),
		EditsInPlace: true,
	}

	// Try to extract files_changed from the output
	if filesRaw, ok := raw["files_changed"]; ok {
		var files []string
		if err := json.Unmarshal(filesRaw, &files); err == nil {
			result.FilesChanged = files
		}
	}
	result.Usage = parseClaudeUsage(raw)

	return result, nil
}

func parseClaudeUsage(raw map[string]json.RawMessage) Usage {
	var u Usage
	if m, ok := raw["model"]; ok {
		_ = json.Unmarshal(m, &u.Model)
	}
	if c, ok := raw["total_cost_usd"]; ok {
		_ = json.Unmarshal(c, &u.CostUSD)
	}
	if usageRaw, ok := raw["usage"]; ok {
		var usage struct {
			InputTokens  int64 `json:"input_tokens"`
			OutputTokens int64 `json:"output_tokens"`
		}
		if err := json.Unmarshal(usageRaw, &usage); err == nil {
			u.InputTokens = usage.InputTokens
			u.OutputTokens = usage.OutputTokens
			u.TotalTokens = usage.InputTokens + usage.OutputTokens
		}
	}
	return u
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// AutoEngine tries Claude Code first, then falls back to Codex.
type AutoEngine struct {
	engines []SubprocessEngine
}

// NewAutoEngine creates an engine that tries Claude Code first, then Codex.
func NewAutoEngine() *AutoEngine {
	return &AutoEngine{
		engines: []SubprocessEngine{&ClaudeCode{}, &Codex{}, &OpenCode{}},
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
	return nil, fmt.Errorf("no fix engine available (tried claude-code, codex, opencode)")
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
	case name == "opencode":
		return &OpenCode{}, nil
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
