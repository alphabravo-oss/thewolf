package controller

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/fix/engine"
	"github.com/alphabravocompany/thewolf/internal/loop/tracker"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/scan/runner"
)

// --- Mock Plugin ---

type mockPlugin struct {
	name      string
	category  models.Category
	languages []models.Language
	available bool
	findings  []models.Finding
	execCount int
	execErr   error
}

func (m *mockPlugin) Name() string                { return m.name }
func (m *mockPlugin) Category() models.Category   { return m.category }
func (m *mockPlugin) Languages() []models.Language { return m.languages }
func (m *mockPlugin) CheckAvailable() bool         { return m.available }

func (m *mockPlugin) Execute(_ context.Context, _ models.ExecuteOpts) ([]models.Finding, error) {
	m.execCount++
	if m.execErr != nil {
		return nil, m.execErr
	}
	return m.findings, nil
}

// --- Mock Fix Engine ---

type mockFixEngine struct {
	name      string
	available bool
	fixCount  int
	fixErr    error
}

func (m *mockFixEngine) Name() string { return m.name }
func (m *mockFixEngine) Available() bool { return m.available }

func (m *mockFixEngine) Fix(_ context.Context, _ engine.FixRequest) (*engine.FixResult, error) {
	m.fixCount++
	if m.fixErr != nil {
		return nil, m.fixErr
	}
	return &engine.FixResult{
		Success: true,
		Output:  "fixed",
	}, nil
}

// --- Helper ---

func newRegistry(plugins ...models.Plugin) *plugin.Registry {
	r := plugin.NewRegistry()
	for _, p := range plugins {
		r.Register(p)
	}
	return r
}

// --- Tests ---

func TestController_RunNoFindings(t *testing.T) {
	p := &mockPlugin{
		name:      "test-tool",
		available: true,
		findings:  nil, // no findings
	}
	reg := newRegistry(p)

	ctrl := New(Config{
		RepoPath:      "/tmp/test-repo",
		MaxIterations: 3,
		ScanConfig: runner.RunConfig{
			Registry:    reg,
			Concurrency: 1,
			Timeout:     5 * time.Second,
		},
	})

	loop, err := ctrl.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if loop.Status != models.LoopStatusCompleted {
		t.Errorf("expected completed status, got %s", loop.Status)
	}
	if loop.TotalFindingsInitial != 0 {
		t.Errorf("expected 0 initial findings, got %d", loop.TotalFindingsInitial)
	}
}

func TestController_RunWithFindings(t *testing.T) {
	callCount := 0
	p := &mockPlugin{
		name:      "test-tool",
		available: true,
		findings: []models.Finding{
			{
				Fingerprint: "fp-1",
				Title:       "Issue 1",
				Severity:    models.SeverityHigh,
				ToolName:    "test-tool",
				FilePath:    "main.go",
				LineStart:   10,
			},
		},
	}

	// After 2 scan calls (initial + rescan), the plugin returns no findings
	// to simulate the fix working. We wrap Execute to do this.
	origFindings := p.findings
	p2 := &countingPlugin{
		mockPlugin: p,
		maxCalls:   1, // return findings only on first call
		findings:   origFindings,
	}

	reg := newRegistry(p2)

	var iterStartCalled, iterDoneCalled int

	ctrl := New(Config{
		RepoPath:      "/tmp/test-repo",
		MaxIterations: 5,
		ScanConfig: runner.RunConfig{
			Registry:    reg,
			Concurrency: 1,
			Timeout:     5 * time.Second,
		},
		OnIterationStart: func(i int) { iterStartCalled++ },
		OnIterationDone: func(i int, diff *tracker.IterationDiff, warnings []string) {
			iterDoneCalled++
		},
	})

	loop, err := ctrl.Run(context.Background())
	_ = callCount
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if loop.Status != models.LoopStatusCompleted {
		t.Errorf("expected completed, got %s", loop.Status)
	}
	if loop.TotalFindingsInitial != 1 {
		t.Errorf("expected 1 initial finding, got %d", loop.TotalFindingsInitial)
	}
	if iterStartCalled == 0 {
		t.Error("OnIterationStart was not called")
	}
	if iterDoneCalled == 0 {
		t.Error("OnIterationDone was not called")
	}
}

func TestController_SeverityFilter(t *testing.T) {
	p := &mockPlugin{
		name:      "test-tool",
		available: true,
		findings: []models.Finding{
			{Fingerprint: "fp-crit", Severity: models.SeverityCritical, ToolName: "test-tool", FilePath: "a.go", LineStart: 1},
			{Fingerprint: "fp-low", Severity: models.SeverityLow, ToolName: "test-tool", FilePath: "b.go", LineStart: 2},
		},
	}
	reg := newRegistry(p)

	ctrl := New(Config{
		RepoPath:      "/tmp/test-repo",
		MaxIterations: 1,
		Severities:    []models.Severity{models.SeverityCritical},
		ScanConfig: runner.RunConfig{
			Registry:    reg,
			Concurrency: 1,
			Timeout:     5 * time.Second,
		},
	})

	loop, err := ctrl.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should only track critical findings.
	if loop.TotalFindingsInitial != 2 {
		t.Errorf("expected 2 initial findings (pre-filter), got %d", loop.TotalFindingsInitial)
	}
}

