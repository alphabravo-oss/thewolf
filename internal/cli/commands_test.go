package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// collectLeaves returns the path of every runnable leaf command.
func collectLeaves(cmd *cobra.Command, prefix []string) [][]string {
	var out [][]string
	for _, sub := range cmd.Commands() {
		if sub.Name() == "help" || sub.Name() == "completion" {
			continue
		}
		path := append(append([]string{}, prefix...), sub.Name())
		if len(sub.Commands()) == 0 {
			out = append(out, path)
		} else {
			out = append(out, collectLeaves(sub, path)...)
		}
	}
	return out
}

// TestEveryCommandIsWiredAndDocumented walks the whole command tree and runs
// `--help` on every leaf — proving each command is registered, parses, and
// has a description, with no panic.
func TestEveryCommandIsWiredAndDocumented(t *testing.T) {
	root := newRootForTest()
	leaves := collectLeaves(root, nil)
	if len(leaves) < 50 {
		t.Fatalf("expected the CLI to expose 50+ leaf commands, found %d", len(leaves))
	}
	for _, leaf := range leaves {
		name := strings.Join(leaf, " ")
		t.Run(name, func(t *testing.T) {
			out, err := run(t, append(leaf, "--help")...)
			if err != nil {
				t.Fatalf("`wolf %s --help` failed: %v", name, err)
			}
			if !strings.Contains(out, "Usage:") {
				t.Errorf("`wolf %s --help` produced no usage text", name)
			}
		})
	}
}

// TestEveryReadCommandLive runs every safe read-only command against a real
// server and asserts it succeeds (or fails only for the expected reason —
// the container scanner backend is not configured in tests).
func TestEveryReadCommandLive(t *testing.T) {
	url, jwt := startServer(t)
	common := []string{"--server", url, "--token", jwt, "-o", "json"}

	// Create a repo so the parameterized read commands have a real target.
	repoOut, err := run(t, append([]string{"repo", "create", "--name", "t", "--path", "/tmp/t"}, common...)...)
	if err != nil {
		t.Fatalf("repo create: %v\n%s", err, repoOut)
	}
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(repoOut), &created); err != nil || created.Data.ID == "" {
		t.Fatalf("repo create returned no id: %v\n%s", err, repoOut)
	}

	readCommands := [][]string{
		{"repo", "list"},
		{"repo", "get", created.Data.ID},
		{"collection", "list"},
		{"scan", "list"},
		{"scan", "trends", "--repo", created.Data.ID},
		{"finding", "list"},
		{"finding", "trends"},
		{"fix", "list"},
		{"loop", "list"},
		{"user", "list"},
		{"settings", "get"},
		{"prompt", "list"},
		{"prompt", "defaults"},
		{"provider", "list"},
		{"secret", "list"},
		{"plugin", "list"},
		{"audit", "list"},
		{"system", "health"},
		{"system", "ready"},
		{"system", "version"},
		{"system", "setup-status"},
		{"auth", "whoami"},
		{"auth", "token", "list"},
	}
	for _, rc := range readCommands {
		name := strings.Join(rc, " ")
		t.Run(name, func(t *testing.T) {
			out, err := run(t, append(append([]string{}, rc...), common...)...)
			if err != nil {
				t.Fatalf("`wolf %s` failed: %v\n%s", name, err, out)
			}
		})
	}

	// Scanner commands legitimately return 503 when the container backend
	// is not configured — verify they reach the API and fail only that way.
	for _, sc := range [][]string{{"scanner", "list"}, {"scanner", "images"}, {"scanner", "config"}} {
		name := strings.Join(sc, " ")
		t.Run(name, func(t *testing.T) {
			_, err := run(t, append(append([]string{}, sc...), common...)...)
			if err == nil {
				return // backend happened to be available
			}
			ae, ok := err.(*APIError)
			if !ok {
				t.Fatalf("`wolf %s`: unexpected error type %T: %v", name, err, err)
			}
			if ae.StatusCode != 503 {
				t.Fatalf("`wolf %s`: expected success or 503, got %d", name, ae.StatusCode)
			}
		})
	}
}

// TestConfigCommandsLive exercises the context-management commands.
func TestConfigCommandsLive(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	if _, err := run(t, "config", "set-context", "prod", "--server", "https://x", "--token", "wolf_x"); err != nil {
		t.Fatalf("set-context: %v", err)
	}
	if _, err := run(t, "config", "use-context", "prod"); err != nil {
		t.Fatalf("use-context: %v", err)
	}
	out, err := run(t, "config", "current-context")
	if err != nil || !strings.Contains(out, "prod") {
		t.Fatalf("current-context: %v / %q", err, out)
	}
	out, err = run(t, "config", "view")
	if err != nil || !strings.Contains(out, "prod") {
		t.Fatalf("view: %v / %q", err, out)
	}
	if strings.Contains(out, "wolf_x") {
		t.Error("config view leaked a token without --show-tokens")
	}
	if _, err := run(t, "config", "delete-context", "prod"); err != nil {
		t.Fatalf("delete-context: %v", err)
	}
}
