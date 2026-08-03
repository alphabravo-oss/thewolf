package scannerreleasescheduler

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/alphabravocompany/thewolf/internal/scannerpolicy"
	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
	"github.com/alphabravocompany/thewolf/internal/scannerschedule"
)

// JobProvider is re-evaluated on every scheduler tick so a newly activated
// policy revision changes cadence without restarting any replica.
type JobProvider interface {
	Jobs(context.Context) ([]Job, error)
}

type StaticJobs []Job

func (jobs StaticJobs) Jobs(context.Context) ([]Job, error) {
	return append([]Job(nil), jobs...), nil
}

type ActivePolicyJobs struct {
	Store scannerrelease.PolicyRepository
	Scope string
}

func (p ActivePolicyJobs) Jobs(ctx context.Context) ([]Job, error) {
	if p.Store == nil {
		return nil, errors.New("scanner release policy store is required")
	}
	policies, err := p.Store.ListPolicies(ctx, p.Scope, true)
	if err != nil {
		return nil, err
	}
	if len(policies) == 0 {
		return nil, fmt.Errorf("no enabled scanner release policy for scope %q", p.Scope)
	}
	sort.Slice(policies, func(i, j int) bool { return policies[i].Revision > policies[j].Revision })
	policy, err := scannerpolicy.ValidateScheduleJSON([]byte(policies[0].ScheduleJSON))
	if err != nil {
		return nil, fmt.Errorf(
			"active scanner release policy %s revision %d has invalid schedule: %w",
			policies[0].ID, policies[0].Revision, err,
		)
	}
	return JobsFromPolicy(policy)
}

func JobsFromPolicy(policy scannerpolicy.SchedulePolicy) ([]Job, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	dailyHour, dailyMinute, _ := parseClock(policy.DailyDiscovery.At)
	weeklyHour, weeklyMinute, _ := parseClock(policy.WeeklyCandidate.At)
	dailyJitter, _ := time.ParseDuration(policy.DailyDiscovery.Jitter)
	weeklyJitter, _ := time.ParseDuration(policy.WeeklyCandidate.Jitter)
	dailyCatchUp, _ := time.ParseDuration(policy.DailyDiscovery.CatchUp)
	weeklyCatchUp, _ := time.ParseDuration(policy.WeeklyCandidate.CatchUp)
	weekday, err := policyWeekday(policy.WeeklyCandidate.Weekday)
	if err != nil {
		return nil, err
	}
	jobs := []Job{
		{
			Schedule: scannerschedule.Schedule{
				Key: "daily-discovery", Kind: KindDiscovery, Enabled: policy.DailyDiscovery.IsEnabled(),
				Frequency: scannerschedule.Daily, Timezone: policy.Timezone,
				Hour: dailyHour, Minute: dailyMinute, Jitter: dailyJitter, CatchUp: dailyCatchUp,
			},
			Scope: ScopeComplete,
		},
		{
			Schedule: scannerschedule.Schedule{
				Key: "weekly-candidate", Kind: KindCandidate, Enabled: policy.WeeklyCandidate.IsEnabled(),
				Frequency: scannerschedule.Weekly, Timezone: policy.Timezone,
				Hour: weeklyHour, Minute: weeklyMinute, Weekday: weekday,
				Jitter: weeklyJitter, CatchUp: weeklyCatchUp,
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

func policyWeekday(value string) (time.Weekday, error) {
	weekdays := map[string]time.Weekday{
		"Sunday": time.Sunday, "Monday": time.Monday, "Tuesday": time.Tuesday,
		"Wednesday": time.Wednesday, "Thursday": time.Thursday,
		"Friday": time.Friday, "Saturday": time.Saturday,
	}
	weekday, exists := weekdays[value]
	if !exists {
		return 0, fmt.Errorf("invalid scanner release policy weekday %q", value)
	}
	return weekday, nil
}
