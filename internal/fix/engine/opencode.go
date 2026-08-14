package engine

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// wolfOpenCodeConfig is the permission document we force via OPENCODE_CONFIG.
// A repo-supplied opencode.json overrides this file, so Fix strips those
// paths from the worktree first (see docs/superpowers/specs/2026-08-03-opencode-spike-findings.md).
//
// task is allowed (in-process subagents, same Node heap). bash is denied —
// the hung Trivy turn was `go list …@latest` via bash. Wolf formats/builds.
const wolfOpenCodeConfig = `{
  "$schema": "https://opencode.ai/config.json",
  "*": "deny",
  "read": "allow",
  "edit": "allow",
  "glob": "allow",
  "grep": "allow",
  "list": "allow",
  "todowrite": "allow",
  "task": "allow"
}
`

// ErrStall is returned when OpenCode emits no JSON events for StallAfter.
var ErrStall = errors.New("stall: no opencode event")

// IsStall reports whether err is (or wraps) a stream stall.
func IsStall(err error) bool {
	return err != nil && (errors.Is(err, ErrStall) || strings.Contains(err.Error(), ErrStall.Error()))
}

// IsStallMessage reports whether an engine error string is a stall.
func IsStallMessage(s string) bool {
	return strings.Contains(s, ErrStall.Error())
}

// streamOpts configures the OpenCode stdout watchdog. Tests inject short
// intervals; production uses defaultStallAfter / defaultHeartbeat.
type streamOpts struct {
	StallAfter time.Duration
	Heartbeat  time.Duration
}

const (
	defaultStallAfter = 4 * time.Minute
	defaultHeartbeat  = 30 * time.Second
)

// OpenCode implements SubprocessEngine using the OpenCode CLI.
type OpenCode struct{}

func (o *OpenCode) Name() string { return "opencode" }

func (o *OpenCode) Available() bool {
	_, err := exec.LookPath("opencode")
	return err == nil
}

func (o *OpenCode) Fix(ctx context.Context, req FixRequest) (*FixResult, error) {
	timeout := req.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	stripped := stripRepoOpenCodeConfig(req.RepoPath)

	cfgDir, err := os.MkdirTemp("", "wolf-opencode-")
	if err != nil {
		return &FixResult{Error: "create opencode config dir: " + err.Error()}, nil
	}
	defer os.RemoveAll(cfgDir)
	cfgPath := filepath.Join(cfgDir, "opencode.json")
	if err := os.WriteFile(cfgPath, []byte(wolfOpenCodeConfig), 0o600); err != nil {
		return &FixResult{Error: "write opencode config: " + err.Error()}, nil
	}

	prompt := RenderPrompt(req.Instructions, req)
	// --format json emits one event per line. --auto approves tools that
	// our config does not deny (without it, run auto-rejects permission
	// prompts and the model looks idle). --file attaches the review doc.
	args := []string{"run", "--format", "json", "--auto"}
	if model := strings.TrimSpace(req.Model); model != "" {
		args = append(args, "-m", model)
	}
	variant := strings.TrimSpace(req.Variant)
	if variant == "" {
		variant = strings.TrimSpace(req.Effort)
	}
	if variant != "" {
		args = append(args, "--variant", variant)
	}
	// Do not pass --file: yargs treats it as an array and swallows the
	// prompt as another filename ("File not found: You are Wolf's fixer…").
	// The review doc is already in the repo at FindingsFile.
	args = append(args, prompt)
	cmd := exec.CommandContext(ctx, "opencode", args...) // #nosec G204
	cmd.Dir = req.RepoPath
	cmd.Stdin = nil // inherited stdin hangs opencode run indefinitely
	env := append(cmd.Environ(), "OPENCODE_CONFIG="+cfgPath)
	if len(req.Env) > 0 {
		env = append(env, req.Env...)
	}
	cmd.Env = env

	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	output, runErr := runOpenCodeStreaming(ctx, cmd, req.Progress)
	res := &FixResult{
		Output:       string(output),
		EditsInPlace: true,
		Usage:        parseOpenCodeUsage(output),
		FilesChanged: changedFiles(req.RepoPath),
	}
	if len(stripped) > 0 {
		res.Output = "stripped repo opencode config: " + strings.Join(stripped, ", ") + "\n" + res.Output
	}
	if runErr != nil {
		res.Error = runErr.Error()
		applySkipVerdict(res, requestIDs(req))
		res.Success = len(res.FilesChanged) > 0
		return res, nil
	}
	applySkipVerdict(res, requestIDs(req))
	if res.Skipped {
		return res, nil
	}
	res.Success = len(res.FilesChanged) > 0
	if !res.Success {
		res.Error = "opencode made no file changes"
		if len(req.Batch()) <= 1 {
			res.Skipped = true
			res.SkipReason = "no file changes — treated as not a real/fixable issue"
		}
	}
	return res, nil
}

func runOpenCodeStreaming(ctx context.Context, cmd *exec.Cmd, progress func(string)) ([]byte, error) {
	return runOpenCodeStreamingOpts(ctx, cmd, progress, streamOpts{
		StallAfter: defaultStallAfter,
		Heartbeat:  defaultHeartbeat,
	})
}

