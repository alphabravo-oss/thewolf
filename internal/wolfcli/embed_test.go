package wolfcli

import (
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// An overlay embeds the whole tree: its Setup runs before every command, an
// extra command is added, and one with a built-in's name replaces it.
func TestOverlayHooksRunBeforeEveryCommand(t *testing.T) {
	var order []string
	root := NewRootCommand(Options{
		Setup: func() error { order = append(order, "setup"); return nil },
		Commands: []*cobra.Command{
			{Use: "extra", RunE: func(*cobra.Command, []string) error { order = append(order, "extra"); return nil }},
			{Use: "version", RunE: func(*cobra.Command, []string) error { order = append(order, "overlay-version"); return nil }},
		},
	})
	for _, args := range [][]string{{"extra"}, {"version"}} {
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	if got := strings.Join(order, ","); got != "setup,extra,setup,overlay-version" {
		t.Fatalf("order = %s", got)
	}
	for _, name := range []string{"serve", "scan-worker", "scanner-release-worker", "scan"} {
		if cmd, _, err := root.Find([]string{name}); err != nil || cmd.Name() != name {
			t.Fatalf("built-in %q missing from the embedded tree: %v", name, err)
		}
	}
}

// A failing Setup (for example an unregistrable module) stops the command.
func TestOverlaySetupFailureStopsCommand(t *testing.T) {
	ran := false
	root := NewRootCommand(Options{
		Setup:    func() error { return errors.New("no entitlement") },
		Commands: []*cobra.Command{{Use: "extra", RunE: func(*cobra.Command, []string) error { ran = true; return nil }}},
	})
	root.SetArgs([]string{"extra"})
	if err := root.Execute(); err == nil || ran {
		t.Fatalf("setup failure must stop the command: err=%v ran=%v", err, ran)
	}
}
