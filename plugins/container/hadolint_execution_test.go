package container

import (
	"context"
	"github.com/alphabravocompany/thewolf/internal/models"
	scannercontainer "github.com/alphabravocompany/thewolf/internal/plugin/container"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHadolintInvocationPreservesFailures(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "Dockerfile"), []byte("FROM alpine"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		output, code string
		count        int
		failed       bool
	}{
		{`[{"line":1,"code":"DL3006","file":"/scan/Dockerfile","level":"warning"}]`, "1", 1, false},
		{"", "127", 0, true}, {"invalid", "0", 0, true}, {"[]", "0", 0, false},
	} {
		scannercontainer.SetCommandFactory(func(ctx context.Context, cfg *scannercontainer.Config, opts scannercontainer.Options, tool string, args ...string) *exec.Cmd {
			_, dockerArgs, err := scannercontainer.BuildDockerArgs(cfg, opts, tool, args...)
			if err != nil || !strings.Contains(strings.Join(dockerArgs, " "), "--entrypoint hadolint") {
				t.Fatalf("args=%v err=%v", dockerArgs, err)
			}
			return exec.CommandContext(ctx, "sh", "-c", `printf '%s' "$1"; echo execution-error >&2; exit "$2"`, "test", tc.output, tc.code)
		})
		t.Cleanup(func() { scannercontainer.SetCommandFactory(nil) })
		findings, err := (&HadolintPlugin{}).Execute(t.Context(), models.ExecuteOpts{RepoPath: repo, ContainerCfg: &scannercontainer.Config{Image: "test", UpstreamTools: scannercontainer.DefaultUpstreamTools()}})
		if len(findings) != tc.count || (err != nil) != tc.failed {
			t.Fatalf("findings=%v err=%v", findings, err)
		}
	}
}
