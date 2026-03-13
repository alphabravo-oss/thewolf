package ai

import (
	"context"
	"testing"
)

// ---------------------------------------------------------------------------
// TestNoopProvider
// ---------------------------------------------------------------------------

func TestNoopProvider(t *testing.T) {
	p := NewNoopProvider()

	t.Run("Name returns noop", func(t *testing.T) {
		if p.Name() != "noop" {
			t.Errorf("Name() = %q, want %q", p.Name(), "noop")
		}
	})

	t.Run("Analyze returns default response", func(t *testing.T) {
		resp, err := p.Analyze(context.Background(), AnalyzeRequest{
			Finding: FindingContext{
				ToolName: "semgrep",
				Title:    "Eval usage",
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp == nil {
			t.Fatal("expected non-nil response")
		}
		if resp.ContextScore != 5.0 {
			t.Errorf("ContextScore = %f, want 5.0", resp.ContextScore)
		}
		if resp.FixSuggestion != "" {
			t.Errorf("expected empty FixSuggestion, got %q", resp.FixSuggestion)
		}
		if resp.Explanation == "" {
			t.Error("expected non-empty Explanation")
		}
	})

	t.Run("Score returns default scores for all findings", func(t *testing.T) {
		findings := []FindingContext{
			{ToolName: "tool-a", Title: "finding-1"},
			{ToolName: "tool-b", Title: "finding-2"},
			{ToolName: "tool-c", Title: "finding-3"},
		}
		resp, err := p.Score(context.Background(), ScoreRequest{Findings: findings})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(resp.Scores) != 3 {
			t.Fatalf("expected 3 scores, got %d", len(resp.Scores))
		}
		for i, s := range resp.Scores {
			if s.Index != i {
				t.Errorf("Scores[%d].Index = %d, want %d", i, s.Index, i)
			}
			if s.ContextScore != 5.0 {
				t.Errorf("Scores[%d].ContextScore = %f, want 5.0", i, s.ContextScore)
			}
			if s.Explanation == "" {
				t.Errorf("Scores[%d].Explanation is empty", i)
			}
		}
	})

	t.Run("Score with empty findings returns empty scores", func(t *testing.T) {
		resp, err := p.Score(context.Background(), ScoreRequest{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(resp.Scores) != 0 {
			t.Errorf("expected 0 scores, got %d", len(resp.Scores))
		}
	})

	t.Run("Summarize returns formatted summary", func(t *testing.T) {
		summary, err := p.Summarize(context.Background(), SummarizeRequest{
			RepoName:      "test-repo",
			ScanID:        "scan-123",
			TotalFindings: 42,
			BySeverity:    map[string]int{"high": 10, "low": 32},
			ByTool:        map[string]int{"semgrep": 25, "trivy": 17},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if summary == "" {
			t.Fatal("expected non-empty summary")
		}
		// Check key content is present.
		checks := []string{"test-repo", "scan-123", "42"}
		for _, want := range checks {
			if !containsStr(summary, want) {
				t.Errorf("summary missing %q", want)
			}
		}
	})

	t.Run("Summarize with empty request still works", func(t *testing.T) {
		summary, err := p.Summarize(context.Background(), SummarizeRequest{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if summary == "" {
			t.Error("expected non-empty summary even with empty request")
		}
	})
}

// ---------------------------------------------------------------------------
// TestProviderInterface
// ---------------------------------------------------------------------------

func TestProviderInterface(t *testing.T) {
	t.Run("NoopProvider implements Provider", func(t *testing.T) {
		var _ Provider = (*NoopProvider)(nil)
	})
}

// --- Helpers ---

func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && searchStr(s, substr)
}

func searchStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
