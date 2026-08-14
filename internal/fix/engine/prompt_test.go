package engine

import (
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestDefaultPromptsAreFileFirstAndShort(t *testing.T) {
	for _, body := range []string{DefaultInitialInstructions, DefaultFollowupInstructions} {
		if !strings.Contains(body, "false positive") {
			t.Fatalf("expected false-positive skip rule")
		}
		if strings.Contains(body, "Fan out") || strings.Contains(body, "at most 4") {
			t.Fatal("prompt must not require a worker farm")
		}
		if strings.Contains(body, "Time is not a reason to skip") {
			t.Fatal("blank time-is-not-a-reason check should be gone")
		}
	}
	if !strings.Contains(DefaultInitialInstructions, "Medium and low") {
		t.Fatal("initial prompt should include medium/low")
	}
	if !strings.Contains(DefaultInitialInstructions, "Do the edits yourself") {
		t.Fatal("initial prompt should tell the model to edit, not orchestrate")
	}
	if !strings.Contains(DefaultInitialInstructions, "1.25.1") || !strings.Contains(DefaultInitialInstructions, "Do not mute Renovate") {
		t.Fatal("initial prompt should prefer patch bumps and not mute Renovate")
	}
	if !strings.Contains(DefaultInitialInstructions, "Helm") || !strings.Contains(DefaultInitialInstructions, "OpenAPI") {
		t.Fatal("initial prompt should forbid breaking Helm and OpenAPI contracts")
	}
	if !strings.Contains(DefaultClassifyInstructions, "Do not edit") {
		t.Fatal("classify prompt must forbid edits")
	}
}

func TestInstructionForLoop(t *testing.T) {
	if InstructionForLoop(1, "", "") != DefaultInitialInstructions {
		t.Fatal("loop 1 should use default initial")
	}
	if InstructionForLoop(2, "", "") != DefaultFollowupInstructions {
		t.Fatal("loop 2 should use default follow-up")
	}
	if got := InstructionForLoop(1, "FIRST", "LATER"); got != "FIRST" {
		t.Fatalf("got %q", got)
	}
	if got := InstructionForLoop(3, "FIRST", "LATER"); got != "LATER" {
		t.Fatalf("got %q", got)
	}
}

func TestRenderPromptInjectsFinding(t *testing.T) {
	f := models.Finding{Title: "SQL Injection", FilePath: "main.go", LineStart: 42, Severity: "high"}
	got := RenderPrompt("Rules here\n"+FindingPlaceholder, FixRequest{Finding: f})
	if !strings.Contains(got, "Rules here") || !strings.Contains(got, "SQL Injection") || !strings.Contains(got, "main.go:42") {
		t.Fatalf("render = %s", got)
	}
	appended := RenderPrompt("No placeholder", FixRequest{Finding: f})
	if !strings.Contains(appended, "No placeholder") || !strings.Contains(appended, "## Finding") {
		t.Fatalf("append = %s", appended)
	}
}

func TestRenderPromptAlwaysIncludesHandsOff(t *testing.T) {
	custom := RenderPrompt("just fix the yaml please", FixRequest{
		Tool:     "yamllint",
		Findings: []models.Finding{{ID: "a", FilePath: "x.yaml"}},
	})
	if !strings.Contains(custom, HandsOffRules) {
		t.Fatalf("custom Settings prompt must still get hands-off rules:\n%s", custom)
	}
	if !strings.Contains(custom, "Helm") || !strings.Contains(custom, "OpenAPI") {
		t.Fatalf("expected Helm/OpenAPI in custom render:\n%s", custom)
	}
	if strings.Count(custom, HandsOffRules) != 1 {
		t.Fatalf("hands-off should appear once, got %d", strings.Count(custom, HandsOffRules))
	}
	def := RenderPrompt(DefaultInitialInstructions, FixRequest{Tool: "bearer"})
	if !strings.Contains(def, HandsOffRules) {
		t.Fatal("default prompt must include hands-off footer")
	}
	if strings.Count(def, HandsOffRules) != 1 {
		t.Fatalf("default should not double the footer, got %d", strings.Count(def, HandsOffRules))
	}
}

func TestRenderPromptToolFile(t *testing.T) {
	req := FixRequest{
		Tool:         "bearer",
		FindingsFile: "/tmp/findings-bearer.md",
		Findings:     []models.Finding{{ID: "a"}, {ID: "b"}},
	}
	got := RenderPrompt(DefaultInitialInstructions, req)
	if !strings.Contains(got, "bearer") || !strings.Contains(got, "/tmp/findings-bearer.md") || !strings.Contains(got, "2 findings") {
		t.Fatalf("render = %s", got)
	}
}

func TestParseDecisions(t *testing.T) {
	out := ParseDecisions("looked\nSKIP: abcdef01 test fixture\nFIX: abcdef02 escaped exec\n")
	if len(out) != 2 || out[0].Kind != "skip" || out[0].ID != "abcdef01" || out[1].Kind != "fix" {
		t.Fatalf("got %+v", out)
	}
}

func TestParseDecisionsFromOpenCodeJSON(t *testing.T) {
	raw := `{"type":"text","part":{"type":"text","text":"SKIP: 07d0d11d-9dbf-4954-8b4d-a6293e844ca9 false positive: exec.CommandContext uses argv\nFIX: 865d7d08-9e0b-4510-a0bb-c8eb070301be added ReadHeaderTimeout"}}`
	out := ParseDecisions(raw)
	if len(out) != 2 {
		t.Fatalf("got %d decisions: %+v", len(out), out)
	}
	if out[0].Kind != "skip" || !strings.HasPrefix(out[0].ID, "07d0d11d") {
		t.Fatalf("skip = %+v", out[0])
	}
	if out[1].Kind != "fix" || !strings.HasPrefix(out[1].ID, "865d7d08") {
		t.Fatalf("fix = %+v", out[1])
	}
}

func TestParseDecisionsIgnoresPromptExamples(t *testing.T) {
	raw := "Print exactly `SKIP: <finding-id> false positive / scanner noise — <reason>`\n"
	if got := ParseDecisions(raw); len(got) != 0 {
		t.Fatalf("prompt example should not parse, got %+v", got)
	}
}

func TestFormatFindingsFileGroupsByPath(t *testing.T) {
	got := FormatFindingsFile("bearer", []models.Finding{
		{ID: "a", FilePath: "b.go", Title: "tls", Severity: "high", ToolName: "bearer"},
		{ID: "b", FilePath: "a.go", Title: "timeout", Severity: "high", ToolName: "bearer", LineStart: 21},
		{ID: "c", FilePath: "a.go", Title: "other", Severity: "medium", ToolName: "bearer"},
	})
	if !strings.Contains(got, "## a.go") || !strings.Contains(got, "## b.go") {
		t.Fatalf("expected file headings, got %s", got)
	}
	if strings.Index(got, "## a.go") > strings.Index(got, "## b.go") {
		t.Fatal("files should be sorted")
	}
	if !strings.Contains(got, "in 2 files") {
		t.Fatalf("expected file count, got %s", got)
	}
}

func TestDetectSkip(t *testing.T) {
	if _, ok := detectSkip("fixed it"); ok {
		t.Fatal("expected no skip")
	}
	reason, ok := detectSkip("looked at the code\nSKIP: test fixture only\n")
	if !ok || reason != "test fixture only" {
		t.Fatalf("got %q %v", reason, ok)
	}
}
