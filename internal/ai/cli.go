package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// CLIProvider implements Provider by shelling out to a local CLI tool
// (e.g. claude, codex) that is already authenticated. No API keys needed.
type CLIProvider struct {
	engine  string // display name: "claude-code", "codex"
	command string // binary name: "claude", "codex"
	// useShellWrapper signals that the command should be invoked via sh -c
	// to ensure proper shell quoting of arguments like --tools "".
	useShellWrapper bool
	systemPrompt    string
	logCallback     LogCallback
}

// SetLogCallback configures the logging callback.
func (p *CLIProvider) SetLogCallback(cb LogCallback) {
	p.logCallback = cb
}

// NewCLIProvider creates a provider that calls a local CLI tool.
func NewCLIProvider(engine string) *CLIProvider {
	switch strings.ToLower(engine) {
	case "claude-code":
		return &CLIProvider{
			engine:          "claude-code",
			command:         "claude",
			useShellWrapper: true,
			systemPrompt:    "You are a security engineer. Analyze findings concisely. Output valid JSON only.",
		}
	case "codex":
		return &CLIProvider{
			engine:  "codex",
			command: "codex",
		}
	default:
		return &CLIProvider{
			engine:  engine,
			command: engine,
		}
	}
}

func (p *CLIProvider) Name() string {
	return p.engine
}

func (p *CLIProvider) Analyze(ctx context.Context, req AnalyzeRequest) (*AnalyzeResponse, error) {
	prompt := buildAnalyzePrompt(req)
	body, err := p.send(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("%s analyze: %w", p.engine, err)
	}
	var resp AnalyzeResponse
	if err := extractJSON(body, &resp); err != nil {
		return nil, fmt.Errorf("%s analyze parse: %w", p.engine, err)
	}
	return &resp, nil
}

func (p *CLIProvider) Score(ctx context.Context, req ScoreRequest) (*ScoreResponse, error) {
	prompt := buildScorePrompt(req)
	body, err := p.send(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("%s score: %w", p.engine, err)
	}
	var resp ScoreResponse
	if err := extractJSON(body, &resp); err != nil {
		return nil, fmt.Errorf("%s score parse: %w", p.engine, err)
	}
	return &resp, nil
}

func (p *CLIProvider) Summarize(ctx context.Context, req SummarizeRequest) (string, error) {
	prompt := buildSummarizePrompt(req)
	body, err := p.send(ctx, prompt)
	if err != nil {
		return "", fmt.Errorf("%s summarize: %w", p.engine, err)
	}
	return body, nil
}

func (p *CLIProvider) Complete(ctx context.Context, prompt string) (string, error) {
	body, err := p.send(ctx, prompt)
	if err != nil {
		return "", fmt.Errorf("%s complete: %w", p.engine, err)
	}
	return body, nil
}

// send executes the CLI tool with the given prompt via stdin and returns stdout.
func (p *CLIProvider) send(ctx context.Context, prompt string) (string, error) {
	start := time.Now()

	// Check that the binary exists
	if _, err := exec.LookPath(p.command); err != nil {
		p.emitLog(prompt, "", err.Error(), start)
		return "", fmt.Errorf("%s binary not found in PATH: %w", p.command, err)
	}

	// Hard timeout of 5 minutes per CLI call to prevent hangs
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	var cmd *exec.Cmd
	if p.useShellWrapper {
		// Use sh -c so the shell handles --tools "" quoting correctly.
		// When passed via exec.Command args, "" is an empty argv element
		// that the CLI arg parser doesn't interpret as "no tools".
		shellCmd := fmt.Sprintf(
			`%s -p --output-format json --no-session-persistence --model sonnet --max-turns 1 --effort low --system-prompt %s`,
			p.command, shellQuote(p.systemPrompt),
		)
		cmd = exec.CommandContext(callCtx, "sh", "-c", shellCmd)
	} else {
		cmd = exec.CommandContext(callCtx, p.command, "-p")
	}

	// Clear environment variables that prevent nested CLI sessions
	env := os.Environ()
	filtered := make([]string, 0, len(env))
	for _, e := range env {
		if !strings.HasPrefix(e, "CLAUDECODE=") && !strings.HasPrefix(e, "CLAUDE_CODE_SESSION=") {
			filtered = append(filtered, e)
		}
	}
	cmd.Env = filtered

	// Pass prompt via stdin to avoid OS argument length limits
	cmd.Stdin = bytes.NewReader([]byte(prompt))

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		stderrStr := strings.TrimSpace(stderr.String())
		var errMsg string
		if stderrStr != "" {
			errMsg = fmt.Sprintf("%s exited with error: %v\nstderr: %s", p.command, err, stderrStr)
		} else {
			errMsg = fmt.Sprintf("%s exited with error: %v", p.command, err)
		}
		p.emitLog(prompt, "", errMsg, start)
		return "", fmt.Errorf("%s", errMsg)
	}

	output := strings.TrimSpace(stdout.String())
	if output == "" {
		stderrStr := strings.TrimSpace(stderr.String())
		errMsg := fmt.Sprintf("empty response from %s (stderr: %.200s)", p.command, stderrStr)
		p.emitLog(prompt, "", errMsg, start)
		return "", fmt.Errorf("%s", errMsg)
	}

	// If using JSON output format, extract the result from the envelope.
	// Claude Code JSON envelope: {"type":"result","subtype":"success","result":"..."}
	var envelope struct {
		Type    string          `json:"type"`
		Subtype string          `json:"subtype"`
		Result  json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal([]byte(output), &envelope); err == nil && envelope.Type == "result" {
		// Handle error subtypes with no result.
		if len(envelope.Result) == 0 || string(envelope.Result) == "null" || string(envelope.Result) == `""` {
			errMsg := fmt.Sprintf("%s returned no result (subtype: %s)", p.command, envelope.Subtype)
			p.emitLog(prompt, "", errMsg, start)
			return "", fmt.Errorf("%s", errMsg)
		}
		// Result can be a JSON string (quoted) or a JSON object/array (unquoted).
		var strResult string
		if json.Unmarshal(envelope.Result, &strResult) == nil {
			// It was a quoted string — return the unquoted content.
			p.emitLog(prompt, strResult, "", start)
			return strResult, nil
		}
		// It was a JSON object/array — return as-is.
		result := string(envelope.Result)
		p.emitLog(prompt, result, "", start)
		return result, nil
	}

	// Fall back to raw output if not in envelope format.
	p.emitLog(prompt, output, "", start)
	return output, nil
}

func (p *CLIProvider) emitLog(prompt, response, errMsg string, start time.Time) {
	if p.logCallback == nil {
		return
	}
	p.logCallback(AICallLog{
		Provider:       p.engine,
		Model:          "",
		Prompt:         prompt,
		Response:       response,
		Error:          errMsg,
		DurationMs:     time.Since(start).Milliseconds(),
		PromptTokens:   EstimateTokens(prompt),
		ResponseTokens: EstimateTokens(response),
	})
}

// shellQuote wraps a string in single quotes for safe shell embedding,
// escaping any embedded single quotes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// IsCLIEngine returns true if the engine name refers to a local CLI tool.
func IsCLIEngine(engine string) bool {
	switch strings.ToLower(engine) {
	case "claude-code", "codex":
		return true
	default:
		return false
	}
}
