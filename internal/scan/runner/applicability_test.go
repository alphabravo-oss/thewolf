package runner_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/scan/runner"
	_ "github.com/alphabravocompany/thewolf/plugins/additional"
	_ "github.com/alphabravocompany/thewolf/plugins/container"
	_ "github.com/alphabravocompany/thewolf/plugins/docs"
	_ "github.com/alphabravocompany/thewolf/plugins/infra"
	_ "github.com/alphabravocompany/thewolf/plugins/security"
)

func TestInputGuardsAreSharedBySelectionAndExecution(t *testing.T) {
	for _, tc := range []struct{ tool, file, content, target string }{
		{"tflint", "main.tf", "resource {}", ""},
		{"yamllint", "config.yaml", "setting: value", ""},
		{"kubescape", "nested/pod.yaml", "apiVersion: v1\nkind: Pod", ""},
		{"kube-linter", "nested/pod.yaml", "apiVersion: v1\nkind: Pod", ""},
		{"pluto", "nested/pod.yaml", "apiVersion: v1\nkind: Pod", ""},
		{"conftest", "policy/test.rego", "package test", ""},
		{"spectral", "openapi.yaml", "openapi: 3.0.0", ""},
		{"vale", ".vale.ini", "StylesPath = styles", ""},
		{"poutine", ".github/workflows/ci.yml", "name: ci", ""},
		{"zizmor", ".github/workflows/ci.yml", "name: ci", ""},
		{"dockle", "", "", "example:tag"},
		{"nuclei", "", "", "https://example.invalid"},
		{"zap", "", "", "https://example.invalid"},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			repo := t.TempDir()
			scanner, err := plugin.Global.Get(tc.tool)
			if err != nil {
				t.Fatal(err)
			}
			registry := plugin.NewRegistry()
			registry.Register(scanner)
			cfg := runner.RunConfig{Registry: registry, RepoPath: repo, Tools: []string{tc.tool}}
			if len(runner.SelectTools(cfg)) != 0 {
				t.Fatal("preflight selected absent inputs")
			}
			if _, err := scanner.Execute(t.Context(), models.ExecuteOpts{RepoPath: repo}); !plugin.IsNotApplicable(err) {
				t.Fatalf("execution disagrees: %v", err)
			}
			if tc.file != "" {
				p := filepath.Join(repo, tc.file)
				if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(tc.content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.tool == "vale" {
				if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("Docs"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			cfg.Target = tc.target
			if len(runner.SelectTools(cfg)) != 1 {
				t.Fatal("selection excluded present inputs")
			}
			if err := plugin.CheckApplicability(scanner, repo, tc.target); err != nil {
				t.Fatal(err)
			}
		})
	}
}
