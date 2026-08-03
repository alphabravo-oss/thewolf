// Package scannerreleasescheduler connects logical scanner schedules to the
// durable release persistence lease contract.
package scannerreleasescheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/scannerobservability"
	"github.com/alphabravocompany/thewolf/internal/scannerpolicy"
	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
	"github.com/alphabravocompany/thewolf/internal/scannerschedule"
	"github.com/alphabravocompany/thewolf/internal/scannertrace"
)

const (
	KindDiscovery = "discovery"
	KindCandidate = "candidate"
	ScopeComplete = "complete"
)

var ErrScheduleLeaseLost = errors.New("scanner release schedule lease lost")

type Job struct {
	Schedule scannerschedule.Schedule
	Scope    string
}

func (j Job) validate() error {
	if err := j.Schedule.Validate(); err != nil {
		return err
	}
	switch j.Schedule.Kind {
	case KindDiscovery, KindCandidate:
	default:
		return fmt.Errorf("unsupported scanner release schedule kind %q", j.Schedule.Kind)
	}
	if strings.TrimSpace(j.Scope) == "" {
		return errors.New("scanner release schedule scope is required")
	}
	return nil
}

func (j Job) leaseKey() string {
	return j.Schedule.Key + "/" + j.Schedule.Kind + "/" + j.Scope
}

type Request struct {
	Kind           string
	Scope          string
	Trigger        scannerrelease.DiscoveryTrigger
	ScheduleKey    string
	SchedulePeriod string
	Actor          string
	IdempotencyKey string
	ScheduledAt    time.Time
}

type Enqueuer interface {
	EnqueueScannerRelease(context.Context, Request) (string, error)
}

type Config struct {
	Store             scannerrelease.ScheduleLeaseRepository
	Enqueuer          Enqueuer
	Owner             string
	LeaseDuration     time.Duration
	HeartbeatInterval time.Duration
	Now               func() time.Time
	Observer          scannerobservability.Observer
}

type Scheduler struct {
	config Config
}

func New(config Config) (*Scheduler, error) {
	switch {
	case config.Store == nil:
		return nil, errors.New("scanner release scheduler store is required")
	case config.Enqueuer == nil:
		return nil, errors.New("scanner release scheduler enqueuer is required")
	case strings.TrimSpace(config.Owner) == "":
		return nil, errors.New("scanner release scheduler owner is required")
	}
	if config.LeaseDuration == 0 {
		config.LeaseDuration = 5 * time.Minute
	}
	if config.HeartbeatInterval == 0 {
		config.HeartbeatInterval = time.Minute
	}
	if config.Now == nil {
		config.Now = func() time.Time { return time.Now().UTC() }
	}
	if config.LeaseDuration <= config.HeartbeatInterval*2 {
		return nil, errors.New("scanner schedule lease must exceed two heartbeat intervals")
	}
	return &Scheduler{config: config}, nil
}

// Tick evaluates all schedules once. Replica safety comes from a database
// lease keyed by schedule name, operation kind, scope, and logical period.
func (s *Scheduler) Tick(ctx context.Context, jobs []Job) error {
	ctx, _ = scannertrace.Ensure(ctx, "scheduler")
	started := s.now()
	result := "success"
	if s.config.Observer != nil {
		s.config.Observer.SetState(scannerobservability.ComponentScheduler, "active")
	}
	defer func() {
		if s.config.Observer != nil {
			s.config.Observer.ObserveRun(
				scannerobservability.ComponentScheduler, result, s.now().Sub(started),
			)
			if result == "error" {
				s.config.Observer.SetState(scannerobservability.ComponentScheduler, "degraded")
			}
		}
	}()
	now := s.now()
	var failures []error
	for _, job := range jobs {
		if err := job.validate(); err != nil {
			failures = append(failures, fmt.Errorf("scanner release schedule %q: %w", job.Schedule.Key, err))
			continue
		}
		if !job.Schedule.Enabled {
			continue
		}
		period, due, err := job.Schedule.IsDue(now)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if !due {
			continue
		}
		if err := s.runPeriod(ctx, job, period); err != nil {
			failures = append(failures, err)
		}
	}
	if len(failures) > 0 {
		result = "error"
	}
	return errors.Join(failures...)
}

