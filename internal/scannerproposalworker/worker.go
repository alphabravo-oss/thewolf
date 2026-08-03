package scannerproposalworker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/alphabravocompany/thewolf/internal/scannercontrol"
	"github.com/alphabravocompany/thewolf/internal/scannerdiscovery"
	"github.com/alphabravocompany/thewolf/internal/scannerobservability"
	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
	"github.com/alphabravocompany/thewolf/internal/scannertrace"
)

const (
	defaultPollInterval      = 3 * time.Second
	defaultHeartbeatInterval = 10 * time.Second
	defaultLeaseDuration     = 45 * time.Second
	defaultDrainTimeout      = time.Minute
)

var (
	digestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	commitPattern = regexp.MustCompile(`^[a-f0-9]{7,64}$`)
)

func New(config Config) (*Worker, error) {
	if config.Store == nil || config.Proposer == nil || strings.TrimSpace(config.WorkerID) == "" {
		return nil, errors.New("scanner proposal store, proposer, and worker ID are required")
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
		return nil, errors.New("scanner proposal poll interval must be positive")
	case config.HeartbeatInterval <= 0:
		return nil, errors.New("scanner proposal heartbeat interval must be positive")
	case config.LeaseDuration <= config.HeartbeatInterval*2:
		return nil, errors.New("scanner proposal lease duration must exceed two heartbeat intervals")
	case config.DrainTimeout <= 0:
		return nil, errors.New("scanner proposal drain timeout must be positive")
	}
	return &Worker{config: config}, nil
}

type Worker struct {
	config Config
}

func (w *Worker) Run(ctx context.Context) error {
	for {
		processed, err := w.RunOnce(ctx)
		if err != nil && !errors.Is(err, ErrProposalRaceLost) {
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
				scannerobservability.ComponentProposal, runResult, w.now().Sub(started),
			)
		}
	}()
	now := w.now()
	reclaimed, err := w.config.Store.ReclaimStaleCandidateProposals(ctx, now)
	if err != nil {
		if isContentionError(err) {
			runResult = "contention"
			w.observeRetry("contention")
			w.setState("active")
			return false, fmt.Errorf("%w: reclaim stale scanner proposals: %v", ErrProposalRaceLost, err)
		}
		runResult = "error"
		w.setState("degraded")
		return false, fmt.Errorf("reclaim stale scanner proposals: %w", err)
	}
	if reclaimed > 0 {
		w.observeLease("reclaimed")
		w.observeRetry("stale_lease")
	}
	// Reclaimed proposals are no longer stuck; retain the incident as bounded
	// counters and expose only currently unresolved work in the gauge.
	w.setStuck("expired_lease", 0)
	candidate, err := w.config.Store.ClaimNextCandidateProposal(
		ctx, w.config.WorkerID, now.Add(w.config.LeaseDuration),
	)
	if err != nil {
		if isContentionError(err) {
			runResult = "contention"
			w.observeClaim("contended")
			w.observeRetry("contention")
			w.setState("active")
			return false, fmt.Errorf("%w: claim scanner proposal: %v", ErrProposalRaceLost, err)
		}
		runResult = "error"
		w.observeClaim("error")
		w.setState("degraded")
		return false, fmt.Errorf("claim scanner proposal: %w", err)
	}
	if candidate == nil {
		w.observeClaim("empty")
		processed, reconcileErr := w.reconcileQueuedCandidate(ctx)
		if reconcileErr != nil {
			runResult = "error"
			w.setState("degraded")
		} else {
			w.setState("idle")
		}
		return processed, reconcileErr
	}
	w.observeClaim("acquired")
	w.setState("busy")
	claimContext, _, traceErr := scannertrace.Resume(
		ctx, w.config.Store, "candidate", candidate.ID, "proposal-worker",
	)
	if traceErr != nil {
		runResult = "error"
		w.setState("degraded")
		return true, fmt.Errorf("resume scanner proposal operation correlation: %w", traceErr)
	}
	scannertrace.Logger(claimContext).Info().
		Str("aggregate_type", "candidate").
		Str("aggregate_id", candidate.ID).
		Str("state", string(candidate.State)).
		Msg("scanner release work claimed")
	err = w.runClaimWithDrain(claimContext, candidate)
	if err != nil {
		runResult = "error"
		w.setState("degraded")
		w.observeResult(proposalResultState(err))
		if errors.Is(err, ErrProposalRaceLost) {
			w.observeRetry("contention")
		}
		scannertrace.Logger(claimContext).Warn().
			Str("aggregate_type", "candidate").
			Str("aggregate_id", candidate.ID).
			Str("error_class", proposalResultState(err)).
			Msg("scanner release work failed")
		return true, err
	}
	w.setState("active")
	w.setStuck("lease_lost", 0)
	w.observeResult("completed")
	scannertrace.Logger(claimContext).Info().
		Str("aggregate_type", "candidate").
		Str("aggregate_id", candidate.ID).
		Str("state", "completed").
		Msg("scanner release work completed")
	return true, nil
}

