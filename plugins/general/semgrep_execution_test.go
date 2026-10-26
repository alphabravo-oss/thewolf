package general

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
)

func TestSemgrepExecutionPreservesFailureDetailsAndPartialFindings(t *testing.T) {
	for _, tc := range []struct {
		name, output, diagnostic, code, wantError string
		findings                                  int
	}{
		{"registry unavailable", "", "failed to resolve semgrep.dev", "2", "failed to resolve semgrep.dev", 0},
		{"partial results", `{"results":[{"check_id":"test-rule","path":"/scan/test.py","extra":{"severity":"ERROR"}}]}`, "fatal scanner error", "2", "fatal scanner error", 1},
		{"variant error types", `{"results":[{"check_id":"test-rule","path":"/scan/test.py","extra":{"severity":"ERROR"}}],"errors":[{"type":["PartialParsing",[]],"message":"syntax not understood"}]}`, "", "0", "syntax not understood", 1},
		{"JSON scan errors", `{"results":[],"errors":[{"type":"Timeout","message":"analysis timed out"}]}`, "", "0", "analysis timed out", 0},
		{"successful clean scan", `{"results":[],"errors":[]}`, "", "0", "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "result.json")
			if err := os.WriteFile(output, []byte(tc.output), 0600); err != nil {
				t.Fatal(err)
			}
			container.SetCommandFactory(func(ctx context.Context, cfg *container.Config, opts container.Options, tool string, args ...string) *exec.Cmd {
				if !strings.Contains(strings.Join(args, " "), "--jobs 2") {
					t.Fatalf("bad scan args: %v", args)
				}
				return exec.CommandContext(ctx, "sh", "-c", `cat "$1"; printf '%s' "$2" >&2; exit "$3"`, "semgrep-test", output, tc.diagnostic, tc.code)
			})
			t.Cleanup(func() { container.SetCommandFactory(nil) })
			findings, err := (&SemgrepPlugin{}).Execute(t.Context(), models.ExecuteOpts{RepoPath: t.TempDir(), ContainerCfg: &container.Config{Image: "test", CPUs: "2"}})
			if len(findings) != tc.findings {
				t.Fatalf("lost findings: %v", findings)
			}
			if tc.wantError == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("failure detail lost: %v", err)
			}
		})
	}
}
