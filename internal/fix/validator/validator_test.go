package validator

import (
	"context"
	"testing"
)

func TestValidate_NoFiles(t *testing.T) {
	v := NewValidator()
	result, err := v.Validate(context.Background(), "bandit", "/tmp", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Pass {
		t.Error("expected pass for no files")
	}
}

func TestValidate_UnknownTool(t *testing.T) {
	v := NewValidator()
	result, err := v.Validate(context.Background(), "unknown-tool-xyz", "/tmp", []string{"file.py"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Pass {
		t.Error("expected pass for unknown tool (skip validation)")
	}
	if result.Output == "" {
		t.Error("expected non-empty output explaining skip")
	}
}

func TestToolCommandMappings(t *testing.T) {
	expectedTools := []string{"bandit", "ruff", "eslint", "gosec", "semgrep"}
	for _, tool := range expectedTools {
		if _, ok := ToolCommand[tool]; !ok {
			t.Errorf("expected ToolCommand to have entry for %q", tool)
		}
	}
}
