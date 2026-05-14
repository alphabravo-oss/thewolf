package container

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

// Options controls per-invocation container behavior. Most plugins pass only
// RepoDir; gosec/codeql/infer override WorkDir to run from a specific
// subdirectory; nuclei sets NoRepoMount because DAST scans don't read a repo.
type Options struct {
	// RepoDir is the wolf-slim-visible path to the repo to scan. The shim
	// translates this to the host-visible path (via cfg.HostReposRoot) before
	// passing to docker's -v flag. REQUIRED unless NoRepoMount is true.
	RepoDir string

	// WorkDir is the absolute container path to set as the working directory.
	// Defaults to /scan when empty. Used by gosec, infer, codeql — tools that
	// must run from a specific subdirectory of the repo.
	WorkDir string

	// NoRepoMount disables the /scan bind-mount. Set this for tools that
	// don't read the repo (nuclei DAST, syft of a remote image, etc.).
	NoRepoMount bool

	// ReadWrite, when true, makes the /scan mount writable. The default is
	// read-only. Almost no scanner needs this; the exception is codeql which
	// writes a database into the scan tree if you let it (we route it to
	// /tmp instead via --codeql-cache-dir).
	ReadWrite bool

	// ExtraMounts are appended to the `-v` flag list. Use for tool-specific
	// caches (e.g. semgrep's rule cache). Format follows docker -v syntax.
	ExtraMounts []string

	// ExtraEnv merges with cfg.ExtraEnv for this single invocation.
	ExtraEnv map[string]string

	// Stdin, if non-empty, is set as the cmd's Stdin. Currently unused by
	// any plugin but reserved for future tools (e.g. piping a list of files).
	Stdin string
}

// containerCounter is monotonic; used to disambiguate container names within
// the same Unix nanosecond (rare, but possible under high concurrency).
var containerCounter uint64

// CommandContext returns an *exec.Cmd whose Run/Output executes the named tool
// inside cfg.Image. The returned cmd:
//
//   - Forwards container stdout to its Stdout (so cmd.Output() works as before).
//   - Forwards container stderr to its Stderr (so cmd.StderrPipe() works).
//   - On context cancel, runs `docker kill <name>` first, then SIGTERMs the
//     local docker CLI process — kills the tool *inside* the container.
//
// Plugins migrate from:
//
//	cmd := plugin.CommandContext(ctx, "bandit", "-r", repo, "-f", "json")
//
// to:
//
//	cmd := container.CommandContext(ctx, cfg, container.Options{RepoDir: repo},
//	    "bandit", "-r", "/scan", "-f", "json")
//
// Note that the tool's arguments must now reference container paths
// (/scan, /out) rather than host paths.
func CommandContext(ctx context.Context, cfg *Config, opts Options, tool string, args ...string) *exec.Cmd {
	if cfg == nil || cfg.Disabled {
		// Returning a guaranteed-failure command lets callers surface a clean
		// error instead of nil-deref. Operators should never hit this path.
		cmd := exec.CommandContext(ctx, "false")
		return cmd
	}

	id := atomic.AddUint64(&containerCounter, 1)
	name := fmt.Sprintf("wolf-scan-%s-%d-%d", sanitizeName(tool), time.Now().UnixNano(), id)

	dockerArgs := []string{
		"run",
		"--rm",
		"--name", name,
		"--user", fmt.Sprintf("%d:%d", cfg.UID, cfg.GID),
		"--read-only",
		"--tmpfs", "/tmp:rw,size=512m,mode=1777",
	}

	if !opts.NoRepoMount {
		if opts.RepoDir == "" {
			// Returning a failing command keeps the type signature simple and
			// lets the runner's normal error pathway surface this to the user.
			cmd := exec.CommandContext(ctx, "false")
			return cmd
		}
		hostPath := cfg.TranslateRepoPath(opts.RepoDir)
		mountFlag := fmt.Sprintf("%s:%s:ro", hostPath, ScanMountPoint)
		if opts.ReadWrite {
			mountFlag = fmt.Sprintf("%s:%s", hostPath, ScanMountPoint)
		}
		dockerArgs = append(dockerArgs, "-v", mountFlag)
	}

	workdir := opts.WorkDir
	if workdir == "" {
		if opts.NoRepoMount {
			workdir = "/tmp"
		} else {
			workdir = ScanMountPoint
		}
	}
	dockerArgs = append(dockerArgs, "--workdir", workdir)

	// Network policy.
	dockerArgs = append(dockerArgs, "--network", cfg.Network)

	// Resource limits.
	if cfg.Memory != "" {
		dockerArgs = append(dockerArgs, "--memory", cfg.Memory)
	}
	if cfg.CPUs != "" {
		dockerArgs = append(dockerArgs, "--cpus", cfg.CPUs)
	}

	// Vuln-DB shared volume.
	if cfg.DBVolume != "" {
		dockerArgs = append(dockerArgs, "-v", fmt.Sprintf("%s:/var/lib/wolf-db", cfg.DBVolume))
	}

	// Extra mounts.
	for _, m := range opts.ExtraMounts {
		dockerArgs = append(dockerArgs, "-v", m)
	}

	// Env: merge cfg.ExtraEnv and opts.ExtraEnv, opts wins on conflict.
	envKeys := make(map[string]string, len(cfg.ExtraEnv)+len(opts.ExtraEnv))
	for k, v := range cfg.ExtraEnv {
		envKeys[k] = v
	}
	for k, v := range opts.ExtraEnv {
		envKeys[k] = v
	}
	// Sort for deterministic arg ordering — keeps tests stable.
	keys := make([]string, 0, len(envKeys))
	for k := range envKeys {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		dockerArgs = append(dockerArgs, "-e", fmt.Sprintf("%s=%s", k, envKeys[k]))
	}

	// Upstream-image path: --entrypoint override + drop the tool name from
	// the argv (the image's entrypoint, or our explicit override, IS the tool).
	if spec, ok := cfg.UpstreamSpec(tool); ok {
		if spec.Entrypoint != "" {
			// Splice --entrypoint <name> before the image ref (docker requires
			// --entrypoint to be a flag of `docker run`, not a positional arg).
			dockerArgs = append(dockerArgs, "--entrypoint", spec.Entrypoint)
		}
		dockerArgs = append(dockerArgs, spec.Image)
		dockerArgs = append(dockerArgs, args...)
	} else {
		dockerArgs = append(dockerArgs, cfg.ImageFor(tool), tool)
		dockerArgs = append(dockerArgs, args...)
	}

	cmd := exec.CommandContext(ctx, "docker", dockerArgs...)

	if opts.Stdin != "" {
		cmd.Stdin = strings.NewReader(opts.Stdin)
	}

	// Cancel hits the running container directly. `docker kill` is the only
	// signal that stops the tool *inside* the container (SIGKILL on the docker
	// CLI process only orphans the container otherwise — see PLAN.md §5.7).
	cmd.Cancel = func() error {
		_ = exec.Command("docker", "kill", name).Run() // best-effort
		if cmd.Process != nil {
			return syscall.Kill(cmd.Process.Pid, syscall.SIGTERM)
		}
		return nil
	}
	// Give the container 5s to die after we issue docker kill before we
	// abandon waiting on the CLI process.
	cmd.WaitDelay = 5 * time.Second

	return cmd
}

