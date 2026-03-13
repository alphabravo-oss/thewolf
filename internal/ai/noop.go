package ai

import (
	"context"
	"fmt"
	"strings"
)

// NoopProvider is a fallback provider that returns sensible defaults without
// calling any external API. Use it when no API key is configured.
type NoopProvider struct {
	logCallback LogCallback
}

// SetLogCallback configures the logging callback (no-op provider rarely fires it).
func (p *NoopProvider) SetLogCallback(cb LogCallback) {
	p.logCallback = cb
}

// NewNoopProvider returns a new no-op AI provider.
func NewNoopProvider() *NoopProvider {
	return &NoopProvider{}
}

func (p *NoopProvider) Name() string {
	return "noop"
}

func (p *NoopProvider) Analyze(_ context.Context, req AnalyzeRequest) (*AnalyzeResponse, error) {
	return &AnalyzeResponse{
		FixSuggestion: "",
		ContextScore:  5.0,
		Explanation:   fmt.Sprintf("AI analysis unavailable. Raw finding from %s: %s", req.Finding.ToolName, req.Finding.Title),
	}, nil
}

func (p *NoopProvider) Score(_ context.Context, req ScoreRequest) (*ScoreResponse, error) {
	scores := make([]FindingScore, len(req.Findings))
	for i := range req.Findings {
		scores[i] = FindingScore{
			Index:        i,
			ContextScore: 5.0,
			Explanation:  "AI scoring unavailable; default score assigned.",
		}
	}
	return &ScoreResponse{Scores: scores}, nil
}

func (p *NoopProvider) Complete(_ context.Context, _ string) (string, error) {
	return "", fmt.Errorf("AI completion unavailable (noop provider)")
}

func (p *NoopProvider) Summarize(_ context.Context, req SummarizeRequest) (string, error) {
	var b strings.Builder

	fmt.Fprintf(&b, "Scan Summary for %s (scan %s)\n", req.RepoName, req.ScanID)
	fmt.Fprintf(&b, "Total findings: %d\n\n", req.TotalFindings)

	if len(req.BySeverity) > 0 {
		b.WriteString("By severity:\n")
		for sev, count := range req.BySeverity {
			fmt.Fprintf(&b, "  %s: %d\n", sev, count)
		}
		b.WriteString("\n")
	}

	if len(req.ByTool) > 0 {
		b.WriteString("By tool:\n")
		for tool, count := range req.ByTool {
			fmt.Fprintf(&b, "  %s: %d\n", tool, count)
		}
		b.WriteString("\n")
	}

	b.WriteString("Note: AI-powered summary unavailable. Configure an API key for richer analysis.")

	return b.String(), nil
}
