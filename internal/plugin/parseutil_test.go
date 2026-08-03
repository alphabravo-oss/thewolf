package plugin

import (
	"encoding/json"
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

func TestExtractJSONReturnsOriginalWhenNoBoundedValueExists(t *testing.T) {
	t.Parallel()
	input := []byte("warning only [main]")
	if got := ExtractJSON(input); string(got) != string(input) {
		t.Fatalf("ExtractJSON() = %q, want original", got)
	}
}
