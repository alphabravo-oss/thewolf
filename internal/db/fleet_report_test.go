package db

import (
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestReportAgingAndTopRepos(t *testing.T) {
	now := time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)
	ago := func(d int) time.Time { return now.AddDate(0, 0, -d) }
	got := reportAging([]FleetPriority{
		{Severity: models.SeverityCritical, FirstSeenAt: ago(10)},
		{Severity: models.SeverityCritical, FirstSeenAt: ago(45)},
		{Severity: models.SeverityHigh, FirstSeenAt: ago(61)},
		{Severity: models.SeverityHigh, FirstSeenAt: ago(120)},
		{Severity: models.SeverityHigh}, // unknown first-seen counts as oldest
		{Severity: models.SeverityMedium, FirstSeenAt: ago(200)},
	}, now)
	want := []ReportAge{{30, 1, 3}, {60, 0, 3}, {90, 0, 2}}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("aging = %+v, want %+v", got, want)
		}
	}
	top := reportTopRepos([]FindingsByRepoRow{{Name: "b", High: 5, Total: 5}, {Name: "a", Critical: 1, Total: 1}, {Name: "c", High: 5, Total: 9}}, 2)
	if len(top) != 2 || top[0].Name != "a" || top[1].Name != "c" {
		t.Fatalf("top repos = %+v", top)
	}
}
