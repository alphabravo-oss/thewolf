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
	"github.com/alphabravocompany/thewolf/internal/fix"
	fixauth "github.com/alphabravocompany/thewolf/internal/fix/auth"
	"github.com/alphabravocompany/thewolf/internal/fix/console"
	"github.com/alphabravocompany/thewolf/internal/fix/engine"
	"github.com/alphabravocompany/thewolf/internal/fix/fixstore"
	"github.com/alphabravocompany/thewolf/internal/fix/install"
	"github.com/alphabravocompany/thewolf/internal/fix/lineage"
	"github.com/alphabravocompany/thewolf/internal/fix/orchestrator"
	"github.com/alphabravocompany/thewolf/internal/fix/workspace"
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
	if cfg.Heartbeat >= cfg.StaleAfter {
		return nil, errors.New("worker: heartbeat must be shorter than the stale lease")
	}
	return &Worker{cfg: cfg}, nil
}

// Run drives the loop until the context is cancelled (or, in --once mode, until
// one job is processed / the queue is found empty). It never returns an error
// for a per-job failure — those are recorded on the job — only for a fatal
// loop-level condition.
func (w *Worker) Run(ctx context.Context) error {
	install.EnsureLocalBinOnPATH()
	log := wolflog.L().With().Str("component", "fixer").Str("worker", w.cfg.WorkerID).Logger()
	log.Info().Bool("once", w.cfg.Once).Msg("fix worker started")
	if w.cfg.Fixstore != nil {
		engines := fixauth.ProbeAll(ctx, fixauth.Resolve(ctx, w.cfg.Store, ""))
		if err := fixauth.WriteStatus(w.cfg.Fixstore.Root(), engines); err != nil {
			log.Warn().Err(err).Msg("could not write fixer engine status")
		}
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Requeue stale jobs and close expired interactive sessions. A new
		// console cannot safely replay a lost terminal's queued keystrokes.
		if n, err := w.cfg.Store.ReclaimStaleJobs(ctx, time.Now().UTC().Add(-w.cfg.StaleAfter)); err != nil {
			log.Warn().Err(err).Msg("reclaim stale jobs failed")
		} else if n > 0 {
			log.Info().Int("reclaimed", n).Msg("requeued stale jobs")
		}
		if n, err := w.cfg.Store.ReclaimStaleConsoles(ctx, time.Now().UTC().Add(-w.cfg.StaleAfter)); err != nil {
			log.Warn().Err(err).Msg("reclaim stale consoles failed")
		} else if n > 0 {
			log.Info().Int("expired", n).Msg("closed stale consoles")
		}

		// Interactive login/shell waits on a human — claim those first.
		cons, err := w.cfg.Store.ClaimNextFixerConsole(ctx, w.cfg.WorkerID)
		if err != nil {
			log.Warn().Err(err).Msg("console claim failed")
			if w.cfg.Once {
				return err
			}
			if !sleepCtx(ctx, w.cfg.PollInterval) {
				return ctx.Err()
			}
			continue
		}
		if cons != nil {
			w.processConsole(ctx, &log, cons)
			if w.cfg.Once {
				log.Info().Str("console", cons.ID).Msg("--once: one console processed; exiting")
				return nil
			}
			continue
		}

		if err := fix.CheckEnabled(ctx, w.cfg.Store); err != nil {
			if w.cfg.Once {
				if errors.Is(err, fix.ErrDisabled) {
					return nil
				}
				return err
			}
			if !errors.Is(err, fix.ErrDisabled) {
				log.Warn().Err(err).Msg("autofix setting unavailable; queue remains paused")
			}
			if !sleepCtx(ctx, w.cfg.PollInterval) {
				return ctx.Err()
			}
			continue
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
		if _, err := w.completeCancellation(ctx, job); err != nil {
			log.Warn().Err(err).Str("job", job.ID).Msg("cancellation cleanup pending")
		}
		return
	}
	if job.ClaimedAt == nil || w.cfg.Store.HeartbeatFixJob(ctx, job.ID, job.ClaimedBy, *job.ClaimedAt) != nil {
		w.appendLog(job.ID, "claim is no longer active; skipping run")
		if _, err := w.completeCancellation(ctx, job); err != nil {
			log.Warn().Err(err).Str("job", job.ID).Msg("cancellation cleanup pending")
		}
		return
	}
	if err := fix.CheckEnabled(ctx, w.cfg.Store); err != nil {
		w.appendLog(job.ID, err.Error()+"; returning unstarted job to the queue")
		job.Status, job.ClaimedBy, job.ClaimedAt = models.FixJobQueued, "", nil
		done, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if err := w.cfg.Store.UpdateFixJob(done, job); err != nil {
			log.Warn().Err(err).Msg("could not release disabled fix job")
		}
		return
	}
	if err := lineage.SupersedeSiblings(ctx, w.cfg.Store, job); err != nil {
		log.Warn().Err(err).Str("job", job.ID).Msg("supersede siblings failed")
	}

	jobCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopHeartbeat := w.startHeartbeat(jobCtx, job, cancel)
	defer stopHeartbeat()

	deps := w.cfg.Deps
	deps.Store = w.cfg.Store
	deps.Diffs = w.cfg.Fixstore
	deps.CLIEnv = fixauth.Resolve(ctx, w.cfg.Store, job.UserID).Env()
	deps.Model = job.Model
	deps.Effort = job.Effort
	deps.Variant = job.Variant
	if w.cfg.Store != nil {
		if v, err := w.cfg.Store.GetSetting(ctx, engine.SettingPromptInitial); err == nil {
			deps.PromptInitial = v
		}
		if v, err := w.cfg.Store.GetSetting(ctx, engine.SettingPromptFollowup); err == nil {
			deps.PromptFollowup = v
		}
	}
	deps.Log = func(format string, args ...any) {
		w.appendLog(job.ID, fmt.Sprintf(format, args...))
	}

	w.appendLog(job.ID, fmt.Sprintf("worker %s claimed job %s", w.cfg.WorkerID, job.ID))
	_, runErr := RunOrchestrator(jobCtx, job, deps)
	stopHeartbeat()
	if cancelled, err := w.completeCancellation(ctx, job); cancelled {
		if err != nil {
			log.Warn().Err(err).Str("job", job.ID).Msg("cancellation cleanup pending")
			w.appendLog(job.ID, "cancellation cleanup pending: "+err.Error())
		}
		return
	}
	if err := runErr; err != nil {
		log.Warn().Err(err).Str("job", job.ID).Msg("job failed")
		w.appendLog(job.ID, "job failed: "+err.Error())
		done := context.WithoutCancel(ctx)
		// Keep our revision: adopting a freshly loaded row could fail a new
		// worker's claim or resurrect a cancellation after this run lost ownership.
		if !models.FixJobTerminal(job.Status) {
			now := time.Now().UTC()
			job.Status = models.FixJobFailed
			job.Error = err.Error()
			job.FinishedAt = &now
			if updateErr := w.cfg.Store.UpdateFixJob(done, job); updateErr != nil && !errors.Is(updateErr, db.ErrFixJobConflict) {
				log.Warn().Err(updateErr).Str("job", job.ID).Msg("persist failure failed")
			}
		}
		return
	}
	latest, err := w.cfg.Store.GetFixJobByID(context.WithoutCancel(ctx), job.ID)
	if err != nil || latest == nil || latest.Revision != job.Revision ||
		latest.ClaimedBy != job.ClaimedBy ||
		(!models.FixJobPaused(latest.Status) && latest.Status != models.FixJobSucceeded) {
		return
	}
	job = latest
	followCtx, release := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer release()
	if child, cerr := lineage.AfterAgentRun(followCtx, w.cfg.Store, job); cerr != nil {
		log.Warn().Err(cerr).Str("job", job.ID).Msg("fix follow-up failed")
		w.appendLog(job.ID, "fix follow-up failed: "+cerr.Error())
	} else if child != nil {
		w.appendLog(job.ID, fmt.Sprintf("child scan %s queued on %s", child.ID, child.Branch))
	} else if job.Status == models.FixJobSucceeded && job.Pushed && job.RemediationID != "" && job.WorkspacePath != "" {
		rem, err := w.cfg.Store.GetRemediationByID(followCtx, job.RemediationID)
		if err == nil {
			if rem == nil || rem.State != models.RemediationFrozen {
				err = db.ErrFixFrozen
			} else {
				err = w.cfg.Store.AcceptFixRemediation(followCtx, rem, workspace.AcceptRemediation)
			}
		}
		if err != nil {
			job.Error = "workspace cleanup failed; recovery path retained: " + err.Error()
			w.appendLog(job.ID, job.Error)
			writeCtx, cancelWrite := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			if saveErr := w.cfg.Store.UpdateFixJob(writeCtx, job); saveErr != nil {
				log.Warn().Err(saveErr).Str("job", job.ID).Msg("could not save cleanup error")
			}
			cancelWrite()
		}
	}
	log.Info().Str("job", job.ID).Str("status", job.Status).Msg("job complete")
	w.appendLog(job.ID, fmt.Sprintf("job %s finished with status %s", job.ID, job.Status))
}

// completeCancellation acknowledges that this claim's engine has stopped. A
// replacement claim's cancellation never authorizes an old worker to clean it.
func (w *Worker) completeCancellation(ctx context.Context, job *models.FixJob) (bool, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	latest, err := w.cfg.Store.GetFixJobByID(ctx, job.ID)
	if err != nil || latest == nil || latest.Status != models.FixJobCancelled {
		return false, err
	}
	if latest.PauseReason != models.FixPauseCancellationCleanup || latest.ClaimedBy != job.ClaimedBy ||
		latest.ClaimedAt == nil || job.ClaimedAt == nil || !latest.ClaimedAt.Equal(*job.ClaimedAt) {
		return true, nil
	}
	if latest.WorkspacePath == "" {
		latest.WorkspacePath = job.WorkspacePath
		latest.ResultBranch = job.ResultBranch
	}
	latest.ClaimedBy = ""
	if err := w.cfg.Store.UpdateFixJob(ctx, latest); err != nil {
		return true, err
	}
	if err := w.cfg.Store.CompleteFixCancellation(ctx, latest, workspace.DiscardFixJob); err != nil {
		latest.Error = "cancellation cleanup pending: " + err.Error()
		_ = w.cfg.Store.UpdateFixJob(ctx, latest)
		return true, err
	}
	return true, nil
}

// processConsole runs a claimed login/shell session on this worker. The
// process inherits HOME so OAuth files persist on the worker volume.
func (w *Worker) processConsole(ctx context.Context, log *zerolog.Logger, cons *models.FixerConsole) {
	log.Info().Str("console", cons.ID).Str("kind", cons.Kind).Str("engine", cons.Engine).Msg("claimed console")
	if cons.ClaimedAt == nil || w.cfg.Store.HeartbeatFixerConsole(ctx, cons.ID, cons.ClaimedBy, *cons.ClaimedAt) != nil {
		return
	}

	consCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopHeartbeat := w.startConsoleHeartbeat(consCtx, cons, cancel)
	defer stopHeartbeat()
	runErr := console.Run(consCtx, w.cfg.Store, w.cfg.Fixstore, cons)
	stopHeartbeat()
	done, release := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer release()
	if runErr != nil && !errors.Is(runErr, db.ErrFixerConsoleConflict) {
		log.Warn().Err(runErr).Str("console", cons.ID).Msg("console failed")
		// Keep the original claim identity; never borrow a replacement's claim.
		cons.Status = models.FixerConsoleExited
		cons.Error = runErr.Error()
		now := time.Now().UTC()
		cons.FinishedAt = &now
		if err := w.cfg.Store.UpdateFixerConsole(done, cons); err != nil && !errors.Is(err, db.ErrFixerConsoleConflict) {
			log.Warn().Err(err).Str("console", cons.ID).Msg("console result could not be saved")
		}
	}

	if w.cfg.Fixstore != nil {
		engines := fixauth.ProbeAll(done, fixauth.Resolve(done, w.cfg.Store, cons.UserID))
		if err := fixauth.WriteStatus(w.cfg.Fixstore.Root(), engines); err != nil {
			log.Warn().Err(err).Msg("could not refresh fixer engine status")
		}
	}
}

// startConsoleHeartbeat renews only the original claim and stops work on any
// renewal failure, including cancellation, expiry and replacement claims.
func (w *Worker) startConsoleHeartbeat(ctx context.Context, cons *models.FixerConsole, cancel context.CancelFunc) func() {
	id, worker := cons.ID, cons.ClaimedBy
	var claimedAt time.Time
	if cons.ClaimedAt != nil {
		claimedAt = *cons.ClaimedAt
	}
	heartbeatCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(w.cfg.Heartbeat)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				renewCtx, release := context.WithTimeout(heartbeatCtx, w.cfg.Heartbeat)
				err := w.cfg.Store.HeartbeatFixerConsole(renewCtx, id, worker, claimedAt)
				release()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()
	return func() { stop(); <-done }
}

// startHeartbeat renews only this claim. A failed renewal cancels engine work;
// it must never refresh a replacement worker's lease or overwrite a result.
func (w *Worker) startHeartbeat(ctx context.Context, job *models.FixJob, cancel context.CancelFunc) func() {
	// Copy immutable claim identity before the orchestrator starts mutating job.
	id, worker := job.ID, job.ClaimedBy
	var claimedAt time.Time
	if job.ClaimedAt != nil {
		claimedAt = *job.ClaimedAt
	}
	heartbeatCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(w.cfg.Heartbeat)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				renewCtx, release := context.WithTimeout(heartbeatCtx, w.cfg.Heartbeat)
				err := fix.CheckEnabled(renewCtx, w.cfg.Store)
				if err == nil {
					err = w.cfg.Store.HeartbeatFixJob(renewCtx, id, worker, claimedAt)
				}
				release()
				if err != nil {
					w.appendLog(id, "worker execution gate or lease failed; stopping run: "+err.Error())
					cancel()
					return
				}
			}
		}
	}()
	return func() { stop(); <-done }
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