func (w *Worker) runClaimWithDrain(
	parent context.Context,
	candidate *scannerrelease.Candidate,
) error {
	workContext, cancelWork := context.WithCancelCause(context.WithoutCancel(parent))
	defer cancelWork(nil)
	done := make(chan error, 1)
	go func() {
		done <- w.processClaim(workContext, candidate)
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
	candidate *scannerrelease.Candidate,
) error {
	request, err := proposalRequest(ctx, w.config.Store, candidate)
	if err != nil {
		if errors.Is(err, ErrNoCandidateUpdates) {
			return w.completeNoOp(context.WithoutCancel(ctx), candidate)
		}
		return w.releaseFailure(
			context.WithoutCancel(ctx), candidate, "invalid_selection",
			scannerdiscovery.RedactText(err.Error()), err,
		)
	}

	claimContext, cancelClaim := context.WithCancelCause(ctx)
	defer cancelClaim(nil)
	stopHeartbeat := make(chan struct{})
	heartbeatDone := make(chan error, 1)
	go func() {
		heartbeatDone <- w.monitorLease(
			claimContext, candidate, cancelClaim, stopHeartbeat,
		)
	}()
	result, proposerErr := w.config.Proposer.Propose(claimContext, request)
	close(stopHeartbeat)
	monitorErr := <-heartbeatDone
	cause := context.Cause(claimContext)

	if errors.Is(cause, ErrDrainDeadline) {
		return cause
	}
	if monitorErr != nil {
		if errors.Is(monitorErr, ErrProposalRaceLost) {
			return ErrProposalRaceLost
		}
		return monitorErr
	}
	if cause != nil {
		if errors.Is(cause, ErrProposalRaceLost) {
			return ErrProposalRaceLost
		}
		return cause
	}
	if proposerErr != nil {
		safeError := scannerdiscovery.RedactText(proposerErr.Error())
		return w.releaseFailure(
			context.WithoutCancel(ctx), candidate, "proposal_execution",
			safeError,
			fmt.Errorf("generate scanner proposal for %s: %s", candidate.ID, safeError),
		)
	}
	if err := validateResult(result); err != nil {
		return w.releaseFailure(
			context.WithoutCancel(ctx), candidate, "invalid_result",
			scannerdiscovery.RedactText(err.Error()),
			fmt.Errorf("validate scanner proposal for %s: %w", candidate.ID, err),
		)
	}

	status, err := w.heartbeat(context.WithoutCancel(ctx), candidate)
	if err != nil {
		return fmt.Errorf("final scanner proposal heartbeat: %w", err)
	}
	if !status.Current {
		return ErrProposalRaceLost
	}
	candidate.Version = status.Version
	candidate.ProposedCommit = result.ProposedCommit
	candidate.ProposalURL = result.ProposalURL
	candidate.LockDigest = result.LockDigest
	candidate.LockURI = result.LockURI
	candidate.RiskSummaryJSON = string(result.RiskSummary)
	queued, err := w.config.Store.FinalizeCandidateProposal(
		context.WithoutCancel(ctx), candidate, candidate.Version,
		candidate.ProposalLeaseToken,
		scannerrelease.TransitionCommand{
			Actor: w.config.WorkerID, Reason: "deterministic scanner definition proposal generated",
			PolicyRevision: candidate.PolicyRevision,
			IdempotencyKey: request.IdempotencyKey + "/finalized",
			PayloadJSON:    `{"redacted":true}`,
		},
	)
	if errors.Is(err, scannerrelease.ErrVersionConflict) ||
		errors.Is(err, scannerrelease.ErrLeaseNotOwned) {
		return ErrProposalRaceLost
	}
	if isContentionError(err) {
		return fmt.Errorf("%w: %v", ErrProposalRaceLost, err)
	}
	if err != nil {
		return err
	}
	if err := scannercontrol.EnqueueCandidateBuildPlan(ctx, w.config.Store, queued, result.Images); err != nil {
		if errors.Is(err, scannerrelease.ErrIdempotencyConflict) ||
			errors.Is(err, scannerrelease.ErrVersionConflict) ||
			isContentionError(err) {
			return fmt.Errorf("%w: %v", ErrProposalRaceLost, err)
		}
		return fmt.Errorf("enqueue scanner candidate build plan: %w", err)
	}
	return nil
}

func (w *Worker) completeNoOp(
	ctx context.Context,
	candidate *scannerrelease.Candidate,
) error {
	payload, _ := json.Marshal(map[string]any{
		"outcome":           "no_changes",
		"discovery_run_id":  candidate.DiscoveryRunID,
		"definition_commit": candidate.DefinitionCommit,
		"policy_id":         candidate.PolicyID,
		"policy_revision":   candidate.PolicyRevision,
	})
	_, err := w.config.Store.FinalizeCandidateProposalNoOp(
		ctx, candidate.ID, w.config.WorkerID, candidate.ProposalLeaseToken,
		"no_changes", "scheduled discovery found no scanner definition changes",
		scannerrelease.TransitionCommand{
			Actor: w.config.WorkerID, Reason: "scheduled scanner definition is already current",
			PolicyRevision: candidate.PolicyRevision,
			IdempotencyKey: candidate.IdempotencyKey + "/proposal/noop",
			PayloadJSON:    string(payload),
		},
	)
	if errors.Is(err, scannerrelease.ErrLeaseNotOwned) ||
		errors.Is(err, scannerrelease.ErrVersionConflict) || isContentionError(err) {
		return ErrProposalRaceLost
	}
	return err
}

func (w *Worker) monitorLease(
	ctx context.Context,
	candidate *scannerrelease.Candidate,
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
			status, err := w.heartbeat(ctx, candidate)
			if err != nil {
				err = fmt.Errorf("heartbeat scanner proposal: %w", err)
				cancel(err)
				return err
			}
			if !status.Current {
				cancel(ErrProposalRaceLost)
				return ErrProposalRaceLost
			}
		}
	}
}

