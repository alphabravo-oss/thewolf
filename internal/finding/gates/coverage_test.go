package gates

import (
	"github.com/alphabravocompany/thewolf/internal/scan/coverage"
	"testing"
)

func TestCoverageRequirements(t *testing.T) {
	r, err := coverage.ParseReports([]coverage.ReportInput{{Format: "lcov", Content: "SF:web/a.ts\nDA:1,1\nDA:2,0\nBRF:0\nend_of_record\n"}, {Format: "go", Content: "mode: set\napi/main.go:1.1,2.2 2 1\n"}})
	if err != nil {
		t.Fatal(err)
	}
	r.CommitSHA = "head"
	for _, tc := range []struct {
		name, metric, prefix, scope, initial, expected string
		minimum                                        float64
		report                                         *coverage.MeasuredReport
	}{
		{"mixed metrics are incomplete", "lines", "", "", "pass", "incomplete", 80, r},
		{"source root passes", "statements", "api", "", "pass", "pass", 80, r},
		{"line threshold fails", "lines", "web", "", "pass", "fail", 80, r},
		{"exact threshold passes", "lines", "web", "", "pass", "pass", 50, r},
		{"no measurements", "lines", "", "", "pass", "incomplete", 0, nil},
		{"unknown source root", "lines", "absent", "", "pass", "incomplete", 0, r},
		{"no branches is NA", "branches", "web", "", "pass", "pass", 80, r},
		{"missing changed evidence", "lines", "web", "changed", "pass", "incomplete", 80, r},
		{"scan still running", "statements", "api", "", "pending", "pending", 80, r},
		{"security failure remains", "statements", "api", "", "fail", "fail", 80, r},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := Policy{Rules: []Rule{{ID: "coverage", Action: ActionFail, TestCoverage: &CoverageRequirement{Metric: tc.metric, Minimum: tc.minimum, PathPrefix: tc.prefix, Scope: tc.scope}}}}
			e := Evaluation{Status: tc.initial, Summary: Summary{Status: tc.initial}}
			ApplyCoverage(&e, p, tc.report, "head")
			if e.Status != tc.expected || len(e.Coverage) != 1 {
				t.Fatalf("got %+v", e)
			}
		})
	}
	for _, rule := range []string{`{"metric":"lines","minimum":101}`, `{"metric":"bananas","minimum":80}`, `{"metric":"branches","minimum":80,"scope":"changed"}`, `{"metric":"lines","minimum":80,"path_prefix":"../outside"}`} {
		if _, err := ParsePolicy("test", "fail", `[{"id":"min","action":"fail","test_coverage":`+rule+`}]`); err == nil {
			t.Fatalf("invalid rule accepted %s", rule)
		}
	}
}
