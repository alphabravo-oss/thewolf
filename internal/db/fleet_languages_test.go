package db

import (
	"slices"
	"testing"
)

func TestParseDetectedLanguages(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want []string
	}{
		{`{"go":219,"python":2,"rust":0}`, []string{"go", "python"}},
		{`["python","go","go",""]`, []string{"go", "python"}},
		{" python, go ", []string{"go", "python"}},
		{`{"go":`, nil}, {`["go",`, nil}, {"null", nil}, {"", nil},
	} {
		if got := parseLanguageList(tc.raw); !slices.Equal(got, tc.want) {
			t.Errorf("parseLanguageList(%q) = %v; want %v", tc.raw, got, tc.want)
		}
	}
}
