package driver

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
	"sync/atomic"
	"time"

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
	// UID and GID are the host uid:gid passed via --user, so files the
	// agent creates in the bind-mounted worktree are owned by whatever
	// created it on the host. The image's own baked-in "wolf" user (uid
	// 1000) does not own a host-created worktree in general, and git
	// refuses most operations — including reads — on a directory it does
	// not own ("detected dubious ownership"); see buildInvocation.
	// Resolved via os.Getuid()/os.Getgid() when both are zero, mirroring
	// internal/plugin/container.DefaultConfig's UID/GID.
	UID int
	GID int
}

type execDriver struct{ cfg ExecConfig }

// NewExec returns a Driver that runs OpenCode in a container.
func NewExec(cfg ExecConfig) Driver {
	if cfg.Binary == "" {
		cfg.Binary = "docker"
	}
	if cfg.UID == 0 && cfg.GID == 0 {
		cfg.UID, cfg.GID = os.Getuid(), os.Getgid()
	}
	return &execDriver{cfg: cfg}
}

// containerCounter disambiguates two runs started in the same nanosecond.
// Mirrors internal/plugin/container/runner.go's BuildDockerArgs convention
// for generating a unique, killable --name value.
var containerCounter uint64

func nextContainerName() string {
	id := atomic.AddUint64(&containerCounter, 1)
	return fmt.Sprintf("wolf-remediate-%d-%d", time.Now().UnixNano(), id)
}

// gitCommonDirMount returns the extra bind mount a run needs when
// WorktreePath was created via `git worktree add` (internal/fix/workspace's
// local-repo path): its `.git` is a FILE containing `gitdir: <path>`, an
// ABSOLUTE path into the origin repo's own checkout — outside the
// `/workspace` mount. Without this mount, every git command inside the
// container fails to find its object store and refs. A plain clone's `.git`
// is a directory, and nothing extra is needed.
//
// The mount target is the exact host path the gitdir file names, so the
// worktree's own relative "commondir" pointer (normally "../..") resolves
// inside the container exactly as it does on the host. It must be
// read-write: `git commit` in a worktree updates branch refs and writes
// objects in the COMMON git dir, not just the worktree-local HEAD/index.
//
// A worktreePath with no .git at all (e.g. an unprepared test fixture) is
// not an error — it just means there is nothing to add.
func gitCommonDirMount(worktreePath string) ([]string, error) {
	gitPath := filepath.Join(worktreePath, ".git")
	info, err := os.Lstat(gitPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat %s: %w", gitPath, err)
	}
	if info.IsDir() {
		return nil, nil // plain clone; nothing to do
	}
	raw, err := os.ReadFile(gitPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", gitPath, err)
	}
	const prefix = "gitdir: "
	line := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(line, prefix) {
		return nil, fmt.Errorf("%s does not look like a worktree pointer: %q", gitPath, line)
	}
	worktreeGitDir := strings.TrimPrefix(line, prefix)
	if !filepath.IsAbs(worktreeGitDir) {
		return nil, fmt.Errorf("%s: unexpected relative gitdir %q", gitPath, worktreeGitDir)
	}
	// worktreeGitDir is <mainGitDir>/worktrees/<name>; mount the whole
	// common git dir (its grandparent) so worktreeGitDir's own relative
	// "commondir" (../..) resolves once inside the container.
	mainGitDir := filepath.Dir(filepath.Dir(worktreeGitDir))
	return []string{"-v", mainGitDir + ":" + mainGitDir}, nil
}

