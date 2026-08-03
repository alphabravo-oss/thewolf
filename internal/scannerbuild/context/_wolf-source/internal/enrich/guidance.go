package enrich

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/alphabravocompany/thewolf/internal/ai"
	"github.com/alphabravocompany/thewolf/internal/models"
)

// AIGenerator is the optional AI-backed enrichment generator. It produces
// the deterministic template first, then asks the configured AI provider
// to expand it into specific, codebase-aware remediation guidance.
//
// If the provider call fails, Prompt returns the error — enrichment fails
// loudly rather than silently degrading, so a misconfigured provider is
// obvious. (Callers that want a graceful fallback use TemplateGenerator.)
type AIGenerator struct {
	provider ai.Provider
	timeout  time.Duration
}

// NewAIGenerator builds an AIGenerator from environment configuration:
// WOLF_AI_ENGINE (anthropic | openai | claude-code | codex) and, for
// key-based engines, WOLF_AI_API_KEY. Returns an error when no usable
// provider is configured, so `enrich --ai` fails with a clear message
// instead of silently producing template-only output.
func NewAIGenerator() (*AIGenerator, error) {
	engine := strings.TrimSpace(os.Getenv("WOLF_AI_ENGINE"))
	if engine == "" {
		return nil, fmt.Errorf("WOLF_AI_ENGINE is not set")
	}
	key := strings.TrimSpace(os.Getenv("WOLF_AI_API_KEY"))
	provider := ai.NewProvider(engine, key)
	if provider == nil || provider.Name() == "noop" {
		return nil, fmt.Errorf("no usable AI provider for engine %q (missing API key?)", engine)
	}
	return &AIGenerator{provider: provider, timeout: 90 * time.Second}, nil
}

// NewAIGeneratorWithProvider wraps an explicit provider — used by tests
// and by callers that resolve the provider themselves (e.g. server-side).
func NewAIGeneratorWithProvider(p ai.Provider) *AIGenerator {
	return &AIGenerator{provider: p, timeout: 90 * time.Second}
}

// Prompt returns AI-authored remediation guidance for the finding.
func (g *AIGenerator) Prompt(f models.Finding) (string, error) {
	base := BuildPrompt(f)
	ctx, cancel := context.WithTimeout(context.Background(), g.timeout)
	defer cancel()

	meta := "You are a security remediation assistant. Below is a code " +
		"finding reported by a scanner, with a templated remediation " +
		"prompt. Produce an improved remediation prompt: keep the same " +
		"five sections (Problem, Location, Repo context, Task, " +
		"Acceptance criteria), but make the Task and Acceptance criteria " +
		"specific and actionable for this exact code. Do NOT invent new " +
		"findings. If the finding looks like a false positive, say so in " +
		"the Task section. Return only the prompt, no preamble.\n\n" +
		base

	out, err := g.provider.Complete(ctx, meta)
	if err != nil {
		return "", fmt.Errorf("AI provider %s: %w", g.provider.Name(), err)
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return "", fmt.Errorf("AI provider %s returned an empty response", g.provider.Name())
	}
	return out, nil
}
