package security

import (
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

const renovateFixture = `
{"level":"info","msg":"Repo started"}
{"level":"info","msg":"branchify","depName":"lodash","packageFile":"package.json","manager":"npm","currentValue":"4.17.20","newValue":"4.17.21","updateType":"patch"}
{"level":"info","msg":"branchify","depName":"react","packageFile":"package.json","manager":"npm","currentValue":"17.0.2","newValue":"18.2.0","updateType":"major"}
{"level":"info","msg":"branchify","depName":"actions/checkout","packageFile":".github/workflows/ci.yml","manager":"github-actions","currentValue":"v3","newValue":"v4","updateType":"major"}
{"level":"info","msg":"branchify","depName":"node","packageFile":"Dockerfile","manager":"dockerfile","currentValue":"18","newValue":"22","updateType":"major"}
{"level":"info","msg":"branchify","depName":"requests","packageFile":"requirements.txt","manager":"pip_requirements","currentValue":"2.20.0","newValue":"2.31.0","updateType":"minor","isVulnerabilityAlert":true,"vulnerabilityFixVersion":"2.31.0"}
{"level":"info","msg":"branchify","depName":"lodash","packageFile":"package.json","manager":"npm","currentValue":"4.17.20","newValue":"4.17.21","updateType":"patch"}
{"level":"info","msg":"Done"}
`

func TestParseRenovateOutput(t *testing.T) {
	findings, err := parseRenovateOutput([]byte(renovateFixture), nil)
	if err != nil {
		t.Fatal(err)
	}
	// 5 distinct upgrades (the 6th "lodash" log line is a duplicate of the 1st).
	if len(findings) != 5 {
		t.Fatalf("expected 5 findings, got %d", len(findings))
	}

	bySev := map[models.Severity]int{}
	byRule := map[string]int{}
	hasVulnHigh := false
	for _, f := range findings {
		bySev[f.Severity]++
		byRule[f.RuleID]++
		if f.Severity == models.SeverityHigh && strings.Contains(f.Description, "Known vulnerability") {
			hasVulnHigh = true
		}
	}

	if bySev[models.SeverityInfo] != 1 { // patch lodash
		t.Errorf("expected 1 info finding (patch), got %d", bySev[models.SeverityInfo])
	}
	if bySev[models.SeverityMedium] != 3 { // 3 majors: react, checkout, node base
		t.Errorf("expected 3 medium findings (majors), got %d", bySev[models.SeverityMedium])
	}
	if bySev[models.SeverityHigh] != 1 { // requests minor + vuln → high
		t.Errorf("expected 1 high finding (vuln), got %d", bySev[models.SeverityHigh])
	}
	if !hasVulnHigh {
		t.Errorf("expected the requests vuln to surface as High with 'Known vulnerability' in description")
	}
	if byRule["renovate-patch"] != 1 || byRule["renovate-major"] != 3 || byRule["renovate-minor"] != 1 {
		t.Errorf("rule-id mix unexpected: %v", byRule)
	}
}

func TestParseRenovateOutput_StreamsNonUpgradeLines(t *testing.T) {
	var streamed []string
	_, err := parseRenovateOutput([]byte(renovateFixture), func(line string) {
		streamed = append(streamed, line)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(streamed) < 2 {
		t.Errorf("expected info-stream lines for non-upgrade entries, got %d", len(streamed))
	}
	for _, s := range streamed {
		if strings.Contains(s, "depName") {
			t.Errorf("streamed line should not contain upgrade JSON: %s", s)
		}
	}
}
