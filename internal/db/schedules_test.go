package db

import (
	"os"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestScanScheduleEnablementSQLite(t *testing.T) {
	testScanScheduleEnablement(t, newTestStore(t))
}

func TestScanScheduleEnablementPostgres(t *testing.T) {
	dsn := os.Getenv("WOLF_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("WOLF_TEST_POSTGRES_DSN is not configured")
	}
	admin, err := NewPostgres(dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := "schedule_contract_" + uuid.NewString()
	if _, err := admin.db.Exec(`CREATE SCHEMA "` + schema + `"`); err != nil {
		_ = admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.db.Exec(`DROP SCHEMA "` + schema + `" CASCADE`)
		_ = admin.Close()
	})
	store, err := NewPostgres(postgresDSNWithSearchPath(t, dsn, schema))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
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
