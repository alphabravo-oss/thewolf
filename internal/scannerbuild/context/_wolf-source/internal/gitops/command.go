package gitops

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/alphabravocompany/thewolf/internal/plugin"
)

// Command applies shared defaults for Wolf's native Git operations. extraEnv is
// exclusively for credentials/settings constructed by the caller, never a copy
// of os.Environ. Repository filters/includes and filesystem access still need
// separate isolation; these defaults are not an OS sandbox.
func Command(ctx context.Context, extraEnv []string, args ...string) *exec.Cmd {
	fixed := []string{"--no-pager", "-c", "core.fsmonitor=false", "-c", "core.hooksPath=" + os.DevNull,
		"-c", "core.askPass=", "-c", "credential.helper=", "-c", "core.sshCommand=ssh",
		"-c", "commit.gpgSign=false", "-c", "tag.gpgSign=false"}
	cmd := plugin.CommandContext(ctx, "git", append(fixed, args...)...)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		key = strings.ToUpper(key)
		if strings.HasPrefix(key, "GIT_") {
			switch key {
			case "GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_AUTHOR_DATE", "GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL", "GIT_COMMITTER_DATE":
			default:
				continue
			}
		}
		if key != "SSH_ASKPASS" && key != "SSH_ASKPASS_REQUIRE" {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, extraEnv...)
	cmd.Env = append(cmd.Env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_SYSTEM="+os.DevNull,
		"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0",
		"GIT_NO_REPLACE_OBJECTS=1", "SSH_ASKPASS_REQUIRE=never")
	// Bound pipe draining if a terminated command left a descendant holding
	// stdout/stderr. Command execution itself follows the caller's context.
	cmd.WaitDelay = 2 * time.Second
	return cmd
}
