// Package scanneralertworker evaluates durable scanner-release operational
// alerts under a replica-safe schedule lease.
package scanneralertworker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alphabravocompany/thewolf/internal/scannerobservability"
	"github.com/alphabravocompany/thewolf/internal/scannerpolicy"
	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

const (
	defaultInterval          = 5 * time.Minute
	defaultHeartbeatInterval = 30 * time.Second
	defaultLeaseDuration     = 2 * time.Minute
	defaultPolicyScope       = "global"
)

var ErrLeaseLost = errors.New("scanner alert evaluator schedule lease lost")

type Store interface {
	scannerrelease.PolicyRepository
	scannerrelease.AlertRepository
	scannerrelease.ScheduleLeaseRepository
}

type Config struct {
	Store             Store
	WorkerID          string
	PolicyScope       string
	Interval          time.Duration
	HeartbeatInterval time.Duration
	LeaseDuration     time.Duration
	Once              bool
	Now               func() time.Time
	Sleep             func(context.Context, time.Duration) error
	Observer          scannerobservability.Observer
}

type Worker struct {
	config Config
}

func New(config Config) (*Worker, error) {
	switch {
	case config.Store == nil:
		return nil, errors.New("scanner alert store is required")
	case strings.TrimSpace(config.WorkerID) == "":
		return nil, errors.New("scanner alert worker ID is required")
	}
	if config.PolicyScope == "" {
		config.PolicyScope = defaultPolicyScope
	}
	if config.Interval == 0 {
		config.Interval = defaultInterval
	}
	if config.HeartbeatInterval == 0 {
		config.HeartbeatInterval = defaultHeartbeatInterval
	}
	if config.LeaseDuration == 0 {
		config.LeaseDuration = defaultLeaseDuration
	}
	if config.Now == nil {
		config.Now = func() time.Time { return time.Now().UTC() }
	}
	if config.Sleep == nil {
		config.Sleep = sleepContext
	}
	switch {
	case strings.TrimSpace(config.PolicyScope) == "":
		return nil, errors.New("scanner alert policy scope is required")
	case config.Interval <= 0:
		return nil, errors.New("scanner alert interval must be positive")
	case config.HeartbeatInterval <= 0:
		return nil, errors.New("scanner alert heartbeat interval must be positive")
	case config.LeaseDuration <= config.HeartbeatInterval*2:
		return nil, errors.New("scanner alert lease duration must exceed two heartbeat intervals")
	}
	return &Worker{config: config}, nil
}

func (w *Worker) Run(ctx context.Context) error {
	for {
		_, err := w.RunOnce(ctx)
		if err != nil && !errors.Is(err, ErrLeaseLost) {
			return err
		}
		if w.config.Once {
			return err
		}
		if err := w.config.Sleep(ctx, w.config.Interval); err != nil {
			return err
		}
	}
}

