package enrich

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/alphabravocompany/thewolf/internal/models"
)

// Generator produces the remediation prompt for a finding. The default
// implementation (TemplateGenerator) is deterministic; an AI-backed
// implementation can be supplied for richer guidance.
type Generator interface {
	Prompt(f models.Finding) (string, error)
}

// TemplateGenerator is the deterministic, no-AI generator. It always
// succeeds and produces the fixed five-section template.
type TemplateGenerator struct{}

func (TemplateGenerator) Prompt(f models.Finding) (string, error) {
	return BuildPrompt(f), nil
}

// Doc is a load-mutate-write view of a findings.json artifact. Every
// field is preserved as raw JSON except the per-finding `ai_fix_prompt`
// key that Apply sets — no finding data is dropped on round-trip.
type Doc struct {
	top      map[string]json.RawMessage
	findings []map[string]json.RawMessage
}

// LoadDoc reads and parses a findings.json artifact.
func LoadDoc(path string) (*Doc, error) {
	// #nosec G304 -- path is a scan-artifact path supplied by the operator.
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read findings json: %w", err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return nil, fmt.Errorf("parse findings json: %w", err)
	}
	rawFindings, ok := top["findings"]
	if !ok {
		return nil, fmt.Errorf("findings json has no \"findings\" array")
	}
	var findings []map[string]json.RawMessage
	if err := json.Unmarshal(rawFindings, &findings); err != nil {
		return nil, fmt.Errorf("parse findings array: %w", err)
	}
	return &Doc{top: top, findings: findings}, nil
}

// FindingCount returns the number of findings in the document.
func (d *Doc) FindingCount() int { return len(d.findings) }

// Apply enriches every finding that passes the filter, setting its
// `ai_fix_prompt` field via the generator. Findings outside the filter
// are left untouched. Returns the number of findings enriched.
//
// A generator error on one finding aborts the run (so a misconfigured
// AI provider fails loudly rather than silently skipping findings).
func (d *Doc) Apply(filter Filter, gen Generator) (int, error) {
	enriched := 0
	for i, raw := range d.findings {
		fn := decodeFinding(raw)
		if !filter.Match(fn) {
			continue
		}
		prompt, err := gen.Prompt(fn)
		if err != nil {
			return enriched, fmt.Errorf("finding %s: %w", fn.ID, err)
		}
		encoded, err := json.Marshal(prompt)
		if err != nil {
			return enriched, fmt.Errorf("encode prompt for %s: %w", fn.ID, err)
		}
		d.findings[i]["ai_fix_prompt"] = encoded
		enriched++
	}
	return enriched, nil
}

// Write serializes the document back to disk (indented, trailing newline).
func (d *Doc) Write(path string) error {
	rawFindings, err := json.Marshal(d.findings)
	if err != nil {
		return fmt.Errorf("encode findings: %w", err)
	}
	d.top["findings"] = rawFindings
	out, err := json.MarshalIndent(d.top, "", "  ")
	if err != nil {
		return fmt.Errorf("encode findings json: %w", err)
	}
	out = append(out, '\n')
	if err := os.WriteFile(path, out, 0o600); err != nil {
		return fmt.Errorf("write findings json: %w", err)
	}
	return nil
}

// decodeFinding pulls the fields enrich needs out of a raw finding object.
// Unknown / extra fields stay untouched in the raw map.
func decodeFinding(raw map[string]json.RawMessage) models.Finding {
	var f models.Finding
	str := func(key string) string {
		if v, ok := raw[key]; ok {
			var s string
			_ = json.Unmarshal(v, &s)
			return s
		}
		return ""
	}
	num := func(key string) int {
		if v, ok := raw[key]; ok {
			var n int
			_ = json.Unmarshal(v, &n)
			return n
		}
		return 0
	}
	f.ID = str("id")
	f.ToolName = str("tool_name")
	f.Category = models.Category(str("category"))
	f.Severity = models.Severity(str("severity"))
	f.Title = str("title")
	f.Description = str("description")
	f.FilePath = str("file_path")
	f.LineStart = num("line_start")
	f.LineEnd = num("line_end")
	f.CodeSnippet = str("code_snippet")
	f.CWEID = str("cwe_id")
	f.RuleID = str("rule_id")
	f.ModuleName = str("module_name")
	f.FunctionName = str("function_name")
	f.SymbolKind = str("symbol_kind")
	f.FilePurpose = str("file_purpose")
	f.FineCategory = str("fine_category")
	if v, ok := raw["corroborated_by"]; ok {
		_ = json.Unmarshal(v, &f.CorroboratedBy)
	}
	if v, ok := raw["dependents"]; ok {
		f.DependentsJSON = string(v)
	}
	return f
}
