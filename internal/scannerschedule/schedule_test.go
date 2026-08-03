package scannerschedule

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestDailyAndWeeklyPeriodsUseOrganizationTimezone(t *testing.T) {
	t.Parallel()
	daily := Schedule{
		Key: "daily-discovery", Kind: "discovery", Enabled: true,
		Frequency: Daily, Timezone: "America/New_York", Hour: 2,
		CatchUp: 4 * time.Hour,
	}
	now := time.Date(2026, 7, 30, 7, 0, 0, 0, time.UTC) // 03:00 local
	period, due, err := daily.IsDue(now)
	if err != nil {
		t.Fatal(err)
	}
	if !due || period.Key != "2026-07-30" {
		t.Fatalf("daily period = %#v, due=%v", period, due)
	}
	if period.ScheduledAt != time.Date(2026, 7, 30, 6, 0, 0, 0, time.UTC) {
		t.Fatalf("scheduled at = %s", period.ScheduledAt)
	}

	weekly := daily
	weekly.Key = "weekly-candidate"
	weekly.Kind = "candidate"
	weekly.Frequency = Weekly
	weekly.Weekday = time.Sunday
	weekly.Hour = 3
	weekly.CatchUp = 48 * time.Hour
	period, _, err = weekly.IsDue(time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if period.Key != "2026-08-02" {
		t.Fatalf("weekly period key = %q", period.Key)
	}
}

func TestDSTTransitionStillHasOneLogicalPeriod(t *testing.T) {
	t.Parallel()
	schedule := Schedule{
		Key: "daily-discovery", Kind: "discovery", Enabled: true,
		Frequency: Daily, Timezone: "America/New_York", Hour: 2, Minute: 30,
		CatchUp: 6 * time.Hour,
	}
	// 02:30 does not exist on the spring-forward date. time.Date resolves it
	// to a real instant, but the logical local date remains unique.
	first, err := schedule.CurrentPeriod(time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	second, err := schedule.CurrentPeriod(time.Date(2026, 3, 8, 11, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if first.Key != "2026-03-08" || second.Key != first.Key {
		t.Fatalf("DST periods differ: %#v %#v", first, second)
	}
}

func TestDeterministicJitterIsStableAndBounded(t *testing.T) {
	t.Parallel()
	maximum := 20 * time.Minute
	first := deterministicJitter("daily", "2026-07-30", maximum)
	second := deterministicJitter("daily", "2026-07-30", maximum)
	if first != second {
		t.Fatalf("jitter differs: %s != %s", first, second)
	}
	if first < 0 || first >= maximum {
		t.Fatalf("jitter %s is outside [0,%s)", first, maximum)
	}
}

func TestRunnerReplicasEnqueueOneLogicalOperation(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 30, 8, 0, 0, 0, time.UTC)
	store := newMemoryLeases(func() time.Time { return now })
	queue := &memoryQueue{}
	schedule := Schedule{
		Key: "daily-discovery", Kind: "discovery", Enabled: true,
		Frequency: Daily, Timezone: "UTC", Hour: 7,
		CatchUp: 4 * time.Hour,
	}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(replica int) {
			defer wg.Done()
			runner := Runner{
				Store: store, Enqueuer: queue, Owner: "replica-" + string(rune('a'+replica)),
				LeaseDuration: time.Minute, Now: func() time.Time { return now },
			}
			if err := runner.Tick(context.Background(), []Schedule{schedule}); err != nil {
				t.Errorf("Tick: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if got := queue.Count(); got != 1 {
		t.Fatalf("enqueued operations = %d, want 1", got)
	}
	if queue.Keys()[0] != "scanner-schedule/daily-discovery/2026-07-30" {
		t.Fatalf("idempotency key = %q", queue.Keys()[0])
	}
}

func TestRunnerRecoversFailedEnqueueAfterLeaseExpiry(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 30, 8, 0, 0, 0, time.UTC)
	store := newMemoryLeases(func() time.Time { return now })
	queue := &memoryQueue{fail: true}
	schedule := Schedule{
		Key: "daily-discovery", Kind: "discovery", Enabled: true,
		Frequency: Daily, Timezone: "UTC", Hour: 7,
		CatchUp: 4 * time.Hour,
	}
	runner := Runner{
		Store: store, Enqueuer: queue, Owner: "first",
		LeaseDuration: time.Minute, Now: func() time.Time { return now },
	}
	if err := runner.Tick(context.Background(), []Schedule{schedule}); err == nil {
		t.Fatal("failed enqueue unexpectedly succeeded")
	}
	queue.fail = false
	runner.Owner = "second"
	if err := runner.Tick(context.Background(), []Schedule{schedule}); err != nil {
		t.Fatal(err)
	}
	if queue.Count() != 0 {
		t.Fatal("lease was recovered before expiry")
	}
	now = now.Add(2 * time.Minute)
	if err := runner.Tick(context.Background(), []Schedule{schedule}); err != nil {
		t.Fatal(err)
	}
	if queue.Count() != 1 {
		t.Fatalf("recovered enqueue count = %d", queue.Count())
	}
}

type memoryLease struct {
	owner     string
	expiresAt time.Time
	completed bool
}

type memoryLeases struct {
	mu     sync.Mutex
	now    func() time.Time
	leases map[string]memoryLease
}

func newMemoryLeases(now func() time.Time) *memoryLeases {
	return &memoryLeases{now: now, leases: make(map[string]memoryLease)}
}

func (s *memoryLeases) AcquireScheduleLease(
	_ context.Context,
	scheduleKey, periodKey, owner string,
	leaseUntil time.Time,
) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := scheduleKey + "/" + periodKey
	existing, exists := s.leases[key]
	if exists && (existing.completed || existing.expiresAt.After(s.now())) {
		return false, nil
	}
	s.leases[key] = memoryLease{owner: owner, expiresAt: leaseUntil}
	return true, nil
}

func (s *memoryLeases) CompleteScheduleLease(
	_ context.Context,
	scheduleKey, periodKey, owner string,
	_ time.Time,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := scheduleKey + "/" + periodKey
	existing, exists := s.leases[key]
	if !exists || existing.owner != owner {
		return errors.New("stale lease owner")
	}
	existing.completed = true
	s.leases[key] = existing
	return nil
}

type memoryQueue struct {
	mu   sync.Mutex
	keys []string
	fail bool
}

func (q *memoryQueue) EnqueueScheduled(_ context.Context, _ Schedule, _ Period, key string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.fail {
		return errors.New("queue unavailable")
	}
	q.keys = append(q.keys, key)
	return nil
}

func (q *memoryQueue) Count() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.keys)
}

func (q *memoryQueue) Keys() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]string(nil), q.keys...)
}