func (s *Scheduler) runPeriod(ctx context.Context, job Job, period scannerschedule.Period) error {
	ctx, _ = scannertrace.Ensure(ctx, "scheduler")
	now := s.now()
	leaseKey := job.leaseKey()
	lease, acquired, err := s.config.Store.AcquireScheduleLease(
		ctx, leaseKey, period.Key, s.config.Owner, now, now.Add(s.config.LeaseDuration),
	)
	if err != nil {
		s.observeClaim("error")
		return fmt.Errorf("acquire scanner schedule %s/%s: %w", leaseKey, period.Key, err)
	}
	if !acquired {
		s.observeClaim("contended")
		return nil
	}
	scannertrace.Logger(ctx).Info().
		Str("aggregate_type", "schedule").
		Str("aggregate_id", leaseKey+"/"+period.Key).
		Str("state", "acquired").
		Msg("scanner release work claimed")
	s.observeClaim("acquired")
	if lease.Version > 1 {
		s.observeLease("reclaimed")
		s.observeRetry("stale_lease")
		s.setStuck("expired_lease", 1)
	}
	runContext, cancel := context.WithCancelCause(ctx)
	stop := make(chan struct{})
	done := make(chan struct{})
	go s.monitor(runContext, lease, cancel, stop, done)
	idempotencyKey := strings.Join(
		[]string{"scanner-schedule", job.Schedule.Key, job.Schedule.Kind, job.Scope, period.Key},
		"/",
	)
	resultRef, enqueueErr := s.config.Enqueuer.EnqueueScannerRelease(runContext, Request{
		Kind: job.Schedule.Kind, Scope: job.Scope, Trigger: scannerrelease.DiscoveryScheduled,
		ScheduleKey: job.Schedule.Key, SchedulePeriod: period.Key,
		Actor: "scanner-scheduler:" + s.config.Owner, IdempotencyKey: idempotencyKey,
		ScheduledAt: now,
	})
	close(stop)
	<-done
	cause := context.Cause(runContext)
	cancel(nil)
	if cause != nil && !errors.Is(cause, context.Canceled) {
		s.observeLease("lost")
		s.setStuck("lease_lost", 1)
		return cause
	}
	if enqueueErr != nil {
		// Leave the lease active. After expiration another replica can safely
		// retry the same idempotent enqueue.
		s.observeResult("failed")
		s.setStuck("expired_lease", 1)
		return fmt.Errorf("enqueue scanner schedule %s/%s: %w", leaseKey, period.Key, enqueueErr)
	}
	now = s.now()
	completed, err := s.config.Store.CompleteScheduleLease(
		ctx, leaseKey, period.Key, s.config.Owner, lease.Token,
		scannerrelease.LeaseCompleted, resultRef, now,
	)
	if err != nil {
		s.observeLease("error")
		return fmt.Errorf("complete scanner schedule %s/%s: %w", leaseKey, period.Key, err)
	}
	if !completed {
		s.observeLease("lost")
		s.setStuck("lease_lost", 1)
		return ErrScheduleLeaseLost
	}
	s.observeLease("completed")
	s.observeResult("completed")
	s.setStuck("expired_lease", 0)
	s.setStuck("lease_lost", 0)
	scannertrace.Logger(ctx).Info().
		Str("aggregate_type", "schedule").
		Str("aggregate_id", leaseKey+"/"+period.Key).
		Str("state", "completed").
		Msg("scanner release work completed")
	return nil
}

// EnqueueOnDemand uses caller-supplied idempotency instead of a schedule
// lease. API and CLI callers can therefore request complete or selected scopes
// immediately without changing periodic scheduling.
func (s *Scheduler) EnqueueOnDemand(ctx context.Context, request Request) (string, error) {
	if request.Kind != KindDiscovery && request.Kind != KindCandidate {
		return "", fmt.Errorf("unsupported scanner release operation kind %q", request.Kind)
	}
	if request.Scope == "" || request.Actor == "" || request.IdempotencyKey == "" {
		return "", errors.New("on-demand scanner release scope, actor, and idempotency key are required")
	}
	if request.Trigger == "" {
		request.Trigger = scannerrelease.DiscoveryOnDemand
	}
	if request.Trigger != scannerrelease.DiscoveryOnDemand &&
		request.Trigger != scannerrelease.DiscoverySecurity {
		return "", fmt.Errorf("invalid on-demand scanner release trigger %q", request.Trigger)
	}
	return s.config.Enqueuer.EnqueueScannerRelease(ctx, request)
}

func (s *Scheduler) monitor(
	ctx context.Context,
	lease *scannerrelease.ScheduleLease,
	cancel context.CancelCauseFunc,
	stop <-chan struct{},
	done chan<- struct{},
) {
	defer close(done)
	ticker := time.NewTicker(s.config.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-ticker.C:
			now := s.now()
			ok, err := s.config.Store.HeartbeatScheduleLease(
				ctx, lease.ScheduleKey, lease.PeriodKey, s.config.Owner, lease.Token,
				now, now.Add(s.config.LeaseDuration),
			)
			if err != nil || !ok {
				if err != nil {
					s.observeLease("error")
				} else {
					s.observeLease("lost")
				}
				cancel(ErrScheduleLeaseLost)
				return
			}
			s.observeLease("heartbeat")
		}
	}
}

