package scannerrollout

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/alphabravocompany/thewolf/internal/scannerobservability"
	"github.com/alphabravocompany/thewolf/internal/scannerpolicy"
	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
	"github.com/alphabravocompany/thewolf/internal/scannertrace"
)

const (
	defaultControllerPoll       = 2 * time.Second
	defaultReconcileInterval    = 15 * time.Second
	defaultRolloutHeartbeat     = 10 * time.Second
	defaultRolloutLease         = 45 * time.Second
	defaultRolloutCohortTimeout = time.Hour
)

type Controller struct {
	config  Config
	writeMu sync.Mutex
}

type leaseGuard struct {
	mu      sync.RWMutex
	version int64
	state   scannerrelease.RolloutState
}

func (g *leaseGuard) expected() (int64, scannerrelease.RolloutState) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.version, g.state
}

func (g *leaseGuard) accept(rollout *scannerrelease.Rollout) {
	g.mu.Lock()
	g.version = rollout.Version
	g.state = rollout.State
	g.mu.Unlock()
}

func NewController(config Config) (*Controller, error) {
	switch {
	case config.Store == nil:
		return nil, errors.New("scanner rollout controller store is required")
	case config.Runtime == nil:
		return nil, errors.New("scanner rollout controller runtime is required")
	case config.WorkerID == "":
		return nil, errors.New("scanner rollout controller worker ID is required")
	}
	if config.Gate == nil {
		config.Gate = SnapshotProgressGate{}
	}
	if config.PollInterval == 0 {
		config.PollInterval = defaultControllerPoll
	}
	if config.ReconcileInterval == 0 {
		config.ReconcileInterval = defaultReconcileInterval
	}
	if config.HeartbeatInterval == 0 {
		config.HeartbeatInterval = defaultRolloutHeartbeat
	}
	if config.LeaseDuration == 0 {
		config.LeaseDuration = defaultRolloutLease
	}
	if config.CohortTimeout == 0 {
		config.CohortTimeout = defaultRolloutCohortTimeout
	}
	if config.Now == nil {
		config.Now = func() time.Time { return time.Now().UTC() }
	}
	if config.Sleep == nil {
		config.Sleep = sleepContext
	}
	switch {
	case config.PollInterval <= 0:
		return nil, errors.New("scanner rollout poll interval must be positive")
	case config.ReconcileInterval <= 0:
		return nil, errors.New("scanner rollout reconcile interval must be positive")
	case config.HeartbeatInterval <= 0:
		return nil, errors.New("scanner rollout heartbeat interval must be positive")
	case config.LeaseDuration <= config.HeartbeatInterval*2:
		return nil, errors.New("scanner rollout lease must exceed two heartbeat intervals")
	case config.CohortTimeout <= 0:
		return nil, errors.New("scanner rollout cohort timeout must be positive")
	}
	return &Controller{config: config}, nil
}

func (c *Controller) Run(ctx context.Context) error {
	for {
		processed, err := c.RunOnce(ctx)
		if err != nil {
			return err
		}
		if c.config.Once {
			return nil
		}
		if processed {
			continue
		}
		if err := c.config.Sleep(ctx, c.config.PollInterval); err != nil {
			return err
		}
	}
}

func (c *Controller) RunOnce(ctx context.Context) (bool, error) {
	started := c.now()
	runResult := "success"
	defer func() {
		if c.config.Observer != nil {
			c.config.Observer.ObserveRun(
				scannerobservability.ComponentRollout, runResult, c.now().Sub(started),
			)
		}
	}()
	now := c.now()
	claim, err := c.config.Store.ClaimNextRollout(
		ctx, c.config.WorkerID, now, now.Add(c.config.LeaseDuration),
	)
	if err != nil {
		runResult = "error"
		c.observeClaim("error")
		c.setState("degraded")
		return false, fmt.Errorf("claim scanner rollout: %w", err)
	}
	if claim == nil {
		c.observeClaim("empty")
		c.setState("idle")
		return false, nil
	}
	c.observeClaim("acquired")
	c.setState("busy")
	if claim.Reclaimed {
		c.observeLease("reclaimed")
		c.observeRetry("stale_lease")
	}
	// ClaimNextRollout has replaced any expired owner before returning.
	c.setStuck("expired_lease", 0)
	claimContext, _, traceErr := scannertrace.Resume(
		ctx, c.config.Store, "rollout", claim.RolloutID, "rollout-controller",
	)
	if traceErr != nil {
		runResult = "error"
		c.setState("degraded")
		return true, fmt.Errorf("resume rollout operation correlation: %w", traceErr)
	}
	scannertrace.Logger(claimContext).Info().
		Str("aggregate_type", "rollout").
		Str("aggregate_id", claim.RolloutID).
		Str("state", string(claim.State)).
		Msg("scanner release work claimed")
	err = c.processClaim(claimContext, claim)
	if err != nil {
		runResult = "error"
		c.setState("degraded")
		c.observeResult(rolloutResultState(err))
		if errors.Is(err, ErrRolloutLeaseLost) {
			c.observeLease("lost")
			c.setStuck("lease_lost", 1)
		}
		scannertrace.Logger(claimContext).Warn().
			Str("aggregate_type", "rollout").
			Str("aggregate_id", claim.RolloutID).
			Str("error_class", rolloutResultState(err)).
			Msg("scanner release work failed")
		return true, err
	}
	c.setState("active")
	c.setStuck("lease_lost", 0)
	c.observeResult("completed")
	scannertrace.Logger(claimContext).Info().
		Str("aggregate_type", "rollout").
		Str("aggregate_id", claim.RolloutID).
		Str("state", "reconciled").
		Msg("scanner release work completed")
	return true, nil
}

