package security

import (
	"fmt"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/finding/identity"
	"github.com/alphabravocompany/thewolf/internal/finding/knowledge"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/scan/runner"
)

func TestDetectSecretsIdentitySurvivesInsertedLinesAndRetainsDistinctSecrets(t *testing.T) {
	parse := func(line int, hashes ...string) []models.Finding {
		var rows []string
		for _, hash := range hashes {
			rows = append(rows, fmt.Sprintf(`{"type":"AWS Access Key","line_number":%d,"hashed_secret":%q}`, line, hash))
		}
		fs, err := parseDetectSecretsOutput([]byte(`{"results":{"/scan/settings.py":[` + strings.Join(rows, ",") + `]}}`))
		if err != nil {
			t.Fatal(err)
		}
		for i := range fs {
			fs[i].FineCategory, fs[i].FixStrategyID = knowledge.Categorize(fs[i].ToolName, fs[i].RuleID)
			identity.Apply(&fs[i])
		}
		return fs
	}
	a, b := strings.Repeat("a", 40), strings.Repeat("b", 40)
	first := parse(20, a, b)
	next := parse(21, strings.Repeat("c", 40), b, a)
	if first[0].StableFingerprint != next[2].StableFingerprint || first[1].StableFingerprint != next[1].StableFingerprint || first[0].StableFingerprint == first[1].StableFingerprint {
		t.Fatal("secret identity changed or collapsed")
	}
	clusters := runner.Deduplicate(first)
	if len(clusters) != 2 || clusters[0].Confidence != "low" || first[0].FilePath != "settings.py" || first[0].IdentityVersion != 3 {
		t.Fatalf("normalized secrets: %+v", clusters)
	}
	copy := first[0]
	copy.FilePath = "another.py"
	if identity.ClusterKey(copy) != identity.ClusterKey(first[0]) {
		t.Fatal("same detector fingerprint lost multi-location identity")
	}
	legacy := first[0]
	legacy.IdentityVersion = 1
	legacy.StableFingerprint = "old"
	identity.Apply(&legacy)
	if legacy.StableFingerprint != "old" || legacy.IdentityVersion != 1 {
		t.Fatal("rewrote legacy identity")
	}
	unknown := parse(20, "")
	moved := parse(21, "")
	if unknown[0].StableFingerprint == moved[0].StableFingerprint {
		t.Fatal("invented semantic identity for missing hash")
	}
}

func TestOSVSeverityProvenanceAndPackageOverride(t *testing.T) {
	for _, tc := range []struct {
		fields string
		want   models.Severity
		reason string
	}{
		{`"severity":[{"type":"CVSS_V3","score":"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H"}]`, models.SeverityCritical, "9.8"},
		{`"database_specific":{"severity":"MODERATE"}`, models.SeverityMedium, "MODERATE"},
		{`"severity":[]`, models.SeverityHigh, "fallback"},
		{`"severity":[{"type":"CVSS_V3","score":"9.8"}],"affected":[{"package":{"name":"package","ecosystem":"PyPI"},"severity":[{"type":"CVSS_V3","score":"3.1"}]}]`, models.SeverityLow, "3.1"},
	} {
		fs, err := parseOSVScannerOutput([]byte(`{"results":[{"source":{"path":"requirements.txt"},"packages":[{"package":{"name":"package","ecosystem":"PyPI"},"vulnerabilities":[{"id":"PYSEC-1",` + tc.fields + `}]}]}]}`))
		if err != nil || len(fs) != 1 || fs[0].Severity != tc.want || !strings.Contains(fs[0].Description, tc.reason) {
			t.Fatalf("%s: %+v %v", tc.fields, fs, err)
		}
	}
}
