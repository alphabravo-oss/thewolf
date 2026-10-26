package plugin

import (
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestResolveSeverity(t *testing.T) {
	for _, tc := range []struct {
		kind, score string
		want        models.Severity
	}{
		{"CVSS_V2", "AV:N/AC:L/Au:N/C:C/I:C/A:C", models.SeverityCritical},
		{"CVSS_V3", "CVSS:3.0/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H", models.SeverityCritical},
		{"CVSS_V3", "CVSS:3.1/AV:N/AC:L/PR:L/UI:R/S:C/C:L/I:L/A:N", models.SeverityMedium},
		{"CVSS_V4", "CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:H/VI:H/VA:H/SC:H/SI:H/SA:H", models.SeverityCritical},
		{"CVSS_V3", "9.8", models.SeverityCritical},
		{"CVSS_V3", "7.5", models.SeverityHigh},
		{"CVSS_V3", "4.0", models.SeverityMedium},
		{"CVSS_V3", "3.9", models.SeverityLow},
		{"CVSS_V3", "0", models.SeverityInfo},
	} {
		t.Run(tc.kind+tc.score, func(t *testing.T) {
			got, why := ResolveSeverity("", []CVSS{{Type: tc.kind, Score: tc.score}}, models.SeverityHigh)
			if got != tc.want || !strings.Contains(why, tc.score) || strings.Contains(why, "fallback") {
				t.Fatalf("%s: %s", got, why)
			}
		})
	}
	for _, score := range []string{"", "NaN", "Inf", "-1", "11", "CVSS:3.1/AV:N", strings.Repeat("x", 4097)} {
		got, why := ResolveSeverity("", []CVSS{{Type: "CVSS_V3", Score: score}}, models.SeverityMedium)
		if got != models.SeverityMedium || !strings.Contains(why, "fallback") {
			t.Fatalf("invalid %q: %s %s", score, got, why)
		}
	}
	got, why := ResolveSeverity(" MODERATE ", nil, models.SeverityHigh)
	if got != models.SeverityMedium || !strings.Contains(why, "tool rating") {
		t.Fatalf("tool severity: %s %s", got, why)
	}
	got, _ = ResolveSeverity("low", []CVSS{{Type: "CVSS_V3", Score: "4.5"}, {Type: "CVSS_V3", Score: "9.8"}}, models.SeverityHigh)
	if got != models.SeverityCritical {
		t.Fatalf("highest valid rating: %s", got)
	}
	got, why = ResolveSeverity("low", []CVSS{{Type: "UNKNOWN", Score: "10"}, {Type: "CVSS_V4", Score: "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H"}}, models.SeverityHigh)
	if got != models.SeverityLow || !strings.Contains(why, "tool rating") {
		t.Fatalf("unknown/mismatched version: %s %s", got, why)
	}
}