func (c *Controller) processClaim(
	parent context.Context,
	claim *scannerrelease.RolloutClaim,
) error {
	rollout, err := c.config.Store.GetRollout(parent, claim.RolloutID)
	if err != nil {
		return fmt.Errorf("load claimed scanner rollout: %w", err)
	}
	guard := &leaseGuard{version: rollout.Version, state: rollout.State}
	runContext, cancel := context.WithCancelCause(parent)
	stop := make(chan struct{})
	done := make(chan struct{})
	go c.monitorLease(runContext, claim, guard, cancel, stop, done)

	reconcileErr := c.reconcile(runContext, claim, guard, rollout)
	close(stop)
	<-done
	cause := context.Cause(runContext)
	cancel(nil)
	if errors.Is(cause, ErrRolloutLeaseLost) || errors.Is(cause, ErrRolloutChanged) ||
		errors.Is(cause, context.Canceled) {
		reconcileErr = cause
	} else if cause != nil && reconcileErr == nil {
		reconcileErr = cause
	}
	if errors.Is(reconcileErr, ErrRolloutLeaseLost) {
		return reconcileErr
	}

	now := c.now()
	released, releaseErr := c.config.Store.ReleaseRolloutClaim(
		context.WithoutCancel(parent), claim.RolloutID, c.config.WorkerID, claim.LeaseToken,
		now, now.Add(c.config.ReconcileInterval),
		scannerrelease.TransitionCommand{
			Actor:          c.config.WorkerID,
			Reason:         "rollout reconciliation pass finished",
			IdempotencyKey: "rollout-release:" + claim.LeaseToken,
		},
	)
	if releaseErr != nil {
		c.observeLease("error")
		releaseErr = fmt.Errorf("release scanner rollout claim: %w", releaseErr)
	} else if !released && reconcileErr == nil {
		c.observeLease("lost")
		releaseErr = ErrRolloutLeaseLost
	} else if released {
		c.observeLease("completed")
	}
	if errors.Is(reconcileErr, ErrRolloutChanged) {
		// Pause, resume, cancellation, and manual rollback are expected
		// optimistic changes. The next claim reconciles the new state.
		reconcileErr = nil
	}
	return errors.Join(reconcileErr, releaseErr)
}

