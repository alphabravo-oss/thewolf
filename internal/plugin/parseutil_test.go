package plugin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractJSONSkipsStructuredLookingLogPrefixes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"object", `{"results":[]}`, `{"results":[]}`},
		{"bandit prefix", "[main]\tINFO\tprofile include tests: None\n{\"results\":[{\"test_id\":\"B404\"}]}", `{"results":[{"test_id":"B404"}]}`},
		{"array after bracketed warning", "warning [not-json]\n[{\"rule\":\"fixture\"}]", `[{"rule":"fixture"}]`},
		{"object after brace warning", "{not-json}\n{\"ok\":true}", `{"ok":true}`},
	}
	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			got := ExtractJSON([]byte(testCase.input))
			if string(got) != testCase.want || !json.Valid(got) {
				t.Fatalf("ExtractJSON() = %q", got)
			}
		})
	}
}

func TestHasZizmorAndPipelineInputs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if HasZizmorInputs(dir) || HasPipelineConfig(dir) {
		t.Fatal("empty tree should not look like CI")
	}
	if err := os.MkdirAll(filepath.Join(dir, ".github", "workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".github", "workflows", "ci.yml"), []byte("on: push\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !HasZizmorInputs(dir) || !HasPipelineConfig(dir) {
		t.Fatal("expected GitHub workflows to enable both detectors")
	}
}

func TestExtractJSONReturnsOriginalWhenNoBoundedValueExists(t *testing.T) {
	t.Parallel()
	input := []byte("warning only [main]")
	if got := ExtractJSON(input); string(got) != string(input) {
		t.Fatalf("ExtractJSON() = %q, want original", got)
	}
}
