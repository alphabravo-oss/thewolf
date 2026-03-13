// Package tui provides a bubbletea-based terminal UI for displaying scan progress.
package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ToolStatus represents the current state of a scanning tool.
type ToolStatus int

const (
	ToolWaiting ToolStatus = iota
	ToolRunning
	ToolDone
	ToolFailed
)

// ToolState holds the runtime state for a single tool in the scan.
type ToolState struct {
	Name      string
	Status    ToolStatus
	Findings  int
	Error     string
	StartedAt time.Time
	DoneAt    time.Time
}

// Model is the bubbletea model for the scan progress TUI.
type Model struct {
	Tools         []ToolState
	TotalFindings int
	BySeverity    map[string]int // severity -> count
	StartedAt     time.Time
	Done          bool
	Error         string
}

// --- Messages sent to the TUI via tea.Program.Send() ---

// ToolStartMsg indicates a tool has begun scanning.
type ToolStartMsg struct{ Name string }

// ToolDoneMsg indicates a tool has finished scanning.
type ToolDoneMsg struct {
	Name     string
	Findings int
}

// ToolFailMsg indicates a tool has failed.
type ToolFailMsg struct {
	Name  string
	Error string
}

// FindingMsg reports a single finding with a severity level.
type FindingMsg struct{ Severity string }

// ScanDoneMsg indicates the entire scan is complete.
type ScanDoneMsg struct{ Error string }

// tickMsg is an internal message for refreshing elapsed time display.
type tickMsg time.Time

// --- Styles ---

var (
	styleBold     = lipgloss.NewStyle().Bold(true)
	styleRunning  = lipgloss.NewStyle().Foreground(lipgloss.Color("11")) // yellow
	styleDone     = lipgloss.NewStyle().Foreground(lipgloss.Color("10")) // green
	styleFailed   = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))  // red
	styleWaiting  = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))  // gray
	styleHeader   = lipgloss.NewStyle().Bold(true).Underline(true)
	styleCritical = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)  // red
	styleHigh     = lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Bold(true) // yellow
	styleMedium   = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))            // yellow
	styleLow      = lipgloss.NewStyle().Foreground(lipgloss.Color("12"))            // blue
	styleInfo     = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))             // gray
)

// progressBar renders a simple Unicode block progress bar.
// ratio should be between 0.0 and 1.0. width is the total character width.
func progressBar(ratio float64, width int) string {
	if ratio < 0 {
		ratio = 0
	}
	if ratio > 1 {
		ratio = 1
	}
	filled := int(ratio * float64(width))
	empty := width - filled
	return strings.Repeat("█", filled) + strings.Repeat("░", empty)
}

// NewModel creates a new TUI model pre-populated with the given tool names.
func NewModel(toolNames []string) Model {
	tools := make([]ToolState, len(toolNames))
	for i, name := range toolNames {
		tools[i] = ToolState{
			Name:   name,
			Status: ToolWaiting,
		}
	}
	return Model{
		Tools:      tools,
		BySeverity: make(map[string]int),
		StartedAt:  time.Now(),
	}
}

// Init returns the initial command (a tick to start the clock).
func (m Model) Init() tea.Cmd {
	return doTick()
}

func doTick() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

// Update handles incoming messages and updates model state.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		}

	case tickMsg:
		if m.Done {
			return m, nil
		}
		return m, doTick()

	case ToolStartMsg:
		for i := range m.Tools {
			if m.Tools[i].Name == msg.Name {
				m.Tools[i].Status = ToolRunning
				m.Tools[i].StartedAt = time.Now()
				break
			}
		}

	case ToolDoneMsg:
		for i := range m.Tools {
			if m.Tools[i].Name == msg.Name {
				m.Tools[i].Status = ToolDone
				m.Tools[i].Findings = msg.Findings
				m.Tools[i].DoneAt = time.Now()
				break
			}
		}

	case ToolFailMsg:
		for i := range m.Tools {
			if m.Tools[i].Name == msg.Name {
				m.Tools[i].Status = ToolFailed
				m.Tools[i].Error = msg.Error
				m.Tools[i].DoneAt = time.Now()
				break
			}
		}

	case FindingMsg:
		m.TotalFindings++
		sev := strings.ToLower(msg.Severity)
		if sev == "" {
			sev = "info"
		}
		m.BySeverity[sev]++

	case ScanDoneMsg:
		m.Done = true
		m.Error = msg.Error
		return m, tea.Quit
	}

	return m, nil
}

