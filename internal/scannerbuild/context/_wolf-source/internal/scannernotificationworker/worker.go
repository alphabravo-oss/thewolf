// Package scannernotificationworker delivers the durable scanner-release
// notification outbox without coupling domain transitions to external systems.
package scannernotificationworker

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alphabravocompany/thewolf/internal/scannerdiscovery"
	"github.com/alphabravocompany/thewolf/internal/scannernotification"
	"github.com/alphabravocompany/thewolf/internal/scannerobservability"
	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

const (
	defaultPollInterval      = 2 * time.Second
	defaultHeartbeatInterval = 10 * time.Second
	defaultLeaseDuration     = 45 * time.Second
	defaultDeliveryTimeout   = 30 * time.Second
	defaultDrainTimeout      = time.Minute
	defaultBaseBackoff       = 15 * time.Second
	defaultMaxBackoff        = 30 * time.Minute
)

var (
	ErrLeaseLost     = errors.New("scanner notification delivery lease lost")
	errDrainDeadline = errors.New("scanner notification worker drain deadline exceeded")
)

type Worker struct {
	config Config
}

func New(config Config) (*Worker, error) {
	switch {
	case config.Store == nil:
		return nil, errors.New("scanner notification store is required")
	case config.Dispatcher == nil:
		return nil, errors.New("scanner notification dispatcher is required")
	case strings.TrimSpace(config.WorkerID) == "":
		return nil, errors.New("scanner notification worker ID is required")
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
	if config.DeliveryTimeout == 0 {
		config.DeliveryTimeout = defaultDeliveryTimeout
	}
	if config.DrainTimeout == 0 {
		config.DrainTimeout = defaultDrainTimeout
	}
	if config.BaseBackoff == 0 {
		config.BaseBackoff = defaultBaseBackoff
	}
	if config.MaxBackoff == 0 {
		config.MaxBackoff = defaultMaxBackoff
	}
	if config.Now == nil {
		config.Now = func() time.Time { return time.Now().UTC() }
	}
	if config.Sleep == nil {
		config.Sleep = sleepContext
	}
	switch {
	case config.PollInterval <= 0:
		return nil, errors.New("scanner notification poll interval must be positive")
	case config.HeartbeatInterval <= 0:
		return nil, errors.New("scanner notification heartbeat interval must be positive")
	case config.LeaseDuration <= config.HeartbeatInterval*2:
		return nil, errors.New("scanner notification lease duration must exceed two heartbeat intervals")
	case config.DeliveryTimeout <= 0:
		return nil, errors.New("scanner notification delivery timeout must be positive")
	case config.DrainTimeout <= 0:
		return nil, errors.New("scanner notification drain timeout must be positive")
	case config.BaseBackoff <= 0 || config.MaxBackoff < config.BaseBackoff:
		return nil, errors.New("scanner notification backoff range is invalid")
	}
	return &Worker{config: config}, nil
}

func (w *Worker) Run(ctx context.Context) error {
	for {
		processed, err := w.RunOnce(ctx)
		if err != nil && !errors.Is(err, ErrLeaseLost) {
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
				scannerobservability.ComponentNotification,
				runResult,
				w.now().Sub(started),
			)
		}
	}()
	now := w.now()
	reclaimed, err := w.config.Store.ReclaimStaleNotifications(ctx, now)
	if err != nil {
		runResult = "error"
		w.setState("degraded")
		return false, fmt.Errorf("reclaim stale scanner notifications: %w", err)
	}
	for index := 0; index < reclaimed.Retried; index++ {
		w.observeLease("reclaimed")
		w.observeRetry("worker_lost")
	}
	for index := 0; index < reclaimed.DeadLettered; index++ {
		w.observeLease("reclaimed")
		w.observeResult("dead_letter")
	}
	if err := w.refreshQueueDepth(ctx); err != nil {
		runResult = "error"
		w.setState("degraded")
		return false, err
	}
	notification, err := w.config.Store.ClaimNextNotification(
		ctx, w.config.WorkerID, now, now.Add(w.config.LeaseDuration),
	)
	if err != nil {
		runResult = "error"
		w.observeClaim("error")
		w.setState("degraded")
		return false, fmt.Errorf("claim scanner notification: %w", err)
	}
	if notification == nil {
		w.observeClaim("empty")
		w.setState("idle")
		return false, nil
	}
	w.observeClaim("acquired")
	w.setState("busy")
	outcome, err := w.runClaimWithDrain(ctx, notification)
	if err != nil {
		runResult = "error"
		w.setState("degraded")
		if errors.Is(err, ErrLeaseLost) {
			w.setStuck("lease_lost", 1)
		}
		return true, err
	}
	w.setState("active")
	w.setStuck("lease_lost", 0)
	w.observeResult(string(outcome))
	if outcome == scannerrelease.NotificationRetry {
		w.observeRetry("delivery_failure")
	}
	if err := w.refreshQueueDepth(context.WithoutCancel(ctx)); err != nil {
		runResult = "error"
		w.setState("degraded")
		return true, err
	}
	return true, nil
}

