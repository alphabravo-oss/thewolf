// Package worker is the brain of the `wolf fixer` command: a stateless loop
// that atomically claims queued fix jobs, runs the orchestrator, streams the
// worker's logs to the durable fixstore (which the server relays over SSE),
// and persists the job's terminal status. It is deliberately separable from the
// server — one or many workers, a k8s Deployment or a Job-per-task — because
// the agent engines must run inside an engine container, not in the API process
// (design §4).
//
// Two reliability mechanics, both from the design's queue-correctness risk:
//
//   - Heartbeat: while a job runs, the worker periodically stamps
//     heartbeat_at so a crash is detectable.
//   - Stale reclaim: before claiming, the worker requeues jobs whose worker
//     stopped heartbeating past the lease, so a dead worker's job is retried.
//
// The orchestrator is invoked through the package-level RunOrchestrator
// indirection so tests can stub the whole fix run (no real agents, docker, or
// network) while still exercising the claim → run → stream → finalize loop.
package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/rs/zerolog"

	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/fix/fixstore"
	"github.com/alphabravocompany/thewolf/internal/fix/orchestrator"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/wolflog"
)

// Defaults for the worker's timing knobs. They are conservative: a job that
// stops heartbeating for staleAfter is presumed dead and reclaimed.
const (
	defaultPollInterval   = 2 * time.Second
	defaultHeartbeat      = 10 * time.Second
	defaultStaleAfter     = 2 * time.Minute
	defaultClaimEmptyWait = 2 * time.Second
)

// RunOrchestrator is the package-level seam the worker calls to execute a
// claimed job. Production points it at orchestrator.Run; tests replace it with
// a stub so the loop is exercised without any real fixing. It returns the
// orchestrator Result (or nil) and an error (a non-nil error means the job
// failed before producing a branch).
var RunOrchestrator = func(ctx context.Context, job *models.FixJob, deps orchestrator.Deps) (*orchestrator.Result, error) {
	return orchestrator.Run(ctx, job, deps)
}

// Config configures a Worker. Only Store and Fixstore are required; the rest
// default sensibly.
type Config struct {
	// Store is the durable job queue.
	Store db.Store
	// Fixstore is where the worker writes the streamed log + the diff artifact;
	// the server reads the same files to relay logs and serve the diff.
	Fixstore *fixstore.Store
	// WorkerID identifies this worker in claimed_by. Defaults to a hostname/pid
	// label when empty.
	WorkerID string
	// Once, when true, processes a single job (or exits immediately if the
	// queue is empty) — the k8s Job-per-task shape.
	Once bool

	PollInterval time.Duration
	Heartbeat    time.Duration
	StaleAfter   time.Duration

	// Deps lets the caller (or a test) supply the orchestrator's collaborators
	// (writability, workspace, engine, verifier). The worker always overrides
	// Deps.Store, Deps.Diffs, and Deps.Log so they point at this worker's store
	// and fixstore. A zero Deps is valid when RunOrchestrator is stubbed.
	Deps orchestrator.Deps
}

// Worker runs the claim → orchestrate → finalize loop.
type Worker struct {
	cfg Config
}

// New builds a Worker, applying defaults. It errors if the required
// dependencies are missing.
func New(cfg Config) (*Worker, error) {
	if cfg.Store == nil {
		return nil, errors.New("worker: store is required")
	}
	if cfg.Fixstore == nil {
		return nil, errors.New("worker: fixstore is required")
	}
	if cfg.WorkerID == "" {
		cfg.WorkerID = defaultWorkerID()
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = defaultPollInterval
	}
	if cfg.Heartbeat <= 0 {
		cfg.Heartbeat = defaultHeartbeat
	}
	if cfg.StaleAfter <= 0 {
		cfg.StaleAfter = defaultStaleAfter
	}
	return &Worker{cfg: cfg}, nil
}

