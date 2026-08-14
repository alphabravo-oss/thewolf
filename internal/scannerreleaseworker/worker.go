package scannerreleaseworker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/scannerobservability"
	"github.com/alphabravocompany/thewolf/internal/scannerpipeline"
	"github.com/alphabravocompany/thewolf/internal/scannerpolicy"
	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
	"github.com/alphabravocompany/thewolf/internal/scannerreleaseworkspace"
	"github.com/alphabravocompany/thewolf/internal/scannertrace"
)

const (
	defaultPollInterval      = 2 * time.Second
	defaultHeartbeatInterval = 10 * time.Second
	defaultLeaseDuration     = 45 * time.Second
	defaultDrainTimeout      = 2 * time.Minute
	defaultMaxParallel       = 2
	defaultMaxAttempts       = 2
)

var digestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

type Worker struct {
	config Config
	// Executor work may run concurrently, but persistence mutations are kept
	// short and serialized per worker. This preserves SQLite single-writer
	// compatibility without reducing parallel build/test execution.
	writeMu sync.Mutex
}

func New(config Config) (*Worker, error) {
	switch {
	case config.Store == nil:
		return nil, errors.New("scanner release worker store is required")
	case config.Executor == nil:
		return nil, errors.New("scanner release worker executor is required")
	case config.WorkerID == "":
		return nil, errors.New("scanner release worker ID is required")
	}
	if config.MaxParallelSteps == 0 {
		config.MaxParallelSteps = defaultMaxParallel
	}
	if config.MaxStepAttempts == 0 {
		config.MaxStepAttempts = defaultMaxAttempts
	}
	if config.PollInterval == 0 {
		config.PollInterval = defaultPollInterval
	}
	if config.HeartbeatInterval == 0 {
		config.HeartbeatInterval = defaultHeartbeatInterval
	}
	if config.LeaseDuration == 0 {
		config.LeaseDuration = defaultLeaseDuration
	}
	if config.DrainTimeout == 0 {
		config.DrainTimeout = defaultDrainTimeout
	}
	if config.Now == nil {
		config.Now = func() time.Time { return time.Now().UTC() }
	}
	if config.Sleep == nil {
		config.Sleep = sleepContext
	}
	if config.WorkspaceRoot == "" {
		config.WorkspaceRoot = filepath.Join(os.TempDir(), "wolf-scanner-release-workspaces")
	}
	if config.RemoveAll == nil {
		config.RemoveAll = os.RemoveAll
	}
	switch {
	case config.MaxParallelSteps < 1 || config.MaxParallelSteps > 64:
		return nil, errors.New("max parallel scanner release steps must be from 1 through 64")
	case config.MaxStepAttempts < 1 || config.MaxStepAttempts > 10:
		return nil, errors.New("max scanner release step attempts must be from 1 through 10")
	case config.PollInterval <= 0:
		return nil, errors.New("scanner release worker poll interval must be positive")
	case config.HeartbeatInterval <= 0:
		return nil, errors.New("scanner release heartbeat interval must be positive")
	case config.LeaseDuration <= config.HeartbeatInterval*2:
		return nil, errors.New("scanner release lease duration must exceed two heartbeat intervals")
	case config.DrainTimeout <= 0:
		return nil, errors.New("scanner release drain timeout must be positive")
	case !filepath.IsAbs(config.WorkspaceRoot):
		return nil, errors.New("scanner release workspace root must be absolute")
	}
	platforms := make(map[string]struct{}, len(config.SupportedPlatforms))
	for _, platform := range config.SupportedPlatforms {
		if platform == "" {
			return nil, errors.New("scanner release worker platform cannot be empty")
		}
		if _, duplicate := platforms[platform]; duplicate {
			return nil, fmt.Errorf("duplicate scanner release worker platform %q", platform)
		}
		platforms[platform] = struct{}{}
	}
	return &Worker{config: config}, nil
}

