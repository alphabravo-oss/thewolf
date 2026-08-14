package routes

import (
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestScannerToolStats_CountsByFindingScanner(t *testing.T) {
	findings := []models.Finding{
		{ID: "a", ToolName: "trivy"},
		{ID: "b", ToolName: "trivy"},
		{ID: "c", ToolName: "trivy"},
		{ID: "d", ToolName: "bearer"},
	}
	attempts := []models.FixAttempt{
		{FindingID: "a", Outcome: models.FixOutcomeKept, EngineUsed: "cli:opencode"},
		{FindingID: "b", Outcome: models.FixOutcomeKept, EngineUsed: "hygiene"},
		{FindingID: "c", Outcome: models.FixOutcomeRolledBack, EngineUsed: "cli:opencode"},
	}
	got := scannerToolStats(findings, attempts)
	if len(got) != 2 {
		t.Fatalf("tools = %d, want 2: %+v", len(got), got)
	}
	if got[0].Tool != "trivy" || got[0].Total != 3 || got[0].Kept != 2 || got[0].Open != 1 || got[0].Rolled != 1 || got[0].After != 1 {
		t.Fatalf("trivy = %+v, want total 3 kept 2 open 1 rolled 1 after 1", got[0])
	}
	if got[1].Tool != "bearer" || got[1].Total != 1 || got[1].Open != 1 || got[1].Kept != 0 {
		t.Fatalf("bearer = %+v, want 1 still open", got[1])
	}
}

func TestAnnotateAttemptTools(t *testing.T) {
	attempts := []models.FixAttempt{
		{FindingID: "a", EngineUsed: "cli:opencode"},
	}
	annotateAttemptTools(attempts, []models.Finding{{
		ID: "a", ToolName: "trivy", Title: "CVE-1", FilePath: "go.mod", LineStart: 3,
	}})
	if attempts[0].ToolName != "trivy" || attempts[0].Title != "CVE-1" || attempts[0].FilePath != "go.mod" {
		t.Fatalf("got %+v", attempts[0])
	}
}
