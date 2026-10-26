package coverage

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMeasuredFormats(t *testing.T) {
	cases := []struct {
		format, report, metric string
		covered, total         int
	}{
		{"lcov", "TN:unit\nSF:src/app.rs\nDA:3,2\nDA:4,0\nBRDA:3,0,0,2\nBRDA:3,0,1,-\nBRF:2\nend_of_record\n", "lines", 1, 2},
		{"cobertura", `<coverage><packages><package><classes><class filename="src/app.py"><methods><method><lines><line number="3" hits="8"/></lines></method></methods><lines><line number="3" hits="0" branch="true" condition-coverage="50% (1/2)"/><line number="4" hits="1"/></lines></class></classes></package></packages></coverage>`, "branches", 1, 2},
		{"jacoco", `<?xml version="1.0"?><!DOCTYPE report PUBLIC "-//JACOCO//DTD Report 1.1//EN" "report.dtd"><report name="tests"><group name="module"><package name="org/acme"><sourcefile name="App.kt"><line nr="3" mi="2" ci="0" mb="1" cb="1"/><line nr="4" mi="0" ci="2" mb="0" cb="0"/></sourcefile></package></group></report>`, "lines", 1, 2},
		{"clover", `<coverage><project><package><file name="src/app.php"><line num="2" type="method" count="1"/><line num="3" type="stmt" count="1"/><line num="4" type="cond" truecount="1" falsecount="0"/></file></package></project></coverage>`, "branches", 1, 2},
		{"opencover", `<CoverageSession><Modules><Module><Files><File uid="1" fullPath="src/app.cs"/></Files><Classes><Class><Methods><Method><FileRef uid="1"/><SequencePoints><SequencePoint sl="3" vc="1" fileid="1"/><SequencePoint sl="4" vc="0" fileid="1"/></SequencePoints><BranchPoints><BranchPoint sl="3" vc="1" offset="0" path="0"/><BranchPoint sl="3" vc="0" offset="0" path="1"/></BranchPoints></Method></Methods></Class></Classes></Module></Modules></CoverageSession>`, "branches", 1, 2},
		{"go", "mode: atomic\nsrc/app.go:3.1,5.2 3 1\nsrc/app.go:6.1,8.2 2 0\n", "statements", 3, 5},
		{"istanbul", `{"src/app.ts":{"path":"src/app.ts","statementMap":{"0":{"start":{"line":3}},"1":{"start":{"line":4}}},"s":{"0":2,"1":0},"b":{"0":[1,0]},"f":{"0":1}}}`, "statements", 1, 2},
		{"simplecov", `{"RSpec":{"coverage":{"src/app.rb":{"lines":[null,null,1,0],"branches":{"if,0,3":{"then,1":1,"else,2":0}}}},"timestamp":123}}`, "lines", 1, 2},
		{"sonar", `<coverage version="1"><file path="src/app.swift"><lineToCover lineNumber="3" covered="true" branchesToCover="2" coveredBranches="1"/><lineToCover lineNumber="4" covered="false"/></file></coverage>`, "lines", 1, 2},
	}
	for _, tc := range cases {
		t.Run(tc.format, func(t *testing.T) {
			r, err := ParseReports([]ReportInput{{Format: tc.format, Content: tc.report}})
			if err != nil {
				t.Fatal(err)
			}
			if got := r.Metrics[tc.metric]; got != (Metric{tc.covered, tc.total}) {
				t.Fatalf("%s = %+v", tc.metric, got)
			}
			if len(r.Files) != 1 || r.MetricFiles[tc.metric] != 1 || len(r.Sources[0].SHA256) != 64 {
				t.Fatalf("incomplete normalized report: %+v", r)
			}
			if tc.format == "go" {
				if _, ok := r.Metrics["lines"]; ok {
					t.Fatal("Go statement coverage mislabeled as lines")
				}
			}
		})
	}
}