// Run claims release builds until cancellation. Once mode performs one reclaim
// and claim pass, then exits even when the queue is empty.
func (w *Worker) Run(ctx context.Context) error {
	for {
		processed, err := w.claimAndRun(ctx)
		if err != nil {
			return err
		}
		if w.config.Once {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if processed {
			continue
		}
		if err := w.config.Sleep(ctx, w.config.PollInterval); err != nil {
			return err
		}
	}
}

// RunOnce is useful for controllers and deterministic tests.
func (w *Worker) RunOnce(ctx context.Context) (bool, error) {
	return w.claimAndRun(ctx)
}

func (w *Worker) claimAndRun(ctx context.Context) (bool, error) {
	started := w.now()
	runResult := "success"
	defer func() {
		if w.config.Observer != nil {
			w.config.Observer.ObserveRun(
				scannerobservability.ComponentBuild, runResult, w.now().Sub(started),
			)
		}
	}()
	now := w.now()
	reclaimed, err := w.config.Store.ReclaimStaleBuildRuns(ctx, now)
	if err != nil {
		runResult = "error"
		w.setState("degraded")
		return false, fmt.Errorf("reclaim stale scanner release builds: %w", err)
	}
	if reclaimed > 0 {
		w.observeLease("reclaimed")
		w.observeRetry("stale_lease")
	}
	// Reclaimed builds are no longer stuck; retain the incident as bounded
	// counters and expose only currently unresolved work in the gauge.
	w.setStuck("expired_lease", 0)
	build, err := w.config.Store.ClaimNextBuildRun(
		ctx, w.config.WorkerID, w.config.SupportedPlatforms, now.Add(w.config.LeaseDuration),
	)
	if err != nil {
		runResult = "error"
		w.observeClaim("error")
		w.setState("degraded")
		return false, fmt.Errorf("claim scanner release build: %w", err)
	}
	if build == nil {
		w.observeClaim("empty")
		w.setState("idle")
		return false, nil
	}
	w.observeClaim("acquired")
	w.setState("busy")
	claimContext, _, traceErr := scannertrace.Resume(
		ctx, w.config.Store, "build", build.ID, "build-worker",
	)
	if traceErr != nil {
		runResult = "error"
		w.setState("degraded")
		return true, fmt.Errorf("resume scanner build operation correlation: %w", traceErr)
	}
	scannertrace.Logger(claimContext).Info().
		Str("aggregate_type", "build").
		Str("aggregate_id", build.ID).
		Str("state", string(build.State)).
		Msg("scanner release work claimed")
	err = w.runClaimWithDrain(claimContext, build)
	if err != nil {
		runResult = "error"
		w.setState("degraded")
		w.observeResult(buildResultState(err))
		if errors.Is(err, ErrLeaseLost) {
			w.observeLease("lost")
			w.setStuck("lease_lost", 1)
		}
		scannertrace.Logger(claimContext).Warn().
			Str("aggregate_type", "build").
			Str("aggregate_id", build.ID).
			Str("error_class", buildResultState(err)).
			Msg("scanner release work failed")
		return true, err
	}
	w.setState("active")
	w.setStuck("lease_lost", 0)
	w.observeResult("completed")
	scannertrace.Logger(claimContext).Info().
		Str("aggregate_type", "build").
		Str("aggregate_id", build.ID).
		Str("state", "completed").
		Msg("scanner release work completed")
	return true, nil
}

func (w *Worker) runClaimWithDrain(parent context.Context, build *scannerrelease.BuildRun) error {
	workContext, cancelWork := context.WithCancelCause(context.WithoutCancel(parent))
	defer cancelWork(nil)
	done := make(chan error, 1)
	go func() {
		done <- w.processClaim(workContext, build)
	}()
	select {
	case err := <-done:
		return err
	case <-parent.Done():
		timer := time.NewTimer(w.config.DrainTimeout)
		defer timer.Stop()
		select {
		case err := <-done:
			return err
		case <-timer.C:
			cancelWork(ErrDrainDeadline)
			<-done
			return parent.Err()
		}
	}
}

func (w *Worker) processClaim(
	ctx context.Context,
	build *scannerrelease.BuildRun,
) (returnErr error) {
	cleanupTerminalWorkspace := false
	defer func() {
		if cleanupTerminalWorkspace {
			returnErr = errors.Join(returnErr, w.removeBuildWorkspace(build.ID))
		}
	}()
	candidate, policy, err := w.validateCandidateSnapshot(ctx, build)
	if err != nil {
		finalizeErr := w.finalizeBuild(ctx, build, scannerrelease.BuildFailed)
		cleanupTerminalWorkspace = finalizeErr == nil
		var candidateErr error
		if candidate != nil {
			candidateErr = w.markCandidateFailed(context.WithoutCancel(ctx), candidate.ID, err)
		}
		return errors.Join(err, finalizeErr, candidateErr)
	}
	status, err := w.checkLease(ctx, build)
	if err != nil {
		if errors.Is(err, ErrCancellationRequested) {
			finalizeErr := w.finalizeBuild(ctx, build, scannerrelease.BuildCancelled)
			cleanupTerminalWorkspace = finalizeErr == nil
			blockErr := w.markCandidateBlocked(
				context.WithoutCancel(ctx), candidate.ID, "build cancelled before execution",
			)
			return errors.Join(finalizeErr, blockErr)
		}
		return err
	}
	build.Version = status.Version
	w.writeMu.Lock()
	running, err := w.config.Store.TransitionBuildRun(
		ctx, build.ID, build.Version, scannerrelease.BuildRunning,
		w.buildCommand(build, "start", "release build execution started"),
	)
	w.writeMu.Unlock()
	if err != nil {
		return fmt.Errorf("start scanner release build %s: %w", build.ID, err)
	}
	build = running
	if err := w.markCandidateStarted(ctx, candidate); err != nil {
		finalizeErr := w.finalizeBuild(ctx, build, scannerrelease.BuildFailed)
		cleanupTerminalWorkspace = finalizeErr == nil
		return errors.Join(err, finalizeErr)
	}

	buildContext, cancelBuild := context.WithCancelCause(ctx)
	stopHeartbeat := make(chan struct{})
	heartbeatDone := make(chan struct{})
	go w.monitorLease(buildContext, build, cancelBuild, stopHeartbeat, heartbeatDone)

	workspace, err := w.prepareBuildWorkspace(build, candidate, policy)
	if err != nil {
		close(stopHeartbeat)
		<-heartbeatDone
		workspaceErr := fmt.Errorf("create scanner release workspace: %w", err)
		finalizeErr := w.finalizeBuild(ctx, build, scannerrelease.BuildFailed)
		cleanupTerminalWorkspace = finalizeErr == nil
		candidateErr := w.markCandidateFailed(context.WithoutCancel(ctx), candidate.ID, workspaceErr)
		return errors.Join(workspaceErr, finalizeErr, candidateErr)
	}

	executionErr := w.executePlan(buildContext, build, candidate, policy, workspace)
	close(stopHeartbeat)
	<-heartbeatDone
	heartbeatCause := context.Cause(buildContext)
	cancelBuild(nil)
	if heartbeatCause != nil && executionErr == nil {
		executionErr = heartbeatCause
	}
	switch {
	case executionErr == nil:
		if err := w.finalizeBuild(ctx, build, scannerrelease.BuildCompleted); err != nil {
			return err
		}
		cleanupTerminalWorkspace = true
		decision, err := w.loadPolicyDecision(ctx, build.ID)
		if err != nil {
			return errors.Join(err, w.markCandidateFailed(context.WithoutCancel(ctx), candidate.ID, err))
		}
		if decision.Outcome == scannerpolicy.OutcomeBlocked {
			return w.markCandidatePolicyBlocked(ctx, candidate.ID, decision.BlockingReasons)
		}
		if err := w.markCandidateAwaitingApproval(ctx, candidate.ID); err != nil {
			return err
		}
		if decision.Outcome == scannerpolicy.OutcomeAutoApproved {
			receiptDigest, err := w.loadPublicationReceiptDigest(ctx, build.ID)
			if err != nil {
				return errors.Join(err, w.markCandidateFailed(context.WithoutCancel(ctx), candidate.ID, err))
			}
			return w.markCandidateAutoApproved(ctx, candidate.ID, decision, receiptDigest)
		}
		return nil
	case errors.Is(executionErr, ErrCancellationRequested):
		finalizeErr := w.finalizeBuild(context.WithoutCancel(ctx), build, scannerrelease.BuildCancelled)
		cleanupTerminalWorkspace = finalizeErr == nil
		blockErr := w.markCandidateBlocked(
			context.WithoutCancel(ctx), candidate.ID, "build cancelled by operator",
		)
		return errors.Join(finalizeErr, blockErr)
	case errors.Is(executionErr, ErrLeaseLost):
		return ErrLeaseLost
	case errors.Is(executionErr, context.Canceled), errors.Is(executionErr, ErrDrainDeadline):
		// Do not publish a result after local shutdown. The durable lease will
		// expire and the repository's stale recovery policy owns classification.
		return executionErr
	default:
		finalizeErr := w.finalizeBuild(context.WithoutCancel(ctx), build, scannerrelease.BuildFailed)
		cleanupTerminalWorkspace = finalizeErr == nil
		candidateErr := w.markCandidateFailed(context.WithoutCancel(ctx), candidate.ID, executionErr)
		return errors.Join(executionErr, finalizeErr, candidateErr)
	}
}

func (w *Worker) validateCandidateSnapshot(
	ctx context.Context,
	build *scannerrelease.BuildRun,
) (*scannerrelease.Candidate, *scannerrelease.Policy, error) {
	candidate, err := w.config.Store.GetCandidate(ctx, build.CandidateID)
	if err != nil {
		return nil, nil, fmt.Errorf("load scanner release candidate: %w", err)
	}
	switch {
	case candidate.DefinitionCommit == "":
		return candidate, nil, errors.New("scanner release candidate has no definition commit")
	case !digestPattern.MatchString(candidate.LockDigest):
		return candidate, nil, fmt.Errorf("scanner release candidate has invalid lock digest %q", candidate.LockDigest)
	case candidate.PolicyID == "" || candidate.PolicyRevision <= 0:
		return candidate, nil, errors.New("scanner release candidate has no policy snapshot")
	}
	policy, err := w.config.Store.GetPolicy(ctx, candidate.PolicyID)
	if err != nil {
		return candidate, nil, fmt.Errorf("load scanner release policy: %w", err)
	}
	if policy.Revision != candidate.PolicyRevision || !policy.Enabled {
		return candidate, policy, fmt.Errorf(
			"scanner release policy snapshot mismatch: candidate=%s/%d policy=%s/%d enabled=%t",
			candidate.PolicyID, candidate.PolicyRevision, policy.ID, policy.Revision, policy.Enabled,
		)
	}
	return candidate, policy, nil
}

func (w *Worker) executePlan(
	ctx context.Context,
	build *scannerrelease.BuildRun,
	candidate *scannerrelease.Candidate,
	policy *scannerrelease.Policy,
	workspace string,
) error {
	for {
		if _, err := w.checkLease(ctx, build); err != nil {
			return err
		}
		records, err := w.config.Store.ListBuildSteps(ctx, build.ID)
		if err != nil {
			return fmt.Errorf("list scanner release build steps: %w", err)
		}
		_, logical, err := restorePlan(records)
		if err != nil {
			return err
		}
		if err := w.recoverOrRetrySteps(ctx, build, logical); err != nil {
			return err
		}
		// Refresh after recovery/retry mutations.
		records, err = w.config.Store.ListBuildSteps(ctx, build.ID)
		if err != nil {
			return fmt.Errorf("refresh scanner release build steps: %w", err)
		}
		var plan scannerpipeline.Plan
		plan, logical, err = restorePlan(records)
		if err != nil {
			return err
		}
		if err := rehydrateWorkspaceEvidence(
			workspace, build, candidate, policy, logical,
		); err != nil {
			return fmt.Errorf("rehydrate scanner release workspace evidence: %w", err)
		}
		completed := make(map[string]bool, len(logical))
		allCompleted := true
		for key, step := range logical {
			latest := latestAttempt(step)
			if latest.State == scannerrelease.BuildCompleted {
				completed[key] = true
				continue
			}
			allCompleted = false
			if latest.State == scannerrelease.BuildFailed || latest.State == scannerrelease.BuildCancelled {
				if latest.ErrorClass == "reconciliation_required" {
					return fmt.Errorf(
						"%w: scanner release step %q operation could not be reconciled: %s",
						ErrReconciliationRequired, key, redactText(latest.ErrorDetail),
					)
				}
				return fmt.Errorf("required scanner release step %q exhausted after %d attempt(s): %s",
					key, latest.Attempt, redactText(latest.ErrorDetail))
			}
		}
		if allCompleted {
			return nil
		}
		ready, err := plan.Ready(completed, nil)
		if err != nil {
			return err
		}
		batch := w.selectBatch(ready)
		if len(batch) == 0 {
			return errors.New("scanner release pipeline has no runnable step")
		}
		if err := w.executeBatch(ctx, build, candidate, policy, workspace, batch, logical); err != nil {
			return err
		}
	}
}

func (w *Worker) recoverOrRetrySteps(
	ctx context.Context,
	build *scannerrelease.BuildRun,
	logical map[string]*logicalStep,
) error {
	keys := make([]string, 0, len(logical))
	for key := range logical {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		step := logical[key]
		latest := latestAttempt(step)
		if latest.State == scannerrelease.BuildRunning {
			// A stale build claim does not imply that its external operation
			// failed. Keep the same attempt running so the executor can first
			// replay its durable result or reconcile the same logical sink key.
			continue
		}
		if latest.State != scannerrelease.BuildFailed {
			continue
		}
		if latest.ErrorClass == "reconciliation_required" {
			continue
		}
		if !step.Plan.Retryable || latest.Attempt >= w.config.MaxStepAttempts {
			continue
		}
		retry := &scannerrelease.BuildStep{
			ID:             uuid.NewString(),
			BuildRunID:     build.ID,
			StepKey:        key,
			State:          scannerrelease.BuildQueued,
			Attempt:        latest.Attempt + 1,
			SummaryJSON:    metadataJSON(step.Metadata),
			RetentionClass: latest.RetentionClass,
			Protected:      latest.Protected,
		}
		w.writeMu.Lock()
		err := w.config.Store.CreateBuildStep(
			ctx, retry,
			w.stepCommand(build, retry, "retry", "retryable release step requeued"),
		)
		w.writeMu.Unlock()
		if err != nil {
			return fmt.Errorf("create retry for scanner release step %q: %w", key, err)
		}
		w.observeRetry("step_failure")
	}
	return nil
}

func (w *Worker) selectBatch(ready []scannerpipeline.Step) []scannerpipeline.Step {
	batch := make([]scannerpipeline.Step, 0, min(w.config.MaxParallelSteps, len(ready)))
	concurrencyKeys := make(map[string]struct{})
	for _, step := range ready {
		if len(batch) == w.config.MaxParallelSteps {
			break
		}
		if step.ConcurrencyKey != "" {
			if _, busy := concurrencyKeys[step.ConcurrencyKey]; busy {
				continue
			}
			concurrencyKeys[step.ConcurrencyKey] = struct{}{}
		}
		batch = append(batch, step)
	}
	return batch
}

func (w *Worker) executeBatch(
	ctx context.Context,
	build *scannerrelease.BuildRun,
	candidate *scannerrelease.Candidate,
	policy *scannerrelease.Policy,
	workspace string,
	batch []scannerpipeline.Step,
	logical map[string]*logicalStep,
) error {
	var wait sync.WaitGroup
	results := make(chan error, len(batch))
	for _, planStep := range batch {
		planStep := planStep
		record := *latestAttempt(logical[planStep.Key])
		wait.Add(1)
		go func() {
			defer wait.Done()
			results <- w.executeStep(ctx, build, candidate, policy, workspace, planStep, &record, logical)
		}()
	}
	wait.Wait()
	close(results)
	var failures []error
	for err := range results {
		if err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (w *Worker) executeStep(
	ctx context.Context,
	build *scannerrelease.BuildRun,
	candidate *scannerrelease.Candidate,
	policy *scannerrelease.Policy,
	workspace string,
	planStep scannerpipeline.Step,
	record *scannerrelease.BuildStep,
	logical map[string]*logicalStep,
) error {
	if _, err := w.checkLease(ctx, build); err != nil {
		return err
	}
	dependencies, err := directDependencyEvidence(planStep, logical)
	if err != nil {
		return err
	}
	running := record
	reconciling := record.State == scannerrelease.BuildRunning
	if !reconciling {
		if record.State != scannerrelease.BuildQueued {
			return fmt.Errorf(
				"scanner release step %q cannot execute from state %q",
				planStep.Key, record.State,
			)
		}
		w.writeMu.Lock()
		started, startErr := w.config.Store.TransitionBuildStep(
			ctx, record.ID, record.Version, scannerrelease.BuildRunning,
			w.stepCommand(build, record, "start", "release step execution started"),
		)
		w.writeMu.Unlock()
		if startErr != nil {
			return fmt.Errorf("start scanner release step %q: %w", planStep.Key, startErr)
		}
		running = started
	}
	request := StepRequest{
		BuildRunID: build.ID, CandidateID: candidate.ID, BuildAttempt: build.Attempt,
		Step: planStep, StepAttempt: running.Attempt, Workspace: workspace,
		DefinitionCommit: scannerrelease.EffectiveDefinitionCommit(candidate), LockDigest: candidate.LockDigest,
		PolicyID: policy.ID, PolicyRevision: policy.Revision, PlatformsJSON: build.PlatformsJSON,
		Dependencies: dependencies,
	}
	request.LogicalOperationID = DeriveLogicalOperationID(request)
	stepContext, cancel := context.WithTimeout(ctx, planStep.Timeout)
	result, executeErr := w.config.Executor.Execute(stepContext, request)
	cancel()
	if result.Summary == nil {
		result.Summary = make(map[string]any)
	}
	result.Summary["logical_operation_id"] = request.LogicalOperationID
	result.Summary["diagnostic_attempt"] = running.Attempt
	if reconciling {
		result.Summary["reconciled_after_worker_loss"] = true
	}

	if cause := context.Cause(ctx); cause != nil {
		if errors.Is(cause, ErrLeaseLost) {
			return ErrLeaseLost
		}
		if errors.Is(cause, ErrCancellationRequested) {
			if _, err := w.checkLeaseAllowCancellation(context.WithoutCancel(ctx), build); err != nil {
				return err
			}
			w.writeMu.Lock()
			_, err := w.config.Store.TransitionBuildStep(
				context.WithoutCancel(ctx), running.ID, running.Version, scannerrelease.BuildCancelled,
				w.stepCommand(build, running, "cancel", "release step cancelled by operator"),
			)
			w.writeMu.Unlock()
			return errors.Join(ErrCancellationRequested, err)
		}
		return cause
	}
	errorClass := ""
	errorDetail := ""
	if executeErr != nil {
		if errors.Is(executeErr, ErrReconciliationRequired) {
			errorClass = "reconciliation_required"
		} else if errors.Is(stepContext.Err(), context.DeadlineExceeded) {
			errorClass = "timeout"
		} else {
			errorClass = "execution_failed"
		}
		errorDetail = redactText(executeErr.Error())
	} else {
		if planStep.Key == "policy-evaluation" {
			if result.PolicyInput != nil {
				merged, mergeErr := w.mergePersistedExceptions(ctx, candidate, *result.PolicyInput)
				if mergeErr != nil {
					errorClass = "invalid_evidence"
					errorDetail = redactText(mergeErr.Error())
				} else {
					result.PolicyInput = &merged
				}
			}
			if errorClass == "" {
				decision, decisionErr := trustedPolicyDecision(candidate, policy, result, w.now())
				if decisionErr != nil {
					errorClass = "invalid_evidence"
					errorDetail = redactText(decisionErr.Error())
				} else {
					// The executor reports normalized evidence; the trusted worker
					// owns both the policy outcome and approval binding. Never let
					// an executor choose what an approver is authorizing.
					result.PolicyDecision = &decision
					result.Verification.PolicyDecisionDigest = decision.PolicyDecisionDigest
					result.OutputDigest = decision.PolicyDecisionDigest
				}
			}
		}
		if errorClass == "" {
			validationCandidate := candidate
			if planStep.Key == "candidate-evidence-summary" {
				current, currentErr := w.config.Store.GetCandidate(ctx, candidate.ID)
				if currentErr != nil {
					errorClass = "invalid_evidence"
					errorDetail = redactText(currentErr.Error())
				} else {
					validationCandidate = current
				}
			}
			if errorClass == "" {
				if err := validateStepResult(planStep, build.ID, validationCandidate, policy, result); err != nil {
					errorClass = "invalid_evidence"
					errorDetail = redactText(err.Error())
				}
			}
		}
	}
	if _, err := w.checkLease(ctx, build); err != nil {
		return err
	}
	updated, err := w.persistEvidence(ctx, build, running, result, errorClass, errorDetail)
	if err != nil {
		return err
	}
	target := scannerrelease.BuildCompleted
	reason := "release step completed"
	if errorClass != "" {
		target = scannerrelease.BuildFailed
		reason = "release step failed"
	}
	w.writeMu.Lock()
	completed, err := w.config.Store.TransitionBuildStep(
		ctx, updated.ID, updated.Version, target,
		w.stepCommand(build, updated, "finish", reason),
	)
	w.writeMu.Unlock()
	if err != nil {
		return fmt.Errorf("finish scanner release step %q: %w", planStep.Key, err)
	}
	if target == scannerrelease.BuildCompleted {
		binding := scannerreleaseworkspace.NewBinding(
			build.ID, candidate.ID, build.Attempt,
			scannerrelease.EffectiveDefinitionCommit(candidate), candidate.LockDigest,
			policy.ID, policy.Revision,
		)
		if err := scannerreleaseworkspace.WriteEvidence(
			workspace, planStep, completed.Attempt, binding, result,
		); err != nil {
			return fmt.Errorf(
				"record completed transitive evidence for %q: %w", planStep.Key, err,
			)
		}
	}
	if target == scannerrelease.BuildCompleted && planStep.Key == "policy-evaluation" {
		if err := w.recordPolicyDecision(ctx, candidate.ID, result.Verification.PolicyDecisionDigest); err != nil {
			return err
		}
	}
	_ = completed
	return nil
}

func (w *Worker) mergePersistedExceptions(
	ctx context.Context,
	candidate *scannerrelease.Candidate,
	input PolicyInput,
) (PolicyInput, error) {
	approvals, err := w.config.Store.ListApprovals(ctx, candidate.ID, "")
	if err != nil {
		return PolicyInput{}, fmt.Errorf("load candidate exception ledger: %w", err)
	}
	// The control-plane ledger is authoritative. An executor cannot introduce
	// an exception by returning it in a StepResult.
	input.Exceptions = nil
	seen := make(map[string]struct{})
	for _, approval := range approvals {
		if approval.Action != "exception" {
			continue
		}
		if approval.ID == "" || approval.ExceptionScope == "" ||
			approval.ExceptionOwner == "" || approval.Reason == "" ||
			approval.CompensatingControl == "" || approval.ExpiresAt == nil {
			return PolicyInput{}, fmt.Errorf("candidate exception %q is incomplete", approval.ID)
		}
		if _, duplicate := seen[approval.ID]; duplicate {
			return PolicyInput{}, fmt.Errorf("candidate exception %q is duplicated", approval.ID)
		}
		seen[approval.ID] = struct{}{}
		input.Exceptions = append(input.Exceptions, scannerpolicy.Exception{
			ID: approval.ID, Gate: approval.ExceptionScope,
			OwnerID: approval.ExceptionOwner, Reason: approval.Reason,
			CompensatingControl: approval.CompensatingControl,
			ApprovedBy:          approval.Actor, ExpiresAt: approval.ExpiresAt.UTC(),
		})
	}
	return input, nil
}

func directDependencyEvidence(
	step scannerpipeline.Step,
	logical map[string]*logicalStep,
) (map[string]DependencyEvidence, error) {
	if len(step.DependsOn) == 0 {
		return nil, nil
	}
	out := make(map[string]DependencyEvidence, len(step.DependsOn))
	for _, dependency := range step.DependsOn {
		logicalDependency := logical[dependency]
		latest := latestAttempt(logicalDependency)
		if latest == nil || latest.State != scannerrelease.BuildCompleted {
			return nil, fmt.Errorf(
				"scanner release step %q dependency %q is not complete",
				step.Key, dependency,
			)
		}
		if latest.OutputDigest != "" && !digestPattern.MatchString(latest.OutputDigest) {
			return nil, fmt.Errorf(
				"scanner release step %q dependency %q has an invalid digest",
				step.Key, dependency,
			)
		}
		out[dependency] = DependencyEvidence{
			OutputURI: latest.OutputURI, OutputDigest: latest.OutputDigest,
		}
	}
	return out, nil
}

func rehydrateWorkspaceEvidence(
	workspace string,
	build *scannerrelease.BuildRun,
	candidate *scannerrelease.Candidate,
	policy *scannerrelease.Policy,
	logical map[string]*logicalStep,
) error {
	binding := scannerreleaseworkspace.NewBinding(
		build.ID, candidate.ID, build.Attempt,
		scannerrelease.EffectiveDefinitionCommit(candidate), candidate.LockDigest,
		policy.ID, policy.Revision,
	)
	keys := make([]string, 0, len(logical))
	for key := range logical {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		step := logical[key]
		latest := latestAttempt(step)
		if latest == nil || latest.State != scannerrelease.BuildCompleted {
			continue
		}
		var persisted struct {
			Evidence struct {
				Summary        map[string]any          `json:"summary"`
				Verification   Verification            `json:"verification"`
				PolicyDecision *scannerpolicy.Decision `json:"policy_decision"`
			} `json:"evidence"`
		}
		if err := json.Unmarshal([]byte(latest.SummaryJSON), &persisted); err != nil {
			return fmt.Errorf("decode completed step %q evidence: %w", key, err)
		}
		if !digestPattern.MatchString(latest.OutputDigest) {
			return fmt.Errorf("completed step %q has no valid durable evidence digest", key)
		}
		verification := persisted.Evidence.Verification
		if verification.DefinitionCommit != scannerrelease.EffectiveDefinitionCommit(candidate) ||
			verification.LockDigest != candidate.LockDigest ||
			verification.PolicyID != policy.ID ||
			verification.PolicyRevision != policy.Revision {
			return fmt.Errorf("completed step %q durable immutable binding is absent or stale", key)
		}
		result := StepResult{
			OutputURI: latest.OutputURI, OutputDigest: latest.OutputDigest,
			Summary:        persisted.Evidence.Summary,
			RetentionClass: latest.RetentionClass, RetainUntil: latest.RetainUntil,
			Protected: latest.Protected, Verification: verification,
			PolicyDecision: persisted.Evidence.PolicyDecision,
		}
		if err := scannerreleaseworkspace.WriteEvidence(
			workspace, step.Plan, latest.Attempt, binding, result,
		); err != nil {
			return fmt.Errorf("write completed step %q evidence: %w", key, err)
		}
	}
	return nil
}

func trustedPolicyDecision(
	candidate *scannerrelease.Candidate,
	policy *scannerrelease.Policy,
	result StepResult,
	now time.Time,
) (scannerpolicy.Decision, error) {
	if candidate == nil || policy == nil {
		return scannerpolicy.Decision{}, errors.New("candidate and policy are required for policy evaluation")
	}
	if result.PolicyInput == nil {
		return scannerpolicy.Decision{}, errors.New("policy evaluation returned no normalized policy input")
	}
	var rules scannerpolicy.Policy
	decoder := json.NewDecoder(strings.NewReader(policy.RulesJSON))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&rules); err != nil {
		return scannerpolicy.Decision{}, fmt.Errorf("decode immutable policy rules: %w", err)
	}
	if rules.Revision != policy.Revision {
		return scannerpolicy.Decision{}, fmt.Errorf(
			"policy rules revision %d does not match persisted revision %d",
			rules.Revision, policy.Revision,
		)
	}
	risk, changes, err := trustedCandidateRisk(candidate, *result.PolicyInput)
	if err != nil {
		return scannerpolicy.Decision{}, err
	}
	schedule, err := scannerpolicy.ValidateScheduleJSON([]byte(policy.ScheduleJSON))
	if err != nil {
		return scannerpolicy.Decision{}, fmt.Errorf("decode immutable policy schedule: %w", err)
	}
	maintenanceWindowOpen, _, err := schedule.MaintenanceWindowStatus(now)
	if err != nil {
		return scannerpolicy.Decision{}, fmt.Errorf("evaluate immutable maintenance windows: %w", err)
	}
	input := scannerpolicy.Candidate{
		ID: candidate.ID, DefinitionCommit: scannerrelease.EffectiveDefinitionCommit(candidate),
		LockDigest: candidate.LockDigest, PolicyID: policy.ID,
		PolicyRevision: candidate.PolicyRevision, CreatorID: candidate.Actor,
		Risk: risk, Changes: changes,
		Gates: result.PolicyInput.Gates, Exceptions: result.PolicyInput.Exceptions,
		MaintenanceWindowOpen: maintenanceWindowOpen,
		Evidence:              result.PolicyInput.Evidence,
	}
	decision, err := scannerpolicy.Evaluate(input, rules, now)
	if err != nil {
		return scannerpolicy.Decision{}, fmt.Errorf("evaluate immutable scanner release policy: %w", err)
	}
	return decision, nil
}

func trustedCandidateRisk(
	candidate *scannerrelease.Candidate,
	input PolicyInput,
) (scannerpolicy.Risk, []scannerpolicy.Change, error) {
	var persisted struct {
		HighestRisk string                 `json:"highest_risk"`
		Highest     string                 `json:"highest"`
		Risk        string                 `json:"risk"`
		Changes     []scannerpolicy.Change `json:"changes"`
	}
	if strings.TrimSpace(candidate.RiskSummaryJSON) != "" {
		if err := json.Unmarshal([]byte(candidate.RiskSummaryJSON), &persisted); err != nil {
			return "", nil, fmt.Errorf("decode persisted candidate risk: %w", err)
		}
	}
	risk := scannerpolicy.Risk(firstNonempty(persisted.HighestRisk, persisted.Highest, persisted.Risk))
	if risk == "" {
		risk = input.Risk
	}
	if input.Risk != "" && risk != input.Risk {
		return "", nil, fmt.Errorf(
			"executor risk %q does not match persisted candidate risk %q",
			input.Risk, risk,
		)
	}
	changes := persisted.Changes
	if len(changes) == 0 {
		changes = input.Changes
	} else if len(input.Changes) != 0 {
		persistedJSON, _ := json.Marshal(changes)
		inputJSON, _ := json.Marshal(input.Changes)
		if string(persistedJSON) != string(inputJSON) {
			return "", nil, errors.New("executor changes do not match persisted candidate changes")
		}
	}
	return risk, changes, nil
}

func firstNonempty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (w *Worker) persistEvidence(
	ctx context.Context,
	build *scannerrelease.BuildRun,
	record *scannerrelease.BuildStep,
	result StepResult,
	errorClass, errorDetail string,
) (*scannerrelease.BuildStep, error) {
	summary := make(map[string]any)
	if err := json.Unmarshal([]byte(record.SummaryJSON), &summary); err != nil {
		return nil, fmt.Errorf("decode scanner release step summary: %w", err)
	}
	summary["evidence"] = redactValue("", map[string]any{
		"summary":         result.Summary,
		"verification":    result.Verification,
		"policy_decision": result.PolicyDecision,
	})
	encoded, err := json.Marshal(summary)
	if err != nil {
		return nil, fmt.Errorf("encode scanner release step evidence: %w", err)
	}
	update := *record
	update.OutputURI = redactURI(result.OutputURI)
	update.OutputDigest = result.OutputDigest
	update.SummaryJSON = string(encoded)
	if result.RetentionClass != "" {
		update.RetentionClass = result.RetentionClass
	}
	update.RetainUntil = result.RetainUntil
	update.Protected = result.Protected
	update.ErrorClass = errorClass
	update.ErrorDetail = redactText(errorDetail)
	w.writeMu.Lock()
	updated, err := w.config.Store.UpdateBuildStepEvidence(
		ctx, &update, record.Version,
		w.stepCommand(build, record, "evidence", "structured release step evidence recorded"),
	)
	w.writeMu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("record scanner release step %q evidence: %w", record.StepKey, err)
	}
	return updated, nil
}

func validateStepResult(
	step scannerpipeline.Step,
	buildRunID string,
	candidate *scannerrelease.Candidate,
	policy *scannerrelease.Policy,
	result StepResult,
) error {
	verification := result.Verification
	if step.Required && !digestPattern.MatchString(result.OutputDigest) {
		return fmt.Errorf("required step %q returned no valid evidence digest", step.Key)
	}
	if verification.DefinitionCommit != scannerrelease.EffectiveDefinitionCommit(candidate) ||
		verification.LockDigest != candidate.LockDigest ||
		verification.PolicyID != policy.ID ||
		verification.PolicyRevision != policy.Revision {
		return fmt.Errorf("step %q returned stale or incomplete immutable verification", step.Key)
	}
	switch step.Key {
	case "checkout":
		if verification.DefinitionCommit != scannerrelease.EffectiveDefinitionCommit(candidate) {
			return errors.New("checkout did not verify the selected definition commit")
		}
		if verification.LockDigest != candidate.LockDigest {
			return errors.New("checkout did not verify the selected lock digest")
		}
	case "lock-reproducibility":
		if verification.LockDigest != candidate.LockDigest {
			return errors.New("lock reproducibility digest does not match candidate")
		}
	case "policy-evaluation":
		if verification.PolicyID != policy.ID || verification.PolicyRevision != policy.Revision {
			return errors.New("policy evaluation did not use the selected policy snapshot")
		}
		if !digestPattern.MatchString(verification.PolicyDecisionDigest) {
			return errors.New("policy evaluation returned no valid decision digest")
		}
	case "candidate-evidence-summary":
		receipt, err := decodePublicationReceipt(result.Summary)
		if err != nil {
			return err
		}
		digest, err := scannerrelease.PublicationReceiptDigest(receipt)
		if err != nil || digest != result.OutputDigest {
			return errors.New("publication receipt digest does not match final step output")
		}
		switch {
		case receipt.SchemaVersion != scannerrelease.PublicationReceiptSchema:
			return errors.New("publication receipt schema is invalid")
		case receipt.CandidateID != candidate.ID || receipt.BuildRunID != buildRunID:
			return errors.New("publication receipt candidate build binding is invalid")
		case receipt.DefinitionCommit != scannerrelease.EffectiveDefinitionCommit(candidate) || receipt.LockDigest != candidate.LockDigest:
			return errors.New("publication receipt source binding is invalid")
		case receipt.PolicyID != policy.ID || receipt.PolicyRevision != policy.Revision:
			return errors.New("publication receipt policy snapshot is invalid")
		case receipt.PolicyDecisionDigest != candidate.PolicyDecision:
			return errors.New("publication receipt policy decision is invalid")
		case !digestPattern.MatchString(receipt.ManifestDigest) ||
			!strings.Contains(receipt.ManifestURI, receipt.ManifestDigest):
			return errors.New("publication receipt manifest identity is invalid")
		case strings.TrimSpace(receipt.SignerIdentity) == "":
			return errors.New("publication receipt signer identity is absent")
		}
		if err := scannerrelease.ValidatePublicationReceiptInventory(receipt); err != nil {
			return fmt.Errorf("publication receipt inventory is invalid: %w", err)
		}
	}
	return nil
}

func decodePublicationReceipt(summary map[string]any) (scannerrelease.PublicationReceipt, error) {
	value, exists := summary["publication_receipt"]
	if !exists {
		return scannerrelease.PublicationReceipt{}, errors.New("final evidence step returned no publication receipt")
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return scannerrelease.PublicationReceipt{}, errors.New("encode publication receipt")
	}
	var receipt scannerrelease.PublicationReceipt
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&receipt); err != nil {
		return scannerrelease.PublicationReceipt{}, fmt.Errorf("decode publication receipt: %w", err)
	}
	return receipt, nil
}

