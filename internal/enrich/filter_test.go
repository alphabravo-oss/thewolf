package enrich

import (
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestFilter_Match(t *testing.T) {
	fn := models.Finding{
		ID:       "abc",
		ToolName: "gosec",
		Category: models.CategorySAST,
		Severity: models.SeverityHigh,
		FilePath: "internal/db/users.go",
	}

	cases := []struct {
		name   string
		filter Filter
		want   bool
	}{
		{"empty matches all", Filter{}, true},
		{"severity hit", Filter{Severities: []string{"high"}}, true},
		{"severity miss", Filter{Severities: []string{"low", "critical"}}, false},
		{"severity case-insensitive", Filter{Severities: []string{"HIGH"}}, true},
		{"category hit", Filter{Categories: []string{"sast"}}, true},
		{"category miss", Filter{Categories: []string{"sca"}}, false},
		{"tool hit", Filter{Tools: []string{"semgrep", "gosec"}}, true},
		{"tool miss", Filter{Tools: []string{"semgrep"}}, false},
		{"id hit", Filter{IDs: []string{"abc"}}, true},
		{"id miss", Filter{IDs: []string{"xyz"}}, false},
		{"exclude path hit -> excluded", Filter{ExcludePaths: []string{"internal/db/**"}}, false},
		{"exclude path miss -> kept", Filter{ExcludePaths: []string{"test/**"}}, true},
		{"exclude *.go suffix", Filter{ExcludePaths: []string{"*.go"}}, false},
		{"AND: severity hit + category miss", Filter{Severities: []string{"high"}, Categories: []string{"sca"}}, false},
		{"AND: all hit", Filter{Severities: []string{"high"}, Categories: []string{"sast"}, Tools: []string{"gosec"}}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.filter.Match(fn); got != c.want {
				t.Errorf("Match() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestFilter_IsEmpty(t *testing.T) {
	if !(Filter{}).IsEmpty() {
		t.Error("zero Filter should be empty")
	}
	if (Filter{Tools: []string{"x"}}).IsEmpty() {
		t.Error("Filter with a tool should not be empty")
	}
}

func TestPathGlobMatch(t *testing.T) {
	cases := []struct {
		pattern, name string
		want          bool
	}{
		{"test/**", "test/foo/bar.go", true},
		{"**/test/**", "a/b/test/x.go", true},
		{"*.go", "main.go", true},
		{"*.go", "src/main.go", true},
		{"src/*.go", "src/main.go", true},
		{"src/*.go", "src/sub/main.go", false},
		{"vendor/**", "internal/x.go", false},
	}
	for _, c := range cases {
		if got := pathGlobMatch(c.pattern, c.name); got != c.want {
			t.Errorf("pathGlobMatch(%q,%q) = %v, want %v", c.pattern, c.name, got, c.want)
		}
	}
}
