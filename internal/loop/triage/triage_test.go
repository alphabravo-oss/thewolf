package triage

import (
	"context"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/ai"
	"github.com/alphabravocompany/thewolf/internal/models"
)

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

func sampleFindings() []models.Finding {
	return []models.Finding{
		{ID: "1", Title: "SQLi", Severity: models.SeverityHigh, Status: models.StatusOpen},
		{ID: "2", Title: "hardcoded secret in test", Severity: models.SeverityMedium, Status: models.StatusOpen},
	}
}

func TestTriage_DismissesFalsePositive(t *testing.T) {
	prov := fakeProvider{reply: `[
	  {"id":"1","valid":true,"reason":"real"},
	  {"id":"2","valid":false,"reason":"test fixture, not real creds"}
	]`}
	findings := sampleFindings()
	decisions, err := Triage(context.Background(), prov, findings)
	if err != nil {
		t.Fatalf("Triage: %v", err)
	}
	dismissed := Apply(findings, decisions)
	if dismissed != 1 {
		t.Fatalf("expected 1 dismissed, got %d", dismissed)
	}
	if findings[1].Status != models.StatusFalsePositive {
		t.Errorf("finding 2 should be false_positive, got %s", findings[1].Status)
	}
	if findings[1].TriagedBy != "ai" {
		t.Errorf("finding 2 should be TriagedBy=ai, got %q", findings[1].TriagedBy)
	}
	if findings[0].Status != models.StatusOpen {
		t.Errorf("finding 1 (genuine) should stay open, got %s", findings[0].Status)
	}
	if CountValid(findings) != 1 {
		t.Errorf("CountValid = %d, want 1", CountValid(findings))
	}
}

func TestTriage_ProviderErrorIsFailSafe(t *testing.T) {
	prov := fakeProvider{err: context.DeadlineExceeded}
	findings := sampleFindings()
	decisions, err := Triage(context.Background(), prov, findings)
	if err == nil {
		t.Error("expected the provider error to surface")
	}
	// Fail-safe: nothing dismissed on error.
	if Apply(findings, decisions) != 0 {
		t.Error("provider error must not auto-dismiss any finding")
	}
}

func TestTriage_NoopProviderKeepsAllValid(t *testing.T) {
	findings := sampleFindings()
	decisions, err := Triage(context.Background(), ai.NewNoopProvider(), findings)
	if err != nil {
		t.Fatalf("Triage with noop: %v", err)
	}
	if Apply(findings, decisions) != 0 {
		t.Error("noop provider should dismiss nothing")
	}
}

func TestTriage_GarbageResponseIsFailSafe(t *testing.T) {
	prov := fakeProvider{reply: "I am not JSON at all"}
	findings := sampleFindings()
	decisions, err := Triage(context.Background(), prov, findings)
	if err == nil {
		t.Error("expected a parse error")
	}
	if Apply(findings, decisions) != 0 {
		t.Error("unparseable response must not auto-dismiss findings")
	}
}

func TestTriage_FencedJSON(t *testing.T) {
	prov := fakeProvider{reply: "Here:\n```json\n[{\"id\":\"2\",\"valid\":false,\"reason\":\"x\"}]\n```"}
	findings := sampleFindings()
	decisions, _ := Triage(context.Background(), prov, findings)
	if Apply(findings, decisions) != 1 {
		t.Error("should parse fenced JSON and dismiss finding 2")
	}
}