// buildInvocation returns argv, env, and the container's --name for a run.
// Credentials go in env only: argv is visible in `ps` inside the container.
func (d *execDriver) buildInvocation(req ExecuteRequest, configPath, prompt string) (args []string, env []string, containerName string, err error) {
	gitMount, err := gitCommonDirMount(req.WorktreePath)
	if err != nil {
		return nil, nil, "", err
	}

	containerName = nextContainerName()
	args = []string{
		"run", "--rm", "--name", containerName,
		// Matches the uid:gid that created the worktree on the host (see
		// ExecConfig.UID/GID) — the image's own baked-in "wolf" (uid 1000)
		// does not own the bind-mounted worktree in general, and git
		// refuses to operate on a directory it doesn't own.
		"--user", fmt.Sprintf("%d:%d", d.cfg.UID, d.cfg.GID),
		// The image's baked-in $HOME (/home/wolf) is owned by uid 1000 and
		// unwritable by the arbitrary --user above. A dedicated tmpfs HOME,
		// owned by that same uid and mode 0700, is tighter than pointing
		// HOME at the world-writable /tmp this image already tmpfs-mounts
		// for scanners (internal/plugin/container/runner.go's
		// --tmpfs /tmp:...,mode=1777 convention) — nothing else in the
		// container can read whatever OpenCode or git write there.
		"-e", "HOME=/home/agent",
		"--tmpfs", fmt.Sprintf("/home/agent:rw,mode=0700,uid=%d,gid=%d", d.cfg.UID, d.cfg.GID),
		"-v", req.WorktreePath + ":/workspace",
		"-v", filepath.Dir(configPath) + ":/config:ro",
	}
	args = append(args, gitMount...)
	args = append(args,
		"-e", "OPENCODE_AUTH_CONTENT",
		// OPENCODE_CONFIG names the config FILE. OPENCODE_CONFIG_DIR is
		// the directory for agents/commands/plugins and does NOT load
		// opencode.json — setting it instead means the golden-tested
		// permission document is silently never applied and the agent
		// runs with defaults. Confirmed against 1.18.11 in the spike.
		"-e", "OPENCODE_CONFIG=/config/opencode.json",
		// Git needs an identity to commit, and there is no /etc/passwd
		// entry for the arbitrary --user above for it to auto-detect one
		// from. GIT_CONFIG_COUNT/KEY_n/VALUE_n injects safe.directory
		// without a wrapper script: the bind-mounted worktree (and, for a
		// `git worktree add` checkout, the common git dir mounted above)
		// is owned by the host uid, which git's ownership check would
		// otherwise refuse to operate on — for reads as well as writes.
		"-e", "GIT_AUTHOR_NAME=Wolf Remediation Agent",
		"-e", "GIT_AUTHOR_EMAIL=remediation@invalid",
		"-e", "GIT_COMMITTER_NAME=Wolf Remediation Agent",
		"-e", "GIT_COMMITTER_EMAIL=remediation@invalid",
		"-e", "GIT_CONFIG_COUNT=1",
		"-e", "GIT_CONFIG_KEY_0=safe.directory",
		"-e", "GIT_CONFIG_VALUE_0=*",
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
	)
	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}
	args = append(args, prompt)

	env = append(os.Environ(), "OPENCODE_AUTH_CONTENT="+req.AuthContent)
	return args, env, containerName, nil
}

// textEvent decodes only the field meter.Event does not carry: the agent's
// own prose. meter.Event itself is not extended with it — other code
// depends on its exact shape (see internal/remediate/meter) — so this is a
// package-local, driver-only superset decoded purely to recover plan text.
type textEvent struct {
	Part struct {
		Text string `json:"text"`
	} `json:"part"`
}

// killContainer force-stops a run's container by name after budget
// exhaustion. Canceling the driver's own context only SIGKILLs the local
// `docker run` CLI client process; SIGKILL cannot be signal-proxied into
// the container it's attached to, so without an explicit kill the agent
// keeps running — still spending tokens and still writing to the
// worktree — after Wolf has stopped watching it.
func killContainer(ctx context.Context, binary, name string) {
	if name == "" {
		return
	}
	// context.WithoutCancel: ctx may be canceled moments after this call
	// returns (stream cancels its own derived context right after), and the
	// kill itself must not inherit that.
	killCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	// #nosec G204 -- binary is fixed config; name is generated by this
	// package, not external input.
	_ = exec.CommandContext(killCtx, binary, "kill", name).Run()
}

// tail returns the last n bytes of b, trimmed — enough of a failing run's
// stderr to show the actual error without unbounded output in a wrapped
// error.
func tail(b []byte, n int) string {
	if len(b) > n {
		b = b[len(b)-n:]
	}
	return strings.TrimSpace(string(b))
}

// lastNonEmptyLine returns the last non-blank line of s, trimmed — the plan
// JSON triagePrompt instructs the agent to print with nothing after it.
func lastNonEmptyLine(s string) []byte {
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return []byte(line)
		}
	}
	return nil
}

