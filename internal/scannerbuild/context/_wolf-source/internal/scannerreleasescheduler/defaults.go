package scannerreleasescheduler

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/alphabravocompany/thewolf/internal/scannerschedule"
)

type DefaultsConfig struct {
	Timezone      string
	DailyTime     string
	WeeklyTime    string
	WeeklyWeekday time.Weekday
	Jitter        time.Duration
	DailyCatchUp  time.Duration
	WeeklyCatchUp time.Duration
	DailyEnabled  bool
	WeeklyEnabled bool
}

func DefaultJobs(config DefaultsConfig) ([]Job, error) {
	if config.Timezone == "" {
		config.Timezone = "UTC"
	}
	if config.DailyTime == "" {
		config.DailyTime = "02:00"
	}
	if config.WeeklyTime == "" {
		config.WeeklyTime = "03:00"
	}
	if config.WeeklyWeekday < time.Sunday || config.WeeklyWeekday > time.Saturday {
		config.WeeklyWeekday = time.Sunday
	}
	if config.DailyCatchUp == 0 {
		config.DailyCatchUp = 6 * time.Hour
	}
	if config.WeeklyCatchUp == 0 {
		config.WeeklyCatchUp = 48 * time.Hour
	}
	dailyHour, dailyMinute, err := parseClock(config.DailyTime)
	if err != nil {
		return nil, fmt.Errorf("daily scanner release schedule: %w", err)
	}
	weeklyHour, weeklyMinute, err := parseClock(config.WeeklyTime)
	if err != nil {
		return nil, fmt.Errorf("weekly scanner release schedule: %w", err)
	}
	jobs := []Job{
		{
			Schedule: scannerschedule.Schedule{
				Key: "daily-discovery", Kind: KindDiscovery, Enabled: config.DailyEnabled,
				Frequency: scannerschedule.Daily, Timezone: config.Timezone,
				Hour: dailyHour, Minute: dailyMinute, Jitter: config.Jitter,
				CatchUp: config.DailyCatchUp,
			},
			Scope: ScopeComplete,
		},
		{
			Schedule: scannerschedule.Schedule{
				Key: "weekly-candidate", Kind: KindCandidate, Enabled: config.WeeklyEnabled,
				Frequency: scannerschedule.Weekly, Timezone: config.Timezone,
				Hour: weeklyHour, Minute: weeklyMinute, Weekday: config.WeeklyWeekday,
				Jitter: config.Jitter, CatchUp: config.WeeklyCatchUp,
			},
			Scope: ScopeComplete,
		},
	}
	for _, job := range jobs {
		if err := job.validate(); err != nil {
			return nil, err
		}
	}
	return jobs, nil
}

func parseClock(value string) (int, int, error) {
	parts := strings.Split(value, ":")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("time %q must be HH:MM", value)
	}
	hour, hourErr := strconv.Atoi(parts[0])
	minute, minuteErr := strconv.Atoi(parts[1])
	if hourErr != nil || minuteErr != nil || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, 0, fmt.Errorf("time %q must be a valid 24-hour HH:MM value", value)
	}
	return hour, minute, nil
}