func (c *Controller) reconcile(
	ctx context.Context,
	claim *scannerrelease.RolloutClaim,
	guard *leaseGuard,
	rollout *scannerrelease.Rollout,
) error {
	if err := c.checkLease(ctx, claim, guard); err != nil {
		return err
	}
	policy, canaryPolicy, policyErr := rolloutPolicy(rollout.PolicySnapshotJSON)
	cohorts, err := c.config.Store.ListRolloutCohorts(ctx, rollout.ID)
	if err != nil {
		return fmt.Errorf("list scanner rollout cohorts: %w", err)
	}
	if err := validateCohorts(rollout, cohorts); err != nil {
		return c.failRollout(
			ctx, claim, guard, rollout, policy.Revision, "invalid_rollout_cohorts", err.Error(),
		)
	}
	if rollout.State == scannerrelease.RolloutPaused {
		return c.applyLifecycle(ctx, rollout, cohorts, "pause")
	}
	if policyErr != nil {
		if rollout.State == scannerrelease.RolloutRollingBack {
			if rollbackAvailable, releaseErr := c.validateReleases(ctx, rollout); releaseErr != nil || !rollbackAvailable {
				if releaseErr == nil {
					releaseErr = errors.New("rollback release is unavailable")
				}
				return c.failRollout(
					ctx, claim, guard, rollout, 0,
					"rollback_release_unavailable", releaseErr.Error(),
				)
			}
			if err := c.applyLifecycle(ctx, rollout, cohorts, "cancel"); err != nil {
				return err
			}
			return c.reconcileRollback(ctx, claim, guard, rollout, cohorts, 0)
		}
		return c.failRollout(
			ctx, claim, guard, rollout, 0, "invalid_policy_snapshot", policyErr.Error(),
		)
	}
	rollbackAvailable, releaseErr := c.validateReleases(ctx, rollout)
	if releaseErr != nil {
		if rollbackAvailable && policy.Rollback.Automatic {
			_, transitionErr := c.transition(
				ctx, claim, guard, rollout, scannerrelease.RolloutRollingBack,
				policy.Revision, "release_preflight_failed", releaseErr.Error(),
			)
			return transitionErr
		}
		return c.failRollout(
			ctx, claim, guard, rollout, policy.Revision, "release_preflight_failed", releaseErr.Error(),
		)
	}

	if rollout.State != scannerrelease.RolloutRollingBack {
		gate, err := c.config.Gate.Evaluate(ctx, GateRequest{
			Rollout: rollout, Policy: policy, Now: c.now(),
		})
		if err != nil {
			return fmt.Errorf("evaluate scanner rollout progress gate: %w", err)
		}
		if !gate.Allowed {
			if err := c.applyLifecycle(ctx, rollout, cohorts, "pause"); err != nil {
				return err
			}
			_, err := c.transition(
				ctx, claim, guard, rollout, scannerrelease.RolloutPaused,
				policy.Revision, "maintenance_gate_closed", gate.Reason,
			)
			return err
		}
		if rollout.State != scannerrelease.RolloutPending {
			if err := c.applyLifecycle(ctx, rollout, cohorts, "resume"); err != nil {
				return err
			}
		}
	}

	switch rollout.State {
	case scannerrelease.RolloutPending:
		_, err := c.transition(
			ctx, claim, guard, rollout, scannerrelease.RolloutPreparing,
			policy.Revision, "preflight_passed", "release, policy, and maintenance preflight passed",
		)
		return err
	case scannerrelease.RolloutPreparing:
		if err := c.ensureAssigned(
			ctx, claim, guard, rollout, &cohorts[0], policy.Revision, false,
		); err != nil {
			return err
		}
		_, err := c.transition(
			ctx, claim, guard, rollout, scannerrelease.RolloutCanary,
			policy.Revision, "canary_assigned", "canary cohort assignment accepted",
		)
		return err
	case scannerrelease.RolloutCanary:
		return c.reconcileCanary(
			ctx, claim, guard, rollout, &cohorts[0], cohorts,
			policy, canaryPolicy, false,
		)
	case scannerrelease.RolloutVerifying:
		return c.reconcileCanary(
			ctx, claim, guard, rollout, &cohorts[0], cohorts,
			policy, canaryPolicy, true,
		)
	case scannerrelease.RolloutRollingOut:
		return c.reconcileStable(
			ctx, claim, guard, rollout, cohorts, policy, canaryPolicy,
		)
	case scannerrelease.RolloutRollingBack:
		if err := c.applyLifecycle(ctx, rollout, cohorts, "cancel"); err != nil {
			return err
		}
		return c.reconcileRollback(
			ctx, claim, guard, rollout, cohorts, policy.Revision,
		)
	default:
		return nil
	}
}

func (c *Controller) reconcileCanary(
	ctx context.Context,
	claim *scannerrelease.RolloutClaim,
	guard *leaseGuard,
	rollout *scannerrelease.Rollout,
	cohort *scannerrelease.RolloutCohort,
	cohorts []scannerrelease.RolloutCohort,
	policy scannerpolicy.Policy,
	canaryPolicy CanaryPolicy,
	verification bool,
) error {
	if err := c.ensureAssigned(
		ctx, claim, guard, rollout, cohort, policy.Revision, false,
	); err != nil {
		return err
	}
	snapshot, err := c.observeAndPersist(
		ctx, claim, guard, rollout, cohort, stableCohortName(cohorts),
		policy.Revision, verification,
	)
	if err != nil {
		return err
	}
	decision, err := evaluateSnapshot(canaryPolicy, snapshot, cohort, c.now())
	if err != nil {
		return err
	}
	switch decision.Outcome {
	case CanaryRollback:
		return c.handleUnhealthy(
			ctx, claim, guard, rollout, policy, decision.Reasons,
		)
	case CanaryPending:
		return nil
	}
	if !verification {
		if err := c.setCohortState(
			ctx, claim, guard, rollout, cohort, CohortHealthy,
			policy.Revision, "canary_health_passed", nil,
		); err != nil {
			return err
		}
		_, err := c.transition(
			ctx, claim, guard, rollout, scannerrelease.RolloutVerifying,
			policy.Revision, "canary_observation_passed",
			"canary health thresholds and minimum observation passed",
		)
		return err
	}
	completedAt := c.now()
	if err := c.setCohortState(
		ctx, claim, guard, rollout, cohort, CohortCompleted,
		policy.Revision, "canary_verification_passed", &completedAt,
	); err != nil {
		return err
	}
	_, err = c.transition(
		ctx, claim, guard, rollout, scannerrelease.RolloutRollingOut,
		policy.Revision, "canary_verified", "canary convergence and verification passed",
	)
	return err
}

