package scannerpolicy

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestDefaultScheduleNormalizesAndRoundTrips(t *testing.T) {
	t.Parallel()

	schedule := SchedulePolicy{}
	if err := schedule.Normalize(); err != nil {
		t.Fatal(err)
	}
	if schedule.Timezone != "UTC" ||
		schedule.DailyDiscovery.At != "02:00" ||
		schedule.WeeklyCandidate.Weekday != "Sunday" ||
		schedule.MaximumStableImageAge != "168h0m0s" {
		t.Fatalf("normalized schedule = %#v", schedule)
	}
	encoded, err := json.Marshal(schedule)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := ValidateScheduleJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.WeeklyCandidate.Frequency != "weekly" ||
		decoded.WeeklyCandidate.CatchUp != "48h" {
		t.Fatalf("round-tripped weekly schedule = %#v", decoded.WeeklyCandidate)
	}
}

func TestScheduleValidatesMaximumStableImageAgeAndForcedRebuild(t *testing.T) {
	t.Parallel()

	schedule := DefaultSchedule()
	schedule.MaximumStableImageAge = "96h"
	schedule.ForceWeeklyRebuild = true
	age, err := schedule.MaximumStableAge()
	if err != nil || age != 96*time.Hour || !schedule.ForceWeeklyRebuild {
		t.Fatalf("maximum stable age=%s forced=%t err=%v", age, schedule.ForceWeeklyRebuild, err)
	}
	for _, invalid := range []string{"0s", "-1h", "8761h", "weekly"} {
		candidate := DefaultSchedule()
		candidate.MaximumStableImageAge = invalid
		if err := candidate.Normalize(); err == nil || !strings.Contains(err.Error(), "maximum_stable_image_age") {
			t.Fatalf("maximum stable age %q error = %v", invalid, err)
		}
	}
}

func TestScheduleRejectsUnknownFieldsAndInvalidTimezone(t *testing.T) {
	t.Parallel()

	_, err := ValidateScheduleJSON([]byte(`{"timezone":"UTC","unknown":true}`))
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown field error = %v", err)
	}

	schedule := DefaultSchedule()
	schedule.Timezone = "Mars/Olympus"
	if err := schedule.Normalize(); err == nil || !strings.Contains(err.Error(), "IANA timezone") {
		t.Fatalf("abbreviation timezone error = %v", err)
	}
}

func TestScheduleAcceptsDSTTimezoneWithoutChangingWallClock(t *testing.T) {
	t.Parallel()

	schedule := DefaultSchedule()
	schedule.Timezone = "America/New_York"
	schedule.DailyDiscovery.At = "02:30"
	if err := schedule.Normalize(); err != nil {
		t.Fatal(err)
	}
	location, err := time.LoadLocation(schedule.Timezone)
	if err != nil {
		t.Fatal(err)
	}

	// Policy stores a local wall clock. The scheduler is responsible for
	// choosing the first valid instant on a spring-forward date; normalization
	// must not silently rewrite the requested wall clock.
	before := time.Date(2026, 3, 7, 2, 30, 0, 0, location)
	after := before.AddDate(0, 0, 1)
	if schedule.DailyDiscovery.At != "02:30" || after.Location() != location {
		t.Fatalf("DST schedule was rewritten: %#v after=%s", schedule, after)
	}
}

func TestMaintenanceWindowValidation(t *testing.T) {
	t.Parallel()

	schedule := DefaultSchedule()
	schedule.MaintenanceWindow = []MaintenanceWindow{{
		ID: "weekly-release", Name: "Weekly release",
		Cron: "0 3 * * 0", Duration: "4h",
	}}
	if err := schedule.Normalize(); err != nil {
		t.Fatal(err)
	}

	schedule.MaintenanceWindow = append(schedule.MaintenanceWindow, MaintenanceWindow{
		ID: "weekly-release", Name: "Duplicate", Cron: "0 4 * * 0", Duration: "1h",
	})
	if err := schedule.Normalize(); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate maintenance window error = %v", err)
	}
}

