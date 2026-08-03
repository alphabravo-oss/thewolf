package remediate

import (
	"fmt"
	"os"
	"path/filepath"
)

// agentConfigPaths are repository-level OpenCode configuration locations.
// OpenCode's config precedence places project config ABOVE the config Wolf
// injects via OPENCODE_CONFIG, so a repository carrying any of these can
// override every permission rule Wolf sets.
var agentConfigPaths = []string{"opencode.json", "opencode.jsonc", ".opencode"}

// StripAgentConfig removes repository-level OpenCode configuration from a
// worktree and returns the relative paths it removed.
//
// The scanned repository is untrusted input — that is the premise of the
// product — so its own agent configuration must not be allowed to outrank
// Wolf's. This runs against the ephemeral worktree only; the user's actual
// repository is never modified.
func StripAgentConfig(worktreePath string) ([]string, error) {
	var removed []string
	for _, name := range agentConfigPaths {
		full := filepath.Join(worktreePath, name)
		if _, err := os.Lstat(full); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return removed, fmt.Errorf("stat %s: %w", name, err)
		}
		if err := os.RemoveAll(full); err != nil {
			return removed, fmt.Errorf("remove %s: %w", name, err)
		}
		removed = append(removed, name)
	}
	return removed, nil
}