func (w *Worker) checkLease(
	ctx context.Context,
	build *scannerrelease.BuildRun,
) (scannerrelease.BuildLeaseStatus, error) {
	w.writeMu.Lock()
	status, err := w.config.Store.HeartbeatBuildRun(
		ctx, build.ID, w.config.WorkerID, build.LeaseToken, w.now().Add(w.config.LeaseDuration),
	)
	w.writeMu.Unlock()
	if err != nil {
		w.observeLease("error")
		return scannerrelease.BuildLeaseStatus{}, fmt.Errorf("%w: heartbeat failed: %v", ErrLeaseLost, err)
	}
	if !status.Current {
		w.observeLease("lost")
		return status, ErrLeaseLost
	}
	w.observeLease("heartbeat")
	if status.CancelRequested {
		return status, ErrCancellationRequested
	}
	return status, nil
}

func (w *Worker) checkLeaseAllowCancellation(
	ctx context.Context,
	build *scannerrelease.BuildRun,
) (scannerrelease.BuildLeaseStatus, error) {
	w.writeMu.Lock()
	status, err := w.config.Store.HeartbeatBuildRun(
		ctx, build.ID, w.config.WorkerID, build.LeaseToken, w.now().Add(w.config.LeaseDuration),
	)
	w.writeMu.Unlock()
	if err != nil {
		w.observeLease("error")
		return status, ErrLeaseLost
	}
	if !status.Current {
		w.observeLease("lost")
		return status, ErrLeaseLost
	}
	w.observeLease("heartbeat")
	return status, nil
}