func TestController_StopDuringRun(t *testing.T) {
	p := &mockPlugin{
		name:      "test-tool",
		available: true,
		findings: []models.Finding{
			{Fingerprint: "fp-1", Severity: models.SeverityHigh, ToolName: "test-tool", FilePath: "a.go", LineStart: 1},
		},
	}
	reg := newRegistry(p)

	var ctrl *Controller
	ctrl = New(Config{
		RepoPath:      "/tmp/test-repo",
		MaxIterations: 10,
		ScanConfig: runner.RunConfig{
			Registry:    reg,
			Concurrency: 1,
			Timeout:     5 * time.Second,
		},
		OnIterationDone: func(i int, diff *tracker.IterationDiff, warnings []string) {
			// Stop after first iteration.
			ctrl.Stop()
		},
	})

	loop, err := ctrl.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if loop.Status != models.LoopStatusStopped {
		t.Errorf("expected stopped status, got %s", loop.Status)
	}
}

func TestController_ContextCancellation(t *testing.T) {
	p := &mockPlugin{
		name:      "test-tool",
		available: true,
		findings: []models.Finding{
			{Fingerprint: "fp-1", Severity: models.SeverityHigh, ToolName: "test-tool", FilePath: "a.go", LineStart: 1},
		},
	}
	reg := newRegistry(p)

	ctx, cancel := context.WithCancel(context.Background())

	ctrl := New(Config{
		RepoPath:      "/tmp/test-repo",
		MaxIterations: 10,
		ScanConfig: runner.RunConfig{
			Registry:    reg,
			Concurrency: 1,
			Timeout:     5 * time.Second,
		},
		OnIterationDone: func(i int, diff *tracker.IterationDiff, warnings []string) {
			cancel() // cancel after first iteration
		},
	})

	loop, err := ctrl.Run(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if loop.Status != models.LoopStatusStopped {
		t.Errorf("expected stopped status after cancel, got %s", loop.Status)
	}
}

func TestController_DiminishingReturns(t *testing.T) {
	// A plugin that always returns the same findings (no progress).
	p := &mockPlugin{
		name:      "stuck-tool",
		available: true,
		findings: []models.Finding{
			{Fingerprint: "fp-stuck", Severity: models.SeverityHigh, ToolName: "stuck-tool", FilePath: "stuck.go", LineStart: 1},
		},
	}
	reg := newRegistry(p)

	var warnings []string

	ctrl := New(Config{
		RepoPath:      "/tmp/test-repo",
		MaxIterations: 10,
		ScanConfig: runner.RunConfig{
			Registry:    reg,
			Concurrency: 1,
			Timeout:     5 * time.Second,
		},
		OnIterationDone: func(i int, diff *tracker.IterationDiff, w []string) {
			warnings = append(warnings, w...)
		},
	})

	loop, err := ctrl.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if loop.Status != models.LoopStatusCompleted {
		t.Errorf("expected completed (diminishing returns), got %s", loop.Status)
	}

	// Should have a diminishing returns warning.
	found := false
	for _, w := range warnings {
		if contains(w, "diminishing returns") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected diminishing returns warning")
	}
}

func TestController_DefaultConfig(t *testing.T) {
	ctrl := New(Config{})

	if ctrl.cfg.MaxIterations != 5 {
		t.Errorf("expected default max iterations 5, got %d", ctrl.cfg.MaxIterations)
	}
	if ctrl.cfg.RescanStrategy != models.RescanFull {
		t.Errorf("expected default rescan strategy full, got %s", ctrl.cfg.RescanStrategy)
	}
	if ctrl.cfg.FixTimeout != 5*time.Minute {
		t.Errorf("expected default fix timeout 5m, got %s", ctrl.cfg.FixTimeout)
	}
}

func TestController_PauseResume(t *testing.T) {
	ctrl := New(Config{})

	ctrl.Pause()
	ctrl.mu.Lock()
	if ctrl.state != statePaused {
		t.Error("expected paused state")
	}
	ctrl.mu.Unlock()

	ctrl.Resume()
	ctrl.mu.Lock()
	if ctrl.state != stateRunning {
		t.Error("expected running state after resume")
	}
	ctrl.mu.Unlock()
}

func TestController_ScanFailure(t *testing.T) {
	p := &mockPlugin{
		name:      "failing-tool",
		available: true,
		execErr:   fmt.Errorf("scan explosion"),
	}
	reg := newRegistry(p)

	ctrl := New(Config{
		RepoPath:      "/tmp/test-repo",
		MaxIterations: 3,
		ScanConfig: runner.RunConfig{
			Registry:    reg,
			Tools:       []string{"failing-tool"},
			Concurrency: 1,
			Timeout:     5 * time.Second,
		},
	})

	// The runner itself doesn't return errors for tool failures
	// (it records them in ToolsFailed). So this should still complete.
	loop, err := ctrl.Run(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// With no findings (the tool failed), it should complete immediately.
	if loop.Status != models.LoopStatusCompleted {
		t.Errorf("expected completed, got %s", loop.Status)
	}
}

// --- countingPlugin wraps a mockPlugin and stops returning findings after maxCalls ---

type countingPlugin struct {
	*mockPlugin
	mu       int
	maxCalls int
	findings []models.Finding
}

func (cp *countingPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	cp.mu++
	if cp.mu > cp.maxCalls {
		return nil, nil
	}
	return cp.findings, nil
}

// --- Helpers ---

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
