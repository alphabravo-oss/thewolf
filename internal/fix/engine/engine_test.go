package engine

import (
	"context"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestNewEngine(t *testing.T) {
	tests := []struct {
		name       string
		engineName string
		wantName   string
		wantErr    bool
	}{
		{name: "claude-code", engineName: "claude-code", wantName: "claude-code"},
		{name: "codex", engineName: "codex", wantName: "codex"},
		{name: "auto", engineName: "auto", wantName: "auto"},
		{name: "empty defaults to auto", engineName: "", wantName: "auto"},
		{name: "unknown engine", engineName: "gpt-9000", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eng, err := NewEngine(tt.engineName)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if eng.Name() != tt.wantName {
				t.Errorf("Name() = %q, want %q", eng.Name(), tt.wantName)
			}
		})
	}
}

func TestClaudeCodeName(t *testing.T) {
	c := &ClaudeCode{}
	if c.Name() != "claude-code" {
		t.Errorf("Name() = %q, want %q", c.Name(), "claude-code")
	}
}

func TestCodexName(t *testing.T) {
	c := &Codex{}
	if c.Name() != "codex" {
		t.Errorf("Name() = %q, want %q", c.Name(), "codex")
	}
}

func TestAutoEngineName(t *testing.T) {
	a := NewAutoEngine()
	if a.Name() != "auto" {
		t.Errorf("Name() = %q, want %q", a.Name(), "auto")
	}
}

func TestBuildPrompt(t *testing.T) {
	tests := []struct {
		name     string
		finding  models.Finding
		contains []string
	}{
		{
			name: "basic finding",
			finding: models.Finding{
				Title:       "SQL Injection",
				FilePath:    "main.go",
				LineStart:   42,
				Severity:    models.SeverityHigh,
				Description: "User input not sanitized",
				Category:    models.CategorySAST,
			},
			contains: []string{
				"SQL Injection",
				"`main.go`",
				"42",
				"high",
				"User input not sanitized",
			},
		},
		{
			name: "finding with code snippet",
			finding: models.Finding{
				Title:       "XSS",
				FilePath:    "app.js",
				LineStart:   10,
				Severity:    models.SeverityMedium,
				Description: "Reflected XSS",
				CodeSnippet: "document.write(input)",
				Category:    models.CategorySAST,
			},
			contains: []string{
				"document.write(input)",
				"Current Code",
			},
		},
		{
			name: "finding with AI suggestion",
			finding: models.Finding{
				Title:           "Buffer overflow",
				FilePath:        "buf.c",
				LineStart:       5,
				Severity:        models.SeverityCritical,
				Description:     "Unbounded copy",
				AIFixSuggestion: "Use strncpy instead",
				Category:        models.CategorySAST,
			},
			contains: []string{
				"Use strncpy instead",
				"Suggested Fix",
			},
		},
		{
			name: "finding with line range",
			finding: models.Finding{
				Title:       "Issue",
				FilePath:    "file.py",
				LineStart:   10,
				LineEnd:     20,
				Severity:    models.SeverityLow,
				Description: "Multi-line issue",
				Category:    models.CategoryQuality,
			},
			contains: []string{
				"10-20",
			},
		},
		{
			name: "finding with tool and rule info",
			finding: models.Finding{
				Title:        "Hardcoded secret",
				FilePath:     "config.go",
				LineStart:    1,
				Severity:     models.SeverityHigh,
				Description:  "Secret in code",
				Category:     models.CategorySecrets,
				ToolName:     "gitleaks",
				RuleID:       "generic-api-key",
				CWEID:        "CWE-798",
				FunctionName: "init",
				ModuleName:   "config",
			},
			contains: []string{
				"gitleaks",
				"generic-api-key",
				"CWE-798",
				"`init`",
				"`config`",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildPrompt(tt.finding)
			for _, want := range tt.contains {
				if !containsSubstring(result, want) {
					t.Errorf("buildPrompt() missing %q in output:\n%s", want, result)
				}
			}
		})
	}
}

