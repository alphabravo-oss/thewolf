package security

import (
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

// renovateFixture mirrors the real "packageFiles with updates" log line
// emitted at debug level by renovate 39.x. The previous parser expected
// a top-level depName field — the real format buries everything inside
// config.<manager>[].deps[].updates[]. Real renovate emits each log
// line as single-line JSON, so the fixture does the same.
const renovateFixture = `
{"level":30,"msg":"Repository started"}
{"level":20,"msg":"packageFiles with updates","config":{"npm":[{"packageFile":"package.json","deps":[{"depName":"lodash","currentValue":"4.17.20","updates":[{"newValue":"4.17.21","updateType":"patch"}]},{"depName":"react","currentValue":"17.0.2","updates":[{"newValue":"18.2.0","updateType":"major"}]}]}],"github-actions":[{"packageFile":".github/workflows/ci.yml","deps":[{"depName":"actions/checkout","currentValue":"v3","updates":[{"newValue":"v4","updateType":"major"}]}]}],"dockerfile":[{"packageFile":"Dockerfile","deps":[{"depName":"node","currentValue":"18","updates":[{"newValue":"22","updateType":"major"}]}]}],"pip_requirements":[{"packageFile":"requirements.txt","deps":[{"depName":"requests","currentValue":"2.20.0","isVulnerabilityAlert":true,"vulnerabilities":[{"packageName":"requests","fixedIn":"2.31.0"}],"updates":[{"newValue":"2.31.0","updateType":"minor","isVulnerabilityAlert":true,"vulnerabilityFixVersion":"2.31.0"}]}]}]}}
{"level":30,"msg":"Repository finished"}
`

func TestParseRenovateOutput(t *testing.T) {
	findings, err := parseRenovateOutput([]byte(renovateFixture), nil)
	if err != nil {
		t.Fatal(err)
	}
	// 5 upgrades: lodash patch, react major, checkout major, node major,
	// requests minor (vuln-flagged → High severity).
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

	if bySev[models.SeverityInfo] != 1 {
		t.Errorf("expected 1 info finding (patch), got %d", bySev[models.SeverityInfo])
	}
	if bySev[models.SeverityMedium] != 3 {
		t.Errorf("expected 3 medium findings (majors), got %d", bySev[models.SeverityMedium])
	}
	if bySev[models.SeverityHigh] != 1 {
		t.Errorf("expected 1 high finding (vuln), got %d", bySev[models.SeverityHigh])
	}
	if !hasVulnHigh {
		t.Errorf("expected the requests vuln to surface as High with 'Known vulnerability' in description")
	}
	if byRule["renovate-patch"] != 1 || byRule["renovate-major"] != 3 || byRule["renovate-minor"] != 1 {
		t.Errorf("rule-id mix unexpected: %v", byRule)
	}
}

func TestParseRenovateOutput_DeduplicatesIdenticalUpgrades(t *testing.T) {
	const dup = `
{"level":20,"msg":"packageFiles with updates","config":{"npm":[{"packageFile":"package.json","deps":[{"depName":"lodash","currentValue":"4.17.20","updates":[{"newValue":"4.17.21","updateType":"patch"},{"newValue":"4.17.21","updateType":"patch"}]}]}]}}
`
	findings, err := parseRenovateOutput([]byte(dup), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 {
		t.Errorf("expected 1 deduped finding, got %d", len(findings))
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
		t.Errorf("expected info-stream lines for non-config entries, got %d", len(streamed))
	}
	for _, s := range streamed {
		if strings.Contains(s, "packageFiles") {
			t.Errorf("streamed line should not be the upgrade JSON: %s", s)
		}
	}
}
