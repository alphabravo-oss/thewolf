package php

import (
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestPluginMetadata(t *testing.T) {
	p := &PHPStanPlugin{}

	if p.Name() != "phpstan" {
		t.Errorf("expected name phpstan, got %s", p.Name())
	}
	if p.Category() != models.CategorySAST {
		t.Errorf("expected category sast, got %s", p.Category())
	}
	langs := p.Languages()
	if len(langs) != 1 || langs[0] != models.LangPHP {
		t.Errorf("expected [php], got %v", langs)
	}
}

func TestParsePHPStanOutput(t *testing.T) {
	data := []byte(`{
		"files": {
			"src/Controller.php": {
				"errors": 2,
				"messages": [
					{"message": "Undefined variable $foo", "line": 15, "ignorable": true},
					{"message": "Method returns void but has return statement", "line": 30, "ignorable": false}
				]
			},
			"src/Model.php": {
				"errors": 1,
				"messages": [
					{"message": "Property has no type", "line": 5, "ignorable": true}
				]
			}
		}
	}`)

	findings, err := parsePHPStanOutput(data)
	if err != nil {
		t.Fatalf("parsePHPStanOutput returned error: %v", err)
	}
	if len(findings) != 3 {
		t.Fatalf("expected 3 findings, got %d", len(findings))
	}

	for _, f := range findings {
		if f.ToolName != "phpstan" {
			t.Errorf("expected tool name phpstan, got %s", f.ToolName)
		}
		if f.Category != models.CategorySAST {
			t.Errorf("expected category sast, got %s", f.Category)
		}
		if f.Severity != models.SeverityMedium {
			t.Errorf("expected severity medium, got %s", f.Severity)
		}
	}
}

func TestParsePHPStanOutput_Empty(t *testing.T) {
	data := []byte(`{"files": {}}`)
	findings, err := parsePHPStanOutput(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("expected 0 findings, got %d", len(findings))
	}
}
