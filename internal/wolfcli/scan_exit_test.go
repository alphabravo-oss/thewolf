package wolfcli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
	"github.com/alphabravocompany/thewolf/internal/scantarget"
)

type exitTestPlugin struct {
	fail, blockReport, unavailable bool
	parseError                     bool
	outputRoot                     string
	checkWorkspaceHold             bool
}

func (p exitTestPlugin) Name() string                 { return "exit-test" }
func (p exitTestPlugin) Category() models.Category    { return models.CategorySAST }
func (p exitTestPlugin) Languages() []models.Language { return nil }
func (p exitTestPlugin) CheckAvailable() bool         { return !p.unavailable }
func (p exitTestPlugin) Execute(_ context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	if p.checkWorkspaceHold {
		if err := scantarget.CleanupWorkspace(opts.RepoPath); !errors.Is(err, scantarget.ErrWorkspaceInUse) {
			return nil, errors.New("local CLI source was not protected during execution")
		}
	}
	// Simulate a report write failure after scanning has already succeeded.
	if p.blockReport {
		dirs, err := filepath.Glob(filepath.Join(p.outputRoot, "*"))
		if err != nil || len(dirs) != 1 {
			return nil, errors.New("fixture scan directory missing")
		}
		if err := os.Mkdir(filepath.Join(dirs[0], "findings.json"), 0700); err != nil {
			return nil, err
		}
	}
	var err error
	if p.parseError {
		opts.OnParseError(errors.New("fixture malformed record"))
	}
	if p.fail {
		err = errors.New("fixture analyzer failed after producing evidence")
	}
	return []models.Finding{{ToolName: p.Name(), RuleID: "fixture", FilePath: "main.go", Title: "retained finding", Severity: models.SeverityHigh, Category: models.CategorySAST}}, err
}

func TestLocalScanExitContract(t *testing.T) {
	// Only setup's Docker availability probe is stubbed; the real command,
	// runner and artifact writer execute. No daemon or image is required.
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte("#!/bin/sh\nprintf '{}\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("WOLF_SCANNERS_IMAGE", "wolf-scan-exit-test:fixture")
	t.Setenv("WOLF_SCANNERS_PULL_POLICY", "Never")
	t.Setenv("WOLF_SCANNERS_DB_VOLUME", t.TempDir())
	previousRegistry, previousConfig := plugin.Global, container.Default()
	t.Cleanup(func() { plugin.Global = previousRegistry; container.SetDefault(previousConfig) })

	for _, tc := range []struct {
		name, tools, wantError string
		invalidIgnore          bool
		plugin                 exitTestPlugin
	}{
		{name: "findings are successful execution", tools: "exit-test"},
		{name: "managed source retained", tools: "exit-test", plugin: exitTestPlugin{checkWorkspaceHold: true}},
		{name: "partial findings survive failure", tools: "exit-test", plugin: exitTestPlugin{fail: true}, wantError: "1 tool(s) failed"},
		{name: "invalid ignore file", tools: "exit-test", invalidIgnore: true, wantError: "load suppression rules"},
		{name: "report failure", tools: "exit-test", plugin: exitTestPlugin{blockReport: true}, wantError: "artifact write failed"},
		{name: "unknown tool", tools: "does-not-exist", wantError: "no tools ran"},
		{name: "missing tool alongside success", tools: "exit-test,does-not-exist", wantError: `requested tool "does-not-exist" did not run`},
		{name: "unavailable tool", tools: "exit-test", plugin: exitTestPlugin{unavailable: true}, wantError: "no tools ran"},
		{name: "partial parsing", tools: "exit-test", plugin: exitTestPlugin{parseError: true}, wantError: "1 parse error(s)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plugin.Global = plugin.NewRegistry()
			repo, out := t.TempDir(), t.TempDir()
			if tc.invalidIgnore {
				if err := os.WriteFile(filepath.Join(repo, ".wolfignore"), []byte("** category=\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.plugin.checkWorkspaceHold {
				t.Setenv("WOLF_WORKSPACE_ROOT", repo)
				repo = filepath.Join(repo, "wolf-git-scan-cli")
				if err := os.Mkdir(repo, 0700); err != nil {
					t.Fatal(err)
				}
			}
			tc.plugin.outputRoot = out
			plugin.Global.Register(tc.plugin)
			cmd := newScanCmd()
			cmd.SetArgs([]string{"--repo", repo, "--tools", tc.tools, "--out", out})
			err := cmd.Execute()
			if tc.wantError == "" && err != nil || tc.wantError != "" && (err == nil || !strings.Contains(err.Error(), tc.wantError)) {
				t.Fatalf("error = %v, want %q", err, tc.wantError)
			}
			if tc.plugin.checkWorkspaceHold {
				if _, err := os.Stat(repo); err != nil {
					t.Fatal("CLI release deleted its input")
				}
				if err := scantarget.CleanupWorkspace(repo); err != nil {
					t.Fatalf("CLI did not release its hold: %v", err)
				}
			}
			if tc.plugin.fail {
				files, err := filepath.Glob(filepath.Join(out, "*", "findings.json"))
				if err != nil || len(files) != 1 {
					t.Fatalf("missing partial findings: %v, %v", files, err)
				}
				data, err := os.ReadFile(files[0])
				if err != nil || !strings.Contains(string(data), "retained finding") {
					t.Fatalf("lost evidence: %s, %v", data, err)
				}
			}
		})
	}
}
