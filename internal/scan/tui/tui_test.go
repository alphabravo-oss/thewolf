package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestNewModel(t *testing.T) {
	tests := []struct {
		name      string
		tools     []string
		wantCount int
	}{
		{name: "multiple tools", tools: []string{"gosec", "semgrep", "trivy"}, wantCount: 3},
		{name: "single tool", tools: []string{"gosec"}, wantCount: 1},
		{name: "empty tools", tools: []string{}, wantCount: 0},
		{name: "nil tools", tools: nil, wantCount: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewModel(tt.tools)
			if len(m.Tools) != tt.wantCount {
				t.Errorf("Tools count = %d, want %d", len(m.Tools), tt.wantCount)
			}
			if m.BySeverity == nil {
				t.Error("BySeverity map should be initialized")
			}
			if m.Done {
				t.Error("Done should be false initially")
			}
			if m.TotalFindings != 0 {
				t.Error("TotalFindings should be 0 initially")
			}
			// All tools should start in waiting state
			for i, tool := range m.Tools {
				if tool.Status != ToolWaiting {
					t.Errorf("tool[%d].Status = %v, want ToolWaiting", i, tool.Status)
				}
				if tool.Name != tt.tools[i] {
					t.Errorf("tool[%d].Name = %q, want %q", i, tool.Name, tt.tools[i])
				}
			}
		})
	}
}

func TestUpdateToolStartMsg(t *testing.T) {
	m := NewModel([]string{"gosec", "semgrep"})

	updated, _ := m.Update(ToolStartMsg{Name: "gosec"})
	model := updated.(Model)

	if model.Tools[0].Status != ToolRunning {
		t.Errorf("tool status = %v, want ToolRunning", model.Tools[0].Status)
	}
	if model.Tools[0].StartedAt.IsZero() {
		t.Error("StartedAt should be set")
	}
	// semgrep should still be waiting
	if model.Tools[1].Status != ToolWaiting {
		t.Errorf("semgrep status = %v, want ToolWaiting", model.Tools[1].Status)
	}
}

func TestUpdateToolDoneMsg(t *testing.T) {
	m := NewModel([]string{"gosec"})
	m.Tools[0].Status = ToolRunning

	updated, _ := m.Update(ToolDoneMsg{Name: "gosec", Findings: 5})
	model := updated.(Model)

	if model.Tools[0].Status != ToolDone {
		t.Errorf("tool status = %v, want ToolDone", model.Tools[0].Status)
	}
	if model.Tools[0].Findings != 5 {
		t.Errorf("Findings = %d, want 5", model.Tools[0].Findings)
	}
	if model.Tools[0].DoneAt.IsZero() {
		t.Error("DoneAt should be set")
	}
}

func TestUpdateToolFailMsg(t *testing.T) {
	m := NewModel([]string{"gosec"})
	m.Tools[0].Status = ToolRunning

	updated, _ := m.Update(ToolFailMsg{Name: "gosec", Error: "command not found"})
	model := updated.(Model)

	if model.Tools[0].Status != ToolFailed {
		t.Errorf("tool status = %v, want ToolFailed", model.Tools[0].Status)
	}
	if model.Tools[0].Error != "command not found" {
		t.Errorf("Error = %q, want %q", model.Tools[0].Error, "command not found")
	}
}

func TestUpdateFindingMsg(t *testing.T) {
	m := NewModel([]string{"gosec"})

	tests := []struct {
		severity   string
		wantSev    string
		wantTotal  int
	}{
		{"HIGH", "high", 1},
		{"critical", "critical", 2},
		{"", "info", 3},
		{"low", "low", 4},
	}

	var model tea.Model = m
	for _, tt := range tests {
		updated, _ := model.Update(FindingMsg{Severity: tt.severity})
		model = updated
	}

	final := model.(Model)
	if final.TotalFindings != 4 {
		t.Errorf("TotalFindings = %d, want 4", final.TotalFindings)
	}
	if final.BySeverity["high"] != 1 {
		t.Errorf("high count = %d, want 1", final.BySeverity["high"])
	}
	if final.BySeverity["info"] != 1 {
		t.Errorf("info count = %d, want 1", final.BySeverity["info"])
	}
}

func TestUpdateScanDoneMsg(t *testing.T) {
	m := NewModel([]string{"gosec"})

	updated, cmd := m.Update(ScanDoneMsg{Error: ""})
	model := updated.(Model)

	if !model.Done {
		t.Error("Done should be true after ScanDoneMsg")
	}
	if model.Error != "" {
		t.Error("Error should be empty on success")
	}
	// Should return tea.Quit
	if cmd == nil {
		t.Error("expected tea.Quit command")
	}
}

func TestUpdateScanDoneMsgWithError(t *testing.T) {
	m := NewModel([]string{"gosec"})

	updated, _ := m.Update(ScanDoneMsg{Error: "scan timeout"})
	model := updated.(Model)

	if !model.Done {
		t.Error("Done should be true")
	}
	if model.Error != "scan timeout" {
		t.Errorf("Error = %q, want %q", model.Error, "scan timeout")
	}
}

func TestUpdateTickMsgWhenNotDone(t *testing.T) {
	m := NewModel([]string{"gosec"})

	_, cmd := m.Update(tickMsg{})
	if cmd == nil {
		t.Error("tick should schedule another tick when not done")
	}
}

