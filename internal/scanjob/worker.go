// Package scanjob implements the durable scan queue worker. The API only
// creates pending scan rows; one or more workers atomically claim those rows,
// maintain leases, and invoke the shared scan executor.
package scanjob

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/wolflog"
)

const (
	defaultPollInterval = 2 * time.Second
	defaultHeartbeat    = 10 * time.Second
	defaultLease        = 45 * time.Second
)

type Executor func(context.Context, *models.Scan) error

type Config struct {
	Store        db.Store
	Executor     Executor
	WorkerID     string
	Backend      string
	Capacity     int
	Once         bool
	PollInterval time.Duration
	Heartbeat    time.Duration
	Lease        time.Duration
	Version      string
	Capabilities string
	// CleanupWorkspace removes validated isolated source workspaces left by a
	// worker that lost its lease or exited unexpectedly.
	CleanupWorkspace func(string) error
}

type Worker struct {
	cfg       Config
	active    atomic.Int64
	claimOnce sync.Once
}

func New(cfg Config) (*Worker, error) {
	if cfg.Store == nil {
		return nil, errors.New("scan worker: store is required")
	}
	if cfg.Executor == nil {
		return nil, errors.New("scan worker: executor is required")
	}
	if cfg.WorkerID == "" {
		cfg.WorkerID = defaultWorkerID()
	}
	if cfg.Backend == "" {
		cfg.Backend = "docker"
	}
	if cfg.Capacity <= 0 {
		cfg.Capacity = 1
	}
	if cfg.Once {
		cfg.Capacity = 1
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = defaultPollInterval
	}
	if cfg.Heartbeat <= 0 {
		cfg.Heartbeat = defaultHeartbeat
	}
	if cfg.Lease <= cfg.Heartbeat*2 {
		cfg.Lease = defaultLease
	}
	if cfg.Capabilities == "" {
		cfg.Capabilities = "{}"
	}
	return &Worker{cfg: cfg}, nil
}

func (w *Worker) Run(ctx context.Context) error {
	log := wolflog.Component("scan-worker").With().
		Str("worker_id", w.cfg.WorkerID).
		Str("backend", w.cfg.Backend).
		Int("capacity", w.cfg.Capacity).
		Logger()
	log.Info().Bool("once", w.cfg.Once).Msg("scan worker started")

	workerDone := make(chan struct{})
	heartbeatStopped := make(chan struct{})
	go func() {
		defer close(heartbeatStopped)
		w.workerHeartbeat(ctx, workerDone)
	}()
	defer func() {
		close(workerDone)
		<-heartbeatStopped
		// The run context is normally cancelled during shutdown, so use a
		// bounded independent context to make the final status observable.
		stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		w.writeWorkerStatus(stopCtx, "stopped")
	}()
	w.cleanupReclaimedWorkspaces(ctx)

	var wg sync.WaitGroup
	errs := make(chan error, w.cfg.Capacity)
	for slot := 0; slot < w.cfg.Capacity; slot++ {
		wg.Add(1)
		go func(slot int) {
			defer wg.Done()
			if err := w.claimLoop(ctx, slot); err != nil && !errors.Is(err, context.Canceled) {
				errs <- err
			}
		}(slot)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			return err
		}
	}
	return ctx.Err()
}

func (w *Worker) claimLoop(ctx context.Context, slot int) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		reclaimed, reclaimErr := w.cfg.Store.ReclaimStaleScans(ctx, time.Now().UTC())
		if reclaimErr != nil {
			wolflog.Warn().Err(reclaimErr).Msg("scan worker stale reclaim failed")
		} else if reclaimed > 0 {
			w.cleanupReclaimedWorkspaces(ctx)
		}
		scan, err := w.cfg.Store.ClaimNextScan(
			ctx,
			w.cfg.WorkerID,
			w.cfg.Backend,
			time.Now().UTC().Add(w.cfg.Lease),
		)
		if err != nil {
			if w.cfg.Once {
				return err
			}
			if !sleepContext(ctx, w.cfg.PollInterval) {
				return ctx.Err()
			}
			continue
		}
		if scan == nil {
			if w.cfg.Once {
				return nil
			}
			if !sleepContext(ctx, w.cfg.PollInterval) {
				return ctx.Err()
			}
			continue
		}

		w.active.Add(1)
		w.process(ctx, slot, scan)
		w.active.Add(-1)
		if w.cfg.Once {
			return nil
		}
	}
}