// RunOnce evaluates one logical interval. A completed period is immutable;
// another replica can only reclaim an active period after its lease expires.
func (w *Worker) RunOnce(ctx context.Context) (bool, error) {
	started := w.now()
	runResult := "success"
	defer func() {
		if w.config.Observer != nil {
			w.config.Observer.ObserveRun(
				scannerobservability.ComponentAlert,
				runResult,
				w.now().Sub(started),
			)
		}
	}()
	w.setState("active")
	period := started.Truncate(w.config.Interval).Format(time.RFC3339Nano)
	scheduleKey := "scanner-release-alert-evaluator/" +
		strings.TrimSpace(w.config.PolicyScope)
	lease, acquired, err := w.config.Store.AcquireScheduleLease(
		ctx, scheduleKey, period, w.config.WorkerID,
		started, started.Add(w.config.LeaseDuration),
	)
	if err != nil {
		runResult = "error"
		w.observeClaim("error")
		w.setState("degraded")
		return false, fmt.Errorf("acquire scanner alert evaluator lease: %w", err)
	}
	if !acquired {
		w.observeClaim("contended")
		w.setState("idle")
		return false, nil
	}
	w.observeClaim("acquired")
	if lease.Version > 1 {
		w.observeLease("reclaimed")
		w.setStuck("expired_lease", 1)
	}

	runContext, cancelRun := context.WithCancelCause(ctx)
	stopHeartbeat := make(chan struct{})
	heartbeatDone := make(chan struct{})
	go w.monitorLease(
		runContext, lease, cancelRun, stopHeartbeat, heartbeatDone,
	)

	resultRef, evaluateErr := w.evaluate(runContext)
	close(stopHeartbeat)
	<-heartbeatDone
	cause := context.Cause(runContext)
	cancelRun(nil)
	if errors.Is(cause, ErrLeaseLost) {
		runResult = "error"
		w.observeLease("lost")
		w.setStuck("lease_lost", 1)
		w.setState("degraded")
		return true, ErrLeaseLost
	}
	if cause != nil {
		runResult = "error"
		w.observeLease("error")
		w.setState("degraded")
		return true, fmt.Errorf("scanner alert lease monitor: %w", cause)
	}
	if evaluateErr != nil {
		runResult = "error"
		w.observeResult("failed")
		w.setState("degraded")
		_ = w.completeLease(
			context.WithoutCancel(ctx), lease, scannerrelease.LeaseFailed,
			boundedResult("error:"+evaluateErr.Error()),
		)
		return true, evaluateErr
	}
	if err := w.completeLease(
		ctx, lease, scannerrelease.LeaseCompleted, resultRef,
	); err != nil {
		runResult = "error"
		w.setState("degraded")
		return true, err
	}
	w.observeLease("completed")
	w.observeResult("completed")
	w.setStuck("expired_lease", 0)
	w.setStuck("lease_lost", 0)
	w.setState("active")
	return true, nil
}

func (w *Worker) evaluate(ctx context.Context) (string, error) {
	policies, err := w.config.Store.ListPolicies(
		ctx, w.config.PolicyScope, true,
	)
	if err != nil {
		return "", fmt.Errorf("list scanner alert policies: %w", err)
	}
	if len(policies) == 0 {
		if err := w.refreshCounts(ctx); err != nil {
			return "", err
		}
		return `{"status":"no_enabled_policy"}`, nil
	}
	latest := policies[0]
	for _, policy := range policies[1:] {
		if policy.Revision > latest.Revision {
			latest = policy
		}
	}
	var rules scannerpolicy.Policy
	decoder := json.NewDecoder(strings.NewReader(latest.RulesJSON))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&rules); err != nil {
		return "", fmt.Errorf("decode scanner alert policy %q: %w", latest.ID, err)
	}
	if err := rules.Normalize(); err != nil {
		return "", fmt.Errorf("validate scanner alert policy %q: %w", latest.ID, err)
	}
	summary, err := w.config.Store.EvaluateAlerts(
		ctx,
		scannerrelease.AlertEvaluationRequest{
			PolicyID:       latest.ID,
			PolicyScope:    w.config.PolicyScope,
			PolicyRevision: latest.Revision,
			MissedDiscovery: scannerrelease.AlertDurationThreshold{
				Enabled: rules.Alerts.MissedDiscovery.Enabled,
				After:   rules.Alerts.MissedDiscovery.After,
			},
			StaleStableRelease: scannerrelease.AlertDurationThreshold{
				Enabled: rules.Alerts.StaleStableRelease.Enabled,
				After:   rules.Alerts.StaleStableRelease.After,
			},
			QueueBacklog: scannerrelease.AlertQueueThreshold{
				Enabled:  rules.Alerts.QueueBacklog.Enabled,
				MaxDepth: rules.Alerts.QueueBacklog.MaxDepth,
				MaxAge:   rules.Alerts.QueueBacklog.MaxAge,
			},
			LeaseChurn: scannerrelease.AlertCountThreshold{
				Enabled: rules.Alerts.LeaseChurn.Enabled,
				Count:   rules.Alerts.LeaseChurn.Count,
				Window:  rules.Alerts.LeaseChurn.Window,
			},
			RepeatedGateFailure: scannerrelease.AlertCountThreshold{
				Enabled: rules.Alerts.RepeatedGateFailure.Enabled,
				Count:   rules.Alerts.RepeatedGateFailure.Count,
				Window:  rules.Alerts.RepeatedGateFailure.Window,
			},
			MirrorDrift:     rules.Alerts.MirrorDrift.Enabled,
			RolloutFailure:  rules.Alerts.RolloutFailure.Enabled,
			SignatureHealth: rules.Alerts.SignatureHealth.Enabled,
		},
		w.now(),
	)
	if err != nil {
		return "", fmt.Errorf("evaluate scanner release alerts: %w", err)
	}
	w.setAlertCount("warning", summary.Active.OpenWarning)
	w.setAlertCount("critical", summary.Active.OpenCritical)
	encoded, _ := json.Marshal(summary)
	return boundedResult(string(encoded)), nil
}

