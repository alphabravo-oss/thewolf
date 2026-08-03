package scannerdiscoveryworker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/alphabravocompany/thewolf/internal/scannerdiscovery"
	"github.com/alphabravocompany/thewolf/internal/scannerobservability"
	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
	"github.com/alphabravocompany/thewolf/internal/scannertrace"
)

type Worker struct {
	config Config
}

func New(config Config) (*Worker, error) {
	switch {
	case config.Store == nil:
		return nil, errors.New("scanner discovery worker store is required")
	case config.Runner == nil:
		return nil, errors.New("scanner discovery worker runner is required")
	case config.WorkerID == "":
		return nil, errors.New("scanner discovery worker ID is required")
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
	switch {
	case config.PollInterval <= 0:
		return nil, errors.New("scanner discovery poll interval must be positive")
	case config.HeartbeatInterval <= 0:
		return nil, errors.New("scanner discovery heartbeat interval must be positive")
	case config.LeaseDuration <= config.HeartbeatInterval*2:
		return nil, errors.New("scanner discovery lease duration must exceed two heartbeat intervals")
	case config.DrainTimeout <= 0:
		return nil, errors.New("scanner discovery drain timeout must be positive")
	}
	return &Worker{config: config}, nil
}

// Run continuously reclaims and claims discovery work. Once mode performs one
// reclaim/claim pass and exits even when the queue is empty.
func (w *Worker) Run(ctx context.Context) error {
	for {
		processed, err := w.RunOnce(ctx)
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

func (w *Worker) RunOnce(ctx context.Context) (bool, error) {
	started := w.now()
	runResult := "success"
	defer func() {
		if w.config.Observer != nil {
			w.config.Observer.ObserveRun(
				scannerobservability.ComponentDiscovery, runResult, w.now().Sub(started),
			)
		}
	}()
	now := w.now()
	reclaimed, err := w.config.Store.ReclaimStaleDiscoveryRuns(ctx, now)
	if err != nil {
		runResult = "error"
		w.setState("degraded")
		return false, fmt.Errorf("reclaim stale scanner discovery runs: %w", err)
	}
	if reclaimed > 0 {
		w.observeLease("reclaimed")
		w.observeRetry("stale_lease")
	}
	// ReclaimStaleDiscoveryRuns has already moved every returned item out of
	// stale ownership. The counter records the incident; the gauge represents
	// currently stuck work and is therefore clear after successful recovery.
	w.setStuck("expired_lease", 0)
	run, err := w.config.Store.ClaimNextDiscoveryRun(
		ctx, w.config.WorkerID, now.Add(w.config.LeaseDuration),
	)
	if err != nil {
		runResult = "error"
		w.observeClaim("error")
		w.setState("degraded")
		return false, fmt.Errorf("claim scanner discovery run: %w", err)
	}
	if run == nil {
		w.observeClaim("empty")
		w.setState("idle")
		return false, nil
	}
	w.observeClaim("acquired")
	w.setState("busy")
	claimContext, _, traceErr := scannertrace.Resume(
		ctx, w.config.Store, "discovery", run.ID, "discovery-worker",
	)
	if traceErr != nil {
		runResult = "error"
		w.setState("degraded")
		return true, fmt.Errorf("resume scanner discovery operation correlation: %w", traceErr)
	}
	scannertrace.Logger(claimContext).Info().
		Str("aggregate_type", "discovery").
		Str("aggregate_id", run.ID).
		Str("state", string(run.State)).
		Msg("scanner release work claimed")
	err = w.runClaimWithDrain(claimContext, run)
	if err != nil {
		runResult = "error"
		w.setState("degraded")
		w.observeResult(discoveryResultState(run, err))
		if errors.Is(err, ErrLeaseLost) {
			w.observeLease("lost")
			w.setStuck("lease_lost", 1)
		}
		scannertrace.Logger(claimContext).Warn().
			Str("aggregate_type", "discovery").
			Str("aggregate_id", run.ID).
			Str("error_class", discoveryResultState(run, err)).
			Msg("scanner release work failed")
		return true, err
	}
	w.setState("active")
	w.setStuck("lease_lost", 0)
	w.observeResult(discoveryResultState(run, nil))
	scannertrace.Logger(claimContext).Info().
		Str("aggregate_type", "discovery").
		Str("aggregate_id", run.ID).
		Str("state", string(run.State)).
		Msg("scanner release work completed")
	return true, nil
}

func (w *Worker) runClaimWithDrain(
	parent context.Context,
	run *scannerrelease.DiscoveryRun,
) error {
	workContext, cancelWork := context.WithCancelCause(context.WithoutCancel(parent))
	defer cancelWork(nil)
	done := make(chan error, 1)
	go func() {
		done <- w.processClaim(workContext, run)
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
	claimed *scannerrelease.DiscoveryRun,
) error {
	claimContext, cancelClaim := context.WithCancelCause(ctx)
	defer cancelClaim(nil)
	var version atomic.Int64
	version.Store(claimed.Version)
	var cancellationRequested atomic.Bool
	stopHeartbeat := make(chan struct{})
	heartbeatDone := make(chan error, 1)
	go func() {
		heartbeatDone <- w.monitorLease(
			claimContext, claimed, &version, &cancellationRequested,
			cancelClaim, stopHeartbeat,
		)
	}()

	result, runnerErr := w.config.Runner.Discover(claimContext, *claimed)
	close(stopHeartbeat)
	monitorErr := <-heartbeatDone
	cause := context.Cause(claimContext)

	if errors.Is(cause, ErrDrainDeadline) || errors.Is(cause, ErrLeaseLost) {
		return cause
	}
	if monitorErr != nil && !errors.Is(monitorErr, ErrCancellationRequested) {
		return monitorErr
	}
	if cause != nil && !errors.Is(cause, ErrCancellationRequested) {
		return cause
	}

	status, err := w.heartbeat(context.WithoutCancel(ctx), claimed)
	if err != nil {
		return err
	}
	if !status.Current {
		return ErrLeaseLost
	}
	version.Store(status.Version)
	if status.CancelRequested {
		cancellationRequested.Store(true)
	}

	items := mapUpdateItems(result.Items)
	applyRunResult(claimed, result, runnerErr, cancellationRequested.Load(), w.now())
	command := scannerrelease.TransitionCommand{
		Actor:          w.config.WorkerID,
		Reason:         "discovery worker persisted terminal result",
		PolicyRevision: claimed.PolicyRevision,
		IdempotencyKey: "finalize:" + claimed.LeaseToken,
		PayloadJSON:    discoveryResultPayload(claimed),
	}
	for attempt := 0; attempt < 2; attempt++ {
		_, err = w.config.Store.FinalizeDiscoveryRun(
			context.WithoutCancel(ctx), claimed, version.Load(),
			claimed.LeaseToken, items, command,
		)
		if !errors.Is(err, scannerrelease.ErrVersionConflict) {
			return err
		}
		w.observeRetry("version_conflict")
		status, heartbeatErr := w.heartbeat(context.WithoutCancel(ctx), claimed)
		if heartbeatErr != nil {
			return errors.Join(err, heartbeatErr)
		}
		if !status.Current {
			return ErrLeaseLost
		}
		version.Store(status.Version)
		if status.CancelRequested {
			claimed.State = scannerrelease.DiscoveryCancelled
			claimed.ErrorClass = "cancelled"
			claimed.ErrorDetail = "discovery cancelled by operator request"
		}
	}
	return err
}

func (w *Worker) monitorLease(
	ctx context.Context,
	run *scannerrelease.DiscoveryRun,
	version *atomic.Int64,
	cancellationRequested *atomic.Bool,
	cancel context.CancelCauseFunc,
	stop <-chan struct{},
) error {
	ticker := time.NewTicker(w.config.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return nil
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-ticker.C:
			status, err := w.heartbeat(ctx, run)
			if err != nil {
				err = fmt.Errorf("heartbeat scanner discovery run: %w", err)
				cancel(err)
				return err
			}
			if !status.Current {
				cancel(ErrLeaseLost)
				return ErrLeaseLost
			}
			version.Store(status.Version)
			if status.CancelRequested {
				cancellationRequested.Store(true)
				cancel(ErrCancellationRequested)
				return ErrCancellationRequested
			}
		}
	}
}

func (w *Worker) heartbeat(
	ctx context.Context,
	run *scannerrelease.DiscoveryRun,
) (scannerrelease.DiscoveryLeaseStatus, error) {
	status, err := w.config.Store.HeartbeatDiscoveryRun(
		ctx, run.ID, w.config.WorkerID, run.LeaseToken,
		w.now().Add(w.config.LeaseDuration),
	)
	switch {
	case err != nil:
		w.observeLease("error")
	case !status.Current:
		w.observeLease("lost")
	default:
		w.observeLease("heartbeat")
	}
	return status, err
}

func (w *Worker) now() time.Time {
	return w.config.Now().UTC()
}

func (w *Worker) observeClaim(result string) {
	if w.config.Observer != nil {
		w.config.Observer.ObserveClaim(scannerobservability.ComponentDiscovery, result)
	}
}

func (w *Worker) observeLease(result string) {
	if w.config.Observer != nil {
		w.config.Observer.ObserveLease(scannerobservability.ComponentDiscovery, result)
	}
}

func (w *Worker) observeRetry(reason string) {
	if w.config.Observer != nil {
		w.config.Observer.ObserveRetry(scannerobservability.ComponentDiscovery, reason)
	}
}

func (w *Worker) observeResult(state string) {
	if w.config.Observer != nil {
		w.config.Observer.ObserveResult(scannerobservability.ComponentDiscovery, state)
	}
}

func (w *Worker) setState(state string) {
	if w.config.Observer != nil {
		w.config.Observer.SetState(scannerobservability.ComponentDiscovery, state)
	}
}

func (w *Worker) setStuck(kind string, count int) {
	if w.config.Observer != nil {
		w.config.Observer.SetStuckWork(scannerobservability.ComponentDiscovery, kind, count)
	}
}

func discoveryResultState(run *scannerrelease.DiscoveryRun, err error) string {
	if errors.Is(err, context.Canceled) || errors.Is(err, ErrCancellationRequested) ||
		run.State == scannerrelease.DiscoveryCancelled {
		return "cancelled"
	}
	switch run.State {
	case scannerrelease.DiscoveryCompleted:
		if run.ErrorClass == "partial_coverage" {
			return "partial"
		}
		return "completed"
	default:
		return "failed"
	}
}

func applyRunResult(
	target *scannerrelease.DiscoveryRun,
	result scannerdiscovery.Run,
	runnerErr error,
	cancellationRequested bool,
	now time.Time,
) {
	target.DefinitionDigest = result.DefinitionDigest
	target.LockDigest = result.LockDigest
	if encoded, err := json.Marshal(result.Scope); err == nil && result.Scope.Mode != "" {
		target.ScopeJSON = string(encoded)
	}
	target.Coverage = result.Coverage
	target.TotalCount = result.Counts.Total
	target.CoveredCount = result.Counts.Covered
	target.CurrentCount = result.Counts.Current
	target.AvailableCount = result.Counts.UpdateAvailable
	target.UnreachableCount = result.Counts.Unreachable
	target.UnsupportedCount = result.Counts.Unsupported
	target.HeldCount = result.Counts.Held
	target.YankedCount = result.Counts.Yanked
	target.UnknownCount = result.Counts.Unknown
	if !result.CompletedAt.IsZero() {
		completed := result.CompletedAt.UTC()
		target.CompletedAt = &completed
	} else {
		completed := now.UTC()
		target.CompletedAt = &completed
	}
	target.ErrorClass = ""
	target.ErrorDetail = ""
	switch {
	case cancellationRequested || result.State == scannerdiscovery.RunCancelled:
		target.State = scannerrelease.DiscoveryCancelled
		target.ErrorClass = "cancelled"
		target.ErrorDetail = "discovery cancelled by operator request"
	case runnerErr != nil:
		target.State = scannerrelease.DiscoveryFailed
		target.ErrorClass = "discovery_execution"
		target.ErrorDetail = scannerdiscovery.RedactText(runnerErr.Error())
	case result.State == scannerdiscovery.RunCompleted:
		target.State = scannerrelease.DiscoveryCompleted
	case result.State == scannerdiscovery.RunPartial:
		// Partial is a successful, inspectable discovery result in the durable
		// release lifecycle. Coverage and counts carry the machine-readable
		// distinction without introducing a state older UIs cannot render.
		target.State = scannerrelease.DiscoveryCompleted
		target.ErrorClass = "partial_coverage"
		target.ErrorDetail = "discovery completed with incomplete source coverage"
	case result.State == scannerdiscovery.RunFailed:
		target.State = scannerrelease.DiscoveryFailed
		target.ErrorClass = "source_coverage_failed"
		target.ErrorDetail = "discovery could not resolve any selected source"
	default:
		target.State = scannerrelease.DiscoveryFailed
		target.ErrorClass = "invalid_result"
		target.ErrorDetail = "discovery runner returned an invalid terminal state"
	}
}

func mapUpdateItems(results []scannerdiscovery.ItemResult) []scannerrelease.UpdateItem {
	items := make([]scannerrelease.UpdateItem, 0, len(results))
	for _, result := range results {
		safeItem := scannerdiscovery.RedactItem(result.Item)
		safeEvidence := scannerdiscovery.RedactEvidence(result.Evidence)
		evidenceJSON, _ := json.Marshal(safeEvidence)
		compatibilityJSON, _ := json.Marshal(map[string]any{
			"current_digest":  safeItem.CurrentDigest,
			"definition_risk": safeItem.DefinitionRisk,
			"risk_reasons":    result.Risk.Reasons,
			"source":          safeItem.Source,
			"platforms":       safeItem.Platforms,
			"metadata":        safeItem.Metadata,
		})
		selection := "unselected"
		if result.Status == scannerdiscovery.StatusHeld {
			selection = "held"
		}
		var checkedAt *time.Time
		if !result.CheckedAt.IsZero() {
			checked := result.CheckedAt.UTC()
			checkedAt = &checked
		}
		items = append(items, scannerrelease.UpdateItem{
			ComponentType:      scannerrelease.ComponentType(safeItem.ID.Kind),
			ComponentName:      safeItem.ID.Name,
			CurrentValue:       safeItem.CurrentValue,
			AvailableValue:     scannerdiscovery.RedactText(result.AvailableValue),
			AvailableDigest:    scannerdiscovery.RedactText(result.AvailableDigest),
			Status:             string(result.Status),
			SourceEvidenceJSON: string(evidenceJSON),
			RiskClass:          riskClass(result.Risk.Level),
			CompatibilityJSON:  string(compatibilityJSON),
			SelectionState:     selection,
			ErrorClass:         string(result.ErrorClass),
			ErrorDetail:        scannerdiscovery.RedactText(result.Error),
			Resolver:           scannerdiscovery.RedactText(result.Resolver),
			Attempts:           result.Attempts,
			RetryAt:            result.RetryAt,
			CheckedAt:          checkedAt,
		})
	}
	return items
}

func riskClass(risk scannerdiscovery.Risk) scannerrelease.RiskClass {
	switch risk {
	case scannerdiscovery.RiskLow:
		return scannerrelease.RiskLow
	case scannerdiscovery.RiskMedium:
		return scannerrelease.RiskMedium
	case scannerdiscovery.RiskHigh:
		return scannerrelease.RiskHigh
	case scannerdiscovery.RiskCritical:
		return scannerrelease.RiskCritical
	default:
		return scannerrelease.RiskNone
	}
}

func discoveryResultPayload(run *scannerrelease.DiscoveryRun) string {
	payload, _ := json.Marshal(map[string]any{
		"coverage":          run.Coverage,
		"total":             run.TotalCount,
		"covered":           run.CoveredCount,
		"update_available":  run.AvailableCount,
		"definition_digest": run.DefinitionDigest,
		"lock_digest":       run.LockDigest,
		"error_class":       run.ErrorClass,
	})
	return string(payload)
}
