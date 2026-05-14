package plugin

import (
	"context"
	"os/exec"
	"syscall"
)

// CommandContext creates an exec.Cmd with process-group isolation so that
// cancelling the context kills the entire process tree (not just the
// direct child). This is critical for tools like semgrep that spawn
// multiple subprocesses (pysemgrep → semgrep-core).
func CommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
	cmd := exec.CommandContext(ctx, name, args...)

	// Put the child in its own process group so we can kill the whole tree.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Override the default cancel behavior: kill the process group, not
	// just the direct child PID.
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// Kill the entire process group (negative PID).
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}

	return cmd
}