func (w *Worker) monitorLease(
	ctx context.Context,
	build *scannerrelease.BuildRun,
	cancel context.CancelCauseFunc,
	stop <-chan struct{},
	done chan<- struct{},
) {
	defer close(done)
	ticker := time.NewTicker(w.config.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-ticker.C:
			if _, err := w.checkLease(ctx, build); err != nil {
				cancel(err)
				return
			}
		}
	}
}

func (w *Worker) finalizeBuild(
	ctx context.Context,
	build *scannerrelease.BuildRun,
	target scannerrelease.BuildState,
) error {
	status, err := w.checkLeaseAllowCancellation(ctx, build)
	if err != nil {
		return err
	}
	w.writeMu.Lock()
	updated, err := w.config.Store.TransitionBuildRun(
		ctx, build.ID, status.Version, target,
		w.buildCommand(build, "finalize/"+string(target), "release build "+string(target)),
	)
	w.writeMu.Unlock()
	if err != nil {
		return fmt.Errorf("finalize scanner release build %s as %s: %w", build.ID, target, err)
	}
	*build = *updated
	return nil
}

func (w *Worker) markCandidateStarted(ctx context.Context, candidate *scannerrelease.Candidate) error {
	if candidate.State != scannerrelease.CandidateQueued {
		if candidate.State == scannerrelease.CandidateBuilding {
			return nil
		}
		return fmt.Errorf("scanner release candidate %s is not queued: %s", candidate.ID, candidate.State)
	}
	w.writeMu.Lock()
	updated, err := w.config.Store.TransitionCandidate(
		ctx, candidate.ID, candidate.Version, scannerrelease.CandidateBuilding,
		w.candidateCommand(candidate, "build-started", "candidate build started"),
	)
	w.writeMu.Unlock()
	if err != nil {
		return fmt.Errorf("mark scanner release candidate building: %w", err)
	}
	*candidate = *updated
	return nil
}