func (w *Worker) heartbeat(
	ctx context.Context,
	candidate *scannerrelease.Candidate,
) (scannerrelease.CandidateProposalLeaseStatus, error) {
	status, err := w.config.Store.HeartbeatCandidateProposal(
		ctx, candidate.ID, w.config.WorkerID, candidate.ProposalLeaseToken,
		w.now().Add(w.config.LeaseDuration),
	)
	switch {
	case err != nil:
		w.observeLease("error")
	case !status.Current:
		w.observeLease("lost")
		w.setStuck("lease_lost", 1)
	default:
		w.observeLease("heartbeat")
	}
	return status, err
}

func (w *Worker) releaseFailure(
	ctx context.Context,
	candidate *scannerrelease.Candidate,
	errorClass, errorDetail string,
	operationErr error,
) error {
	_, err := w.config.Store.ReleaseCandidateProposal(
		ctx, candidate.ID, w.config.WorkerID, candidate.ProposalLeaseToken,
		errorClass, scannerdiscovery.RedactText(errorDetail),
		scannerrelease.TransitionCommand{
			Actor: w.config.WorkerID, Reason: "scanner proposal attempt failed",
			PolicyRevision: candidate.PolicyRevision,
			IdempotencyKey: candidate.IdempotencyKey + "/proposal/release/" +
				fmt.Sprint(candidate.ProposalAttempt),
			PayloadJSON: `{"redacted":true}`,
		},
	)
	if errors.Is(err, scannerrelease.ErrLeaseNotOwned) ||
		errors.Is(err, scannerrelease.ErrVersionConflict) {
		return ErrProposalRaceLost
	}
	if err != nil {
		return errors.Join(operationErr, err)
	}
	return operationErr
}

