package routes

import (
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
)

func TestInQuietHoursWrap(t *testing.T) {
	if inQuietHours(time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC), "", "17:00") {
		t.Fatal("empty start is not quiet")
	}
	if !inQuietHours(time.Date(2026, 1, 1, 23, 0, 0, 0, time.UTC), "22:00", "06:00") {
		t.Fatal("23:00 should be quiet in 22:00-06:00")
	}
	if inQuietHours(time.Date(2026, 1, 1, 6, 0, 0, 0, time.UTC), "22:00", "06:00") {
		t.Fatal("06:00 is exclusive end")
	}
	if !inQuietHours(time.Date(2026, 1, 1, 0, 30, 0, 0, time.UTC), "22:00", "06:00") {
		t.Fatal("00:30 should be quiet when window wraps")
	}
	if inQuietHours(time.Date(2026, 1, 1, 7, 0, 0, 0, time.UTC), "22:00", "06:00") {
		t.Fatal("07:00 is outside wrap window")
	}
	if !inQuietHours(time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC), "09:00", "17:00") {
		t.Fatal("10:00 should be quiet in 09:00-17:00")
	}
}

func TestSkipUnchangedSHA(t *testing.T) {
	if !skipUnchangedSHA(&models.Repo{LastCommitSHA: "abc"}, "abc", "") {
		t.Fatal("same LastCommitSHA should skip")
	}
	if skipUnchangedSHA(&models.Repo{LastCommitSHA: "def"}, "abc", "abc") {
		t.Fatal("repo moved on; do not skip")
	}
	if !skipUnchangedSHA(&models.Repo{}, "abc", "abc") {
		t.Fatal("last scan SHA should skip when LastCommitSHA is empty")
	}
	if skipUnchangedSHA(&models.Repo{}, "abc", "") {
		t.Fatal("no recorded SHA should not skip")
	}
	if skipUnchangedSHA(&models.Repo{LastCommitSHA: "abc"}, "", "abc") {
		t.Fatal("empty completed SHA should not skip")
	}
}
