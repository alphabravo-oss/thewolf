package wolfcli

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/alphabravocompany/thewolf/internal/setup/scanners"
)

func TestScanWorkerRuntimeSelection(t *testing.T) {
	for _, tc := range []struct{ environment, want string }{
		{"", "docker"}, {" KUBERNETES ", "kubernetes"}, {"docker", "docker"}, {"invalid", "invalid"},
	} {
		t.Run(tc.environment, func(t *testing.T) {
			t.Setenv("WOLF_SCAN_RUNTIME", tc.environment)
			cmd := newScanWorkerCmd()
			if got := cmd.Flags().Lookup("backend").Value.String(); got != tc.want || got != scanners.RuntimeFromEnv() {
				t.Fatalf("worker runtime %q differs from configured runtime %q", got, tc.want)
			}
			if err := cmd.ParseFlags([]string{"--backend=docker"}); err != nil {
				t.Fatal(err)
			}
			if got := cmd.Flags().Lookup("backend").Value.String(); got != "docker" {
				t.Fatalf("explicit flag did not override environment: %q", got)
			}
		})
	}
}

func TestWorkersRejectInvalidMasterKeyBeforeInitialization(t *testing.T) {
	t.Setenv("WOLF_MASTER_KEY", "invalid")
	t.Setenv("WOLF_DB_DRIVER", "unsupported")
	t.Setenv("PATH", t.TempDir())
	for _, cmd := range []*cobra.Command{newScanWorkerCmd(), newFixerCmd()} {
		cmd.SetArgs([]string{"--once"})
		if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "load secrets master key") {
			t.Fatalf("%s must reject the key before backend/database initialization: %v", cmd.Name(), err)
		}
	}
}
