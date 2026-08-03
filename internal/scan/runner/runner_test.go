package runner

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/finding/identity"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
)

// --- Mock Plugin ---

type mockPlugin struct {
	name        string
	category    models.Category
	languages   []models.Language
	available   bool
	findings    []models.Finding
	execErr     error
	target      *string
	parseErrors int
}

type blockingPlugin struct {
	name      string
	category  models.Category
	languages []models.Language
	available bool
	started   chan<- string
	release   <-chan struct{}
	active    *int32
	maxActive *int32
	timeouts  chan<- time.Duration
}

func (b *blockingPlugin) Name() string                 { return b.name }
func (b *blockingPlugin) Category() models.Category    { return b.category }
func (b *blockingPlugin) Languages() []models.Language { return b.languages }
func (b *blockingPlugin) CheckAvailable() bool         { return b.available }

func (b *blockingPlugin) Execute(ctx context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	if b.timeouts != nil {
		b.timeouts <- opts.Timeout
	}
	current := atomic.AddInt32(b.active, 1)
	for {
		observed := atomic.LoadInt32(b.maxActive)
		if current <= observed || atomic.CompareAndSwapInt32(b.maxActive, observed, current) {
			break
		}
	}
	if b.started != nil {
		b.started <- b.name
	}
	defer atomic.AddInt32(b.active, -1)

	select {
	case <-b.release:
		return nil, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (m *mockPlugin) Name() string                 { return m.name }
func (m *mockPlugin) Category() models.Category    { return m.category }
func (m *mockPlugin) Languages() []models.Language { return m.languages }
func (m *mockPlugin) CheckAvailable() bool         { return m.available }

func (m *mockPlugin) Execute(_ context.Context, opts models.ExecuteOpts) ([]models.Finding, error) {
	if m.target != nil {
		*m.target = opts.Target
	}
	if m.execErr != nil {
		return nil, m.execErr
	}
	for index := 0; index < m.parseErrors; index++ {
		if opts.OnParseError != nil {
			opts.OnParseError(fmt.Errorf("malformed record %d", index+1))
		}
	}
	return m.findings, nil
}

// --- Helper ---

func newRegistry(plugins ...models.Plugin) *plugin.Registry {
	r := plugin.NewRegistry()
	for _, p := range plugins {
		r.Register(p)
	}
	return r
}

func TestRunRecordsStreamingParserErrors(t *testing.T) {
	p := &mockPlugin{name: "streaming-tool", available: true, parseErrors: 2}
	result, err := Run(context.Background(), RunConfig{
		Registry: newRegistry(p), Tools: []string{p.name}, ToolsExplicit: true,
		Concurrency: 1, Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ToolParseErrors[p.name] != 2 {
		t.Fatalf("parse errors = %d, want 2", result.ToolParseErrors[p.name])
	}
}

// --- Tests: Fingerprint ---

func TestFingerprint(t *testing.T) {
	t.Run("uses rule_id when present", func(t *testing.T) {
		fp := Fingerprint("semgrep", "no-eval", "Avoid eval()", "main.py")
		expected := identity.Build(models.Finding{ToolName: "semgrep", RuleID: "no-eval", Title: "Avoid eval()", FilePath: "main.py"}).Stable
		if fp != expected {
			t.Errorf("got %s, want %s", fp, expected)
		}
	})

	t.Run("falls back to title when rule_id is empty", func(t *testing.T) {
		fp := Fingerprint("semgrep", "", "Avoid eval()", "main.py")
		expected := identity.Build(models.Finding{ToolName: "semgrep", Title: "Avoid eval()", FilePath: "main.py"}).Stable
		if fp != expected {
			t.Errorf("got %s, want %s", fp, expected)
		}
	})

	t.Run("different files produce different fingerprints", func(t *testing.T) {
		fp1 := Fingerprint("semgrep", "no-eval", "", "a.py")
		fp2 := Fingerprint("semgrep", "no-eval", "", "b.py")
		if fp1 == fp2 {
			t.Error("expected different fingerprints for different files")
		}
	})

	t.Run("deterministic", func(t *testing.T) {
		fp1 := Fingerprint("gosec", "G101", "Hardcoded creds", "auth.go")
		fp2 := Fingerprint("gosec", "G101", "Hardcoded creds", "auth.go")
		if fp1 != fp2 {
			t.Error("expected identical fingerprints for same input")
		}
	})
}

// --- Tests: Deduplicate ---

func TestDeduplicate(t *testing.T) {
	t.Run("keeps higher severity on duplicate", func(t *testing.T) {
		findings := []models.Finding{
			{
				ToolName:  "semgrep",
				RuleID:    "no-eval",
				FilePath:  "main.py",
				LineStart: 10,
				Severity:  models.SeverityLow,
				Title:     "Eval usage",
			},
			{
				ToolName:  "bandit",
				RuleID:    "no-eval",
				FilePath:  "main.py",
				LineStart: 10,
				Severity:  models.SeverityHigh,
				Title:     "Eval usage",
			},
		}

		result := Deduplicate(findings)
		if len(result) != 1 {
			t.Fatalf("expected 1 finding, got %d", len(result))
		}
		if result[0].Severity != models.SeverityHigh {
			t.Errorf("expected high severity, got %s", result[0].Severity)
		}
		if result[0].ToolName != "bandit" {
			t.Errorf("expected bandit tool, got %s", result[0].ToolName)
		}
	})

	t.Run("keeps first when severity is equal", func(t *testing.T) {
		findings := []models.Finding{
			{
				ToolName:  "tool-a",
				RuleID:    "rule-1",
				FilePath:  "app.go",
				LineStart: 5,
				Severity:  models.SeverityMedium,
			},
			{
				ToolName:  "tool-b",
				RuleID:    "rule-1",
				FilePath:  "app.go",
				LineStart: 5,
				Severity:  models.SeverityMedium,
			},
		}

		result := Deduplicate(findings)
		if len(result) != 1 {
			t.Fatalf("expected 1 finding, got %d", len(result))
		}
		if result[0].ToolName != "tool-a" {
			t.Errorf("expected tool-a (first), got %s", result[0].ToolName)
		}
	})

	t.Run("different lines are not duplicates", func(t *testing.T) {
		findings := []models.Finding{
			{RuleID: "rule-1", FilePath: "main.py", LineStart: 10, Severity: models.SeverityHigh},
			{RuleID: "rule-1", FilePath: "main.py", LineStart: 20, Severity: models.SeverityHigh},
		}

		result := Deduplicate(findings)
		if len(result) != 2 {
			t.Fatalf("expected 2 findings, got %d", len(result))
		}
	})

	t.Run("different rules are not duplicates", func(t *testing.T) {
		findings := []models.Finding{
			{RuleID: "rule-1", FilePath: "main.py", LineStart: 10, Severity: models.SeverityHigh},
			{RuleID: "rule-2", FilePath: "main.py", LineStart: 10, Severity: models.SeverityHigh},
		}

		result := Deduplicate(findings)
		if len(result) != 2 {
			t.Fatalf("expected 2 findings, got %d", len(result))
		}
	})

	t.Run("uses title when rule_id is empty", func(t *testing.T) {
		findings := []models.Finding{
			{Title: "SQL Injection", FilePath: "db.go", LineStart: 42, Severity: models.SeverityLow},
			{Title: "SQL Injection", FilePath: "db.go", LineStart: 42, Severity: models.SeverityCritical},
		}

		result := Deduplicate(findings)
		if len(result) != 1 {
			t.Fatalf("expected 1 finding, got %d", len(result))
		}
		if result[0].Severity != models.SeverityCritical {
			t.Errorf("expected critical severity, got %s", result[0].Severity)
		}
	})

	t.Run("empty input returns empty", func(t *testing.T) {
		result := Deduplicate(nil)
		if len(result) != 0 {
			t.Fatalf("expected 0 findings, got %d", len(result))
		}
	})
}

// --- Tests: SelectTools ---

func TestSelectTools(t *testing.T) {
	goPlugin := &mockPlugin{
		name:      "gosec",
		category:  models.CategorySAST,
		languages: []models.Language{models.LangGo},
		available: true,
	}
	pyPlugin := &mockPlugin{
		name:      "bandit",
		category:  models.CategorySAST,
		languages: []models.Language{models.LangPython},
		available: true,
	}
	jsPlugin := &mockPlugin{
		name:      "eslint",
		category:  models.CategoryQuality,
		languages: []models.Language{models.LangJavaScript},
		available: true,
	}
	allLangPlugin := &mockPlugin{
		name:      "gitleaks",
		category:  models.CategorySecrets,
		languages: nil, // supports all languages
		available: true,
	}

	t.Run("auto mode selects by language", func(t *testing.T) {
		reg := newRegistry(goPlugin, pyPlugin, jsPlugin, allLangPlugin)
		cfg := RunConfig{
			Registry:  reg,
			Languages: []models.Language{models.LangGo},
		}

		selected := SelectTools(cfg)
		names := pluginNames(selected)

		if !contains(names, "gosec") {
			t.Error("expected gosec to be selected")
		}
		if !contains(names, "gitleaks") {
			t.Error("expected gitleaks to be selected (supports all languages)")
		}
		if contains(names, "bandit") {
			t.Error("did not expect bandit for Go language")
		}
	})

	t.Run("explicit tools override auto", func(t *testing.T) {
		reg := newRegistry(goPlugin, pyPlugin, jsPlugin, allLangPlugin)
		cfg := RunConfig{
			Registry:  reg,
			Languages: []models.Language{models.LangGo},
			Tools:     []string{"bandit", "eslint"},
		}

		selected := SelectTools(cfg)
		names := pluginNames(selected)

		if len(names) != 2 {
			t.Fatalf("expected 2 tools, got %d: %v", len(names), names)
		}
		if !contains(names, "bandit") || !contains(names, "eslint") {
			t.Errorf("expected bandit and eslint, got %v", names)
		}
	})

	t.Run("intentional empty selection does not fall back to auto", func(t *testing.T) {
		reg := newRegistry(goPlugin, pyPlugin, jsPlugin, allLangPlugin)
		selected := SelectTools(RunConfig{
			Registry:      reg,
			Languages:     []models.Language{models.LangGo},
			ToolsExplicit: true,
		})
		if len(selected) != 0 {
			t.Fatalf("expected no tools, got %v", pluginNames(selected))
		}
	})

	t.Run("skip-tools excludes from selection", func(t *testing.T) {
		reg := newRegistry(goPlugin, pyPlugin, jsPlugin, allLangPlugin)
		cfg := RunConfig{
			Registry:      reg,
			Languages:     []models.Language{models.LangGo},
			DisabledTools: []string{"gitleaks"},
		}

		selected := SelectTools(cfg)
		names := pluginNames(selected)

		if contains(names, "gitleaks") {
			t.Error("gitleaks should have been skipped")
		}
		if !contains(names, "gosec") {
			t.Error("expected gosec to remain")
		}
	})

	t.Run("skip-tools works with explicit tools", func(t *testing.T) {
		reg := newRegistry(goPlugin, pyPlugin, jsPlugin, allLangPlugin)
		cfg := RunConfig{
			Registry:      reg,
			Tools:         []string{"gosec", "bandit", "eslint"},
			DisabledTools: []string{"bandit"},
		}

		selected := SelectTools(cfg)
		names := pluginNames(selected)

		if contains(names, "bandit") {
			t.Error("bandit should have been skipped")
		}
		if len(names) != 2 {
			t.Fatalf("expected 2 tools, got %d: %v", len(names), names)
		}
	})

	t.Run("no languages and no tools returns all", func(t *testing.T) {
		reg := newRegistry(goPlugin, pyPlugin, jsPlugin, allLangPlugin)
		cfg := RunConfig{
			Registry: reg,
		}

		selected := SelectTools(cfg)
		if len(selected) != 4 {
			t.Errorf("expected 4 tools (all), got %d", len(selected))
		}
	})

	t.Run("unknown tool name is silently ignored", func(t *testing.T) {
		reg := newRegistry(goPlugin)
		cfg := RunConfig{
			Registry: reg,
			Tools:    []string{"gosec", "nonexistent"},
		}

		selected := SelectTools(cfg)
		if len(selected) != 1 {
			t.Fatalf("expected 1 tool, got %d", len(selected))
		}
		if selected[0].Name() != "gosec" {
			t.Errorf("expected gosec, got %s", selected[0].Name())
		}
	})
}

// --- Tests: Run ---

func TestRun(t *testing.T) {
	t.Run("runs available plugins and collects findings", func(t *testing.T) {
		p1 := &mockPlugin{
			name:      "tool-a",
			available: true,
			findings: []models.Finding{
				{ToolName: "tool-a", RuleID: "r1", FilePath: "a.go", LineStart: 1, Severity: models.SeverityHigh},
			},
		}
		p2 := &mockPlugin{
			name:      "tool-b",
			available: true,
			findings: []models.Finding{
				{ToolName: "tool-b", RuleID: "r2", FilePath: "b.go", LineStart: 2, Severity: models.SeverityLow},
			},
		}

		reg := newRegistry(p1, p2)
		result, err := Run(context.Background(), RunConfig{
			Registry:    reg,
			Concurrency: 2,
			Timeout:     5 * time.Second,
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.Findings) != 2 {
			t.Errorf("expected 2 findings, got %d", len(result.Findings))
		}
		if len(result.ToolsRun) != 2 {
			t.Errorf("expected 2 tools run, got %d", len(result.ToolsRun))
		}
		// Verify fingerprints were assigned.
		for _, f := range result.Findings {
			if f.Fingerprint == "" {
				t.Errorf("finding %s has empty fingerprint", f.RuleID)
			}
		}
	})

	t.Run("passes an explicit target to the selected plugin", func(t *testing.T) {
		var target string
		selected := &mockPlugin{name: "target-tool", available: true, target: &target}
		_, err := Run(context.Background(), RunConfig{
			Registry: newRegistry(selected), Tools: []string{"target-tool"},
			ToolsExplicit: true, Target: "http://wolf-quality-nuclei:8080",
		})
		if err != nil {
			t.Fatal(err)
		}
		if target != "http://wolf-quality-nuclei:8080" {
			t.Fatalf("plugin target = %q", target)
		}
	})

	t.Run("skips unavailable plugins", func(t *testing.T) {
		available := &mockPlugin{
			name:      "available-tool",
			available: true,
			findings:  []models.Finding{{ToolName: "available-tool", RuleID: "r1", FilePath: "a.go", LineStart: 1}},
		}
		unavailable := &mockPlugin{
			name:      "unavailable-tool",
			available: false,
		}

		reg := newRegistry(available, unavailable)
		result, err := Run(context.Background(), RunConfig{
			Registry:    reg,
			Concurrency: 2,
			Timeout:     5 * time.Second,
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.ToolsSkipped) != 1 || result.ToolsSkipped[0] != "unavailable-tool" {
			t.Errorf("expected unavailable-tool in skipped, got %v", result.ToolsSkipped)
		}
		if len(result.ToolsRun) != 1 {
			t.Errorf("expected 1 tool run, got %d", len(result.ToolsRun))
		}
	})

	t.Run("records tool failures without cancelling others", func(t *testing.T) {
		good := &mockPlugin{
			name:      "good-tool",
			available: true,
			findings:  []models.Finding{{ToolName: "good-tool", RuleID: "r1", FilePath: "a.go", LineStart: 1}},
		}
		bad := &mockPlugin{
			name:      "bad-tool",
			available: true,
			execErr:   fmt.Errorf("tool crashed"),
		}

		reg := newRegistry(good, bad)
		result, err := Run(context.Background(), RunConfig{
			Registry:    reg,
			Concurrency: 2,
			Timeout:     5 * time.Second,
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.ToolsFailed) != 1 {
			t.Fatalf("expected 1 failed tool, got %d", len(result.ToolsFailed))
		}
		if _, ok := result.ToolsFailed["bad-tool"]; !ok {
			t.Error("expected bad-tool in failures")
		}
		if len(result.Findings) != 1 {
			t.Errorf("expected 1 finding from good-tool, got %d", len(result.Findings))
		}
	})

	t.Run("deduplicates across tools", func(t *testing.T) {
		p1 := &mockPlugin{
			name:      "tool-a",
			available: true,
			findings: []models.Finding{
				{ToolName: "tool-a", RuleID: "sql-inject", FilePath: "db.go", LineStart: 55, Severity: models.SeverityMedium},
			},
		}
		p2 := &mockPlugin{
			name:      "tool-b",
			available: true,
			findings: []models.Finding{
				{ToolName: "tool-b", RuleID: "sql-inject", FilePath: "db.go", LineStart: 55, Severity: models.SeverityCritical},
			},
		}

		reg := newRegistry(p1, p2)
		result, err := Run(context.Background(), RunConfig{
			Registry:    reg,
			Concurrency: 2,
			Timeout:     5 * time.Second,
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.Findings) != 1 {
			t.Fatalf("expected 1 deduplicated finding, got %d", len(result.Findings))
		}
		if result.Findings[0].Severity != models.SeverityCritical {
			t.Errorf("expected critical severity after dedup, got %s", result.Findings[0].Severity)
		}
	})

	t.Run("calls OnToolStart and OnToolDone callbacks", func(t *testing.T) {
		p := &mockPlugin{
			name:      "callback-tool",
			available: true,
			findings:  []models.Finding{{ToolName: "callback-tool", RuleID: "r1", FilePath: "x.go", LineStart: 1}},
		}

		var started, done bool
		reg := newRegistry(p)
		_, err := Run(context.Background(), RunConfig{
			Registry:    reg,
			Concurrency: 1,
			Timeout:     5 * time.Second,
			OnToolStart: func(name string) { started = true },
			OnToolDone:  func(name string, findings []models.Finding, e error) { done = true },
		})

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !started {
			t.Error("OnToolStart was not called")
		}
		if !done {
			t.Error("OnToolDone was not called")
		}
	})

	t.Run("limits heavy scanner concurrency", func(t *testing.T) {
		release := make(chan struct{})
		started := make(chan string, 3)
		var activeHeavy int32
		var maxActiveHeavy int32
		var activeLight int32
		var maxActiveLight int32

		heavyA := &blockingPlugin{
			name:      "heavy-a",
			category:  models.CategorySCA,
			available: true,
			started:   started,
			release:   release,
			active:    &activeHeavy,
			maxActive: &maxActiveHeavy,
		}
		heavyB := &blockingPlugin{
			name:      "heavy-b",
			category:  models.CategoryContainer,
			available: true,
			started:   started,
			release:   release,
			active:    &activeHeavy,
			maxActive: &maxActiveHeavy,
		}
		light := &blockingPlugin{
			name:      "light",
			category:  models.CategorySecrets,
			available: true,
			started:   started,
			release:   release,
			active:    &activeLight,
			maxActive: &maxActiveLight,
		}

		reg := newRegistry(heavyA, heavyB, light)
		done := make(chan error, 1)
		go func() {
			_, err := Run(context.Background(), RunConfig{
				Registry:         reg,
				Tools:            []string{"heavy-a", "heavy-b", "light"},
				Concurrency:      3,
				HeavyConcurrency: 1,
				Timeout:          5 * time.Second,
			})
			done <- err
		}()

		for i := 0; i < 2; i++ {
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("timed out waiting for initial tools to start")
			}
		}

		select {
		case tool := <-started:
			t.Fatalf("unexpected third tool started before heavy slot released: %s", tool)
		case <-time.After(50 * time.Millisecond):
		}

		if got := atomic.LoadInt32(&maxActiveHeavy); got != 1 {
			t.Fatalf("expected at most one active heavy scanner, got %d", got)
		}

		close(release)

		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for run to finish")
		}
	})

	t.Run("limits network scanners and applies per-tool timeout", func(t *testing.T) {
		release := make(chan struct{})
		started := make(chan string, 2)
		timeouts := make(chan time.Duration, 2)
		var activeNetwork int32
		var maxActiveNetwork int32

		networkA := &blockingPlugin{
			name:      "network-a",
			category:  models.CategoryQuality,
			available: true,
			started:   started,
			release:   release,
			active:    &activeNetwork,
			maxActive: &maxActiveNetwork,
			timeouts:  timeouts,
		}
		networkB := &blockingPlugin{
			name:      "network-b",
			category:  models.CategoryQuality,
			available: true,
			started:   started,
			release:   release,
			active:    &activeNetwork,
			maxActive: &maxActiveNetwork,
			timeouts:  timeouts,
		}

		reg := newRegistry(networkA, networkB)
		done := make(chan error, 1)
		go func() {
			_, err := Run(context.Background(), RunConfig{
				Registry:           reg,
				Tools:              []string{"network-a", "network-b"},
				Concurrency:        2,
				NetworkConcurrency: 1,
				Timeout:            10 * time.Second,
				ToolResources: map[string]ResourceSpec{
					"network-a": {Class: "network", Timeout: 3 * time.Second, NetworkRequired: true},
					"network-b": {Class: "network", Timeout: 3 * time.Second, NetworkRequired: true},
				},
			})
			done <- err
		}()

		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for first network scanner to start")
		}
		select {
		case got := <-timeouts:
			if got != 3*time.Second {
				t.Fatalf("tool timeout = %s, want 3s", got)
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for tool timeout")
		}

		select {
		case tool := <-started:
			t.Fatalf("unexpected second network tool started before slot released: %s", tool)
		case <-time.After(50 * time.Millisecond):
		}

		if got := atomic.LoadInt32(&maxActiveNetwork); got != 1 {
			t.Fatalf("expected at most one active network scanner, got %d", got)
		}

		close(release)

		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for run to finish")
		}
	})
}

// --- Helpers ---

func pluginNames(plugins []models.Plugin) []string {
	names := make([]string, len(plugins))
	for i, p := range plugins {
		names[i] = p.Name()
	}
	return names
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
