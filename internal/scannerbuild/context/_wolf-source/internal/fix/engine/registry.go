package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// ToolDefinition is a config-driven CLI agent definition. It lets an
// operator add an AI fix tool (cursor-agent, opencode, antigravity, ...)
// without recompiling — the definition is stored as JSON in the settings
// store and resolved into a ConfigEngine at runtime.
type ToolDefinition struct {
	// Name is the unique tool name used to select it (e.g. "cursor-agent").
	Name string `json:"name"`
	// Command is the binary to execute.
	Command string `json:"command"`
	// Args is the argument template. The placeholders {prompt} and {repo}
	// are substituted per invocation. If no {prompt} placeholder appears,
	// the prompt is appended as the final argument.
	Args []string `json:"args"`
	// Workdir is the working directory template ({repo} placeholder).
	// Defaults to "{repo}" when empty.
	Workdir string `json:"workdir"`
	// SuccessRule decides how a run's success is judged:
	//   "repo_diff"  (default) — success = the run changed tracked files
	//   "exit_zero"            — success = the process exited 0
	// The loop's rescan is always the authoritative validator regardless.
	SuccessRule string `json:"success_rule"`
	// Env is extra environment for the process (merged over os.Environ).
	Env map[string]string `json:"env"`
}

// ParseToolDefinitions decodes the JSON array stored under the settings
// key that holds config-driven AI tools. An empty/blank input yields no
// definitions and no error (no tools configured is valid).
func ParseToolDefinitions(data []byte) ([]ToolDefinition, error) {
	s := strings.TrimSpace(string(data))
	if s == "" || s == "null" {
		return nil, nil
	}
	var defs []ToolDefinition
	if err := json.Unmarshal([]byte(s), &defs); err != nil {
		return nil, fmt.Errorf("parse ai tool definitions: %w", err)
	}
	for i, d := range defs {
		if strings.TrimSpace(d.Name) == "" {
			return nil, fmt.Errorf("ai tool #%d: name is required", i)
		}
		if strings.TrimSpace(d.Command) == "" {
			return nil, fmt.Errorf("ai tool %q: command is required", d.Name)
		}
		switch d.SuccessRule {
		case "", "repo_diff", "exit_zero":
		default:
			return nil, fmt.Errorf("ai tool %q: unknown success_rule %q", d.Name, d.SuccessRule)
		}
	}
	return defs, nil
}

// ConfigEngine is a SubprocessEngine backed by a ToolDefinition.
type ConfigEngine struct {
	def ToolDefinition
}

// NewConfigEngine wraps a ToolDefinition as a SubprocessEngine.
func NewConfigEngine(def ToolDefinition) *ConfigEngine {
	return &ConfigEngine{def: def}
}

func (e *ConfigEngine) Name() string { return e.def.Name }

func (e *ConfigEngine) Available() bool {
	_, err := exec.LookPath(e.def.Command)
	return err == nil
}

func (e *ConfigEngine) Fix(ctx context.Context, req FixRequest) (*FixResult, error) {
	timeout := req.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	prompt := RenderPrompt(req.Instructions, req)
	args := e.renderArgs(prompt, req.RepoPath)

	workdir := e.def.Workdir
	if workdir == "" {
		workdir = "{repo}"
	}
	workdir = strings.ReplaceAll(workdir, "{repo}", req.RepoPath)

	// #nosec G204 -- Command is an operator-configured tool name; args are
	// the operator's template with wolf-controlled prompt/repo substitution.
	cmd := exec.CommandContext(ctx, e.def.Command, args...)
	cmd.Dir = workdir
	if len(e.def.Env) > 0 {
		env := cmd.Environ()
		for k, v := range e.def.Env {
			env = append(env, k+"="+v)
		}
		cmd.Env = env
	}

	output, runErr := cmd.CombinedOutput()
	res := &FixResult{Output: string(output), EditsInPlace: true}
	if runErr != nil {
		res.Error = runErr.Error()
	}

	switch e.def.SuccessRule {
	case "exit_zero":
		res.Success = runErr == nil
	default: // "repo_diff" / ""
		changed := changedFiles(req.RepoPath)
		res.FilesChanged = changed
		res.Success = len(changed) > 0
	}
	return res, nil
}

// renderArgs substitutes {prompt}/{repo} placeholders into the arg
// template. When the template has no {prompt}, the prompt is appended.
func (e *ConfigEngine) renderArgs(prompt, repo string) []string {
	out := make([]string, 0, len(e.def.Args)+1)
	hasPrompt := false
	for _, a := range e.def.Args {
		if strings.Contains(a, "{prompt}") {
			hasPrompt = true
		}
		a = strings.ReplaceAll(a, "{prompt}", prompt)
		a = strings.ReplaceAll(a, "{repo}", repo)
		out = append(out, a)
	}
	if !hasPrompt {
		out = append(out, prompt)
	}
	return out
}

// changedFiles returns tracked files modified in the repo (best-effort).
func changedFiles(repoPath string) []string {
	if repoPath == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", repoPath, "status", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// porcelain: "XY <path>"
		if idx := strings.LastIndex(line, " "); idx >= 0 {
			files = append(files, line[idx+1:])
		}
	}
	return files
}

// Registry resolves AI fix engines by name. It is seeded with the
// built-in Go engines (claude-code, codex, auto) and can be extended
// with config-driven CLI tool definitions.
type Registry struct {
	engines map[string]SubprocessEngine
}

// NewRegistry returns a Registry seeded with the built-in engines.
func NewRegistry() *Registry {
	r := &Registry{engines: map[string]SubprocessEngine{}}
	r.Register(&ClaudeCode{})
	r.Register(&Codex{})
	r.Register(&OpenCode{})
	r.Register(NewAutoEngine())
	return r
}

// Register adds (or replaces) an engine under its Name().
func (r *Registry) Register(e SubprocessEngine) {
	r.engines[e.Name()] = e
}

// RegisterToolDefinitions adds config-driven CLI tools to the registry.
// A definition whose name collides with a built-in overrides it.
func (r *Registry) RegisterToolDefinitions(defs []ToolDefinition) {
	for _, d := range defs {
		r.Register(NewConfigEngine(d))
	}
}

// Resolve returns the engine registered under name. The empty name and
// "auto" both resolve to the auto engine.
func (r *Registry) Resolve(name string) (SubprocessEngine, error) {
	if name == "" {
		name = "auto"
	}
	// "custom:<cmd>" syntax is still honored for ad-hoc tools.
	if strings.HasPrefix(name, "custom:") {
		return parseCustomEngine(name)
	}
	e, ok := r.engines[name]
	if !ok {
		return nil, fmt.Errorf("unknown AI tool %q (known: %s)", name, strings.Join(r.Names(), ", "))
	}
	return e, nil
}

// Names returns the registered engine names, sorted.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.engines))
	for n := range r.engines {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
