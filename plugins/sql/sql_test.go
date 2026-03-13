package sql

import (
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestPluginMetadata(t *testing.T) {
	p := &SQLFluffPlugin{}

	if p.Name() != "sqlfluff" {
		t.Errorf("expected name sqlfluff, got %s", p.Name())
	}
	if p.Category() != models.CategoryQuality {
		t.Errorf("expected category quality, got %s", p.Category())
	}
	langs := p.Languages()
	if len(langs) != 1 || langs[0] != models.LangSQL {
		t.Errorf("expected [sql], got %v", langs)
	}
}

func TestParseSQLFluffOutput(t *testing.T) {
	data := []byte(`[
		{
			"filepath": "queries/users.sql",
			"violations": [
				{
					"code": "L001",
					"description": "Unnecessary trailing whitespace",
					"start_line_no": 5,
					"start_line_pos": 20
				},
				{
					"code": "L010",
					"description": "Inconsistent capitalisation of keywords",
					"start_line_no": 12,
					"start_line_pos": 1
				}
			]
		},
		{
			"filepath": "queries/orders.sql",
			"violations": [
				{
					"code": "L003",
					"description": "Expected 4 spaces indentation",
					"start_line_no": 3,
					"start_line_pos": 1
				}
			]
		}
	]`)

	findings, err := parseSQLFluffOutput(data)
	if err != nil {
		t.Fatalf("parseSQLFluffOutput returned error: %v", err)
	}
	if len(findings) != 3 {
		t.Fatalf("expected 3 findings, got %d", len(findings))
	}

	f := findings[0]
	if f.ToolName != "sqlfluff" {
		t.Errorf("expected tool name sqlfluff, got %s", f.ToolName)
	}
	if f.Category != models.CategoryQuality {
		t.Errorf("expected category quality, got %s", f.Category)
	}
	if f.Severity != models.SeverityLow {
		t.Errorf("expected severity low, got %s", f.Severity)
	}
	if f.RuleID != "L001" {
		t.Errorf("expected rule ID L001, got %s", f.RuleID)
	}
	if f.FilePath != "queries/users.sql" {
		t.Errorf("expected file path queries/users.sql, got %s", f.FilePath)
	}
	if f.LineStart != 5 {
		t.Errorf("expected line 5, got %d", f.LineStart)
	}
}

func TestParseSQLFluffOutput_Empty(t *testing.T) {
	data := []byte(`[]`)
	findings, err := parseSQLFluffOutput(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("expected 0 findings, got %d", len(findings))
	}
}
