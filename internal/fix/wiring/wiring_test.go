package wiring

import (
	"context"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/scan/runner"
)

// TestDepsArePopulated is the regression guard for the worker-had-nil-deps bug:
// orchestrator.Run fails fast unless Workspaces, Engines, and Verifier (plus the
// other collaborators) are non-nil. If a refactor drops one, this fails before
// a real job ever would.
func TestDepsArePopulated(t *testing.T) {
	d := Deps(nil)
	if d.Writability == nil {
		t.Error("Writability dep is nil")
	}
	if d.Workspaces == nil {
		t.Error("Workspaces dep is nil")
	}
	if d.Engines == nil {
		t.Error("Engines dep is nil")
	}
	if d.Verifier == nil {
		t.Error("Verifier dep is nil")
	}
	if d.GitApply == nil {
		t.Error("GitApply dep is nil")
	}
}

// TestScannerScopesToToolAndFile verifies the targeted rescan builds a run
// config scoped to the single tool + file and returns only that file's findings
// (the gate does the per-rule matching). No real docker — runScan is stubbed.
func TestScannerScopesToToolAndFile(t *testing.T) {
	orig := runScan
	defer func() { runScan = orig }()

	var got runner.RunConfig
	runScan = func(_ context.Context, cfg runner.RunConfig) (*runner.RunResult, error) {
		got = cfg
		return &runner.RunResult{Findings: []models.Finding{
			{ToolName: "gosec", FilePath: "internal/x.go", RuleID: "G101"},
			{ToolName: "gosec", FilePath: "internal/x.go", RuleID: "G404"},
			{ToolName: "gosec", FilePath: "other/y.go", RuleID: "G102"},
		}}, nil
	}

	out, err := Scanner{}.RescanFile(context.Background(), "/repo", "internal/x.go", "gosec", "G101")
	if err != nil {
		t.Fatalf("RescanFile: %v", err)
	}
	if len(got.Tools) != 1 || got.Tools[0] != "gosec" {
		t.Errorf("run not scoped to the one tool: %v", got.Tools)
	}
	if len(got.IncludePaths) != 1 || got.IncludePaths[0] != "internal/x.go" {
		t.Errorf("run not scoped to the changed file: %v", got.IncludePaths)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 findings for internal/x.go, got %d: %+v", len(out), out)
	}
	for _, f := range out {
		if f.FilePath != "internal/x.go" {
			t.Errorf("leaked a finding from another file: %+v", f)
		}
	}
}

// TestScannerNoToolIsNoop: a finding with no tool can't be rescanned.
func TestScannerNoToolIsNoop(t *testing.T) {
	out, err := Scanner{}.RescanFile(context.Background(), "/repo", "x.go", "", "")
	if err != nil || out != nil {
		t.Errorf("empty tool should be a no-op, got %v / %v", out, err)
	}
}
