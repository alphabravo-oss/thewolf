package scannerschedule

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type LeaseStore interface {
	AcquireScheduleLease(
		ctx context.Context,
		scheduleKey, periodKey, owner string,
		leaseUntil time.Time,
	) (bool, error)
	CompleteScheduleLease(
		ctx context.Context,
		scheduleKey, periodKey, owner string,
		completedAt time.Time,
	) error
}

type Enqueuer interface {
	EnqueueScheduled(ctx context.Context, schedule Schedule, period Period, idempotencyKey string) error
}

type Runner struct {
	Store         LeaseStore
	Enqueuer      Enqueuer
	Owner         string
	LeaseDuration time.Duration
	Now           func() time.Time
}

func (r Runner) Tick(ctx context.Context, schedules []Schedule) error {
	if r.Store == nil || r.Enqueuer == nil {
		return errors.New("schedule runner store and enqueuer are required")
	}
	if r.Owner == "" {
		return errors.New("schedule runner owner is required")
	}
	leaseDuration := r.LeaseDuration
	if leaseDuration <= 0 {
		leaseDuration = 5 * time.Minute
	}
	now := time.Now().UTC()
	if r.Now != nil {
		now = r.Now().UTC()
	}
	var failures []error
	for _, schedule := range schedules {
		if !schedule.Enabled {
			continue
		}
		period, due, err := schedule.IsDue(now)
		if err != nil {
			failures = append(failures, fmt.Errorf("schedule %q: %w", schedule.Key, err))
			continue
		}
		if !due {
			continue
		}
		acquired, err := r.Store.AcquireScheduleLease(
			ctx,
			schedule.Key,
			period.Key,
			r.Owner,
			now.Add(leaseDuration),
		)
		if err != nil {
			failures = append(failures, fmt.Errorf("acquire schedule %q period %q: %w", schedule.Key, period.Key, err))
			continue
		}
		if !acquired {
			continue
		}
		idempotencyKey := "scanner-schedule/" + schedule.Key + "/" + period.Key
		if err := r.Enqueuer.EnqueueScheduled(ctx, schedule, period, idempotencyKey); err != nil {
			// Do not complete the lease. Another replica can recover the same
			// idempotent enqueue after this claim expires.
			failures = append(failures, fmt.Errorf("enqueue schedule %q period %q: %w", schedule.Key, period.Key, err))
			continue
		}
		if err := r.Store.CompleteScheduleLease(ctx, schedule.Key, period.Key, r.Owner, now); err != nil {
			failures = append(failures, fmt.Errorf("complete schedule %q period %q: %w", schedule.Key, period.Key, err))
		}
	}
	return errors.Join(failures...)
}