func (w *Worker) recordPolicyDecision(ctx context.Context, candidateID, digest string) error {
	candidate, err := w.config.Store.GetCandidate(ctx, candidateID)
	if err != nil {
		return err
	}
	if candidate.PolicyDecision == digest {
		return nil
	}
	candidate.PolicyDecision = digest
	w.writeMu.Lock()
	_, err = w.config.Store.UpdateCandidateProposal(
		ctx, candidate, candidate.Version,
		w.candidateCommand(candidate, "policy-decision", "candidate policy decision evidence recorded"),
	)
	w.writeMu.Unlock()
	if err != nil {
		return fmt.Errorf("record scanner release policy decision: %w", err)
	}
	return nil
}

func (w *Worker) loadPolicyDecision(
	ctx context.Context,
	buildID string,
) (scannerpolicy.Decision, error) {
	steps, err := w.config.Store.ListBuildSteps(ctx, buildID)
	if err != nil {
		return scannerpolicy.Decision{}, fmt.Errorf("load policy decision evidence: %w", err)
	}
	var selected *scannerrelease.BuildStep
	for index := range steps {
		step := &steps[index]
		if step.StepKey != "policy-evaluation" || step.State != scannerrelease.BuildCompleted {
			continue
		}
		if selected == nil || step.Attempt > selected.Attempt {
			selected = step
		}
	}
	if selected == nil {
		return scannerpolicy.Decision{}, errors.New("completed build has no trusted policy decision")
	}
	var payload struct {
		Evidence struct {
			PolicyDecision *scannerpolicy.Decision `json:"policy_decision"`
		} `json:"evidence"`
	}
	if err := json.Unmarshal([]byte(selected.SummaryJSON), &payload); err != nil {
		return scannerpolicy.Decision{}, fmt.Errorf("decode trusted policy decision: %w", err)
	}
	if payload.Evidence.PolicyDecision == nil ||
		!digestPattern.MatchString(payload.Evidence.PolicyDecision.PolicyDecisionDigest) {
		return scannerpolicy.Decision{}, errors.New("completed build has invalid trusted policy decision")
	}
	switch payload.Evidence.PolicyDecision.Outcome {
	case scannerpolicy.OutcomeBlocked, scannerpolicy.OutcomeAwaitingApproval,
		scannerpolicy.OutcomeApproved, scannerpolicy.OutcomeAutoApproved:
	default:
		return scannerpolicy.Decision{}, fmt.Errorf(
			"completed build has invalid policy outcome %q",
			payload.Evidence.PolicyDecision.Outcome,
		)
	}
	return *payload.Evidence.PolicyDecision, nil
}