func (c *Controller) reconcileStable(
	ctx context.Context,
	claim *scannerrelease.RolloutClaim,
	guard *leaseGuard,
	rollout *scannerrelease.Rollout,
	cohorts []scannerrelease.RolloutCohort,
	policy scannerpolicy.Policy,
	canaryPolicy CanaryPolicy,
) error {
	var current *scannerrelease.RolloutCohort
	for index := range cohorts {
		if cohorts[index].State != CohortCompleted {
			current = &cohorts[index]
			break
		}
	}
	if current == nil {
		_, err := c.transition(
			ctx, claim, guard, rollout, scannerrelease.RolloutCompleted,
			policy.Revision, "all_cohorts_completed", "all rollout cohorts converged and passed health gates",
		)
		return err
	}
	if err := c.ensureAssigned(
		ctx, claim, guard, rollout, current, policy.Revision, false,
	); err != nil {
		return err
	}
	snapshot, err := c.observeAndPersist(
		ctx, claim, guard, rollout, current, stableCohortName(cohorts),
		policy.Revision, false,
	)
	if err != nil {
		return err
	}
	decision, err := evaluateSnapshot(canaryPolicy, snapshot, current, c.now())
	if err != nil {
		return err
	}
	switch decision.Outcome {
	case CanaryRollback:
		return c.handleUnhealthy(ctx, claim, guard, rollout, policy, decision.Reasons)
	case CanaryPending:
		return nil
	}
	completedAt := c.now()
	return c.setCohortState(
		ctx, claim, guard, rollout, current, CohortCompleted,
		policy.Revision, "cohort_health_passed", &completedAt,
	)
}

func (c *Controller) reconcileRollback(
	ctx context.Context,
	claim *scannerrelease.RolloutClaim,
	guard *leaseGuard,
	rollout *scannerrelease.Rollout,
	cohorts []scannerrelease.RolloutCohort,
	policyRevision int64,
) error {
	if rollout.FromReleaseID == "" {
		return c.failRollout(
			ctx, claim, guard, rollout, policyRevision,
			"rollback_release_unavailable", "rollout has no prior release to restore",
		)
	}
	for index := len(cohorts) - 1; index >= 0; index-- {
		cohort := &cohorts[index]
		if cohort.State == CohortRolledBack {
			continue
		}
		if err := c.ensureAssigned(
			ctx, claim, guard, rollout, cohort, policyRevision, true,
		); err != nil {
			return err
		}
		snapshot, err := c.observeAndPersist(
			ctx, claim, guard, rollout, cohort, "", policyRevision, false,
		)
		if err != nil {
			return err
		}
		if snapshot.TotalWorkers > 0 &&
			snapshot.ReadyWorkers == snapshot.TotalWorkers &&
			snapshot.FailedWorkers == 0 &&
			snapshot.ObservedReleaseID == rollout.FromReleaseID {
			completedAt := c.now()
			return c.setCohortState(
				ctx, claim, guard, rollout, cohort, CohortRolledBack,
				policyRevision, "rollback_cohort_converged", &completedAt,
			)
		}
		if cohort.Deadline != nil && !c.now().Before(cohort.Deadline.UTC()) {
			_ = c.setCohortState(
				ctx, claim, guard, rollout, cohort, CohortReconciliationFailed,
				policyRevision, "rollback_deadline_exceeded", nil,
			)
			return c.failRollout(
				ctx, claim, guard, rollout, policyRevision,
				"rollback_deadline_exceeded", "rollback cohort did not converge before its deadline",
			)
		}
		return nil
	}
	_, err := c.transition(
		ctx, claim, guard, rollout, scannerrelease.RolloutRolledBack,
		policyRevision, "rollback_completed", "all cohorts restored the prior release",
	)
	return err
}