func TestUpdateTickMsgWhenDone(t *testing.T) {
	m := NewModel([]string{"gosec"})
	m.Done = true

	_, cmd := m.Update(tickMsg{})
	if cmd != nil {
		t.Error("tick should not schedule when done")
	}
}

func TestUpdateUnknownToolName(t *testing.T) {
	m := NewModel([]string{"gosec"})

	// Sending a message for an unknown tool should not panic or change anything
	updated, _ := m.Update(ToolStartMsg{Name: "unknown"})
	model := updated.(Model)

	if model.Tools[0].Status != ToolWaiting {
		t.Error("existing tool should remain unchanged")
	}
}

func TestProgressBar(t *testing.T) {
	tests := []struct {
		name  string
		ratio float64
		width int
	}{
		{name: "zero", ratio: 0, width: 10},
		{name: "half", ratio: 0.5, width: 10},
		{name: "full", ratio: 1.0, width: 10},
		{name: "negative clamped", ratio: -0.5, width: 10},
		{name: "over one clamped", ratio: 1.5, width: 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bar := progressBar(tt.ratio, tt.width)
			// Count runes since we use multi-byte characters
			runeCount := 0
			for range bar {
				runeCount++
			}
			if runeCount != tt.width {
				t.Errorf("progressBar width = %d runes, want %d", runeCount, tt.width)
			}
		})
	}
}

func TestProgressBarContent(t *testing.T) {
	bar := progressBar(0.0, 5)
	if bar != "░░░░░" {
		t.Errorf("0%% bar = %q, want all empty", bar)
	}

	bar = progressBar(1.0, 5)
	if bar != "█████" {
		t.Errorf("100%% bar = %q, want all filled", bar)
	}
}

func TestViewContainsScanProgress(t *testing.T) {
	m := NewModel([]string{"gosec", "semgrep"})
	view := m.View()

	if !strings.Contains(view, "Scan Progress") {
		t.Error("View should contain 'Scan Progress'")
	}
	if !strings.Contains(view, "gosec") {
		t.Error("View should contain tool name 'gosec'")
	}
	if !strings.Contains(view, "semgrep") {
		t.Error("View should contain tool name 'semgrep'")
	}
	if !strings.Contains(view, "Findings: 0 total") {
		t.Error("View should contain findings count")
	}
}

func TestViewAfterScanDone(t *testing.T) {
	m := NewModel([]string{"gosec"})
	m.Done = true
	view := m.View()

	if !strings.Contains(view, "Scan complete") {
		t.Error("View should contain 'Scan complete' when done without error")
	}
}

func TestViewAfterScanFailed(t *testing.T) {
	m := NewModel([]string{"gosec"})
	m.Done = true
	m.Error = "timeout"
	view := m.View()

	if !strings.Contains(view, "Scan failed") {
		t.Error("View should contain 'Scan failed' when done with error")
	}
	if !strings.Contains(view, "timeout") {
		t.Error("View should contain error message")
	}
}

func TestViewShowsSeverities(t *testing.T) {
	m := NewModel([]string{"gosec"})
	m.TotalFindings = 3
	m.BySeverity["critical"] = 1
	m.BySeverity["high"] = 2
	view := m.View()

	if !strings.Contains(view, "critical:1") {
		t.Error("View should show critical count")
	}
	if !strings.Contains(view, "high:2") {
		t.Error("View should show high count")
	}
}

func TestToolStatusConstants(t *testing.T) {
	if ToolWaiting != 0 {
		t.Error("ToolWaiting should be 0")
	}
	if ToolRunning != 1 {
		t.Error("ToolRunning should be 1")
	}
	if ToolDone != 2 {
		t.Error("ToolDone should be 2")
	}
	if ToolFailed != 3 {
		t.Error("ToolFailed should be 3")
	}
}

func TestRunTUI(t *testing.T) {
	p, err := RunTUI([]string{"gosec", "semgrep"})
	if err != nil {
		t.Fatalf("RunTUI error: %v", err)
	}
	if p == nil {
		t.Fatal("expected non-nil program")
	}
}

func TestRenderSeverities(t *testing.T) {
	tests := []struct {
		name     string
		m        map[string]int
		contains []string
	}{
		{
			name:     "standard severities",
			m:        map[string]int{"critical": 1, "high": 2, "medium": 3},
			contains: []string{"critical:1", "high:2", "medium:3"},
		},
		{
			name:     "non-standard severity included",
			m:        map[string]int{"custom": 1},
			contains: []string{"custom:1"},
		},
		{
			name:     "zero count excluded",
			m:        map[string]int{"critical": 0, "high": 1},
			contains: []string{"high:1"},
		},
		{
			name:     "empty map",
			m:        map[string]int{},
			contains: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := renderSeverities(tt.m)
			for _, want := range tt.contains {
				if !strings.Contains(result, want) {
					t.Errorf("renderSeverities() missing %q in %q", want, result)
				}
			}
		})
	}
}

func TestInitReturnsCmd(t *testing.T) {
	m := NewModel([]string{"gosec"})
	cmd := m.Init()
	if cmd == nil {
		t.Error("Init should return a tick command")
	}
}
