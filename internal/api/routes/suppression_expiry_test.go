package routes

import (
	"context"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/models"
)

type expiryLoopStore struct {
	db.Store
	called chan time.Time
}

func (s expiryLoopStore) ExpireFindingSuppressions(ctx context.Context, now time.Time) (int, error) {
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 30*time.Second {
		panic("expiry is not bounded")
	}
	s.called <- now
	return 0, nil
}
func (s expiryLoopStore) ListEnabledScanSchedules(context.Context) ([]models.ScanSchedule, error) {
	return nil, nil
}
func TestScheduleLoopExpiresSuppressionsAtStartup(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	store := expiryLoopStore{called: make(chan time.Time, 1)}
	done := make(chan struct{})
	go func() { StartScheduleLoop(ctx, &Handler{Store: store}); close(done) }()
	select {
	case at := <-store.called:
		if time.Since(at) > time.Second {
			t.Fatal("expiry cutoff is stale")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("startup skipped expiry")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("expiry loop ignored cancellation")
	}
}