// View renders the current state of the TUI.
func (m Model) View() string {
	var b strings.Builder

	elapsed := time.Since(m.StartedAt).Truncate(time.Millisecond)
	b.WriteString(styleHeader.Render("Scan Progress"))
	b.WriteString(fmt.Sprintf("  %s\n\n", elapsed))

	// Count completed tools for overall progress.
	done := 0
	for _, t := range m.Tools {
		if t.Status == ToolDone || t.Status == ToolFailed {
			done++
		}
	}
	total := len(m.Tools)
	ratio := 0.0
	if total > 0 {
		ratio = float64(done) / float64(total)
	}
	b.WriteString(fmt.Sprintf("  Overall: %s %d/%d tools\n\n", progressBar(ratio, 20), done, total))

	// Per-tool rows.
	for _, t := range m.Tools {
		b.WriteString(renderTool(t))
		b.WriteByte('\n')
	}

	// Findings summary.
	b.WriteByte('\n')
	b.WriteString(styleBold.Render(fmt.Sprintf("  Findings: %d total", m.TotalFindings)))
	if len(m.BySeverity) > 0 {
		b.WriteString("  ")
		b.WriteString(renderSeverities(m.BySeverity))
	}
	b.WriteByte('\n')

	if m.Done {
		b.WriteByte('\n')
		if m.Error != "" {
			b.WriteString(styleFailed.Render(fmt.Sprintf("  Scan failed: %s", m.Error)))
		} else {
			b.WriteString(styleDone.Render("  Scan complete."))
		}
		b.WriteByte('\n')
	}

	return b.String()
}

// renderTool renders a single tool's status line.
func renderTool(t ToolState) string {
	name := styleBold.Render(fmt.Sprintf("  %-20s", t.Name))

	switch t.Status {
	case ToolWaiting:
		return fmt.Sprintf("%s %s", name, styleWaiting.Render("◌ waiting"))

	case ToolRunning:
		elapsed := time.Since(t.StartedAt).Truncate(time.Millisecond)
		bar := styleRunning.Render(progressBar(0.5, 10)) // indeterminate
		return fmt.Sprintf("%s %s %s %s",
			name,
			styleRunning.Render("▸ running"),
			bar,
			styleWaiting.Render(elapsed.String()),
		)

	case ToolDone:
		elapsed := t.DoneAt.Sub(t.StartedAt).Truncate(time.Millisecond)
		findings := ""
		if t.Findings > 0 {
			findings = fmt.Sprintf("  %d findings", t.Findings)
		}
		return fmt.Sprintf("%s %s %s%s",
			name,
			styleDone.Render("✓ done"),
			styleWaiting.Render(elapsed.String()),
			findings,
		)

	case ToolFailed:
		elapsed := t.DoneAt.Sub(t.StartedAt).Truncate(time.Millisecond)
		errStr := ""
		if t.Error != "" {
			errStr = fmt.Sprintf("  %s", t.Error)
		}
		return fmt.Sprintf("%s %s %s%s",
			name,
			styleFailed.Render("✗ failed"),
			styleWaiting.Render(elapsed.String()),
			styleFailed.Render(errStr),
		)

	default:
		return name
	}
}

// renderSeverities renders the severity breakdown with colors.
func renderSeverities(m map[string]int) string {
	order := []string{"critical", "high", "medium", "low", "info"}
	var parts []string
	for _, sev := range order {
		count, ok := m[sev]
		if !ok || count == 0 {
			continue
		}
		label := fmt.Sprintf("%s:%d", sev, count)
		switch sev {
		case "critical":
			label = styleCritical.Render(label)
		case "high":
			label = styleHigh.Render(label)
		case "medium":
			label = styleMedium.Render(label)
		case "low":
			label = styleLow.Render(label)
		case "info":
			label = styleInfo.Render(label)
		}
		parts = append(parts, label)
	}
	// Include any severities not in the standard set.
	for sev, count := range m {
		switch sev {
		case "critical", "high", "medium", "low", "info":
			continue
		}
		if count > 0 {
			parts = append(parts, fmt.Sprintf("%s:%d", sev, count))
		}
	}
	return strings.Join(parts, "  ")
}

// RunTUI creates a bubbletea program with the given tool names and returns it.
// The caller controls the scan lifecycle by sending messages via p.Send(),
// for example:
//
//	p, _ := tui.RunTUI([]string{"gosec", "semgrep"})
//	go p.Run()
//	p.Send(tui.ToolStartMsg{Name: "gosec"})
//	p.Send(tui.FindingMsg{Severity: "high"})
//	p.Send(tui.ToolDoneMsg{Name: "gosec", Findings: 1})
//	p.Send(tui.ScanDoneMsg{})
func RunTUI(toolNames []string) (*tea.Program, error) {
	m := NewModel(toolNames)
	p := tea.NewProgram(m)
	return p, nil
}
