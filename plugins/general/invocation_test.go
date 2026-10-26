package general

import (
	"context"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
)

func TestGitleaksInvocationPreservesFindingsAndFailures(t *testing.T) {
	for _, tc := range []struct {
		output, code string
		count        int
		failed       bool
	}{
		{`[{"RuleID":"github-pat","File":"/scan/secrets.env","StartLine":5}]`, "0", 1, false},
		{"", "127", 0, true}, {"", "0", 0, true}, {"[]", "0", 0, false},
		{`[{"RuleID":"github-pat"}]`, "2", 1, true},
	} {
		container.SetCommandFactory(func(ctx context.Context, cfg *container.Config, opts container.Options, tool string, args ...string) *exec.Cmd {
			if !strings.Contains(strings.Join(args, " "), "--report-path - --no-git") {
				t.Fatalf("report must use stdout: %v", args)
			}
			return exec.CommandContext(ctx, "sh", "-c", `printf '%s' "$1"; echo unavailable >&2; exit "$2"`, "test", tc.output, tc.code)
		})
		t.Cleanup(func() { container.SetCommandFactory(nil) })
		findings, err := (&GitleaksPlugin{}).Execute(t.Context(), models.ExecuteOpts{RepoPath: t.TempDir(), ContainerCfg: &container.Config{Image: "test"}})
		if len(findings) != tc.count || (err != nil) != tc.failed {
			t.Fatalf("findings=%v err=%v", findings, err)
		}
	}
}

func TestOfflineDatabaseInvocations(t *testing.T) {
	for _, p := range []models.Plugin{&GrypePlugin{}, &TrivyPlugin{}} {
		t.Run(p.Name(), func(t *testing.T) {
			container.SetCommandFactory(func(ctx context.Context, cfg *container.Config, opts container.Options, tool string, args ...string) *exec.Cmd {
				if tool == "grype" && (opts.ExtraEnv["GRYPE_DB_AUTO_UPDATE"] != "false" || opts.ExtraEnv["GRYPE_DB_CACHE_DIR"] != "/prepared/grype") {
					t.Fatalf("offline env: %v", opts.ExtraEnv)
				}
				if tool == "trivy" && (!slices.Contains(args, "--skip-db-update") || !slices.Contains(args, "--skip-java-db-update") || !slices.Contains(args, "--offline-scan") || !slices.Contains(args, "/prepared/trivy")) {
					t.Fatalf("offline args: %v", args)
				}
				return exec.CommandContext(ctx, "sh", "-c", "echo 'database does not exist' >&2; exit 1")
			})
			t.Cleanup(func() { container.SetCommandFactory(nil) })
			_, err := p.Execute(t.Context(), models.ExecuteOpts{RepoPath: t.TempDir(), ContainerCfg: &container.Config{Image: "test", Network: "none", AllowNetwork: "none", ExtraEnv: map[string]string{"GRYPE_DB_CACHE_DIR": "/prepared/grype", "TRIVY_CACHE_DIR": "/prepared/trivy"}}})
			if err == nil || !strings.Contains(err.Error(), "needs network or database (retryable)") {
				t.Fatalf("prerequisite lost: %v", err)
			}
		})
	}
}

func TestSemgrepOfflineWithoutPreparedRules(t *testing.T) {
	// Use an empty filesystem location for each trusted rule candidate so this
	// check is independent of caches on the developer's host.
	script := semgrepRulesCommand
	for _, path := range []string{"/etc/wolf-scanners/semgrep.yml", "/etc/wolf-scanners/semgrep", "/var/lib/wolf-db/semgrep/rules.yaml", "/var/lib/wolf-db/semgrep/rules"} {
		script = strings.ReplaceAll(script, path, t.TempDir()+"/missing")
	}
	cmd := exec.CommandContext(t.Context(), "sh", "-c", script, "test", "none", "--jobs", "1")
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "needs network or database") {
		t.Fatalf("out=%s err=%v", out, err)
	}
}