func (w *Worker) refreshCounts(ctx context.Context) error {
	counts, err := w.config.Store.AlertCounts(ctx)
	if err != nil {
		return fmt.Errorf("load scanner release alert counts: %w", err)
	}
	w.setAlertCount("warning", counts.OpenWarning)
	w.setAlertCount("critical", counts.OpenCritical)
	return nil
}

func (w *Worker) completeLease(
	ctx context.Context,
	lease *scannerrelease.ScheduleLease,
	state scannerrelease.LeaseState,
	resultRef string,
) error {
	ok, err := w.config.Store.CompleteScheduleLease(
		ctx, lease.ScheduleKey, lease.PeriodKey, w.config.WorkerID,
		lease.Token, state, boundedResult(resultRef), w.now(),
	)
	if err != nil {
		w.observeLease("error")
		return fmt.Errorf("complete scanner alert evaluator lease: %w", err)
	}
	if !ok {
		w.observeLease("lost")
		w.setStuck("lease_lost", 1)
		return ErrLeaseLost
	}
	return nil
}

func (w *Worker) monitorLease(
	ctx context.Context,
	lease *scannerrelease.ScheduleLease,
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
			now := w.now()
			ok, err := w.config.Store.HeartbeatScheduleLease(
				ctx, lease.ScheduleKey, lease.PeriodKey, w.config.WorkerID,
				lease.Token, now, now.Add(w.config.LeaseDuration),
			)
			if err != nil {
				w.observeLease("error")
				cancel(err)
				return
			}
			if !ok {
				w.observeLease("lost")
				cancel(ErrLeaseLost)
				return
			}
			w.observeLease("heartbeat")
		}
	}
}

func (w *Worker) now() time.Time {
	return w.config.Now().UTC()
}

func (w *Worker) observeClaim(result string) {
	if w.config.Observer != nil {
		w.config.Observer.ObserveClaim(scannerobservability.ComponentAlert, result)
	}
}

func (w *Worker) observeLease(result string) {
	if w.config.Observer != nil {
		w.config.Observer.ObserveLease(scannerobservability.ComponentAlert, result)
	}
}

func (w *Worker) observeResult(result string) {
	if w.config.Observer != nil {
		w.config.Observer.ObserveResult(scannerobservability.ComponentAlert, result)
	}
}

func (w *Worker) setState(state string) {
	if w.config.Observer != nil {
		w.config.Observer.SetState(scannerobservability.ComponentAlert, state)
	}
}

func (w *Worker) setStuck(kind string, count int) {
	if w.config.Observer != nil {
		w.config.Observer.SetStuckWork(scannerobservability.ComponentAlert, kind, count)
	}
}

func (w *Worker) setAlertCount(severity string, count int) {
	if observer, ok := w.config.Observer.(interface {
		SetAlertCount(string, int)
	}); ok {
		observer.SetAlertCount(severity, count)
	}
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

func boundedResult(value string) string {
	const maxBytes = 2 << 10
	value = strings.TrimSpace(value)
	if len(value) <= maxBytes {
		return value
	}
	return value[:maxBytes]
}
