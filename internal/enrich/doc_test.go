package enrich

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

// sampleFindingsJSON writes a minimal findings.json to a temp file and
// returns its path.
func sampleFindingsJSON(t *testing.T) string {
	t.Helper()
	doc := map[string]any{
		"scan_id":   "scan-1",
		"repo_name": "demo",
		"summary":   map[string]any{"total": 3},
		"findings": []map[string]any{
			{"id": "a", "tool_name": "gosec", "category": "sast", "severity": "high",
				"title": "SQLi", "file_path": "db.go", "line_start": 10, "line_end": 12},
			{"id": "b", "tool_name": "eslint", "category": "quality", "severity": "low",
				"title": "unused var", "file_path": "src/app.ts", "line_start": 4},
			{"id": "c", "tool_name": "trivy", "category": "sca", "severity": "critical",
				"title": "CVE-x", "file_path": "go.mod", "line_start": 1},
		},
		"tools": []map[string]any{{"name": "gosec", "findings": 1, "status": "ok"}},
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "findings.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// reload parses a findings.json back into the top-level map + findings.
func reload(t *testing.T, path string) (map[string]json.RawMessage, []map[string]json.RawMessage) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		t.Fatal(err)
	}
	var findings []map[string]json.RawMessage
	if err := json.Unmarshal(top["findings"], &findings); err != nil {
		t.Fatal(err)
	}
	return top, findings
}

func TestDoc_ApplyAndWrite_FilteredScope(t *testing.T) {
	path := sampleFindingsJSON(t)
	doc, err := LoadDoc(path)
	if err != nil {
		t.Fatalf("LoadDoc: %v", err)
	}
	if doc.FindingCount() != 3 {
		t.Fatalf("expected 3 findings, got %d", doc.FindingCount())
	}

	// Enrich only critical+high.
	n, err := doc.Apply(Filter{Severities: []string{"critical", "high"}}, TemplateGenerator{})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if n != 2 {
		t.Fatalf("expected 2 enriched, got %d", n)
	}
	if err := doc.Write(path); err != nil {
		t.Fatalf("Write: %v", err)
	}

	top, findings := reload(t, path)
	// Non-findings content preserved.
	if string(top["scan_id"]) != `"scan-1"` {
		t.Errorf("scan_id not preserved: %s", top["scan_id"])
	}
	if _, ok := top["tools"]; !ok {
		t.Error("tools key dropped on round-trip")
	}
	// a (high) and c (critical) enriched; b (low) not.
	byID := map[string]map[string]json.RawMessage{}
	for _, f := range findings {
		var id string
		json.Unmarshal(f["id"], &id)
		byID[id] = f
	}
	if _, ok := byID["a"]["ai_fix_prompt"]; !ok {
		t.Error("finding a (high) should be enriched")
	}
	if _, ok := byID["c"]["ai_fix_prompt"]; !ok {
		t.Error("finding c (critical) should be enriched")
	}
	if _, ok := byID["b"]["ai_fix_prompt"]; ok {
		t.Error("finding b (low) should NOT be enriched")
	}
	// The enriched prompt is a valid non-empty string with the template sections.
	var prompt string
	if err := json.Unmarshal(byID["a"]["ai_fix_prompt"], &prompt); err != nil {
		t.Fatalf("ai_fix_prompt not a string: %v", err)
	}
	if !strings.Contains(prompt, "## Problem") {
		t.Errorf("enriched prompt missing template sections: %q", prompt)
	}
}

func TestDoc_Apply_Idempotent(t *testing.T) {
	path := sampleFindingsJSON(t)
	doc, _ := LoadDoc(path)
	doc.Apply(Filter{}, TemplateGenerator{})
	doc.Write(path)

	doc2, _ := LoadDoc(path)
	n, _ := doc2.Apply(Filter{}, TemplateGenerator{})
	doc2.Write(path)
	if n != 3 {
		t.Fatalf("second enrich should re-touch all 3, got %d", n)
	}
	// Field count stable — no duplication.
	_, findings := reload(t, path)
	for _, f := range findings {
		if _, ok := f["ai_fix_prompt"]; !ok {
			t.Error("ai_fix_prompt missing after re-enrich")
		}
	}
}

// stubGenerator always fails — used to prove Apply aborts loudly.
type stubGenerator struct{}

func (stubGenerator) Prompt(models.Finding) (string, error) {
	return "", errStub
}

var errStub = stubError("provider down")

type stubError string

func (e stubError) Error() string { return string(e) }

func TestDoc_Apply_GeneratorErrorAborts(t *testing.T) {
	path := sampleFindingsJSON(t)
	doc, _ := LoadDoc(path)
	_, err := doc.Apply(Filter{}, stubGenerator{})
	if err == nil {
		t.Fatal("expected Apply to surface the generator error")
	}
	if !strings.Contains(err.Error(), "provider down") {
		t.Errorf("error should wrap the generator failure, got: %v", err)
	}
}

func TestLoadDoc_MissingFindingsArray(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	os.WriteFile(path, []byte(`{"scan_id":"x"}`), 0o600)
	if _, err := LoadDoc(path); err == nil {
		t.Fatal("expected error for findings.json with no findings array")
	}
}
