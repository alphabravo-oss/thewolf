package scannerpolicy

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

// SchedulePolicy is the transport-neutral, versioned scheduling policy stored
// alongside scanner release rules. It deliberately uses wall-clock values and
// an IANA timezone so daylight-saving behavior is explicit.
type SchedulePolicy struct {
	Timezone              string              `json:"timezone"`
	DailyDiscovery        PeriodicSchedule    `json:"daily_discovery"`
	WeeklyCandidate       WeeklySchedule      `json:"weekly_candidate"`
	MaximumStableImageAge string              `json:"maximum_stable_image_age"`
	ForceWeeklyRebuild    bool                `json:"force_weekly_rebuild,omitempty"`
	MaintenanceWindow     []MaintenanceWindow `json:"maintenance_windows,omitempty"`
}

type PeriodicSchedule struct {
	Enabled   *bool  `json:"enabled,omitempty"`
	Frequency string `json:"frequency"`
	At        string `json:"at"`
	Jitter    string `json:"jitter"`
	CatchUp   string `json:"catch_up"`
}

type WeeklySchedule struct {
	PeriodicSchedule
	Weekday string `json:"weekday"`
}

func (s *WeeklySchedule) UnmarshalJSON(value []byte) error {
	var decoded struct {
		Enabled   *bool  `json:"enabled,omitempty"`
		Frequency string `json:"frequency"`
		At        string `json:"at"`
		Jitter    string `json:"jitter"`
		CatchUp   string `json:"catch_up"`
		Weekday   string `json:"weekday"`
	}
	if err := json.Unmarshal(value, &decoded); err != nil {
		return err
	}
	s.PeriodicSchedule = PeriodicSchedule{
		Enabled: decoded.Enabled, Frequency: decoded.Frequency, At: decoded.At,
		Jitter: decoded.Jitter, CatchUp: decoded.CatchUp,
	}
	s.Weekday = decoded.Weekday
	return nil
}

func (s WeeklySchedule) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Enabled   *bool  `json:"enabled,omitempty"`
		Frequency string `json:"frequency"`
		At        string `json:"at"`
		Jitter    string `json:"jitter"`
		CatchUp   string `json:"catch_up"`
		Weekday   string `json:"weekday"`
	}{
		Enabled: s.Enabled, Frequency: s.Frequency, At: s.At, Jitter: s.Jitter,
		CatchUp: s.CatchUp, Weekday: s.Weekday,
	})
}

type MaintenanceWindow struct {
	ID       string `json:"id,omitempty"`
	Name     string `json:"name"`
	Cron     string `json:"cron"`
	Duration string `json:"duration"`
}

// MaintenanceWindowOccurrence is a server-clock preview of the next time a
// configured maintenance window opens. At is always returned in UTC.
type MaintenanceWindowOccurrence struct {
	ID       string        `json:"id,omitempty"`
	Name     string        `json:"name"`
	At       time.Time     `json:"at"`
	Duration time.Duration `json:"-"`
}

func DefaultSchedule() SchedulePolicy {
	enabled := true
	return SchedulePolicy{
		Timezone: "UTC",
		DailyDiscovery: PeriodicSchedule{
			Enabled: &enabled, Frequency: "daily", At: "02:00", Jitter: "20m", CatchUp: "6h",
		},
		WeeklyCandidate: WeeklySchedule{
			PeriodicSchedule: PeriodicSchedule{
				Enabled: &enabled, Frequency: "weekly", At: "03:00", Jitter: "20m", CatchUp: "48h",
			},
			Weekday: "Sunday",
		},
		MaximumStableImageAge: (7 * 24 * time.Hour).String(),
	}
}