func (w *Worker) runClaimWithDrain(
	parent context.Context,
	notification *scannerrelease.Notification,
) (scannerrelease.NotificationState, error) {
	workContext, cancelWork := context.WithCancelCause(context.WithoutCancel(parent))
	defer cancelWork(nil)
	type result struct {
		state scannerrelease.NotificationState
		err   error
	}
	done := make(chan result, 1)
	go func() {
		state, err := w.processClaim(workContext, notification)
		done <- result{state: state, err: err}
	}()
	select {
	case completed := <-done:
		return completed.state, completed.err
	case <-parent.Done():
		timer := time.NewTimer(w.config.DrainTimeout)
		defer timer.Stop()
		select {
		case completed := <-done:
			return completed.state, completed.err
		case <-timer.C:
			// The claim is intentionally left in delivering state. Its lease is
			// the durable hand-off mechanism: another worker will reclaim it
			// after expiry, using the notification ID as the same idempotency key.
			cancelWork(errDrainDeadline)
			return "", parent.Err()
		}
	}
}

func (w *Worker) processClaim(
	ctx context.Context,
	notification *scannerrelease.Notification,
) (scannerrelease.NotificationState, error) {
	claimContext, cancelClaim := context.WithCancelCause(ctx)
	defer cancelClaim(nil)
	stopHeartbeat := make(chan struct{})
	heartbeatDone := make(chan struct{})
	go w.monitorLease(claimContext, notification, cancelClaim, stopHeartbeat, heartbeatDone)

	payload := json.RawMessage(notification.PayloadJSON)
	deliveryContext, cancelDelivery := context.WithTimeout(
		claimContext, w.config.DeliveryTimeout,
	)
	deliveryErr := w.config.Dispatcher.Deliver(deliveryContext, scannernotification.Delivery{
		NotificationID:   notification.ID,
		IdempotencyKey:   notification.ID,
		NotificationType: notification.NotificationType,
		Destination:      notification.DestinationType,
		DestinationRef:   notification.DestinationRef,
		Attempt:          notification.Attempt,
		Payload:          payload,
	})
	cancelDelivery()
	close(stopHeartbeat)
	<-heartbeatDone
	cause := context.Cause(claimContext)
	cancelClaim(nil)
	if errors.Is(cause, ErrLeaseLost) {
		return "", ErrLeaseLost
	}
	if cause != nil {
		// A heartbeat storage failure or drain deadline makes lease ownership
		// uncertain. Never finalize in that state; expiry/reclamation is safer.
		return "", fmt.Errorf("scanner notification lease monitor: %w", cause)
	}
	target := scannerrelease.NotificationDelivered
	availableAt := w.now()
	errorClass := ""
	errorDetail := ""
	if deliveryErr != nil {
		retryable := false
		errorClass, retryable = scannernotification.Classify(deliveryErr)
		errorDetail = scannerdiscovery.RedactText(deliveryErr.Error())
		if retryable && notification.Attempt < notification.MaxAttempts {
			target = scannerrelease.NotificationRetry
			availableAt = availableAt.Add(notificationBackoff(
				notification.ID, notification.Attempt,
				w.config.BaseBackoff, w.config.MaxBackoff,
			))
		} else {
			target = scannerrelease.NotificationDeadLetter
		}
	}
	updated, err := w.config.Store.FinalizeNotification(
		context.WithoutCancel(ctx), notification.ID, w.config.WorkerID,
		notification.LeaseToken, target, availableAt, errorClass, errorDetail, w.now(),
	)
	if errors.Is(err, scannerrelease.ErrLeaseNotOwned) {
		w.observeLease("lost")
		return "", ErrLeaseLost
	}
	if err != nil {
		return "", fmt.Errorf("finalize scanner notification: %w", err)
	}
	w.observeLease("completed")
	return updated.State, nil
}