// Run drives the loop until the context is cancelled (or, in --once mode, until
// one job is processed / the queue is found empty). It never returns an error
// for a per-job failure — those are recorded on the job — only for a fatal
// loop-level condition.
func (w *Worker) Run(ctx context.Context) error {
	log := wolflog.L().With().Str("component", "fixer").Str("worker", w.cfg.WorkerID).Logger()
	log.Info().Bool("once", w.cfg.Once).Msg("fix worker started")

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Requeue any jobs whose worker stopped heartbeating before we try to
		// claim, so a dead worker's job is retried rather than stranded.
		if n, err := w.cfg.Store.ReclaimStaleJobs(ctx, time.Now().UTC().Add(-w.cfg.StaleAfter)); err != nil {
			log.Warn().Err(err).Msg("reclaim stale jobs failed")
		} else if n > 0 {
			log.Info().Int("reclaimed", n).Msg("requeued stale jobs")
		}

		job, err := w.cfg.Store.ClaimNextFixJob(ctx, w.cfg.WorkerID)
		if err != nil {
			log.Warn().Err(err).Msg("claim failed")
			if w.cfg.Once {
				return err
			}
			if !sleepCtx(ctx, w.cfg.PollInterval) {
				return ctx.Err()
			}
			continue
		}
		if job == nil {
			// Queue empty.
			if w.cfg.Once {
				log.Info().Msg("queue empty; --once exiting")
				return nil
			}
			if !sleepCtx(ctx, defaultClaimEmptyWait) {
				return ctx.Err()
			}
			continue
		}

		w.process(ctx, &log, job)

		if w.cfg.Once {
			log.Info().Str("job", job.ID).Msg("--once: one job processed; exiting")
			return nil
		}
	}
}

// process runs one claimed job: it starts the heartbeat, invokes the
// orchestrator with a fixstore-backed log + diff sink, and lets the
// orchestrator persist the terminal status. A cancelled job (the user pressed
// the DELETE button) short-circuits to cancelled.
func (w *Worker) process(ctx context.Context, log *zerolog.Logger, job *models.FixJob) {
	log.Info().Str("job", job.ID).Str("repo", job.RepoID).Msg("claimed job")

	// Honor a pre-claim cancel.
	if job.Status == models.FixJobCancelled {
		w.appendLog(job.ID, "job was cancelled before it started")
		return
	}

	jobCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopHeartbeat := w.startHeartbeat(jobCtx, job)
	defer stopHeartbeat()

	deps := w.cfg.Deps
	deps.Store = w.cfg.Store
	deps.Diffs = w.cfg.Fixstore
	deps.Log = func(format string, args ...any) {
		w.appendLog(job.ID, fmt.Sprintf(format, args...))
	}

	w.appendLog(job.ID, fmt.Sprintf("worker %s claimed job %s", w.cfg.WorkerID, job.ID))
	if _, err := RunOrchestrator(jobCtx, job, deps); err != nil {
		log.Warn().Err(err).Str("job", job.ID).Msg("job failed")
		w.appendLog(job.ID, "job failed: "+err.Error())
		return
	}
	log.Info().Str("job", job.ID).Str("status", job.Status).Msg("job complete")
	w.appendLog(job.ID, fmt.Sprintf("job %s finished with status %s", job.ID, job.Status))
}

// startHeartbeat stamps heartbeat_at periodically while the job runs, returning
// a stop func. It also watches for a cancel (status flipped to cancelled via
// the DELETE endpoint) and cancels the job's context so the orchestrator winds
// down.
func (w *Worker) startHeartbeat(ctx context.Context, job *models.FixJob) func() {
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(w.cfg.Heartbeat)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-ticker.C:
				now := time.Now().UTC()
				job.HeartbeatAt = &now
				_ = w.cfg.Store.UpdateFixJob(ctx, job)
			}
		}
	}()
	return func() { close(done) }
}

// appendLog writes one progress line to the durable fixstore log, swallowing
// errors — a failed log write must not abort the job.
func (w *Worker) appendLog(jobID, line string) {
	_ = w.cfg.Fixstore.AppendLog(jobID, line)
}

// defaultWorkerID builds a stable-ish worker label from the hostname and pid so
// claimed_by is human-readable in the queue.
func defaultWorkerID() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "worker"
	}
	return fmt.Sprintf("%s-%d", host, os.Getpid())
}

// sleepCtx sleeps for d unless ctx is cancelled first. Returns false if the
// context ended.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