func TestParseClaudeOutput(t *testing.T) {
	tests := []struct {
		name        string
		input       []byte
		wantSuccess bool
		wantErr     bool
	}{
		{
			name:        "valid JSON with result",
			input:       []byte(`{"result":"Fixed the issue","session_id":"abc","is_error":false,"total_cost_usd":0.01,"num_turns":3}`),
			wantSuccess: true,
		},
		{
			name:        "error response",
			input:       []byte(`{"result":"API key invalid","is_error":true,"subtype":"error_api"}`),
			wantSuccess: false,
		},
		{
			name:        "max-turns exit treated as success",
			input:       []byte(`{"result":"Partial progress","is_error":true,"subtype":"error_max_turns","num_turns":20}`),
			wantSuccess: true,
		},
		{
			name:        "valid JSON without is_error field",
			input:       []byte(`{"result":"done"}`),
			wantSuccess: true,
		},
		{
			name:        "non-JSON output treated as success",
			input:       []byte("Fixed the issue in main.go"),
			wantSuccess: true,
		},
		{
			name:        "empty output",
			input:       []byte(""),
			wantSuccess: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseClaudeOutput(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Success != tt.wantSuccess {
				t.Errorf("Success = %v, want %v", result.Success, tt.wantSuccess)
			}
		})
	}
}

func TestParseCodexOutput(t *testing.T) {
	result := parseCodexOutput([]byte(`{"type":"message","content":"done"}`))
	if !result.Success {
		t.Error("expected success")
	}
	if result.Output == "" {
		t.Error("expected non-empty output")
	}
}

func TestFixRequestDefaults(t *testing.T) {
	req := FixRequest{
		Finding: models.Finding{
			Title:    "test",
			FilePath: "test.go",
		},
		RepoPath: "/tmp/repo",
	}
	if req.Timeout != 0 {
		t.Error("expected zero timeout as default")
	}
}

func TestDefaultTimeout(t *testing.T) {
	if DefaultTimeout != 5*time.Minute {
		t.Errorf("DefaultTimeout = %v, want %v", DefaultTimeout, 5*time.Minute)
	}
}

// mockEngine implements SubprocessEngine for testing AutoEngine fallback logic.
type mockEngine struct {
	name      string
	available bool
	fixResult *FixResult
	fixErr    error
	fixCalled bool
}

func (m *mockEngine) Name() string { return m.name }
func (m *mockEngine) Available() bool { return m.available }
func (m *mockEngine) Fix(ctx context.Context, req FixRequest) (*FixResult, error) {
	m.fixCalled = true
	return m.fixResult, m.fixErr
}

func TestAutoEngineAvailable(t *testing.T) {
	tests := []struct {
		name    string
		engines []SubprocessEngine
		want    bool
	}{
		{
			name: "one available",
			engines: []SubprocessEngine{
				&mockEngine{name: "a", available: false},
				&mockEngine{name: "b", available: true},
			},
			want: true,
		},
		{
			name: "none available",
			engines: []SubprocessEngine{
				&mockEngine{name: "a", available: false},
				&mockEngine{name: "b", available: false},
			},
			want: false,
		},
		{
			name:    "empty engines",
			engines: []SubprocessEngine{},
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &AutoEngine{engines: tt.engines}
			if got := a.Available(); got != tt.want {
				t.Errorf("Available() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAutoEngineFixFallback(t *testing.T) {
	first := &mockEngine{name: "first", available: false}
	second := &mockEngine{
		name:      "second",
		available: true,
		fixResult: &FixResult{Success: true, Output: "fixed by second"},
	}
	a := &AutoEngine{engines: []SubprocessEngine{first, second}}

	result, err := a.Fix(context.Background(), FixRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if first.fixCalled {
		t.Error("first engine should not have been called")
	}
	if !second.fixCalled {
		t.Error("second engine should have been called")
	}
	if !result.Success {
		t.Error("expected success")
	}
}

func TestAutoEngineNoEngineAvailable(t *testing.T) {
	a := &AutoEngine{engines: []SubprocessEngine{
		&mockEngine{name: "a", available: false},
	}}

	_, err := a.Fix(context.Background(), FixRequest{})
	if err == nil {
		t.Fatal("expected error when no engine available")
	}
	if !containsSubstring(err.Error(), "no fix engine available") {
		t.Errorf("unexpected error message: %s", err.Error())
	}
}

func containsSubstring(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsCheck(s, sub))
}

func containsCheck(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