func (w *Worker) loadPublicationReceiptDigest(ctx context.Context, buildID string) (string, error) {
	steps, err := w.config.Store.ListBuildSteps(ctx, buildID)
	if err != nil {
		return "", fmt.Errorf("load publication receipt evidence: %w", err)
	}
	var selected *scannerrelease.BuildStep
	for index := range steps {
		step := &steps[index]
		if step.StepKey != "candidate-evidence-summary" || step.State != scannerrelease.BuildCompleted {
			continue
		}
		if selected == nil || step.Attempt > selected.Attempt {
			selected = step
		}
	}
	if selected == nil || !digestPattern.MatchString(selected.OutputDigest) {
		return "", errors.New("completed build has no valid publication receipt digest")
	}
	return selected.OutputDigest, nil
}

func (w *Worker) markCandidateAwaitingApproval(ctx context.Context, candidateID string) error {
	for {
		candidate, err := w.config.Store.GetCandidate(ctx, candidateID)
		if err != nil {
			return err
		}
		var next scannerrelease.CandidateState
		switch candidate.State {
		case scannerrelease.CandidateQueued:
			next = scannerrelease.CandidateBuilding
		case scannerrelease.CandidateBuilding:
			next = scannerrelease.CandidateTesting
		case scannerrelease.CandidateTesting:
			next = scannerrelease.CandidateSecurityReview
		case scannerrelease.CandidateSecurityReview:
			if candidate.PolicyDecision == "" {
				return errors.New("candidate cannot await approval without a policy decision digest")
			}
			next = scannerrelease.CandidateAwaitingApproval
		case scannerrelease.CandidateAwaitingApproval:
			return nil
		default:
			return fmt.Errorf("candidate %s cannot advance from %s", candidate.ID, candidate.State)
		}
		w.writeMu.Lock()
		_, err = w.config.Store.TransitionCandidate(
			ctx, candidate.ID, candidate.Version, next,
			w.candidateCommand(candidate, "advance/"+string(next), "candidate evidence phase completed"),
		)
		w.writeMu.Unlock()
		if err != nil {
			return err
		}
	}
}

