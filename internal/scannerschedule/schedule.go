// Package scannerschedule provides timezone-aware, deterministic daily and
// weekly scanner release schedules plus replica-safe enqueue coordination.
package scannerschedule

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"
)

type Frequency string

const (
	Daily  Frequency = "daily"
	Weekly Frequency = "weekly"
)

type Schedule struct {
	Key       string
	Enabled   bool
	Kind      string
	Frequency Frequency
	Timezone  string
	Hour      int
	Minute    int
	Weekday   time.Weekday
	Jitter    time.Duration
	CatchUp   time.Duration
}

type Period struct {
	Key         string
	ScheduledAt time.Time
	DueAt       time.Time
	ExpiresAt   time.Time
}

func (s Schedule) Validate() error {
	if strings.TrimSpace(s.Key) == "" {
		return errors.New("schedule key is required")
	}
	if strings.TrimSpace(s.Kind) == "" {
		return errors.New("schedule kind is required")
	}
	switch s.Frequency {
	case Daily:
	case Weekly:
		if s.Weekday < time.Sunday || s.Weekday > time.Saturday {
			return fmt.Errorf("invalid weekly schedule weekday %d", s.Weekday)
		}
	default:
		return fmt.Errorf("unsupported schedule frequency %q", s.Frequency)
	}
	if s.Hour < 0 || s.Hour > 23 || s.Minute < 0 || s.Minute > 59 {
		return fmt.Errorf("invalid schedule time %02d:%02d", s.Hour, s.Minute)
	}
	if strings.TrimSpace(s.Timezone) == "" {
		return errors.New("schedule timezone is required")
	}
	if _, err := time.LoadLocation(s.Timezone); err != nil {
		return fmt.Errorf("load schedule timezone %q: %w", s.Timezone, err)
	}
	if s.Jitter < 0 || s.Jitter >= 24*time.Hour {
		return errors.New("schedule jitter must be between zero and 24 hours")
	}
	if s.CatchUp <= 0 {
		return errors.New("schedule catch-up window must be positive")
	}
	return nil
}

// CurrentPeriod returns the latest logical period whose un-jittered scheduled
// time is at or before now. The period key is based on the local scheduled
// date, so daylight-saving gaps and repeated hours still produce one key.
func (s Schedule) CurrentPeriod(now time.Time) (Period, error) {
	if err := s.Validate(); err != nil {
		return Period{}, err
	}
	location, _ := time.LoadLocation(s.Timezone)
	localNow := now.In(location)
	year, month, day := localNow.Date()

	var scheduled time.Time
	switch s.Frequency {
	case Daily:
		scheduled = time.Date(year, month, day, s.Hour, s.Minute, 0, 0, location)
		if scheduled.After(now) {
			previous := localNow.AddDate(0, 0, -1)
			year, month, day = previous.Date()
			scheduled = time.Date(year, month, day, s.Hour, s.Minute, 0, 0, location)
		}
	case Weekly:
		delta := (int(localNow.Weekday()) - int(s.Weekday) + 7) % 7
		scheduledDay := localNow.AddDate(0, 0, -delta)
		year, month, day = scheduledDay.Date()
		scheduled = time.Date(year, month, day, s.Hour, s.Minute, 0, 0, location)
		if scheduled.After(now) {
			scheduledDay = scheduledDay.AddDate(0, 0, -7)
			year, month, day = scheduledDay.Date()
			scheduled = time.Date(year, month, day, s.Hour, s.Minute, 0, 0, location)
		}
	}
	key := fmt.Sprintf("%04d-%02d-%02d", year, month, day)
	due := scheduled.Add(deterministicJitter(s.Key, key, s.Jitter))
	return Period{
		Key:         key,
		ScheduledAt: scheduled.UTC(),
		DueAt:       due.UTC(),
		ExpiresAt:   due.Add(s.CatchUp).UTC(),
	}, nil
}

func (s Schedule) IsDue(now time.Time) (Period, bool, error) {
	period, err := s.CurrentPeriod(now)
	if err != nil {
		return Period{}, false, err
	}
	now = now.UTC()
	return period, !now.Before(period.DueAt) && !now.After(period.ExpiresAt), nil
}

func deterministicJitter(scheduleKey, periodKey string, maximum time.Duration) time.Duration {
	if maximum <= 0 {
		return 0
	}
	sum := sha256.Sum256([]byte(scheduleKey + "\x00" + periodKey))
	value := binary.BigEndian.Uint64(sum[:8])
	// time.Duration is an int64 nanosecond count. The positive precondition
	// makes this conversion exact, and the modulus keeps the result below the
	// same int64 upper bound before it is converted back.
	// #nosec G115 -- maximum is positive int64 and the remainder is < maximum.
	limit := uint64(maximum)
	// #nosec G115 -- value%limit is proven to fit in time.Duration above.
	return time.Duration(value % limit)
}
