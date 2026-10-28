package db

import (
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestScanScheduleEnablementPostgres(t *testing.T) {
	store := NewTestPostgres(t)
	testScanScheduleEnablement(t, store)
}

func testScanScheduleEnablement(t *testing.T, store Store) {
	t.Helper()
	ctx := t.Context()
	for _, enabled := range []bool{false, true} {
		schedule := &models.ScanSchedule{ID: uuid.NewString(), UserID: "owner", RepoID: "repo", IntervalMinutes: 60, Enabled: enabled}
		if err := store.CreateScanSchedule(ctx, schedule); err != nil {
			t.Fatal(err)
		}
		for _, want := range []bool{enabled, !enabled, enabled} {
			schedule.Enabled = want
			if err := store.UpdateScanSchedule(ctx, schedule); err != nil {
				t.Fatal(err)
			}
			got, err := store.GetScanScheduleByID(ctx, schedule.ID)
			if err != nil || got.Enabled != want {
				t.Fatalf("schedule enablement: got=%+v err=%v want=%v", got, err, want)
			}
			active, err := store.ListEnabledScanSchedules(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if want && (len(active) != 1 || active[0].ID != schedule.ID) || !want && len(active) != 0 {
				t.Fatalf("active schedules=%+v; want enabled=%v", active, want)
			}
		}
		if err := store.DeleteScanSchedule(ctx, schedule.ID); err != nil {
			t.Fatal(err)
		}
	}
}