func (c *Controller) ensureAssigned(
	ctx context.Context,
	claim *scannerrelease.RolloutClaim,
	guard *leaseGuard,
	rollout *scannerrelease.Rollout,
	cohort *scannerrelease.RolloutCohort,
	policyRevision int64,
	rollback bool,
) error {
	desired := rollout.ToReleaseID
	assigningState := CohortAssigning
	observingState := CohortObserving
	action := "cohort_assignment_started"
	if rollback {
		desired = rollout.FromReleaseID
		assigningState = CohortRollbackAssigning
		observingState = CohortRollbackObserving
		action = "cohort_rollback_started"
	}
	if (!rollback && (cohort.State == CohortObserving ||
		cohort.State == CohortHealthy || cohort.State == CohortCompleted)) ||
		(rollback && (cohort.State == CohortRollbackObserving ||
			cohort.State == CohortRolledBack)) {
		return nil
	}
	if cohort.State != assigningState || cohort.DesiredReleaseID != desired {
		now := c.now()
		deadline := now.Add(c.config.CohortTimeout)
		cohort.DesiredReleaseID = desired
		cohort.ObservedReleaseID = ""
		cohort.State = assigningState
		cohort.StartedAt = &now
		cohort.HealthObservedAt = nil
		cohort.CompletedAt = nil
		cohort.Deadline = &deadline
		if err := c.updateCohort(
			ctx, claim, guard, rollout, cohort, policyRevision, action,
		); err != nil {
			return err
		}
	}
	if err := c.checkLease(ctx, claim, guard); err != nil {
		return err
	}
	err := c.config.Runtime.Assign(ctx, AssignmentRequest{
		OperationID: assignmentOperationID(rollout.ID, cohort.ID, desired),
		RolloutID:   rollout.ID, Target: rollout.Target,
		CohortID: cohort.ID, CohortName: cohort.Name,
		DesiredReleaseID: desired, PreviousReleaseID: rollout.FromReleaseID,
		Rollback: rollback,
	})
	if err != nil {
		return fmt.Errorf("assign scanner rollout cohort %q: %w", cohort.Name, err)
	}
	if err := c.checkLease(ctx, claim, guard); err != nil {
		return err
	}
	cohort.State = observingState
	return c.updateCohort(
		ctx, claim, guard, rollout, cohort, policyRevision,
		action+"_accepted",
	)
}

func assignmentOperationID(rolloutID, cohortID, desiredReleaseID string) string {
	return "rollout/" + rolloutID + "/cohort/" + cohortID + "/release/" + desiredReleaseID
}

func (c *Controller) applyLifecycle(
	ctx context.Context,
	rollout *scannerrelease.Rollout,
	cohorts []scannerrelease.RolloutCohort,
	action string,
) error {
	lifecycle, ok := c.config.Runtime.(LifecycleRuntime)
	if !ok {
		return nil
	}
	for _, cohort := range cohorts {
		if !cohortHasDeployment(cohort.State) ||
			strings.TrimSpace(cohort.DesiredReleaseID) == "" {
			continue
		}
		if action == "resume" &&
			(cohort.State == CohortAssigning ||
				cohort.State == CohortRollbackAssigning) {
			continue
		}
		if action == "cancel" &&
			(cohort.DesiredReleaseID != rollout.ToReleaseID ||
				cohort.State == CohortRollbackAssigning ||
				cohort.State == CohortRollbackObserving ||
				cohort.State == CohortRolledBack) {
			continue
		}
		request := AssignmentRequest{
			OperationID: assignmentOperationID(
				rollout.ID, cohort.ID, cohort.DesiredReleaseID,
			),
			RolloutID: rollout.ID, Target: rollout.Target,
			CohortID: cohort.ID, CohortName: cohort.Name,
			DesiredReleaseID:  cohort.DesiredReleaseID,
			PreviousReleaseID: rollout.FromReleaseID,
			Rollback: cohort.DesiredReleaseID == rollout.FromReleaseID &&
				rollout.FromReleaseID != "",
		}
		var err error
		switch action {
		case "pause":
			err = lifecycle.Pause(ctx, request)
		case "resume":
			err = lifecycle.Resume(ctx, request)
		case "cancel":
			err = lifecycle.Cancel(ctx, request)
		default:
			return fmt.Errorf("unsupported rollout lifecycle action %q", action)
		}
		if err != nil {
			return fmt.Errorf(
				"%s scanner rollout cohort %q: %w", action, cohort.Name, err,
			)
		}
	}
	return nil
}

func cohortHasDeployment(state string) bool {
	switch state {
	case CohortAssigning, CohortObserving, CohortHealthy, CohortCompleted,
		CohortRollbackAssigning, CohortRollbackObserving, CohortRolledBack:
		return true
	default:
		return false
	}
}