// sanitizeName replaces characters disallowed in docker container names so
// that tool names like "pip-audit" yield valid names.
//
// Docker name regex: [a-zA-Z0-9][a-zA-Z0-9_.-]+
func sanitizeName(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '_' || r == '.' || r == '-':
			if i == 0 {
				b.WriteByte('x')
			}
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "tool"
	}
	return b.String()
}

// BuildDockerArgs is exported for tests that want to verify arg construction
// without running docker. It returns the exact []string that CommandContext
// would pass to exec.CommandContext, minus the leading "docker".
//
// The returned name is the unique --name value used; tests can ignore it.
func BuildDockerArgs(cfg *Config, opts Options, tool string, args ...string) (containerName string, dockerArgs []string) {
	id := atomic.AddUint64(&containerCounter, 1)
	containerName = fmt.Sprintf("wolf-scan-%s-%d-%d", sanitizeName(tool), time.Now().UnixNano(), id)

	dockerArgs = []string{
		"run", "--rm", "--name", containerName,
		"--user", fmt.Sprintf("%d:%d", cfg.UID, cfg.GID),
		"--read-only",
		"--tmpfs", "/tmp:rw,size=512m,mode=1777",
	}

	if !opts.NoRepoMount {
		hostPath := cfg.TranslateRepoPath(opts.RepoDir)
		mountFlag := fmt.Sprintf("%s:%s:ro", hostPath, ScanMountPoint)
		if opts.ReadWrite {
			mountFlag = fmt.Sprintf("%s:%s", hostPath, ScanMountPoint)
		}
		dockerArgs = append(dockerArgs, "-v", mountFlag)
	}

	workdir := opts.WorkDir
	if workdir == "" {
		if opts.NoRepoMount {
			workdir = "/tmp"
		} else {
			workdir = ScanMountPoint
		}
	}
	dockerArgs = append(dockerArgs, "--workdir", workdir, "--network", cfg.Network)

	if cfg.Memory != "" {
		dockerArgs = append(dockerArgs, "--memory", cfg.Memory)
	}
	if cfg.CPUs != "" {
		dockerArgs = append(dockerArgs, "--cpus", cfg.CPUs)
	}
	if cfg.DBVolume != "" {
		dockerArgs = append(dockerArgs, "-v", fmt.Sprintf("%s:/var/lib/wolf-db", cfg.DBVolume))
	}
	for _, m := range opts.ExtraMounts {
		dockerArgs = append(dockerArgs, "-v", m)
	}

	envKeys := make(map[string]string, len(cfg.ExtraEnv)+len(opts.ExtraEnv))
	for k, v := range cfg.ExtraEnv {
		envKeys[k] = v
	}
	for k, v := range opts.ExtraEnv {
		envKeys[k] = v
	}
	keys := make([]string, 0, len(envKeys))
	for k := range envKeys {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		dockerArgs = append(dockerArgs, "-e", fmt.Sprintf("%s=%s", k, envKeys[k]))
	}

	if spec, ok := cfg.UpstreamSpec(tool); ok {
		if spec.Entrypoint != "" {
			dockerArgs = append(dockerArgs, "--entrypoint", spec.Entrypoint)
		}
		dockerArgs = append(dockerArgs, spec.Image)
		dockerArgs = append(dockerArgs, args...)
	} else {
		dockerArgs = append(dockerArgs, cfg.ImageFor(tool), tool)
		dockerArgs = append(dockerArgs, args...)
	}
	return containerName, dockerArgs
}