func TestMaintenanceWindowValidationRejectsOverlap(t *testing.T) {
	t.Parallel()

	schedule := DefaultSchedule()
	schedule.MaintenanceWindow = []MaintenanceWindow{
		{ID: "primary", Name: "Primary", Cron: "0 3 * * 0", Duration: "2h"},
		{ID: "secondary", Name: "Secondary", Cron: "0 4 * * 0", Duration: "1h"},
	}
	if err := schedule.Normalize(); err == nil || !strings.Contains(err.Error(), "overlap") {
		t.Fatalf("overlap error = %v", err)
	}

	schedule.MaintenanceWindow[1].Cron = "0 5 * * 0"
	if err := schedule.Normalize(); err != nil {
		t.Fatalf("adjacent windows should be valid: %v", err)
	}
}

func TestNextMaintenanceWindowsUsesTrustedClockAndSorts(t *testing.T) {
	t.Parallel()

	schedule := DefaultSchedule()
	schedule.Timezone = "America/New_York"
	schedule.MaintenanceWindow = []MaintenanceWindow{
		{ID: "later", Name: "Later", Cron: "0 5 * * 4", Duration: "1h"},
		{ID: "earlier", Name: "Earlier", Cron: "0 3 * * 4", Duration: "1h"},
	}
	occurrences, err := schedule.NextMaintenanceWindows(
		time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(occurrences) != 2 || occurrences[0].ID != "earlier" ||
		!occurrences[0].At.Equal(time.Date(2026, 7, 30, 7, 0, 0, 0, time.UTC)) {
		t.Fatalf("occurrences = %#v", occurrences)
	}
}

func TestMaintenanceWindowStatusUsesTrustedPolicyTimezone(t *testing.T) {
	t.Parallel()

	schedule := DefaultSchedule()
	schedule.Timezone = "America/New_York"
	schedule.MaintenanceWindow = []MaintenanceWindow{
		{ID: "weekday-release", Name: "Weekday release", Cron: "0 3 * * 4", Duration: "2h"},
	}
	// Thursday 03:30 local in July is 07:30 UTC.
	open, identities, err := schedule.MaintenanceWindowStatus(
		time.Date(2026, 7, 30, 7, 30, 0, 0, time.UTC),
	)
	if err != nil || !open || len(identities) != 1 || identities[0] != "weekday-release" {
		t.Fatalf("open=%v identities=%v err=%v", open, identities, err)
	}
	open, identities, err = schedule.MaintenanceWindowStatus(
		time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC),
	)
	if err != nil || open || len(identities) != 0 {
		t.Fatalf("closed window open=%v identities=%v err=%v", open, identities, err)
	}

	unrestricted := DefaultSchedule()
	open, identities, err = unrestricted.MaintenanceWindowStatus(time.Now())
	if err != nil || !open || len(identities) != 0 {
		t.Fatalf("unrestricted schedule open=%v identities=%v err=%v", open, identities, err)
	}
}

func TestMaintenanceWindowRejectsInvalidCronAndExcessiveDuration(t *testing.T) {
	t.Parallel()

	schedule := DefaultSchedule()
	schedule.MaintenanceWindow = []MaintenanceWindow{{
		Name: "Invalid", Cron: "99 3 * * 0", Duration: "1h",
	}}
	if err := schedule.Normalize(); err == nil || !strings.Contains(err.Error(), "cron is invalid") {
		t.Fatalf("invalid cron error = %v", err)
	}
	schedule.MaintenanceWindow[0].Cron = "0 3 * * 0"
	schedule.MaintenanceWindow[0].Duration = "169h"
	if err := schedule.Normalize(); err == nil || !strings.Contains(err.Error(), "168h") {
		t.Fatalf("excessive maintenance duration error = %v", err)
	}
}