func (c *Controller) observeAndPersist(
	ctx context.Context,
	claim *scannerrelease.RolloutClaim,
	guard *leaseGuard,
	rollout *scannerrelease.Rollout,
	cohort *scannerrelease.RolloutCohort,
	stableCohort string,
	policyRevision int64,
	syntheticVerification bool,
) (HealthSnapshot, error) {
	if err := c.checkLease(ctx, claim, guard); err != nil {
		return HealthSnapshot{}, err
	}
	snapshot, err := c.config.Runtime.Health(ctx, HealthRequest{
		OperationID: assignmentOperationID(rollout.ID, cohort.ID, cohort.DesiredReleaseID),
		RolloutID:   rollout.ID, Target: rollout.Target,
		CohortID: cohort.ID, CohortName: cohort.Name,
		StableCohortName: stableCohort, DesiredReleaseID: cohort.DesiredReleaseID,
		SyntheticVerification: syntheticVerification,
	})
	if err != nil {
		return HealthSnapshot{}, fmt.Errorf("observe scanner rollout cohort %q: %w", cohort.Name, err)
	}
	if snapshot.ObservedAt.IsZero() {
		snapshot.ObservedAt = c.now()
	}
	snapshot.ObservedAt = snapshot.ObservedAt.UTC()
	if err := snapshot.Validate(); err != nil {
		return HealthSnapshot{}, err
	}
	if err := c.checkLease(ctx, claim, guard); err != nil {
		return HealthSnapshot{}, err
	}
	summary, err := json.Marshal(map[string]any{
		"observed_at":      snapshot.ObservedAt,
		"health":           snapshot.Canary,
		"synthetic_health": snapshot.Synthetic,
		"real_scan_health": snapshot.RealScans,
		"total_workers":    snapshot.TotalWorkers,
		"ready_workers":    snapshot.ReadyWorkers,
		"failed_workers":   snapshot.FailedWorkers,
	})
	if err != nil {
		return HealthSnapshot{}, err
	}
	cohort.ObservedReleaseID = snapshot.ObservedReleaseID
	cohort.TotalWorkers = snapshot.TotalWorkers
	cohort.ReadyWorkers = snapshot.ReadyWorkers
	cohort.FailedWorkers = snapshot.FailedWorkers
	cohort.HealthSummaryJSON = string(summary)
	cohort.HealthObservedAt = &snapshot.ObservedAt
	if err := c.updateCohort(
		ctx, claim, guard, rollout, cohort, policyRevision, "health_snapshot_observed",
	); err != nil {
		return HealthSnapshot{}, err
	}
	return snapshot, nil
}

func (c *Controller) handleUnhealthy(
	ctx context.Context,
	claim *scannerrelease.RolloutClaim,
	guard *leaseGuard,
	rollout *scannerrelease.Rollout,
	policy scannerpolicy.Policy,
	reasons []string,
) error {
	reason := "rollout health gate failed"
	if len(reasons) > 0 {
		reason += ": " + reasons[0]
	}
	target := scannerrelease.RolloutPaused
	code := "health_gate_paused"
	if policy.Rollback.Automatic && rollout.FromReleaseID != "" {
		target = scannerrelease.RolloutRollingBack
		code = "automatic_rollback"
	}
	_, err := c.transition(
		ctx, claim, guard, rollout, target, policy.Revision, code, reason,
	)
	return err
}

func (c *Controller) setCohortState(
	ctx context.Context,
	claim *scannerrelease.RolloutClaim,
	guard *leaseGuard,
	rollout *scannerrelease.Rollout,
	cohort *scannerrelease.RolloutCohort,
	state string,
	policyRevision int64,
	action string,
	completedAt *time.Time,
) error {
	cohort.State = state
	cohort.CompletedAt = completedAt
	return c.updateCohort(
		ctx, claim, guard, rollout, cohort, policyRevision, action,
	)
}

func (c *Controller) updateCohort(
	ctx context.Context,
	claim *scannerrelease.RolloutClaim,
	guard *leaseGuard,
	rollout *scannerrelease.Rollout,
	cohort *scannerrelease.RolloutCohort,
	policyRevision int64,
	action string,
) error {
	if err := c.checkLease(ctx, claim, guard); err != nil {
		return err
	}
	command := scannerrelease.TransitionCommand{
		Actor:          c.config.WorkerID,
		Reason:         action,
		PolicyRevision: policyRevision,
		IdempotencyKey: fmt.Sprintf(
			"rollout/%s/cohort/%s/v%d/%s", rollout.ID, cohort.ID, cohort.Version, action,
		),
		PayloadJSON: `{"source":"rollout_controller"}`,
	}
	c.writeMu.Lock()
	err := c.config.Store.UpdateRolloutCohort(
		ctx, cohort, cohort.Version, command,
	)
	c.writeMu.Unlock()
	if err != nil {
		return fmt.Errorf("update scanner rollout cohort %q: %w", cohort.Name, err)
	}
	return nil
}

func (c *Controller) transition(
	ctx context.Context,
	claim *scannerrelease.RolloutClaim,
	guard *leaseGuard,
	rollout *scannerrelease.Rollout,
	target scannerrelease.RolloutState,
	policyRevision int64,
	code, reason string,
) (*scannerrelease.Rollout, error) {
	if err := c.checkLease(ctx, claim, guard); err != nil {
		return nil, err
	}
	payload, _ := json.Marshal(map[string]string{
		"source": "rollout_controller", "code": code,
	})
	command := scannerrelease.TransitionCommand{
		Actor: c.config.WorkerID, Reason: reason, PolicyRevision: policyRevision,
		IdempotencyKey: fmt.Sprintf(
			"rollout/%s/v%d/%s/%s", rollout.ID, rollout.Version, target, code,
		),
		PayloadJSON: string(payload),
	}
	c.writeMu.Lock()
	updated, err := c.config.Store.TransitionRollout(
		ctx, rollout.ID, rollout.Version, target, command,
	)
	if err == nil {
		*rollout = *updated
		guard.accept(updated)
	}
	c.writeMu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("transition scanner rollout to %s: %w", target, err)
	}
	return updated, nil
}