func (w *Worker) markCandidateAutoApproved(
	ctx context.Context,
	candidateID string,
	decision scannerpolicy.Decision,
	receiptDigest string,
) error {
	candidate, err := w.config.Store.GetCandidate(ctx, candidateID)
	if err != nil {
		return err
	}
	if candidate.State == scannerrelease.CandidateApproved {
		return nil
	}
	if candidate.State != scannerrelease.CandidateAwaitingApproval {
		return fmt.Errorf("candidate %s cannot be auto-approved from %s", candidate.ID, candidate.State)
	}
	approval := &scannerrelease.Approval{
		ID: uuid.NewString(), CandidateID: candidate.ID, Actor: w.config.WorkerID,
		Action: "approve", Reason: "automatic promotion permitted by immutable scanner release policy",
		EvidenceDigest: receiptDigest,
		PolicyDecision: decision.PolicyDecisionDigest,
		IdempotencyKey: "scanner-release-worker/" + candidate.ID + "/auto-approval/" + decision.PolicyDecisionDigest,
	}
	w.writeMu.Lock()
	err = w.config.Store.AddApproval(ctx, approval)
	w.writeMu.Unlock()
	if err != nil {
		return fmt.Errorf("record scanner release automatic approval: %w", err)
	}
	w.writeMu.Lock()
	_, err = w.config.Store.TransitionCandidate(
		ctx, candidate.ID, candidate.Version, scannerrelease.CandidateApproved,
		w.candidateCommand(candidate, "auto-approved", "candidate automatically approved by policy"),
	)
	w.writeMu.Unlock()
	if err != nil {
		return fmt.Errorf("mark scanner release candidate automatically approved: %w", err)
	}
	return nil
}

