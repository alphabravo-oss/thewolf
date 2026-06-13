// Package airunner launches the writable, networked container the
// auto-remediation loop uses to let an agentic AI CLI edit a repo.
//
// Unlike wolf's locked-down scanner containers, the AI runner mounts the
// repo read-write and keeps network access (the agent needs its LLM API
// and package registries). The container is always run with --rm so it
// is removed when the loop ends.
package airunner

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

// DefaultImage is the runner image built from Dockerfile.ai-runner.
const DefaultImage = "thewolf-ai-runner:latest"

// LaunchSpec describes one AI runner container invocation.
type LaunchSpec struct {
	// Image is the runner image. Defaults to DefaultImage when empty.
	Image string
	// HostRepoPath is the repo path on the docker daemon's filesystem.
	// It is bind-mounted read-write at /repo inside the container.
	HostRepoPath string
	// Command is the shell command to run inside the container — the
	// resolved AI tool invocation.
	Command string
	// Env is extra environment (API keys, etc.) injected into the
	// container.
	Env map[string]string
	// Network is the docker network mode. Defaults to "bridge" (the
	// agent needs egress); set "none" to harden once egress allowlisting
	// lands.
	Network string
	// Memory is an optional docker --memory limit (e.g. "4g").
	Memory string
	// Name is an optional container name.
	Name string
}

// DockerArgs builds the `docker run` argument list for the spec. It is a
// pure function so it can be unit-tested without invoking docker.
func (s LaunchSpec) DockerArgs() ([]string, error) {
	if s.HostRepoPath == "" {
		return nil, fmt.Errorf("airunner: HostRepoPath is required")
	}
	if s.Command == "" {
		return nil, fmt.Errorf("airunner: Command is required")
	}
	image := s.Image
	if image == "" {
		image = DefaultImage
	}
	network := s.Network
	if network == "" {
		network = "bridge"
	}

	args := []string{
		"run", "--rm",
		"-v", s.HostRepoPath + ":/repo:rw",
		"--workdir", "/repo",
		"--network", network,
	}
	if s.Name != "" {
		args = append(args, "--name", s.Name)
	}
	if s.Memory != "" {
		args = append(args, "--memory", s.Memory)
	}
	// Deterministic env ordering so the invocation is reproducible.
	keys := make([]string, 0, len(s.Env))
	for k := range s.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "-e", k+"="+s.Env[k])
	}
	args = append(args, image, s.Command)
	return args, nil
}

// Run launches the AI runner container and waits for it to exit. stdout
// and stderr are captured and returned as combined output. A non-nil
// error means docker (or the agent) exited non-zero.
func Run(ctx context.Context, s LaunchSpec) (string, error) {
	args, err := s.DockerArgs()
	if err != nil {
		return "", err
	}
	// #nosec G204 -- args are wolf-constructed; Command is the resolved
	// tool invocation, HostRepoPath an operator-supplied repo path.
	cmd := exec.CommandContext(ctx, "docker", args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// ImageExists reports whether the runner image is present locally.
func ImageExists(ctx context.Context, image string) bool {
	if image == "" {
		image = DefaultImage
	}
	cmd := exec.CommandContext(ctx, "docker", "image", "inspect", image)
	return cmd.Run() == nil
}

// quote is a tiny helper for callers assembling a shell Command string.
func quote(s string) string {
	if s == "" {
		return "''"
	}
	if !strings.ContainsAny(s, " \t\n'\"\\$") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ShellCommand joins a binary + args into a single shell command string
// suitable for LaunchSpec.Command, quoting each token safely.
func ShellCommand(bin string, args ...string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, quote(bin))
	for _, a := range args {
		parts = append(parts, quote(a))
	}
	return strings.Join(parts, " ")
}