func (c *Controller) failRollout(
	ctx context.Context,
	claim *scannerrelease.RolloutClaim,
	guard *leaseGuard,
	rollout *scannerrelease.Rollout,
	policyRevision int64,
	code, reason string,
) error {
	_, err := c.transition(
		ctx, claim, guard, rollout, scannerrelease.RolloutFailed,
		policyRevision, code, reason,
	)
	return err
}

func (c *Controller) validateReleases(
	ctx context.Context,
	rollout *scannerrelease.Rollout,
) (bool, error) {
	rollbackAvailable := false
	if rollout.FromReleaseID != "" {
		previous, err := c.config.Store.GetRelease(ctx, rollout.FromReleaseID)
		if err != nil {
			return false, fmt.Errorf("load rollback release: %w", err)
		}
		if !previous.RollbackEligible || previous.State == scannerrelease.ReleaseRevoked {
			return false, errors.New("prior release is not rollback eligible")
		}
		rollbackAvailable = true
	}
	if rollout.State == scannerrelease.RolloutRollingBack {
		return rollbackAvailable, nil
	}
	destination, err := c.config.Store.GetRelease(ctx, rollout.ToReleaseID)
	if err != nil {
		return rollbackAvailable, fmt.Errorf("load rollout destination release: %w", err)
	}
	if destination.State == scannerrelease.ReleaseRevoked ||
		destination.State == scannerrelease.ReleaseDeprecated {
		return rollbackAvailable, fmt.Errorf("destination release is %s", destination.State)
	}
	return rollbackAvailable, nil
}

func (c *Controller) monitorLease(
	ctx context.Context,
	claim *scannerrelease.RolloutClaim,
	guard *leaseGuard,
	cancel context.CancelCauseFunc,
	stop <-chan struct{},
	done chan<- struct{},
) {
	defer close(done)
	ticker := time.NewTicker(c.config.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-ticker.C:
			if err := c.checkLease(ctx, claim, guard); err != nil {
				cancel(err)
				return
			}
		}
	}
}

func (c *Controller) checkLease(
	ctx context.Context,
	claim *scannerrelease.RolloutClaim,
	guard *leaseGuard,
) error {
	now := c.now()
	c.writeMu.Lock()
	status, err := c.config.Store.HeartbeatRollout(
		ctx, claim.RolloutID, c.config.WorkerID, claim.LeaseToken,
		now, now.Add(c.config.LeaseDuration),
	)
	c.writeMu.Unlock()
	if err != nil {
		c.observeLease("error")
		return fmt.Errorf("%w: heartbeat failed: %v", ErrRolloutLeaseLost, err)
	}
	if !status.Current {
		c.observeLease("lost")
		return ErrRolloutLeaseLost
	}
	c.observeLease("heartbeat")
	version, state := guard.expected()
	if status.RolloutVersion != version || status.State != state {
		return ErrRolloutChanged
	}
	return nil
}

// Cancel maps cancellation to the safe rollback path. A first deployment with
// no prior release cannot be restored and is instead failed explicitly.
func (c *Controller) Cancel(
	ctx context.Context,
	rolloutID string,
	expectedVersion int64,
	actor, reason, idempotencyKey string,
) (*scannerrelease.Rollout, error) {
	if rolloutID == "" || actor == "" || reason == "" || idempotencyKey == "" {
		return nil, errors.New("rollout cancellation requires rollout, actor, reason, and idempotency key")
	}
	rollout, err := c.config.Store.GetRollout(ctx, rolloutID)
	if err != nil {
		return nil, err
	}
	if rollout.Version != expectedVersion {
		return nil, scannerrelease.ErrVersionConflict
	}
	if rollout.State == scannerrelease.RolloutRollingBack {
		return rollout, nil
	}
	if scannerrelease.IsTerminalRolloutState(rollout.State) {
		return nil, fmt.Errorf("cannot cancel terminal scanner rollout %s", rollout.State)
	}
	target := scannerrelease.RolloutRollingBack
	if rollout.FromReleaseID == "" {
		target = scannerrelease.RolloutFailed
	}
	return c.config.Store.TransitionRollout(
		ctx, rollout.ID, rollout.Version, target,
		scannerrelease.TransitionCommand{
			Actor: actor, Reason: reason, IdempotencyKey: idempotencyKey,
			PayloadJSON: `{"requested_action":"cancel"}`,
		},
	)
}

