package javascript

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/finding/identity"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
	"github.com/alphabravocompany/thewolf/internal/scan/runner"
)

func TestSCADependencyIdentityNPMAudit(t *testing.T) {
	findings, err := parseNPMAuditOutput([]byte(`{"vulnerabilities":{"alpha":{"severity":"high","via":[{"cve":"CVE-2026-1001","title":"first","source":1},{"cve":"CVE-2026-1002","title":"second","source":2}]},"beta":{"severity":"high","via":[{"cve":"CVE-2026-1001","title":"first","source":1}]}}}`))
	if err != nil || len(findings) != 3 {
		t.Fatalf("fixture: count=%d err=%v", len(findings), err)
	}
	seen := map[string]bool{}
	for _, f := range findings {
		if f.PackageName == "" || f.PackageEcosystem == "" || f.AdvisoryID == "" {
			t.Fatalf("dependency identity missing: %+v", f)
		}
		fingerprint := identity.Build(f)
		if fingerprint.Version != 2 || seen[fingerprint.Stable] {
			t.Fatalf("distinct dependency occurrence lost: %+v", f)
		}
		seen[fingerprint.Stable] = true
	}
	if got := runner.Deduplicate(findings); len(got) != len(findings) {
		t.Fatalf("package/advisory/location collapse: %d -> %d", len(findings), len(got))
	}
	if got, err := parseNPMAuditOutput([]byte(`{}`)); err != nil || len(got) != 0 {
		t.Fatalf("empty output invented findings: %v", err)
	}
	if _, err := parseNPMAuditOutput([]byte(`{`)); err == nil {
		t.Fatal("malformed output accepted")
	}
}

func TestNPMAuditExecuteAnchorsTheAuditedManifest(t *testing.T) {
	for _, manifest := range []string{"package-lock.json", "package.json"} {
		t.Run(manifest, func(t *testing.T) {
			root := t.TempDir()
			sub := filepath.Join(root, "web")
			if err := os.Mkdir(sub, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(sub, manifest), []byte(`{}`), 0600); err != nil {
				t.Fatal(err)
			}
			output := filepath.Join(t.TempDir(), "result.json")
			if err := os.WriteFile(output, []byte(`{"vulnerabilities":{"alpha":{"severity":"high","via":[{"cve":"CVE-2026-1001","title":"fixture"}]}}}`), 0600); err != nil {
				t.Fatal(err)
			}
			container.SetCommandFactory(func(ctx context.Context, _ *container.Config, opts container.Options, tool string, args ...string) *exec.Cmd {
				if tool != "npm" || opts.WorkDir != "/scan/web" {
					t.Fatalf("wrong audited location: %s %s", tool, opts.WorkDir)
				}
				return exec.CommandContext(ctx, "cat", output)
			})
			t.Cleanup(func() { container.SetCommandFactory(nil) })
			findings, err := (&NPMAuditPlugin{}).Execute(t.Context(), models.ExecuteOpts{RepoPath: root, ContainerCfg: container.DefaultConfig()})
			if err != nil || len(findings) != 1 || findings[0].FilePath != "web/"+manifest {
				t.Fatalf("wrong manifest anchor: %+v %v", findings, err)
			}
		})
	}
}