func (w *Worker) markCandidatePolicyBlocked(
	ctx context.Context,
	candidateID string,
	reasons []string,
) error {
	candidate, err := w.config.Store.GetCandidate(ctx, candidateID)
	if err != nil {
		return err
	}
	if candidate.State == scannerrelease.CandidateBlocked {
		return nil
	}
	if scannerrelease.IsTerminalCandidateState(candidate.State) {
		return fmt.Errorf("candidate %s cannot be policy-blocked from %s", candidate.ID, candidate.State)
	}
	candidate.ErrorClass = "policy_blocked"
	candidate.ErrorDetail = redactText(strings.Join(reasons, "; "))
	w.writeMu.Lock()
	updated, err := w.config.Store.UpdateCandidateProposal(
		ctx, candidate, candidate.Version,
		w.candidateCommand(candidate, "policy-block-evidence", "candidate policy block recorded"),
	)
	w.writeMu.Unlock()
	if err != nil {
		return err
	}
	w.writeMu.Lock()
	_, err = w.config.Store.TransitionCandidate(
		ctx, updated.ID, updated.Version, scannerrelease.CandidateBlocked,
		w.candidateCommand(updated, "policy-blocked", "candidate blocked by immutable policy"),
	)
	w.writeMu.Unlock()
	return err
}

func (w *Worker) markCandidateFailed(ctx context.Context, candidateID string, cause error) error {
	candidate, err := w.config.Store.GetCandidate(ctx, candidateID)
	if err != nil {
		return err
	}
	if scannerrelease.IsTerminalCandidateState(candidate.State) {
		return nil
	}
	candidate.ErrorClass = "build_failed"
	if errors.Is(cause, ErrReconciliationRequired) {
		candidate.ErrorClass = "reconciliation_required"
	}
	candidate.ErrorDetail = redactText(cause.Error())
	w.writeMu.Lock()
	updated, err := w.config.Store.UpdateCandidateProposal(
		ctx, candidate, candidate.Version,
		w.candidateCommand(candidate, "failure-evidence", "candidate failure evidence recorded"),
	)
	w.writeMu.Unlock()
	if err != nil {
		return err
	}
	w.writeMu.Lock()
	_, err = w.config.Store.TransitionCandidate(
		ctx, updated.ID, updated.Version, scannerrelease.CandidateFailed,
		w.candidateCommand(updated, "failed", "candidate build failed"),
	)
	w.writeMu.Unlock()
	return err
}

func (w *Worker) markCandidateBlocked(ctx context.Context, candidateID, reason string) error {
	candidate, err := w.config.Store.GetCandidate(ctx, candidateID)
	if err != nil {
		return err
	}
	switch candidate.State {
	case scannerrelease.CandidateBlocked, scannerrelease.CandidateRejected,
		scannerrelease.CandidateFailed, scannerrelease.CandidatePublished:
		return nil
	case scannerrelease.CandidateQueued, scannerrelease.CandidateBuilding,
		scannerrelease.CandidateTesting, scannerrelease.CandidateSecurityReview,
		scannerrelease.CandidateAwaitingApproval:
	default:
		return fmt.Errorf("candidate %s cannot be blocked from %s", candidate.ID, candidate.State)
	}
	candidate.ErrorClass = "build_cancelled"
	candidate.ErrorDetail = redactText(reason)
	updated, err := w.config.Store.UpdateCandidateProposal(
		ctx, candidate, candidate.Version,
		w.candidateCommand(candidate, "cancellation-evidence", "candidate cancellation evidence recorded"),
	)
	if err != nil {
		return err
	}
	_, err = w.config.Store.TransitionCandidate(
		ctx, updated.ID, updated.Version, scannerrelease.CandidateBlocked,
		w.candidateCommand(updated, "blocked", "candidate build cancelled"),
	)
	return err
}

func (w *Worker) command(aggregateID, phase, reason string) scannerrelease.TransitionCommand {
	return scannerrelease.TransitionCommand{
		Actor: w.config.WorkerID, Reason: reason,
		IdempotencyKey: "scanner-release-worker/" + aggregateID + "/" + phase,
		PayloadJSON:    `{"redacted":true}`,
	}
}

func (w *Worker) stepCommand(
	build *scannerrelease.BuildRun,
	step *scannerrelease.BuildStep,
	phase, reason string,
) scannerrelease.TransitionCommand {
	command := w.buildCommand(
		build, "step/"+step.StepKey+"/"+fmt.Sprint(step.Attempt)+"/"+phase, reason,
	)
	return command
}

func (w *Worker) buildCommand(
	build *scannerrelease.BuildRun,
	phase, reason string,
) scannerrelease.TransitionCommand {
	command := w.command(build.ID, phase, reason)
	// A reclaimed build has a new lease but the same aggregate and logical
	// operations. Lease-scoping lifecycle commands preserves command
	// idempotency within one owner while allowing the replacement owner to
	// append its own recovery audit transitions.
	command.IdempotencyKey += "/claim/" + build.LeaseToken
	return command
}

func (w *Worker) candidateCommand(
	candidate *scannerrelease.Candidate,
	phase, reason string,
) scannerrelease.TransitionCommand {
	command := w.command(candidate.ID, "candidate/"+phase, reason)
	command.PolicyRevision = candidate.PolicyRevision
	return command
}

func (w *Worker) now() time.Time {
	return w.config.Now().UTC()
}

func (w *Worker) observeClaim(result string) {
	if w.config.Observer != nil {
		w.config.Observer.ObserveClaim(scannerobservability.ComponentBuild, result)
	}
}

func (w *Worker) observeLease(result string) {
	if w.config.Observer != nil {
		w.config.Observer.ObserveLease(scannerobservability.ComponentBuild, result)
	}
}

func (w *Worker) observeRetry(reason string) {
	if w.config.Observer != nil {
		w.config.Observer.ObserveRetry(scannerobservability.ComponentBuild, reason)
	}
}

func (w *Worker) observeResult(state string) {
	if w.config.Observer != nil {
		w.config.Observer.ObserveResult(scannerobservability.ComponentBuild, state)
	}
}

func (w *Worker) setState(state string) {
	if w.config.Observer != nil {
		w.config.Observer.SetState(scannerobservability.ComponentBuild, state)
	}
}

func (w *Worker) setStuck(kind string, count int) {
	if w.config.Observer != nil {
		w.config.Observer.SetStuckWork(scannerobservability.ComponentBuild, kind, count)
	}
}

func buildResultState(err error) string {
	if errors.Is(err, ErrReconciliationRequired) {
		return "reconciliation_required"
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, ErrCancellationRequested) {
		return "cancelled"
	}
	return "failed"
}

func metadataJSON(metadata persistedStepMetadata) string {
	encoded, _ := json.Marshal(metadata)
	return string(encoded)
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