// stream runs the command and feeds each decoded event to the meter,
// killing the container the moment the budget is spent. It returns the
// text of the agent's own last "text" event — not the raw last NDJSON
// line, which is normally a step_finish accounting event, never prose (see
// triagePrompt's doc comment for why that distinction matters).
func (d *execDriver) stream(ctx context.Context, args, env []string, containerName string, m meter.Meter, onEvent func(meter.Event)) (string, error) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// #nosec G204 -- binary is fixed config; args are internal.
	cmd := exec.CommandContext(runCtx, d.cfg.Binary, args...)
	cmd.Env = env
	// Stdin MUST be /dev/null. `opencode run` hangs indefinitely on an
	// inherited stdin — reproduced at 120s, 150s and 240s with zero bytes of
	// output and no error. A nil Stdin gives the child /dev/null. Without
	// this, every run hangs until the wall-clock timeout and the turn budget
	// never gets a chance to apply.
	cmd.Stdin = nil
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", err
	}

	var lastText string
	exhausted := false
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		var e meter.Event
		if err := json.Unmarshal(line, &e); err != nil {
			continue // non-event output; ignore
		}
		if onEvent != nil {
			onEvent(e)
		}
		if e.Type == "text" {
			var t textEvent
			if err := json.Unmarshal(line, &t); err == nil {
				lastText = t.Part.Text
			}
		}
		if m.Observe(e) {
			exhausted = true
			killContainer(ctx, d.cfg.Binary, containerName)
			cancel()
			break
		}
	}
	scanErr := scanner.Err()
	waitErr := cmd.Wait()

	if exhausted {
		return lastText, ErrBudgetExhausted
	}
	if waitErr != nil {
		return lastText, fmt.Errorf("opencode run failed: %w: %s", waitErr, tail(stderr.Bytes(), 64*1024))
	}
	if scanErr != nil {
		return lastText, fmt.Errorf("read opencode output: %w", scanErr)
	}
	return lastText, nil
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

	prompt := triagePrompt(req.Findings)
	if req.RepairHint != "" {
		prompt += "\n\n" + req.RepairHint
	}

	m := meter.NewTurns(req.MaxTurns)
	args, env, containerName, err := d.buildInvocation(ExecuteRequest{
		WorktreePath: req.WorktreePath, AuthContent: req.AuthContent,
		Provider: req.Provider, Model: req.Model,
	}, configPath, prompt)
	if err != nil {
		return nil, meter.Usage{}, err
	}

	lastText, err := d.stream(ctx, args, env, containerName, m, req.OnEvent)
	if err != nil {
		return nil, m.Usage(), err
	}
	p, err := plan.Parse(lastNonEmptyLine(lastText))
	if err != nil {
		return nil, m.Usage(), fmt.Errorf("%w: %w", ErrUnparseablePlan, err)
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

	// Captured before the agent runs. collectPatches needs the exact commit
	// the fix branch started from to bound its walk to this run's commits —
	// a reflog-derived fork point breaks under core.logAllRefUpdates=false
	// (reflog show exits 0 with empty output) and under a mid-run
	// `git checkout <sha>` (which the execute permission document's bash
	// allowlist permits), discarding an otherwise-successful, already-paid-
	// for run either way.
	baseOut, err := runGit(ctx, req.WorktreePath, "rev-parse", "HEAD")
	if err != nil {
		return nil, meter.Usage{}, fmt.Errorf("resolve base commit: %w", err)
	}
	base := strings.TrimSpace(baseOut)

	m := meter.NewTurns(req.MaxTurns)
	args, env, containerName, err := d.buildInvocation(req, configPath, executePrompt(req.Plan, req.Findings))
	if err != nil {
		return nil, meter.Usage{}, err
	}

	_, streamErr := d.stream(ctx, args, env, containerName, m, req.OnEvent)
	if streamErr != nil && !errors.Is(streamErr, ErrBudgetExhausted) {
		return nil, m.Usage(), streamErr
	}
	// Exhaustion is the expected way a run stops short: collect whatever the
	// agent already committed rather than discarding an already-paid-for
	// partial result.
	series, collectErr := collectPatches(ctx, req.WorktreePath, base, req.Findings)
	if collectErr != nil {
		if streamErr != nil {
			return nil, m.Usage(), fmt.Errorf("%w (collect patches after exhaustion: %v)", streamErr, collectErr)
		}
		return nil, m.Usage(), collectErr
	}
	// checkClean only runs when the agent finished on its own. Exhaustion
	// kills the container mid-tool-call, so a half-written file is the
	// EXPECTED state on that path — evidence Wolf pulled the plug, not
	// evidence a fix was lost — and must not discard the commits already
	// landed, which is exactly what the exhaustion branch above exists to
	// keep.
	if streamErr == nil {
		if err := checkClean(ctx, req.WorktreePath); err != nil {
			return nil, m.Usage(), err
		}
	}
	return series, m.Usage(), streamErr
}

func writeConfig(doc []byte) (string, func(), error) {
	dir, err := os.MkdirTemp("", "wolf-opencode-cfg-")
	if err != nil {
		return "", nil, err
	}
	// os.MkdirTemp creates the dir at 0700; loosen it alongside the file
	// below (see the comment there for why).
	if err := os.Chmod(dir, 0o755); err != nil {
		_ = os.RemoveAll(dir)
		return "", nil, err
	}
	path := filepath.Join(dir, "opencode.json")
	// 0644, not 0600: this is the permission document Wolf injects, not a
	// secret, and the container runs as --user <hostUID>:<hostGID> (see
	// ExecConfig.UID/GID), which normally matches the uid that wrote this
	// file — but making it world-readable removes that match as a
	// precondition, rather than leaving OPENCODE_CONFIG silently
	// unresolvable (and the agent silently running with default, permissive
	// permissions) on the day it doesn't.
	if err := os.WriteFile(path, doc, 0o644); err != nil {
		_ = os.RemoveAll(dir)
		return "", nil, err
	}
	return path, func() { _ = os.RemoveAll(dir) }, nil
}