func runOpenCodeStreamingOpts(ctx context.Context, cmd *exec.Cmd, progress func(string), opts streamOpts) ([]byte, error) {
	if opts.StallAfter <= 0 {
		opts.StallAfter = defaultStallAfter
	}
	if opts.Heartbeat <= 0 {
		opts.Heartbeat = defaultHeartbeat
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	done := make(chan struct{})
	var once sync.Once
	finish := func() { once.Do(func() { close(done) }) }
	defer finish()

	var mu sync.Mutex
	lastEvent := time.Now()
	lastMsg := "start"
	stalled := false

	if ctx != nil {
		go func() {
			select {
			case <-ctx.Done():
				killProcessGroup(cmd)
			case <-done:
			}
		}()
	}

	go func() {
		ticker := time.NewTicker(opts.Heartbeat)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				mu.Lock()
				silent := time.Since(lastEvent)
				msg := lastMsg
				mu.Unlock()
				if progress != nil {
					progress(fmt.Sprintf("still running, last event %ds ago (%s)", int(silent.Seconds()), msg))
				}
				if silent >= opts.StallAfter {
					mu.Lock()
					stalled = true
					last := lastMsg
					mu.Unlock()
					if progress != nil {
						progress(fmt.Sprintf("stall: no opencode event for %s (last: %s)", opts.StallAfter, last))
					}
					killProcessGroup(cmd)
					return
				}
			}
		}
	}()

	var buf bytes.Buffer
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		buf.WriteString(line)
		buf.WriteByte('\n')
		if strings.TrimSpace(line) != "" && strings.HasPrefix(strings.TrimSpace(line), "{") {
			mu.Lock()
			lastEvent = time.Now()
			if ev := formatOpenCodeEvent(line); ev != "" {
				lastMsg = ev
			} else {
				lastMsg = "json"
			}
			msg := lastMsg
			mu.Unlock()
			if progress != nil && msg != "json" {
				progress(msg)
			}
		} else if progress != nil {
			if msg := formatOpenCodeEvent(line); msg != "" {
				progress(msg)
			}
		}
	}
	waitErr := cmd.Wait()
	finish()
	if stderr.Len() > 0 {
		buf.Write(stderr.Bytes())
	}
	mu.Lock()
	wasStall := stalled
	last := lastMsg
	mu.Unlock()
	if wasStall {
		return buf.Bytes(), fmt.Errorf("%w (last: %s)", ErrStall, last)
	}
	if scanErr := sc.Err(); scanErr != nil && waitErr == nil {
		return buf.Bytes(), scanErr
	}
	return buf.Bytes(), waitErr
}

func killProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}

// CountTaskToolEvents counts tool_use events whose tool name is "task".
func CountTaskToolEvents(output string) int {
	n := 0
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] != '{' {
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		if typ, _ := ev["type"].(string); typ != "tool_use" {
			continue
		}
		part, _ := ev["part"].(map[string]any)
		if part == nil {
			continue
		}
		name := strings.ToLower(firstString(part, "tool", "name"))
		if name == "task" {
			n++
		}
	}
	return n
}

func formatOpenCodeEvent(line string) string {
	line = strings.TrimSpace(line)
	if line == "" || line[0] != '{' {
		return ""
	}
	var ev map[string]any
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		return ""
	}
	typ, _ := ev["type"].(string)
	switch typ {
	case "tool_use":
		part, _ := ev["part"].(map[string]any)
		if part == nil {
			return "tool"
		}
		name := firstString(part, "tool", "name")
		state, _ := part["state"].(map[string]any)
		status := ""
		detail := ""
		if state != nil {
			status, _ = state["status"].(string)
			if in, ok := state["input"].(map[string]any); ok {
				detail = firstString(in, "filePath", "path", "file", "pattern", "query", "command", "glob")
			}
			if status == "error" {
				if errStr := firstString(state, "error"); errStr != "" {
					detail = clip(errStr, 120)
				}
			}
		}
		switch {
		case name != "" && detail != "" && status == "error":
			return "tool " + name + " failed " + detail
		case name != "" && detail != "":
			return "tool " + name + " " + detail
		case name != "":
			return "tool " + name
		default:
			return "tool"
		}
	case "step_start":
		return "step start"
	case "step_finish":
		return "step done"
	case "text":
		part, _ := ev["part"].(map[string]any)
		text := ""
		if part != nil {
			text, _ = part["text"].(string)
		}
		text = strings.TrimSpace(text)
		if text == "" {
			return ""
		}
		return "say " + clip(text, 200)
	case "error":
		return "error " + clip(fmtJSON(ev["error"]), 200)
	default:
		return ""
	}
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	}
	return ""
}

func fmtJSON(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if n > 0 && len(s) > n {
		return s[:n] + "…"
	}
	return s
}

func stripRepoOpenCodeConfig(repoPath string) []string {
	if repoPath == "" {
		return nil
	}
	var stripped []string
	for _, rel := range []string{"opencode.json", "opencode.jsonc", ".opencode"} {
		p := filepath.Join(repoPath, rel)
		if _, err := os.Lstat(p); err != nil {
			continue
		}
		if err := os.RemoveAll(p); err == nil {
			stripped = append(stripped, rel)
		}
	}
	return stripped
}

func parseOpenCodeUsage(output []byte) Usage {
	var u Usage
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ev struct {
			Type string `json:"type"`
			Part struct {
				Tokens struct {
					Total  int64 `json:"total"`
					Input  int64 `json:"input"`
					Output int64 `json:"output"`
				} `json:"tokens"`
				Cost float64 `json:"cost"`
			} `json:"part"`
		}
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		if ev.Type != "step_finish" {
			continue
		}
		u.Turns++
		u.InputTokens += ev.Part.Tokens.Input
		u.OutputTokens += ev.Part.Tokens.Output
		if ev.Part.Tokens.Total > 0 {
			u.TotalTokens += ev.Part.Tokens.Total
		} else {
			u.TotalTokens += ev.Part.Tokens.Input + ev.Part.Tokens.Output
		}
		u.CostUSD += ev.Part.Cost
	}
	return u
}

func countOpenCodeTurns(output []byte) int {
	return parseOpenCodeUsage(output).Turns
}