func (w *Worker) reconcileQueuedCandidate(ctx context.Context) (bool, error) {
	page, err := w.config.Store.ListCandidates(
		ctx,
		scannerrelease.CandidateFilter{State: scannerrelease.CandidateQueued},
		scannerrelease.PageRequest{Limit: 25},
	)
	if err != nil {
		return false, err
	}
	for index := range page.Items {
		candidate := &page.Items[index]
		runs, err := w.config.Store.ListBuildRuns(ctx, candidate.ID)
		if err != nil {
			return false, err
		}
		if len(runs) != 0 {
			continue
		}
		if err := scannercontrol.EnqueueCandidateBuildPlan(ctx, w.config.Store, candidate, nil); err != nil {
			if errors.Is(err, scannerrelease.ErrIdempotencyConflict) ||
				errors.Is(err, scannerrelease.ErrVersionConflict) ||
				isContentionError(err) {
				return true, ErrProposalRaceLost
			}
			return true, err
		}
		return true, nil
	}
	return false, nil
}

func validateResult(result Result) error {
	switch {
	case !commitPattern.MatchString(result.ProposedCommit):
		return errors.New("proposed commit must be a canonical lowercase hexadecimal Git object ID")
	case !digestPattern.MatchString(result.LockDigest):
		return errors.New("proposal lock digest is invalid")
	case len(result.RiskSummary) == 0 || !json.Valid(result.RiskSummary):
		return errors.New("proposal risk summary must be valid JSON")
	case !strings.Contains(result.LockURI, result.LockDigest):
		return errors.New("proposal lock URI must contain its immutable digest")
	}
	if err := safeReference(result.LockURI, true); err != nil {
		return fmt.Errorf("invalid lock URI: %w", err)
	}
	if result.ProposalURL != "" {
		if err := safeReference(result.ProposalURL, false); err != nil {
			return fmt.Errorf("invalid proposal URL: %w", err)
		}
	}
	return nil
}

func safeReference(raw string, allowOCI bool) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User != nil || parsed.Host == "" {
		return errors.New("absolute credential-free reference is required")
	}
	allowed := parsed.Scheme == "https"
	if allowOCI {
		allowed = allowed || parsed.Scheme == "oci" || parsed.Scheme == "git"
	}
	if !allowed {
		return fmt.Errorf("unsupported scheme %q", parsed.Scheme)
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("query strings and fragments are not allowed")
	}
	return nil
}

func isContentionError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "database is locked") ||
		strings.Contains(text, "serialization failure") ||
		strings.Contains(text, "could not serialize") ||
		strings.Contains(text, "deadlock detected")
}

func (w *Worker) now() time.Time {
	return w.config.Now().UTC()
}

func (w *Worker) observeClaim(result string) {
	if w.config.Observer != nil {
		w.config.Observer.ObserveClaim(scannerobservability.ComponentProposal, result)
	}
}

func (w *Worker) observeLease(result string) {
	if w.config.Observer != nil {
		w.config.Observer.ObserveLease(scannerobservability.ComponentProposal, result)
	}
}

func (w *Worker) observeRetry(reason string) {
	if w.config.Observer != nil {
		w.config.Observer.ObserveRetry(scannerobservability.ComponentProposal, reason)
	}
}

func (w *Worker) observeResult(state string) {
	if w.config.Observer != nil {
		w.config.Observer.ObserveResult(scannerobservability.ComponentProposal, state)
	}
}

func (w *Worker) setState(state string) {
	if w.config.Observer != nil {
		w.config.Observer.SetState(scannerobservability.ComponentProposal, state)
	}
}

func (w *Worker) setStuck(kind string, count int) {
	if w.config.Observer != nil {
		w.config.Observer.SetStuckWork(scannerobservability.ComponentProposal, kind, count)
	}
}

func proposalResultState(err error) string {
	if errors.Is(err, context.Canceled) {
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
