package scannerrollout

import (
	"context"
	"errors"
	"time"

	"github.com/alphabravocompany/thewolf/internal/scannerobservability"
	"github.com/alphabravocompany/thewolf/internal/scannerpolicy"
	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

var (
	ErrRolloutLeaseLost = errors.New("scanner rollout lease lost")
	ErrRolloutChanged   = errors.New("scanner rollout changed during reconciliation")
)

const (
	CohortPending              = "pending"
	CohortAssigning            = "assigning"
	CohortObserving            = "observing"
	CohortHealthy              = "healthy"
	CohortCompleted            = "completed"
	CohortRollbackAssigning    = "rollback_assigning"
	CohortRollbackObserving    = "rollback_observing"
	CohortRolledBack           = "rolled_back"
	CohortReconciliationFailed = "failed"
)

// ControllerStore is the smallest durable contract required by the rollout
// reconciler. Runtime assignment remains behind a separate injectable seam.
type ControllerStore interface {
	scannerrelease.RolloutRepository
	scannerrelease.RolloutLeaseRepository
	scannerrelease.ReleaseRepository
}

type AssignmentRequest struct {
	OperationID       string
	RolloutID         string
	Target            string
	CohortID          string
	CohortName        string
	DesiredReleaseID  string
	PreviousReleaseID string
	Rollback          bool
}

type HealthRequest struct {
	OperationID           string
	RolloutID             string
	Target                string
	CohortID              string
	CohortName            string
	StableCohortName      string
	DesiredReleaseID      string
	SyntheticVerification bool
}

// HealthSnapshot is deliberately transport-neutral. Kubernetes, Compose, and
// test adapters can supply the same bounded aggregate without exposing worker
// credentials or raw logs to the controller.
type HealthSnapshot struct {
	ObservedReleaseID string
	TotalWorkers      int
	ReadyWorkers      int
	FailedWorkers     int
	Canary            CanaryHealth
	// Synthetic and RealScans remain separate even though Canary contains the
	// combined counters used by the existing rollback policy. Keeping the
	// evidence classes distinct prevents the API/UI from representing a fixed
	// fixture pass as sampled production-scan health.
	Synthetic  *SyntheticHealthEvidence
	RealScans  *RealScanHealthEvidence
	ObservedAt time.Time
}

func (s HealthSnapshot) Validate() error {
	if s.TotalWorkers < 0 || s.ReadyWorkers < 0 || s.FailedWorkers < 0 ||
		s.ReadyWorkers > s.TotalWorkers || s.FailedWorkers > s.TotalWorkers {
		return errors.New("invalid rollout worker health counts")
	}
	values := []int{
		s.Canary.Samples, s.Canary.InfrastructureFailures,
		s.Canary.StableSamples, s.Canary.StableInfrastructureFailures,
		s.Canary.ParserFailures, s.Canary.PullFailures,
		s.Canary.SignatureFailures, s.Canary.ManifestFailures,
		s.Canary.ExpectedFindingLosses, s.Canary.CrashLoops,
	}
	for _, value := range values {
		if value < 0 {
			return errors.New("rollout health counters must not be negative")
		}
	}
	if s.Canary.CandidateP95Duration < 0 || s.Canary.StableP95Duration < 0 {
		return errors.New("rollout health durations must not be negative")
	}
	if s.RealScans != nil {
		if err := s.RealScans.Validate(); err != nil {
			return err
		}
	}
	if s.Synthetic != nil {
		if err := s.Synthetic.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// SyntheticHealthEvidence is the bounded public projection of the signed
// fixed-corpus run. It intentionally excludes fixture source, finding text,
// image maps, worker IDs, and raw adapter output.
type SyntheticHealthEvidence struct {
	CorpusID      string    `json:"corpus_id"`
	CorpusDigest  string    `json:"corpus_digest"`
	Current       bool      `json:"current"`
	State         string    `json:"state"`
	FixtureTotal  int       `json:"fixture_total"`
	FixturePassed int       `json:"fixture_passed"`
	FixtureFailed int       `json:"fixture_failed"`
	FailureClass  string    `json:"failure_class,omitempty"`
	ObservedAt    time.Time `json:"observed_at"`
}

func (e SyntheticHealthEvidence) Validate() error {
	if e.CorpusID == "" || !validSyntheticDigest(e.CorpusDigest) {
		return errors.New("synthetic health corpus identity is invalid")
	}
	switch e.State {
	case "pending", "passed", "failed":
	default:
		return errors.New("synthetic health state is invalid")
	}
	if e.FixtureTotal < 0 || e.FixturePassed < 0 || e.FixtureFailed < 0 ||
		e.FixturePassed+e.FixtureFailed > e.FixtureTotal ||
		e.ObservedAt.IsZero() {
		return errors.New("synthetic health fixture evidence is invalid")
	}
	return nil
}

// RealScanHealthEvidence is the bounded public projection of sampled scanner
// worker telemetry. It contains aggregate counters only; scan IDs, repository
// paths, findings, logs, and worker identifiers never enter this structure.
type RealScanHealthEvidence struct {
	State                        string    `json:"state"`
	CandidateSamples             int       `json:"candidate_samples"`
	StableSamples                int       `json:"stable_samples"`
	CandidateInfrastructureFails int       `json:"candidate_infrastructure_failures"`
	StableInfrastructureFails    int       `json:"stable_infrastructure_failures"`
	ParserFailures               int       `json:"parser_failures"`
	ExpectedFindingLosses        int       `json:"expected_finding_losses"`
	CandidateP95DurationMS       int64     `json:"candidate_p95_duration_ms"`
	StableP95DurationMS          int64     `json:"stable_p95_duration_ms"`
	WorkersTotal                 int       `json:"workers_total"`
	WorkersReady                 int       `json:"workers_ready"`
	WorkersFailed                int       `json:"workers_failed"`
	ObservedAt                   time.Time `json:"observed_at"`
}

func (e RealScanHealthEvidence) Validate() error {
	switch e.State {
	case "", "pending", "healthy", "degraded":
	default:
		return errors.New("real-scan health state is invalid")
	}
	values := []int{
		e.CandidateSamples, e.StableSamples,
		e.CandidateInfrastructureFails, e.StableInfrastructureFails,
		e.ParserFailures, e.ExpectedFindingLosses,
		e.WorkersTotal, e.WorkersReady, e.WorkersFailed,
	}
	for _, value := range values {
		if value < 0 {
			return errors.New("real-scan health counters must not be negative")
		}
	}
	if e.WorkersReady > e.WorkersTotal || e.WorkersFailed > e.WorkersTotal ||
		e.CandidateP95DurationMS < 0 || e.StableP95DurationMS < 0 {
		return errors.New("real-scan health evidence is invalid")
	}
	if e.State != "" && e.ObservedAt.IsZero() {
		return errors.New("real-scan health observation time is required")
	}
	return nil
}

// Runtime mutations must be idempotent by AssignmentRequest.OperationID and
// honor context cancellation. Health must be a read-only point-in-time view.
type Runtime interface {
	Assign(context.Context, AssignmentRequest) error
	Health(context.Context, HealthRequest) (HealthSnapshot, error)
}

// LifecycleRuntime is implemented by deployment-aware runtimes. Operations
// must be idempotent because paused and rollback states are durably reconciled.
type LifecycleRuntime interface {
	Pause(context.Context, AssignmentRequest) error
	Resume(context.Context, AssignmentRequest) error
	Cancel(context.Context, AssignmentRequest) error
}

type GateRequest struct {
	Rollout *scannerrelease.Rollout
	Policy  scannerpolicy.Policy
	Now     time.Time
}

type GateDecision struct {
	Allowed bool
	Reason  string
}

// ProgressGate evaluates maintenance/emergency constraints before the
// controller advances a rollout. Rollback is intentionally never gated.
type ProgressGate interface {
	Evaluate(context.Context, GateRequest) (GateDecision, error)
}

type Config struct {
	Store             ControllerStore
	Runtime           Runtime
	Gate              ProgressGate
	WorkerID          string
	PollInterval      time.Duration
	ReconcileInterval time.Duration
	HeartbeatInterval time.Duration
	LeaseDuration     time.Duration
	CohortTimeout     time.Duration
	Once              bool
	Observer          scannerobservability.Observer

	Now   func() time.Time
	Sleep func(context.Context, time.Duration) error
}