func (s *Scheduler) now() time.Time {
	return s.config.Now().UTC()
}

func (s *Scheduler) observeClaim(result string) {
	if s.config.Observer != nil {
		s.config.Observer.ObserveClaim(scannerobservability.ComponentScheduler, result)
	}
}

func (s *Scheduler) observeLease(result string) {
	if s.config.Observer != nil {
		s.config.Observer.ObserveLease(scannerobservability.ComponentScheduler, result)
	}
}

func (s *Scheduler) observeRetry(reason string) {
	if s.config.Observer != nil {
		s.config.Observer.ObserveRetry(scannerobservability.ComponentScheduler, reason)
	}
}

func (s *Scheduler) observeResult(state string) {
	if s.config.Observer != nil {
		s.config.Observer.ObserveResult(scannerobservability.ComponentScheduler, state)
	}
}

func (s *Scheduler) setStuck(kind string, count int) {
	if s.config.Observer != nil {
		s.config.Observer.SetStuckWork(scannerobservability.ComponentScheduler, kind, count)
	}
}

type PolicySnapshotProvider interface {
	PolicySnapshot(context.Context) (string, int64, error)
}

type DefinitionProvider interface {
	DefinitionCommit(context.Context) (string, error)
}

type PersistentEnqueuer struct {
	Store interface {
		scannerrelease.PolicyRepository
		scannerrelease.DiscoveryRepository
		scannerrelease.CandidateRepository
		scannerrelease.ReleaseRepository
	}
	Policies   PolicySnapshotProvider
	Definition DefinitionProvider
}

func (e PersistentEnqueuer) EnqueueScannerRelease(
	ctx context.Context,
	request Request,
) (string, error) {
	if e.Store == nil || e.Policies == nil || e.Definition == nil {
		return "", errors.New("persistent scanner release enqueuer dependencies are required")
	}
	policyID, policyRevision, err := e.Policies.PolicySnapshot(ctx)
	if err != nil {
		return "", err
	}
	definitionCommit, err := e.Definition.DefinitionCommit(ctx)
	if err != nil {
		return "", err
	}
	payload, _ := json.Marshal(map[string]string{
		"kind": request.Kind, "scope": request.Scope, "schedule_key": request.ScheduleKey,
		"schedule_period": request.SchedulePeriod, "trigger": string(request.Trigger),
	})
	command := scannerrelease.TransitionCommand{
		Actor: request.Actor, Reason: "scanner release operation enqueued",
		PolicyRevision: policyRevision, IdempotencyKey: request.IdempotencyKey,
		PayloadJSON: string(payload),
	}
	if request.Kind == KindCandidate {
		scopeJSON := discoveryScopeJSON(request.Scope)
		latest, err := e.Store.GetLatestCompletedDiscovery(
			ctx, definitionCommit, policyID, policyRevision, scopeJSON,
		)
		if err != nil {
			return "", err
		}
		if latest == nil || !scannerrelease.DiscoveryEligibleForCandidate(latest) {
			return "", errors.New("no complete scanner discovery snapshot is available for the scheduled candidate")
		}
		policy, err := e.Store.GetPolicy(ctx, policyID)
		if err != nil {
			return "", fmt.Errorf("load scheduled candidate policy: %w", err)
		}
		if policy.Revision != policyRevision {
			return "", errors.New("scheduled candidate policy snapshot changed during enqueue")
		}
		rebuildDue, rebuildReason, err := e.scheduledRebuildDecision(
			ctx, policy, request.ScheduledAt,
		)
		if err != nil {
			return "", err
		}
		requiredGatesJSON, err := scheduledRequiredGates(policy)
		if err != nil {
			return "", err
		}
		selection := map[string]any{
			"mode":               "complete",
			"discovery_run_id":   latest.ID,
			"no_op_if_unchanged": request.Trigger == scannerrelease.DiscoveryScheduled && !rebuildDue,
			"force_rebuild":      rebuildDue,
			"rebuild_reason":     rebuildReason,
		}
		selectionJSON, _ := json.Marshal(selection)
		candidate := &scannerrelease.Candidate{
			ID: uuid.NewString(), DiscoveryRunID: latest.ID,
			SelectionJSON: string(selectionJSON), DefinitionCommit: definitionCommit,
			RiskSummaryJSON: "{}", State: scannerrelease.CandidateAwaitingDefinition,
			RequiredGatesJSON: requiredGatesJSON, PolicyID: policyID, PolicyRevision: policyRevision,
			Actor: request.Actor, IdempotencyKey: request.IdempotencyKey,
		}
		if err := e.Store.CreateCandidate(ctx, candidate, command); err != nil {
			return "", err
		}
		return candidate.ID, nil
	}
	run := &scannerrelease.DiscoveryRun{
		Trigger: request.Trigger, SchedulePeriod: request.SchedulePeriod,
		DefinitionCommit: definitionCommit, PolicyID: policyID, PolicyRevision: policyRevision,
		ScopeJSON: discoveryScopeJSON(request.Scope),
		State:     scannerrelease.DiscoveryQueued, Actor: request.Actor,
		IdempotencyKey: request.IdempotencyKey,
	}
	if err := e.Store.CreateDiscoveryRun(ctx, run, command); err != nil {
		return "", err
	}
	return run.ID, nil
}

