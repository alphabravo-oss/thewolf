package container

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// CommandFactory lets an alternate runtime preserve the plugins' existing
// exec.Cmd contract. Kubernetes installs a factory that returns a local hidden
// wolf command; that command creates/watches a native scanner Job and mirrors
// its stdout, stderr, and exit code.
type CommandFactory func(context.Context, *Config, Options, string, ...string) *exec.Cmd

var (
	commandFactoryMu sync.RWMutex
	commandFactory   CommandFactory
)

func SetCommandFactory(factory CommandFactory) {
	commandFactoryMu.Lock()
	defer commandFactoryMu.Unlock()
	commandFactory = factory
}

func alternateCommandFactory() CommandFactory {
	commandFactoryMu.RLock()
	defer commandFactoryMu.RUnlock()
	return commandFactory
}

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

	// EntrypointOverride, when non-empty, is passed to docker as
	// --entrypoint <name>. This is for plugins that need to run a shell
	// wrapper (`sh -c "tool ... && cat result"`) against an image whose
	// declared entrypoint would otherwise execute the tool directly.
	//
	// This is separate from ToolImageSpec.Entrypoint (which applies to
	// upstream-image routing); EntrypointOverride is per-invocation
	// regardless of which image tier the tool lives in.
	EntrypointOverride string

	// MemoryOverride, when non-empty, replaces cfg.Memory for this
	// invocation. Use for tools with hefty in-memory data structures
	// (grype loads its full vulnerability DB into RAM, bearer parses
	// the whole repo AST) that hit the default 2g cap. Docker units
	// apply: "4g", "512m", etc.
	MemoryOverride string
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
		return scannerFailureCommand(ctx, "scanner container backend is not configured")
	}
	image := cfg.ImageFor(tool)
	if image == "" {
		return scannerFailureCommand(ctx, fmt.Sprintf("scanner image for tool %q is empty", tool))
	}
	if factory := alternateCommandFactory(); factory != nil {
		return factory(ctx, cfg, opts, tool, args...)
	}
	if !scannerImageReady(ctx, cfg, image) {
		if IsLocalOnlyImage(image) {
			return scannerFailureCommand(ctx, fmt.Sprintf(
				"scanner image for tool %q is local-build-only by license and not present: %s (build it locally with `make scanners-build-codeql`; it is never pulled from a registry)",
				tool, image,
			))
		}
		return scannerFailureCommand(ctx, fmt.Sprintf(
			"scanner image for tool %q is not present locally: %s (pull it with `docker pull %s` or `wolf scanner pull-image %s`)",
			tool, image, image, image,
		))
	}

	id := atomic.AddUint64(&containerCounter, 1)
	name := fmt.Sprintf("wolf-scan-%s-%d-%d", sanitizeName(tool), time.Now().UnixNano(), id)

	dockerArgs := []string{
		"run",
		"--rm",
		"--pull", "never",
		"--name", name,
		"--user", fmt.Sprintf("%d:%d", cfg.UID, cfg.GID),
		"--read-only",
		// 4 GiB headroom: trivy + grype each unpack ~1GB vulnerability
		// DBs and grype additionally needs scratch space to copy the
		// downloaded archive to its cache location atomically. Tmpfs
		// only commits pages on write, so this doesn't pin 4GB of
		// physical memory up front.
		"--tmpfs", "/tmp:rw,size=4g,mode=1777",
	}

	if !opts.NoRepoMount {
		if cfg.RepoVolume != "" {
			mountFlag := fmt.Sprintf("%s:%s:ro", cfg.RepoVolume, ScanMountPoint)
			if opts.ReadWrite {
				mountFlag = fmt.Sprintf("%s:%s", cfg.RepoVolume, ScanMountPoint)
			}
			dockerArgs = append(dockerArgs, "-v", mountFlag)
		} else if opts.RepoDir == "" {
			// Returning a failing command keeps the type signature simple and
			// lets the runner's normal error pathway surface this to the user.
			return scannerFailureCommand(ctx, "scanner repo mount path is empty")
		} else {
			hostPath, err := cfg.TranslateRepoPath(opts.RepoDir)
			if err != nil {
				return scannerFailureCommand(ctx, err.Error())
			}
			mountFlag := fmt.Sprintf("%s:%s:ro", hostPath, ScanMountPoint)
			if opts.ReadWrite {
				mountFlag = fmt.Sprintf("%s:%s", hostPath, ScanMountPoint)
			}
			dockerArgs = append(dockerArgs, "-v", mountFlag)
		}
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

	// Resource limits. MemoryOverride wins over cfg.Memory when set.
	memLimit := cfg.Memory
	if opts.MemoryOverride != "" {
		memLimit = opts.MemoryOverride
	}
	if memLimit != "" {
		dockerArgs = append(dockerArgs, "--memory", memLimit)
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
		if err := validateExtraMount(m); err != nil {
			return scannerFailureCommand(ctx, err.Error())
		}
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
	if err := validateScannerEnvironment(envKeys); err != nil {
		return scannerFailureCommand(ctx, err.Error())
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

	// Per-invocation entrypoint override (e.g. plugin wraps with `sh -c`).
	if opts.EntrypointOverride != "" {
		dockerArgs = append(dockerArgs, "--entrypoint", opts.EntrypointOverride)
	}

	// Upstream-image path: drop the tool name from the argv (the image's
	// entrypoint, or our explicit override, IS the tool).
	if spec, ok := cfg.UpstreamSpec(tool); ok {
		// Spec-level entrypoint applies only when no per-invocation override
		// has already been set.
		if spec.Entrypoint != "" && opts.EntrypointOverride == "" {
			dockerArgs = append(dockerArgs, "--entrypoint", spec.Entrypoint)
		}
		dockerArgs = append(dockerArgs, spec.Image)
		dockerArgs = append(dockerArgs, args...)
	} else if opts.EntrypointOverride != "" {
		// Non-upstream image WITH entrypoint override (e.g. plugins that
		// wrap their invocation in `sh -c "..."`). Skip the tool-name
		// dispatcher arg — the override IS the entrypoint and the args
		// go straight to it. Without this branch the docker invocation
		// becomes `<entrypoint> <tool> <args...>`, so plugins setting
		// EntrypointOverride="sh" end up with `sh sh -c "..."` which
		// fails with `sh: 0: cannot open sh: No such file`.
		dockerArgs = append(dockerArgs, image)
		dockerArgs = append(dockerArgs, args...)
	} else {
		dockerArgs = append(dockerArgs, image, tool)
		dockerArgs = append(dockerArgs, args...)
	}

	// #nosec G204 -- command is a configured tool name (docker / claude / codex / scanner binary); args sourced from internal config, not user input
	cmd := dockerCommandContext(ctx, cfg, dockerArgs...)
	if cfg.OnContainerScheduled != nil {
		cfg.OnContainerScheduled(name)
	}

	if opts.Stdin != "" {
		cmd.Stdin = strings.NewReader(opts.Stdin)
	}

	// Cancel hits the running container directly. `docker kill` is the only
	// signal that stops the tool *inside* the container (SIGKILL on the docker
	// CLI process only orphans the container otherwise — see docs/PLAN-containerized-scanner-execution.md §5.7).
	cmd.Cancel = func() error {
		// #nosec G204 -- command is a configured tool name (docker / claude / codex / scanner binary); args sourced from internal config, not user input
		kill := exec.Command(cfg.dockerPath(), "kill", name) // #nosec G204 -- deployment-owned immutable Docker CLI.
		if len(cfg.DockerEnvironment) != 0 {
			kill.Env = append([]string(nil), cfg.DockerEnvironment...)
		}
		_ = kill.Run() // best-effort
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

func scannerImageReady(ctx context.Context, cfg *Config, image string) bool {
	if cfg != nil && image == cfg.Image && ImageReady(cfg) {
		return true
	}
	return imageInspectWithConfig(ctx, cfg, image) == nil
}

func scannerFailureCommand(ctx context.Context, msg string) *exec.Cmd {
	return exec.CommandContext(ctx, "sh", "-c", "printf '%s\n' \"$1\" >&2; exit 127", "wolf-scanner-error", msg)
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
func BuildDockerArgs(cfg *Config, opts Options, tool string, args ...string) (containerName string, dockerArgs []string, err error) {
	id := atomic.AddUint64(&containerCounter, 1)
	containerName = fmt.Sprintf("wolf-scan-%s-%d-%d", sanitizeName(tool), time.Now().UnixNano(), id)

	dockerArgs = []string{
		"run", "--rm", "--pull", "never", "--name", containerName,
		"--user", fmt.Sprintf("%d:%d", cfg.UID, cfg.GID),
		"--read-only",
		// 4 GiB headroom: trivy + grype each unpack ~1GB vulnerability
		// DBs and grype additionally needs scratch space to copy the
		// downloaded archive to its cache location atomically. Tmpfs
		// only commits pages on write, so this doesn't pin 4GB of
		// physical memory up front.
		"--tmpfs", "/tmp:rw,size=4g,mode=1777",
	}

	if !opts.NoRepoMount {
		mountFlag := ""
		if cfg.RepoVolume != "" {
			mountFlag = fmt.Sprintf("%s:%s:ro", cfg.RepoVolume, ScanMountPoint)
		} else {
			hostPath, err := cfg.TranslateRepoPath(opts.RepoDir)
			if err != nil {
				return containerName, nil, err
			}
			mountFlag = fmt.Sprintf("%s:%s:ro", hostPath, ScanMountPoint)
		}
		if opts.ReadWrite {
			mountFlag = strings.TrimSuffix(mountFlag, ":ro")
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

	memLimit := cfg.Memory
	if opts.MemoryOverride != "" {
		memLimit = opts.MemoryOverride
	}
	if memLimit != "" {
		dockerArgs = append(dockerArgs, "--memory", memLimit)
	}
	if cfg.CPUs != "" {
		dockerArgs = append(dockerArgs, "--cpus", cfg.CPUs)
	}
	if cfg.DBVolume != "" {
		dockerArgs = append(dockerArgs, "-v", fmt.Sprintf("%s:/var/lib/wolf-db", cfg.DBVolume))
	}
	for _, m := range opts.ExtraMounts {
		if err := validateExtraMount(m); err != nil {
			return containerName, nil, err
		}
		dockerArgs = append(dockerArgs, "-v", m)
	}

	envKeys := make(map[string]string, len(cfg.ExtraEnv)+len(opts.ExtraEnv))
	for k, v := range cfg.ExtraEnv {
		envKeys[k] = v
	}
	for k, v := range opts.ExtraEnv {
		envKeys[k] = v
	}
	if err := validateScannerEnvironment(envKeys); err != nil {
		return containerName, nil, err
	}
	keys := make([]string, 0, len(envKeys))
	for k := range envKeys {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		dockerArgs = append(dockerArgs, "-e", fmt.Sprintf("%s=%s", k, envKeys[k]))
	}

	if opts.EntrypointOverride != "" {
		dockerArgs = append(dockerArgs, "--entrypoint", opts.EntrypointOverride)
	}
	if spec, ok := cfg.UpstreamSpec(tool); ok {
		if spec.Entrypoint != "" && opts.EntrypointOverride == "" {
			dockerArgs = append(dockerArgs, "--entrypoint", spec.Entrypoint)
		}
		dockerArgs = append(dockerArgs, spec.Image)
		dockerArgs = append(dockerArgs, args...)
	} else if opts.EntrypointOverride != "" {
		// Mirror CommandContext: with an entrypoint override on a
		// non-upstream image, skip the tool-name dispatcher arg.
		dockerArgs = append(dockerArgs, cfg.ImageFor(tool))
		dockerArgs = append(dockerArgs, args...)
	} else {
		dockerArgs = append(dockerArgs, cfg.ImageFor(tool), tool)
		dockerArgs = append(dockerArgs, args...)
	}
	return containerName, dockerArgs, nil
}

func validateScannerEnvironment(values map[string]string) error {
	for name, value := range values {
		if name == "" || strings.ContainsAny(name, "=\x00") || strings.ContainsRune(value, '\x00') {
			return errors.New("scanner container environment contains an invalid name or value")
		}
		switch name {
		case "DOCKER_HOST", "DOCKER_CONFIG", "DOCKER_CERT_PATH", "DOCKER_TLS_VERIFY",
			"WOLF_SCANNER_RELEASE_REGISTRY_CREDENTIAL_DIR", "WOLF_SCANNER_RELEASE_ENGINE_CREDENTIAL_DIR":
			return fmt.Errorf("scanner container environment must not receive release credential variable %q", name)
		}
	}
	return nil
}

func validateExtraMount(m string) error {
	parts := strings.Split(m, ":")
	if len(parts) < 2 {
		return fmt.Errorf("invalid extra mount %q", m)
	}
	hostPath := parts[0]
	containerPath := parts[1]
	if isDockerSocketPath(hostPath) || isDockerSocketPath(containerPath) {
		return fmt.Errorf("refusing to mount Docker socket into scanner container")
	}
	return nil
}

func isDockerSocketPath(p string) bool {
	clean := strings.TrimSpace(p)
	return clean == "/var/run/docker.sock" || strings.HasSuffix(clean, "/docker.sock")
}