func rolloutPolicy(value string) (scannerpolicy.Policy, CanaryPolicy, error) {
	var policy scannerpolicy.Policy
	if err := json.Unmarshal([]byte(value), &policy); err != nil {
		return scannerpolicy.Policy{}, CanaryPolicy{}, fmt.Errorf("decode rollout policy snapshot: %w", err)
	}
	if err := policy.Normalize(); err != nil {
		return scannerpolicy.Policy{}, CanaryPolicy{}, fmt.Errorf("validate rollout policy snapshot: %w", err)
	}
	canary := DefaultCanaryPolicy()
	canary.MinimumSamples = policy.Canary.MinimumSamples
	canary.MinimumObservation = policy.Canary.Observation
	canary.MaxInfrastructureFailureRate = policy.Rollback.MaxInfrastructureFailureRate
	canary.MaxDurationRegression = policy.Rollback.MaxDurationRegression
	canary.MaxParserFailures = policy.Rollback.MaxParserFailures
	return policy, canary, nil
}

func validateCohorts(
	rollout *scannerrelease.Rollout,
	cohorts []scannerrelease.RolloutCohort,
) error {
	if len(cohorts) == 0 {
		return errors.New("rollout has no cohorts")
	}
	sort.Slice(cohorts, func(i, j int) bool { return cohorts[i].Ordinal < cohorts[j].Ordinal })
	seenNames := make(map[string]struct{}, len(cohorts))
	seenOrdinals := make(map[int]struct{}, len(cohorts))
	for _, cohort := range cohorts {
		if cohort.ID == "" || cohort.Name == "" || cohort.RolloutID != rollout.ID {
			return errors.New("rollout cohort identity is invalid")
		}
		if _, duplicate := seenNames[cohort.Name]; duplicate {
			return fmt.Errorf("duplicate rollout cohort %q", cohort.Name)
		}
		if _, duplicate := seenOrdinals[cohort.Ordinal]; duplicate {
			return fmt.Errorf("duplicate rollout cohort ordinal %d", cohort.Ordinal)
		}
		seenNames[cohort.Name] = struct{}{}
		seenOrdinals[cohort.Ordinal] = struct{}{}
	}
	return nil
}

func stableCohortName(cohorts []scannerrelease.RolloutCohort) string {
	if len(cohorts) < 2 {
		return ""
	}
	return cohorts[len(cohorts)-1].Name
}

func evaluateSnapshot(
	policy CanaryPolicy,
	snapshot HealthSnapshot,
	cohort *scannerrelease.RolloutCohort,
	now time.Time,
) (CanaryDecision, error) {
	started := cohort.UpdatedAt
	if cohort.StartedAt != nil {
		started = cohort.StartedAt.UTC()
	}
	decision, err := EvaluateCanary(policy, snapshot.Canary, started, now)
	if err != nil {
		return CanaryDecision{}, err
	}
	if decision.Outcome == CanaryRollback {
		return decision, nil
	}
	if snapshot.TotalWorkers <= 0 {
		decision.Outcome = CanaryPending
		decision.Reasons = append(decision.Reasons, "cohort has no active workers")
	}
	if snapshot.ReadyWorkers != snapshot.TotalWorkers ||
		snapshot.ObservedReleaseID != cohort.DesiredReleaseID {
		decision.Outcome = CanaryPending
		decision.Reasons = append(decision.Reasons, "cohort has not converged on the desired release")
	}
	if snapshot.FailedWorkers > 0 {
		decision.Outcome = CanaryRollback
		decision.Reasons = append(decision.Reasons, "cohort worker verification failed")
	}
	if decision.Outcome == CanaryPending && cohort.Deadline != nil &&
		!now.Before(cohort.Deadline.UTC()) {
		decision.Outcome = CanaryRollback
		decision.Reasons = append(decision.Reasons, "cohort reconciliation deadline exceeded")
	}
	sort.Strings(decision.Reasons)
	return decision, nil
}

func (c *Controller) now() time.Time {
	return c.config.Now().UTC()
}

func (c *Controller) observeClaim(result string) {
	if c.config.Observer != nil {
		c.config.Observer.ObserveClaim(scannerobservability.ComponentRollout, result)
	}
}

func (c *Controller) observeLease(result string) {
	if c.config.Observer != nil {
		c.config.Observer.ObserveLease(scannerobservability.ComponentRollout, result)
	}
}

func (c *Controller) observeRetry(reason string) {
	if c.config.Observer != nil {
		c.config.Observer.ObserveRetry(scannerobservability.ComponentRollout, reason)
	}
}

func (c *Controller) observeResult(state string) {
	if c.config.Observer != nil {
		c.config.Observer.ObserveResult(scannerobservability.ComponentRollout, state)
	}
}

func (c *Controller) setState(state string) {
	if c.config.Observer != nil {
		c.config.Observer.SetState(scannerobservability.ComponentRollout, state)
	}
}

func (c *Controller) setStuck(kind string, count int) {
	if c.config.Observer != nil {
		c.config.Observer.SetStuckWork(scannerobservability.ComponentRollout, kind, count)
	}
}

func rolloutResultState(err error) string {
	if errors.Is(err, context.Canceled) || errors.Is(err, ErrRolloutChanged) {
		return "cancelled"
	}
	return "failed"
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