func ValidateScheduleJSON(value []byte) (SchedulePolicy, error) {
	var schedule SchedulePolicy
	decoder := json.NewDecoder(strings.NewReader(string(value)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&schedule); err != nil {
		return SchedulePolicy{}, fmt.Errorf("decode schedule: %w", err)
	}
	if err := schedule.Normalize(); err != nil {
		return SchedulePolicy{}, err
	}
	return schedule, nil
}

func (s SchedulePolicy) Validate() error {
	return (&s).Normalize()
}

func (s *SchedulePolicy) Normalize() error {
	defaults := DefaultSchedule()
	if strings.TrimSpace(s.Timezone) == "" {
		s.Timezone = defaults.Timezone
	}
	fillPeriodic(&s.DailyDiscovery, defaults.DailyDiscovery)
	fillPeriodic(&s.WeeklyCandidate.PeriodicSchedule, defaults.WeeklyCandidate.PeriodicSchedule)
	if s.WeeklyCandidate.Weekday == "" {
		s.WeeklyCandidate.Weekday = defaults.WeeklyCandidate.Weekday
	}
	if strings.TrimSpace(s.MaximumStableImageAge) == "" {
		s.MaximumStableImageAge = defaults.MaximumStableImageAge
	}
	if strings.TrimSpace(s.Timezone) == "" {
		return errors.New("schedule timezone is required")
	}
	if _, err := time.LoadLocation(s.Timezone); err != nil {
		return fmt.Errorf("schedule timezone %q is not an IANA timezone", s.Timezone)
	}
	if err := validatePeriodic("daily_discovery", s.DailyDiscovery, "daily"); err != nil {
		return err
	}
	if err := validatePeriodic("weekly_candidate", s.WeeklyCandidate.PeriodicSchedule, "weekly"); err != nil {
		return err
	}
	weekdays := map[string]struct{}{
		"Sunday": {}, "Monday": {}, "Tuesday": {}, "Wednesday": {},
		"Thursday": {}, "Friday": {}, "Saturday": {},
	}
	if _, ok := weekdays[s.WeeklyCandidate.Weekday]; !ok {
		return fmt.Errorf("weekly_candidate.weekday %q is invalid", s.WeeklyCandidate.Weekday)
	}
	maximumStableAge, err := time.ParseDuration(s.MaximumStableImageAge)
	if err != nil || maximumStableAge <= 0 || maximumStableAge > 365*24*time.Hour {
		return errors.New("maximum_stable_image_age must be between 1ns and 8760h")
	}
	if len(s.MaintenanceWindow) > 32 {
		return errors.New("maintenance_windows cannot contain more than 32 entries")
	}
	parser := maintenanceCronParser()
	seen := make(map[string]struct{}, len(s.MaintenanceWindow))
	for index, window := range s.MaintenanceWindow {
		if strings.TrimSpace(window.Name) == "" {
			return fmt.Errorf("maintenance_windows[%d].name is required", index)
		}
		identity := window.ID
		if identity == "" {
			identity = window.Name
		}
		if _, duplicate := seen[identity]; duplicate {
			return fmt.Errorf("duplicate maintenance window %q", identity)
		}
		seen[identity] = struct{}{}
		if _, err := parser.Parse(window.Cron); err != nil {
			return fmt.Errorf("maintenance_windows[%d].cron is invalid: %w", index, err)
		}
		duration, err := time.ParseDuration(window.Duration)
		if err != nil || duration <= 0 || duration > 7*24*time.Hour {
			return fmt.Errorf("maintenance_windows[%d].duration must be between 1ns and 168h", index)
		}
	}
	location, _ := time.LoadLocation(s.Timezone)
	if err := validateMaintenanceWindowOverlaps(parser, location, s.MaintenanceWindow); err != nil {
		return err
	}
	return nil
}

// MaximumStableAge returns the normalized freshness ceiling used by the
// weekly candidate scheduler. Validation keeps the scheduler from silently
// treating malformed policy as either a rebuild or a no-op.
func (s SchedulePolicy) MaximumStableAge() (time.Duration, error) {
	if err := (&s).Normalize(); err != nil {
		return 0, err
	}
	return time.ParseDuration(s.MaximumStableImageAge)
}

// NextMaintenanceWindows returns one trusted next-open preview per configured
// window. The policy is normalized first, so invalid timezone, cron, duration,
// duplicate, and overlap configurations fail closed.
func (s SchedulePolicy) NextMaintenanceWindows(now time.Time) ([]MaintenanceWindowOccurrence, error) {
	if err := (&s).Normalize(); err != nil {
		return nil, err
	}
	location, _ := time.LoadLocation(s.Timezone)
	localNow := now.In(location)
	parser := maintenanceCronParser()
	result := make([]MaintenanceWindowOccurrence, 0, len(s.MaintenanceWindow))
	for _, window := range s.MaintenanceWindow {
		schedule, _ := parser.Parse(window.Cron)
		duration, _ := time.ParseDuration(window.Duration)
		result = append(result, MaintenanceWindowOccurrence{
			ID: window.ID, Name: window.Name,
			At: schedule.Next(localNow).UTC(), Duration: duration,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].At.Equal(result[j].At) {
			return result[i].Name < result[j].Name
		}
		return result[i].At.Before(result[j].At)
	})
	return result, nil
}

// validateMaintenanceWindowOverlaps checks a deterministic representative
// horizon. Five-field cron schedules repeat frequently enough that 400 days
// covers every month/day/weekday combination used by the policy language; the
// occurrence cap bounds adversarial per-minute schedules.
func validateMaintenanceWindowOverlaps(
	parser cron.Parser,
	location *time.Location,
	windows []MaintenanceWindow,
) error {
	if len(windows) < 2 {
		return nil
	}
	start := time.Date(2024, time.January, 1, 0, 0, 0, 0, location).Add(-time.Nanosecond)
	end := start.AddDate(0, 0, 400)
	const maxOccurrencesPerPair = 2048
	for leftIndex := 0; leftIndex < len(windows); leftIndex++ {
		leftSchedule, _ := parser.Parse(windows[leftIndex].Cron)
		leftDuration, _ := time.ParseDuration(windows[leftIndex].Duration)
		for rightIndex := leftIndex + 1; rightIndex < len(windows); rightIndex++ {
			rightSchedule, _ := parser.Parse(windows[rightIndex].Cron)
			rightDuration, _ := time.ParseDuration(windows[rightIndex].Duration)
			left := leftSchedule.Next(start)
			right := rightSchedule.Next(start)
			for occurrence := 0; occurrence < maxOccurrencesPerPair && !left.After(end) && !right.After(end); occurrence++ {
				if left.Before(right.Add(rightDuration)) && right.Before(left.Add(leftDuration)) {
					return fmt.Errorf(
						"maintenance windows %q and %q overlap",
						windows[leftIndex].Name,
						windows[rightIndex].Name,
					)
				}
				if !left.After(right) {
					left = leftSchedule.Next(left)
				} else {
					right = rightSchedule.Next(right)
				}
			}
		}
	}
	return nil
}

// MaintenanceWindowStatus evaluates the configured five-field cron schedules
// in the policy timezone. No configured windows means unrestricted operation.
// The returned IDs are stable policy identities suitable for UI/audit output.
func (s SchedulePolicy) MaintenanceWindowStatus(now time.Time) (bool, []string, error) {
	if err := (&s).Normalize(); err != nil {
		return false, nil, err
	}
	if len(s.MaintenanceWindow) == 0 {
		return true, nil, nil
	}
	location, _ := time.LoadLocation(s.Timezone)
	localNow := now.In(location)
	parser := maintenanceCronParser()
	open := make([]string, 0)
	for _, window := range s.MaintenanceWindow {
		duration, _ := time.ParseDuration(window.Duration)
		schedule, _ := parser.Parse(window.Cron)
		firstPossible := localNow.Add(-duration).Add(-time.Nanosecond)
		occurrence := schedule.Next(firstPossible)
		for !occurrence.IsZero() && !occurrence.After(localNow) {
			if localNow.Before(occurrence.Add(duration)) {
				identity := strings.TrimSpace(window.ID)
				if identity == "" {
					identity = window.Name
				}
				open = append(open, identity)
				break
			}
			occurrence = schedule.Next(occurrence)
		}
	}
	sort.Strings(open)
	return len(open) > 0, open, nil
}

func maintenanceCronParser() cron.Parser {
	return cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
}

func fillPeriodic(schedule *PeriodicSchedule, defaults PeriodicSchedule) {
	if schedule.Enabled == nil {
		enabled := true
		schedule.Enabled = &enabled
	}
	if schedule.Frequency == "" {
		schedule.Frequency = defaults.Frequency
	}
	if schedule.At == "" {
		schedule.At = defaults.At
	}
	if schedule.Jitter == "" {
		schedule.Jitter = defaults.Jitter
	}
	if schedule.CatchUp == "" {
		schedule.CatchUp = defaults.CatchUp
	}
}

func (s PeriodicSchedule) IsEnabled() bool {
	return s.Enabled == nil || *s.Enabled
}

func validatePeriodic(field string, schedule PeriodicSchedule, frequency string) error {
	if schedule.Frequency != frequency {
		return fmt.Errorf("%s.frequency must be %q", field, frequency)
	}
	if _, _, err := parsePolicyClock(schedule.At); err != nil {
		return fmt.Errorf("%s.at: %w", field, err)
	}
	jitter, err := time.ParseDuration(schedule.Jitter)
	if err != nil || jitter < 0 || jitter > 24*time.Hour {
		return fmt.Errorf("%s.jitter must be between 0 and 24h", field)
	}
	catchUp, err := time.ParseDuration(schedule.CatchUp)
	if err != nil || catchUp <= 0 {
		return fmt.Errorf("%s.catch_up must be a positive Go duration", field)
	}
	return nil
}

func parsePolicyClock(value string) (int, int, error) {
	parsed, err := time.Parse("15:04", value)
	if err != nil {
		return 0, 0, errors.New("must use 24-hour HH:MM")
	}
	return parsed.Hour(), parsed.Minute(), nil
}