func (w *Worker) monitorLease(
	ctx context.Context,
	notification *scannerrelease.Notification,
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
			status, err := w.config.Store.HeartbeatNotification(
				ctx, notification.ID, w.config.WorkerID, notification.LeaseToken,
				now, now.Add(w.config.LeaseDuration),
			)
			switch {
			case err != nil:
				w.observeLease("error")
				cancel(err)
				return
			case !status.Current:
				w.observeLease("lost")
				cancel(ErrLeaseLost)
				return
			default:
				w.observeLease("heartbeat")
			}
		}
	}
}

func notificationBackoff(
	identifier string,
	attempt int,
	base, maximum time.Duration,
) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	exponent := attempt - 1
	if exponent > 30 {
		exponent = 30
	}
	delay := base
	for index := 0; index < exponent && delay < maximum; index++ {
		if delay > maximum/2 {
			delay = maximum
			break
		}
		delay *= 2
	}
	if delay > maximum {
		delay = maximum
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s/%d", identifier, attempt)))
	// Stable jitter in [0.75, 1.25). It avoids a retry herd while making
	// qualification tests and incident reconstruction deterministic.
	fraction := float64(binary.BigEndian.Uint64(sum[:8])) / float64(^uint64(0))
	jittered := time.Duration(float64(delay) * (0.75 + fraction*0.5))
	if jittered > maximum {
		return maximum
	}
	return jittered
}

func (w *Worker) refreshQueueDepth(ctx context.Context) error {
	counts, err := w.config.Store.NotificationQueueCounts(ctx)
	if err != nil {
		return fmt.Errorf("count scanner notification queue: %w", err)
	}
	if w.config.Observer != nil {
		w.config.Observer.SetQueueDepth(
			scannerobservability.ComponentNotification, "pending", counts.Pending,
		)
		w.config.Observer.SetQueueDepth(
			scannerobservability.ComponentNotification, "delivering", counts.Delivering,
		)
		w.config.Observer.SetQueueDepth(
			scannerobservability.ComponentNotification, "retry", counts.Retry,
		)
		w.config.Observer.SetQueueDepth(
			scannerobservability.ComponentNotification, "delivered", counts.Delivered,
		)
		w.config.Observer.SetQueueDepth(
			scannerobservability.ComponentNotification, "dead_letter", counts.DeadLetter,
		)
	}
	return nil
}

func (w *Worker) now() time.Time {
	return w.config.Now().UTC()
}

func (w *Worker) observeClaim(result string) {
	if w.config.Observer != nil {
		w.config.Observer.ObserveClaim(scannerobservability.ComponentNotification, result)
	}
}

func (w *Worker) observeLease(result string) {
	if w.config.Observer != nil {
		w.config.Observer.ObserveLease(scannerobservability.ComponentNotification, result)
	}
}

func (w *Worker) observeRetry(reason string) {
	if w.config.Observer != nil {
		w.config.Observer.ObserveRetry(scannerobservability.ComponentNotification, reason)
	}
}

func (w *Worker) observeResult(state string) {
	if w.config.Observer != nil {
		if state == string(scannerrelease.NotificationDelivered) {
			state = "completed"
		}
		w.config.Observer.ObserveResult(scannerobservability.ComponentNotification, state)
	}
}

func (w *Worker) setState(state string) {
	if w.config.Observer != nil {
		w.config.Observer.SetState(scannerobservability.ComponentNotification, state)
	}
}

func (w *Worker) setStuck(kind string, count int) {
	if w.config.Observer != nil {
		w.config.Observer.SetStuckWork(scannerobservability.ComponentNotification, kind, count)
	}
}
