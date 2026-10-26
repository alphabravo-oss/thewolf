package python

import (
	"context"
	"github.com/alphabravocompany/thewolf/internal/finding/identity"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
	"github.com/alphabravocompany/thewolf/internal/scan/runner"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSCADependencyIdentityPipAudit(t *testing.T) {
	findings, err := parsePipAuditOutput([]byte(`{"dependencies":[{"name":"alpha","version":"1","vulns":[{"id":"PYSEC-CVE-2026-1001","aliases":["CVE-2026-1001"]}]},{"name":"alpha","version":"1","vulns":[{"id":"PYSEC-CVE-2026-1002","aliases":["CVE-2026-1002"]}]},{"name":"beta","version":"1","vulns":[{"id":"PYSEC-CVE-2026-1001","aliases":["CVE-2026-1001"]}]},{"name":"alpha","version":"2","vulns":[{"id":"PYSEC-CVE-2026-1001","aliases":["CVE-2026-1001"]}]}]}`))
	if err != nil || len(findings) != 4 {
		t.Fatalf("fixture: count=%d err=%v", len(findings), err)
	}
	seen := map[string]bool{}
	for _, f := range findings {
		if f.PackageName == "" || f.PackageVersion == "" || f.PackageEcosystem == "" || f.AdvisoryID == "" {
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
	if got, err := parsePipAuditOutput([]byte(`{}`)); err != nil || len(got) != 0 {
		t.Fatalf("empty output invented findings: %v", err)
	}
	if _, err := parsePipAuditOutput([]byte(`{`)); err == nil {
		t.Fatal("malformed output accepted")
	}
}

func TestPipAuditExecuteAnchorsSelectedRequirements(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "requirements")
	if err := os.Mkdir(sub, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "base.txt"), []byte("alpha==1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "result.json")
	if err := os.WriteFile(output, []byte(`{"dependencies":[{"name":"alpha","version":"1","vulns":[{"id":"CVE-2026-1001"}]}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	container.SetCommandFactory(func(ctx context.Context, _ *container.Config, _ container.Options, tool string, args ...string) *exec.Cmd {
		if tool != "pip-audit" || len(args) < 2 || args[0] != "-r" || args[1] != "requirements/base.txt" {
			t.Fatalf("wrong audited requirements: %s %v", tool, args)
		}
		return exec.CommandContext(ctx, "cat", output)
	})
	t.Cleanup(func() { container.SetCommandFactory(nil) })
	findings, err := (&PipAuditPlugin{}).Execute(t.Context(), models.ExecuteOpts{RepoPath: root, ContainerCfg: container.DefaultConfig()})
	if err != nil || len(findings) != 1 || findings[0].FilePath != "requirements/base.txt" {
		t.Fatalf("wrong requirements anchor: %+v %v", findings, err)
	}
}