func (w *Worker) cleanupReclaimedWorkspaces(ctx context.Context) {
	if w.cfg.CleanupWorkspace == nil {
		return
	}
	scans, err := w.cfg.Store.ListAllScans(ctx)
	if err != nil {
		wolflog.Warn().Err(err).Msg("scan worker could not list stale workspaces")
		return
	}
	for i := range scans {
		scan := &scans[i]
		if scan.PreparedWorkspace == "" || scan.FailureCode != "worker_lost" ||
			(scan.Status != models.ScanStatusPending && scan.Status != models.ScanStatusFailed) {
			continue
		}
		if err := w.cfg.CleanupWorkspace(scan.PreparedWorkspace); err != nil {
			wolflog.Warn().Err(err).Str("scan_id", scan.ID).
				Str("workspace", scan.PreparedWorkspace).
				Msg("scan worker stale workspace cleanup rejected")
		}
	}
}

func (w *Worker) process(parent context.Context, slot int, scan *models.Scan) {
	log := wolflog.Component("scan-worker").With().
		Str("worker_id", w.cfg.WorkerID).
		Int("slot", slot).
		Str("scan_id", scan.ID).
		Int("attempt", scan.Attempt).
		Logger()
	log.Info().Msg("claimed scan")

	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	heartbeatDone := make(chan struct{})
	go w.scanHeartbeat(ctx, cancel, heartbeatDone, scan)

	err := w.cfg.Executor(ctx, scan)
	close(heartbeatDone)
	if err == nil {
		log.Info().Msg("scan executor returned")
		return
	}
	log.Error().Err(err).Msg("scan executor failed")
	current, getErr := w.cfg.Store.GetScanByID(context.Background(), scan.ID)
	if getErr != nil || current.LeaseToken != scan.LeaseToken || isTerminal(current.Status) {
		return
	}
	now := time.Now().UTC()
	current.Status = models.ScanStatusFailed
	current.Phase = "failed"
	current.FailureCode = "executor_error"
	current.FailureMessage = truncate(err.Error(), 500)
	current.CompletedAt = &now
	_, _ = w.cfg.Store.FinalizeScan(context.Background(), current, scan.LeaseToken)
}

func (w *Worker) scanHeartbeat(ctx context.Context, cancel context.CancelFunc, done <-chan struct{}, scan *models.Scan) {
	ticker := time.NewTicker(w.cfg.Heartbeat)
	defer ticker.Stop()
	lastSuccess := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-ticker.C:
			ok, err := w.cfg.Store.HeartbeatScanLease(
				ctx, scan.ID, scan.LeaseToken, time.Now().UTC().Add(w.cfg.Lease),
			)
			if err != nil {
				wolflog.Warn().Err(err).Str("scan_id", scan.ID).Msg("scan lease heartbeat failed")
				// Continuing past the last known lease expiry can create two
				// active executors after another worker reclaims the scan.
				// Stop this executor before that ownership window elapses.
				if time.Since(lastSuccess) >= w.cfg.Lease-w.cfg.Heartbeat {
					wolflog.Error().Str("scan_id", scan.ID).Msg("scan lease could not be renewed; cancelling stale executor")
					cancel()
					return
				}
				continue
			}
			if !ok {
				cancel()
				return
			}
			lastSuccess = time.Now()
		}
	}
}

func (w *Worker) workerHeartbeat(ctx context.Context, done <-chan struct{}) {
	ticker := time.NewTicker(w.cfg.Heartbeat)
	defer ticker.Stop()
	w.writeWorkerStatus(ctx, "ready")
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			w.writeWorkerStatus(ctx, "stopping")
			return
		case <-ticker.C:
			status := "ready"
			if w.active.Load() >= int64(w.cfg.Capacity) {
				status = "busy"
			}
			w.writeWorkerStatus(ctx, status)
		}
	}
}

func (w *Worker) writeWorkerStatus(ctx context.Context, status string) {
	_ = w.cfg.Store.UpsertScanWorker(ctx, &models.ScanWorker{
		ID:               w.cfg.WorkerID,
		Backend:          w.cfg.Backend,
		Status:           status,
		Capacity:         w.cfg.Capacity,
		ActiveScans:      int(w.active.Load()),
		Version:          w.cfg.Version,
		CapabilitiesJSON: w.cfg.Capabilities,
	})
}

func defaultWorkerID() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "scan-worker"
	}
	return fmt.Sprintf("%s-%d", host, os.Getpid())
}

func isTerminal(status models.ScanStatus) bool {
	return status == models.ScanStatusCompleted ||
		status == models.ScanStatusFailed ||
		status == models.ScanStatusCancelled
}

func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max] + "…"
}

func sleepContext(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
