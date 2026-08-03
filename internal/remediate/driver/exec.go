package driver

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/alphabravocompany/thewolf/internal/remediate/meter"
	"github.com/alphabravocompany/thewolf/internal/remediate/permission"
	"github.com/alphabravocompany/thewolf/internal/remediate/plan"
)

// ExecConfig configures the containerized OpenCode invocation.
type ExecConfig struct {
	// Image is the wolf-fixer-opencode image reference.
	Image string
	// Binary is the container runtime ("docker" by default).
	Binary   string
	Provider string
	Model    string
}

type execDriver struct{ cfg ExecConfig }

// NewExec returns a Driver that runs OpenCode in a container.
func NewExec(cfg ExecConfig) Driver {
	if cfg.Binary == "" {
		cfg.Binary = "docker"
	}
	return &execDriver{cfg: cfg}
}

// buildInvocation returns argv and env for a run. Credentials go in env only:
// argv is visible in `ps` inside the container.
func (d *execDriver) buildInvocation(req ExecuteRequest, configPath, prompt string) ([]string, []string) {
	args := []string{
		"run", "--rm",
		"-v", req.WorktreePath + ":/workspace",
		"-v", filepath.Dir(configPath) + ":/config:ro",
		"-e", "OPENCODE_AUTH_CONTENT",
		// OPENCODE_CONFIG names the config FILE. OPENCODE_CONFIG_DIR is
		// the directory for agents/commands/plugins and does NOT load
		// opencode.json — setting it instead means the golden-tested
		// permission document is silently never applied and the agent
		// runs with defaults. Confirmed against 1.18.11 in the spike.
		"-e", "OPENCODE_CONFIG=/config/opencode.json",
		// NOT --network none: an opencode run must reach its provider
		// API, so a fully isolated container cannot start a session at
		// all. Egress is restricted to the provider endpoint instead —
		// see the spec's egress section.
		// The image keeps the repo-wide fixer entrypoint
		// (`wolf fixer`) so the release path's smoke and
		// qualification steps, which append their own arguments with
		// no override, still work. Reaching the CLI is this driver's
		// job, not the image's.
		"--entrypoint", "/usr/local/bin/opencode",
		d.cfg.Image,
		"run", "--format", "json", "--auto",
	}
	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}
	args = append(args, prompt)

	env := append(os.Environ(), "OPENCODE_AUTH_CONTENT="+req.AuthContent)
	return args, env
}

// stream runs the command and feeds each decoded event to the meter, killing
// the process the moment the budget is spent.
func (d *execDriver) stream(ctx context.Context, args, env []string, m meter.Meter, onEvent func(meter.Event)) ([]byte, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// #nosec G204 -- binary is fixed config; args are internal.
	cmd := exec.CommandContext(ctx, d.cfg.Binary, args...)
	cmd.Env = env
	// Stdin MUST be /dev/null. `opencode run` hangs indefinitely on an
	// inherited stdin — reproduced at 120s, 150s and 240s with zero bytes of
	// output and no error. A nil Stdin gives the child /dev/null. Without
	// this, every run hangs until the wall-clock timeout and the turn budget
	// never gets a chance to apply.
	cmd.Stdin = nil
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	var last []byte
	exhausted := false
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		last = line
		var e meter.Event
		if err := json.Unmarshal(line, &e); err != nil {
			continue // non-event output; ignore
		}
		if onEvent != nil {
			onEvent(e)
		}
		if m.Observe(e) {
			exhausted = true
			cancel()
			break
		}
	}
	_ = cmd.Wait()
	if exhausted {
		return last, ErrBudgetExhausted
	}
	return last, nil
}

func (d *execDriver) Plan(ctx context.Context, req PlanRequest) (*plan.Plan, meter.Usage, error) {
	doc, err := permission.Triage()
	if err != nil {
		return nil, meter.Usage{}, err
	}
	configPath, cleanup, err := writeConfig(doc)
	if err != nil {
		return nil, meter.Usage{}, err
	}
	defer cleanup()

	m := meter.NewTurns(req.MaxTurns)
	args, env := d.buildInvocation(ExecuteRequest{
		WorktreePath: req.WorktreePath, AuthContent: req.AuthContent,
		Provider: req.Provider, Model: req.Model,
	}, configPath, triagePrompt(req.Findings))

	last, err := d.stream(ctx, args, env, m, req.OnEvent)
	if err != nil {
		return nil, m.Usage(), err
	}
	p, err := plan.Parse(last)
	if err != nil {
		return nil, m.Usage(), fmt.Errorf("parse plan: %w", err)
	}
	return p, m.Usage(), nil
}

func (d *execDriver) Execute(ctx context.Context, req ExecuteRequest) (*PatchSeries, meter.Usage, error) {
	doc, err := permission.Execute()
	if err != nil {
		return nil, meter.Usage{}, err
	}
	configPath, cleanup, err := writeConfig(doc)
	if err != nil {
		return nil, meter.Usage{}, err
	}
	defer cleanup()

	m := meter.NewTurns(req.MaxTurns)
	args, env := d.buildInvocation(req, configPath, executePrompt(req.Plan, req.Findings))
	if _, err := d.stream(ctx, args, env, m, req.OnEvent); err != nil {
		return nil, m.Usage(), err
	}
	series, err := collectPatches(ctx, req.WorktreePath)
	if err != nil {
		return nil, m.Usage(), err
	}
	return series, m.Usage(), nil
}

func writeConfig(doc []byte) (string, func(), error) {
	dir, err := os.MkdirTemp("", "wolf-opencode-cfg-")
	if err != nil {
		return "", nil, err
	}
	path := filepath.Join(dir, "opencode.json")
	if err := os.WriteFile(path, doc, 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return "", nil, err
	}
	return path, func() { _ = os.RemoveAll(dir) }, nil
}
