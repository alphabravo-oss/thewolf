package airunner

import (
	"strings"
	"testing"
)

func TestDockerArgs_Basic(t *testing.T) {
	args, err := LaunchSpec{
		HostRepoPath: "/host/repo",
		Command:      "claude -p fix",
		Env:          map[string]string{"ANTHROPIC_API_KEY": "sk-x"},
		Name:         "wolf-loop-1",
	}.DockerArgs()
	if err != nil {
		t.Fatalf("DockerArgs: %v", err)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"run --rm",
		"-v /host/repo:/repo:rw",
		"--workdir /repo",
		"--network bridge",
		"--name wolf-loop-1",
		"-e ANTHROPIC_API_KEY=sk-x",
		DefaultImage,
		"claude -p fix",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("docker args missing %q\ngot: %s", want, joined)
		}
	}
}

func TestDockerArgs_RepoMountedReadWrite(t *testing.T) {
	args, _ := LaunchSpec{HostRepoPath: "/r", Command: "x"}.DockerArgs()
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "/r:/repo:rw") {
		t.Errorf("repo must be mounted read-write, got: %s", joined)
	}
	if strings.Contains(joined, ":ro") {
		t.Errorf("repo must NOT be read-only: %s", joined)
	}
}

func TestDockerArgs_Validation(t *testing.T) {
	if _, err := (LaunchSpec{Command: "x"}).DockerArgs(); err == nil {
		t.Error("expected error when HostRepoPath is empty")
	}
	if _, err := (LaunchSpec{HostRepoPath: "/r"}).DockerArgs(); err == nil {
		t.Error("expected error when Command is empty")
	}
}

func TestDockerArgs_EnvDeterministicOrder(t *testing.T) {
	spec := LaunchSpec{
		HostRepoPath: "/r", Command: "x",
		Env: map[string]string{"Z": "1", "A": "2", "M": "3"},
	}
	a, _ := spec.DockerArgs()
	b, _ := spec.DockerArgs()
	if strings.Join(a, " ") != strings.Join(b, " ") {
		t.Error("DockerArgs must be deterministic for the same spec")
	}
	// A before M before Z.
	joined := strings.Join(a, " ")
	ai, mi, zi := strings.Index(joined, "A=2"), strings.Index(joined, "M=3"), strings.Index(joined, "Z=1")
	if !(ai < mi && mi < zi) {
		t.Errorf("env not sorted: %s", joined)
	}
}

func TestShellCommand(t *testing.T) {
	if got := ShellCommand("claude", "-p", "fix the bug"); got != `claude -p 'fix the bug'` {
		t.Errorf("ShellCommand = %q", got)
	}
	if got := ShellCommand("tool", "a'b"); !strings.Contains(got, `'\''`) {
		t.Errorf("ShellCommand should escape single quotes, got %q", got)
	}
}