func (e PersistentEnqueuer) scheduledRebuildDecision(
	ctx context.Context,
	policy *scannerrelease.Policy,
	at time.Time,
) (bool, string, error) {
	if policy == nil {
		return false, "", errors.New("scheduled candidate policy is required")
	}
	schedule, err := scannerpolicy.ValidateScheduleJSON([]byte(policy.ScheduleJSON))
	if err != nil {
		return false, "", fmt.Errorf("validate scheduled candidate freshness policy: %w", err)
	}
	if schedule.ForceWeeklyRebuild {
		return true, "policy_forced_weekly_rebuild", nil
	}
	maximumAge, err := schedule.MaximumStableAge()
	if err != nil {
		return false, "", fmt.Errorf("validate maximum stable image age: %w", err)
	}
	if at.IsZero() {
		at = time.Now().UTC()
	} else {
		at = at.UTC()
	}
	page, err := e.Store.ListReleases(
		ctx,
		scannerrelease.ReleaseFilter{State: scannerrelease.ReleaseStable},
		scannerrelease.PageRequest{Limit: 1},
	)
	if err != nil {
		return false, "", fmt.Errorf("load current stable scanner release: %w", err)
	}
	if len(page.Items) == 0 {
		return true, "no_stable_release", nil
	}
	publishedAt := page.Items[0].PublishedAt
	if publishedAt.IsZero() {
		publishedAt = page.Items[0].CreatedAt
	}
	if publishedAt.IsZero() || !at.Before(publishedAt.Add(maximumAge)) {
		return true, "maximum_stable_image_age_exceeded", nil
	}
	return false, "stable_release_within_maximum_age", nil
}

func scheduledRequiredGates(policy *scannerrelease.Policy) (string, error) {
	if policy == nil {
		return "", errors.New("scheduled candidate policy is required")
	}
	var rules scannerpolicy.Policy
	if err := json.Unmarshal([]byte(policy.RulesJSON), &rules); err != nil {
		return "", fmt.Errorf("decode scheduled candidate policy: %w", err)
	}
	if err := rules.Normalize(); err != nil {
		return "", fmt.Errorf("validate scheduled candidate policy: %w", err)
	}
	encoded, err := json.Marshal(rules.RequiredGates)
	if err != nil {
		return "", fmt.Errorf("encode scheduled candidate gates: %w", err)
	}
	return string(encoded), nil
}

func discoveryScopeJSON(scope string) string {
	scope = strings.TrimSpace(scope)
	if scope == "" || scope == ScopeComplete {
		return `{"mode":"complete"}`
	}
	const selectedPrefix = "selected:"
	if strings.HasPrefix(scope, selectedPrefix) {
		names := strings.Split(strings.TrimPrefix(scope, selectedPrefix), ",")
		selected := make([]string, 0, len(names))
		for _, name := range names {
			if name = strings.TrimSpace(name); name != "" {
				selected = append(selected, name)
			}
		}
		if encoded, err := json.Marshal(map[string]any{
			"mode":  "selected",
			"tools": selected,
		}); err == nil {
			return string(encoded)
		}
	}
	encoded, _ := json.Marshal(map[string]string{"mode": scope})
	return string(encoded)
}

type LatestPolicy struct {
	Store scannerrelease.PolicyRepository
	Scope string
}

func (p LatestPolicy) PolicySnapshot(ctx context.Context) (string, int64, error) {
	if p.Store == nil {
		return "", 0, errors.New("scanner release policy store is required")
	}
	policies, err := p.Store.ListPolicies(ctx, p.Scope, true)
	if err != nil {
		return "", 0, err
	}
	if len(policies) == 0 {
		return "", 0, fmt.Errorf("no enabled scanner release policy for scope %q", p.Scope)
	}
	latest := policies[0]
	for _, policy := range policies[1:] {
		if policy.Revision > latest.Revision {
			latest = policy
		}
	}
	return latest.ID, latest.Revision, nil
}

type StaticDefinition string

func (d StaticDefinition) DefinitionCommit(context.Context) (string, error) {
	value := strings.TrimSpace(string(d))
	if value == "" {
		return "", errors.New("scanner definition commit is required")
	}
	return value, nil
}
