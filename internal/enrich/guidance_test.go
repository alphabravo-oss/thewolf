package enrich

import (
	"context"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/ai"
	"github.com/alphabravocompany/thewolf/internal/models"
)

// fakeProvider is a minimal ai.Provider for testing the AIGenerator.
type fakeProvider struct {
	reply string
	err   error
}

func (p fakeProvider) Name() string { return "fake" }
func (p fakeProvider) Analyze(context.Context, ai.AnalyzeRequest) (*ai.AnalyzeResponse, error) {
	return nil, nil
}
func (p fakeProvider) Score(context.Context, ai.ScoreRequest) (*ai.ScoreResponse, error) {
	return nil, nil
}
func (p fakeProvider) Summarize(context.Context, ai.SummarizeRequest) (string, error) {
	return "", nil
}
func (p fakeProvider) Complete(context.Context, string) (string, error) {
	return p.reply, p.err
}

func TestAIGenerator_Prompt_Success(t *testing.T) {
	gen := NewAIGeneratorWithProvider(fakeProvider{reply: "## Problem\nAI-authored guidance here"})
	got, err := gen.Prompt(fullFinding())
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if !strings.Contains(got, "AI-authored guidance") {
		t.Errorf("expected AI text, got: %q", got)
	}
}

func TestAIGenerator_Prompt_ProviderErrorSurfaces(t *testing.T) {
	gen := NewAIGeneratorWithProvider(fakeProvider{err: context.DeadlineExceeded})
	_, err := gen.Prompt(fullFinding())
	if err == nil {
		t.Fatal("expected provider error to surface")
	}
}

func TestAIGenerator_Prompt_EmptyResponseIsError(t *testing.T) {
	gen := NewAIGeneratorWithProvider(fakeProvider{reply: "   "})
	_, err := gen.Prompt(fullFinding())
	if err == nil {
		t.Fatal("expected empty AI response to be treated as an error")
	}
}

// TestNewAIGenerator_NoEngineFailsClearly verifies enrich --ai degrades
// to a clear error rather than silently producing template-only output.
func TestNewAIGenerator_NoEngineFailsClearly(t *testing.T) {
	t.Setenv("WOLF_AI_ENGINE", "")
	if _, err := NewAIGenerator(); err == nil {
		t.Fatal("expected NewAIGenerator to fail when WOLF_AI_ENGINE is unset")
	}
}

// Ensure the deterministic generator is unaffected — the no-AI baseline.
func TestTemplateGenerator_AlwaysSucceeds(t *testing.T) {
	var gen Generator = TemplateGenerator{}
	out, err := gen.Prompt(models.Finding{Title: "x", Severity: models.SeverityLow})
	if err != nil || out == "" {
		t.Fatalf("TemplateGenerator should always succeed, got %q err=%v", out, err)
	}
}