func TestDiffCoverValidatesAgainstFullEvidence(t *testing.T) {
	r, err := ParseReports([]ReportInput{{Format: "lcov", Content: "SF:a.ts\nDA:1,1\nDA:2,0\nend_of_record\n"}})
	if err != nil {
		t.Fatal(err)
	}
	good := `{"src_stats":{"a.ts":{"covered_lines":[1],"violation_lines":[2]}},"total_num_lines":2,"total_num_violations":1,"num_changed_lines":3}`
	changed, err := ReadDiffCover(json.RawMessage(good), "base", r)
	if err != nil || changed.Metric != (Metric{1, 2}) {
		t.Fatalf("changed: %+v %v", changed, err)
	}
	for _, bad := range []string{strings.Replace(good, `"covered_lines":[1]`, `"covered_lines":[2]`, 1), strings.Replace(good, `"total_num_lines":2`, `"total_num_lines":3`, 1), strings.Replace(good, `"a.ts"`, `"missing.ts"`, 1), `{"total_percent_covered":100}`} {
		if _, err := ReadDiffCover(json.RawMessage(bad), "base", r); err == nil {
			t.Fatal("inconsistent changed coverage accepted")
		}
	}
	aliased := `{"src_stats":{"a.ts":{"covered_lines":[1]},"./a.ts":{"covered_lines":[1]}},"total_num_lines":2,"total_num_violations":0,"num_changed_lines":2}`
	if _, err := ReadDiffCover(json.RawMessage(aliased), "base", r); err == nil {
		t.Fatal("duplicate normalized diff file inflated coverage")
	}
}

func FuzzMeasuredReportParsers(f *testing.F) {
	f.Add("SF:a.go\nDA:1,0\nend_of_record\n")
	f.Add(`<coverage><packages/></coverage>`)
	f.Add(`{"coverage":{"a.rb":[null,0,1]}}`)
	f.Add("mode: atomic\na.go:1.1,2.2 1 1\n")
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > 1<<16 {
			return
		}
		for _, format := range Formats {
			r, err := ParseReports([]ReportInput{{Format: format, Content: input}})
			if err != nil {
				continue
			}
			for _, m := range r.Metrics {
				if m.Covered < 0 || m.Total <= 0 && m.Covered != 0 || m.Covered > m.Total {
					t.Fatalf("invalid accepted metric %+v", m)
				}
			}
		}
	})
}

func TestMeasuredValidationAndAggregation(t *testing.T) {
	base := ReportInput{Format: "lcov", Content: "SF:/ci/repo/a.js\nDA:1,1\nend_of_record\n", StripPrefix: "/ci/repo", PathPrefix: "frontend"}
	r, err := ParseReports([]ReportInput{base, {Format: "lcov", Content: "SF:b.py\nDA:1,0\nDA:2,0\nDA:3,0\nend_of_record\n"}})
	if err != nil {
		t.Fatal(err)
	}
	if r.Metrics["lines"].Percent() != 25 {
		t.Fatal("coverage must be weighted by executable count, not average percentages")
	}
	if r.Files[1].Path != "frontend/a.js" {
		t.Fatalf("prefix normalization: %+v", r.Files)
	}
	for name, inputs := range map[string][]ReportInput{
		"overlap":                 {base, base},
		"traversal":               {{Format: "lcov", Content: "SF:../a.go\nDA:1,1\nend_of_record\n"}},
		"absolute":                {{Format: "lcov", Content: "SF:/ci/a.go\nDA:1,1\nend_of_record\n"}},
		"negative":                {{Format: "lcov", Content: "SF:a.go\nDA:1,-1\nend_of_record\n"}},
		"truncated":               {{Format: "lcov", Content: "SF:a.go\nDA:1,1\n"}},
		"empty":                   {{Format: "cobertura", Content: "<coverage/>"}},
		"bad ratio":               {{Format: "sonar", Content: `<coverage version="1"><file path="a"><lineToCover lineNumber="1" covered="true" branchesToCover="1" coveredBranches="2"/></file></coverage>`}},
		"deep XML":                {{Format: "cobertura", Content: strings.Repeat("<coverage>", 65) + strings.Repeat("</coverage>", 65)}},
		"JUnit is not coverage":   {{Format: "cobertura", Content: `<testsuite tests="9"/>`}},
		"summary is not coverage": {{Format: "istanbul", Content: `{"total":{"lines":{"pct":100}}}`}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseReports(inputs); err == nil {
				t.Fatal("accepted invalid coverage")
			}
		})
	}
}

func TestSimpleCovAndLCovMergeIdentifiedLines(t *testing.T) {
	for _, in := range []ReportInput{
		{Format: "simplecov", Content: `{"unit":{"coverage":{"a.rb":[1,0]}},"integration":{"coverage":{"a.rb":[0,1]}}}`},
		{Format: "lcov", Content: "TN:unit\nSF:a.c\nDA:1,1\nDA:2,0\nend_of_record\nTN:integration\nSF:a.c\nDA:1,0\nDA:2,1\nend_of_record\n"},
	} {
		r, err := ParseReports([]ReportInput{in})
		if err != nil {
			t.Fatal(err)
		}
		if r.Metrics["lines"] != (Metric{2, 2}) {
			t.Fatalf("duplicate lines counted: %+v", r.Metrics)
		}
	}
}
