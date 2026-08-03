// Package scannerrollout contains the deterministic canary-health and rollout
// state machine used by Compose and Kubernetes deployment adapters.
package scannerrollout

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"time"
)

type State string

const (
	StatePending     State = "pending"
	StatePreparing   State = "preparing"
	StateCanary      State = "canary"
	StateVerifying   State = "verifying"
	StateRollingOut  State = "rolling_out"
	StateCompleted   State = "completed"
	StatePaused      State = "paused"
	StateFailed      State = "failed"
	StateRollingBack State = "rolling_back"
	StateRolledBack  State = "rolled_back"
)

var transitions = map[State]map[State]struct{}{
	StatePending: {
		StatePreparing: {}, StatePaused: {}, StateFailed: {},
	},
	StatePreparing: {
		StateCanary: {}, StatePaused: {}, StateFailed: {}, StateRollingBack: {},
	},
	StateCanary: {
		StateVerifying: {}, StatePaused: {}, StateFailed: {}, StateRollingBack: {},
	},
	StateVerifying: {
		StateRollingOut: {}, StatePaused: {}, StateFailed: {}, StateRollingBack: {},
	},
	StateRollingOut: {
		StateCompleted: {}, StatePaused: {}, StateFailed: {}, StateRollingBack: {},
	},
	StatePaused: {
		StatePreparing: {}, StateCanary: {}, StateVerifying: {}, StateRollingOut: {},
		StateFailed: {}, StateRollingBack: {},
	},
	StateFailed: {
		StateRollingBack: {},
	},
	StateRollingBack: {
		StateRolledBack: {}, StateFailed: {},
	},
}

type Rollout struct {
	ID                string    `json:"id"`
	TargetID          string    `json:"target_id"`
	FromReleaseID     string    `json:"from_release_id"`
	ToReleaseID       string    `json:"to_release_id"`
	State             State     `json:"state"`
	ResumeState       State     `json:"resume_state,omitempty"`
	Version           int64     `json:"version"`
	AutomaticRollback bool      `json:"automatic_rollback"`
	StartedAt         time.Time `json:"started_at"`
	UpdatedAt         time.Time `json:"updated_at"`
	FailureReason     string    `json:"failure_reason,omitempty"`
	RollbackTrigger   string    `json:"rollback_trigger,omitempty"`
}

type Event struct {
	PreviousState State     `json:"previous_state"`
	State         State     `json:"state"`
	Version       int64     `json:"version"`
	ActorID       string    `json:"actor_id"`
	Reason        string    `json:"reason"`
	CreatedAt     time.Time `json:"created_at"`
}

func (r Rollout) Validate() error {
	if r.ID == "" || r.TargetID == "" || r.ToReleaseID == "" {
		return errors.New("rollout ID, target ID, and destination release are required")
	}
	if r.FromReleaseID == r.ToReleaseID {
		return errors.New("rollout source and destination releases must differ")
	}
	if _, exists := transitions[r.State]; !exists &&
		r.State != StateCompleted && r.State != StateRolledBack {
		return fmt.Errorf("invalid rollout state %q", r.State)
	}
	if r.Version < 0 {
		return errors.New("rollout version must not be negative")
	}
	return nil
}

// Transition applies one optimistic-concurrency state transition and returns
// the append-only event that must be stored in the same transaction.
func (r Rollout) Transition(expectedVersion int64, next State, actor, reason string, at time.Time) (Rollout, Event, error) {
	if err := r.Validate(); err != nil {
		return Rollout{}, Event{}, err
	}
	if r.Version != expectedVersion {
		return Rollout{}, Event{}, fmt.Errorf("stale rollout version: have %d, expected %d", r.Version, expectedVersion)
	}
	if actor == "" || reason == "" {
		return Rollout{}, Event{}, errors.New("rollout transition actor and reason are required")
	}
	allowed := transitions[r.State]
	if _, ok := allowed[next]; !ok {
		return Rollout{}, Event{}, fmt.Errorf("rollout cannot transition from %s to %s", r.State, next)
	}
	previous := r.State
	if next == StatePaused {
		r.ResumeState = previous
	} else if previous == StatePaused {
		if next != r.ResumeState && next != StateFailed && next != StateRollingBack {
			return Rollout{}, Event{}, fmt.Errorf("paused rollout must resume to %s, not %s", r.ResumeState, next)
		}
		r.ResumeState = ""
	}
	r.State = next
	r.Version++
	r.UpdatedAt = at.UTC()
	event := Event{
		PreviousState: previous,
		State:         next,
		Version:       r.Version,
		ActorID:       actor,
		Reason:        reason,
		CreatedAt:     at.UTC(),
	}
	return r, event, nil
}

type CanaryPolicy struct {
	MinimumSamples               int           `json:"minimum_samples"`
	MinimumObservation           time.Duration `json:"minimum_observation"`
	MaxInfrastructureFailureRate float64       `json:"max_infrastructure_failure_rate"`
	MaxInfrastructureRateDelta   float64       `json:"max_infrastructure_rate_delta"`
	MaxDurationRegression        float64       `json:"max_duration_regression"`
	MaxParserFailures            int           `json:"max_parser_failures"`
	MaxPullFailures              int           `json:"max_pull_failures"`
	MaxCrashLoops                int           `json:"max_crash_loops"`
}

func DefaultCanaryPolicy() CanaryPolicy {
	return CanaryPolicy{
		MinimumSamples:               10,
		MinimumObservation:           15 * time.Minute,
		MaxInfrastructureFailureRate: 0.02,
		MaxInfrastructureRateDelta:   0.01,
		MaxDurationRegression:        0.20,
		MaxParserFailures:            0,
		MaxPullFailures:              0,
		MaxCrashLoops:                0,
	}
}

func (p CanaryPolicy) Validate() error {
	if p.MinimumSamples <= 0 || p.MinimumObservation <= 0 {
		return errors.New("canary minimum samples and observation must be positive")
	}
	for name, value := range map[string]float64{
		"max infrastructure failure rate": p.MaxInfrastructureFailureRate,
		"max infrastructure rate delta":   p.MaxInfrastructureRateDelta,
		"max duration regression":         p.MaxDurationRegression,
	} {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return fmt.Errorf("%s must be a non-negative finite number", name)
		}
	}
	if p.MaxParserFailures < 0 || p.MaxPullFailures < 0 || p.MaxCrashLoops < 0 {
		return errors.New("canary failure thresholds must not be negative")
	}
	return nil
}

type CanaryHealth struct {
	Samples                      int           `json:"samples"`
	InfrastructureFailures       int           `json:"infrastructure_failures"`
	StableSamples                int           `json:"stable_samples"`
	StableInfrastructureFailures int           `json:"stable_infrastructure_failures"`
	ParserFailures               int           `json:"parser_failures"`
	PullFailures                 int           `json:"pull_failures"`
	SignatureFailures            int           `json:"signature_failures"`
	ManifestFailures             int           `json:"manifest_failures"`
	ExpectedFindingLosses        int           `json:"expected_finding_losses"`
	CrashLoops                   int           `json:"crash_loops"`
	CandidateP95Duration         time.Duration `json:"candidate_p95_duration"`
	StableP95Duration            time.Duration `json:"stable_p95_duration"`
}

type CanaryOutcome string

const (
	CanaryPending  CanaryOutcome = "pending"
	CanaryPassed   CanaryOutcome = "passed"
	CanaryRollback CanaryOutcome = "rollback"
)

type CanaryDecision struct {
	Outcome CanaryOutcome `json:"outcome"`
	Reasons []string      `json:"reasons,omitempty"`
}

func EvaluateCanary(
	policy CanaryPolicy,
	health CanaryHealth,
	startedAt, now time.Time,
) (CanaryDecision, error) {
	if err := policy.Validate(); err != nil {
		return CanaryDecision{}, err
	}
	if startedAt.IsZero() || now.Before(startedAt) {
		return CanaryDecision{}, errors.New("invalid canary observation interval")
	}
	var rollback []string
	if health.SignatureFailures > 0 {
		rollback = append(rollback, "scanner signature verification failed")
	}
	if health.ManifestFailures > 0 {
		rollback = append(rollback, "scanner release manifest verification failed")
	}
	if health.ExpectedFindingLosses > 0 {
		rollback = append(rollback, "expected scanner findings disappeared")
	}
	if health.ParserFailures > policy.MaxParserFailures {
		rollback = append(rollback, "parser failure threshold exceeded")
	}
	if health.PullFailures > policy.MaxPullFailures {
		rollback = append(rollback, "image pull failure threshold exceeded")
	}
	if health.CrashLoops > policy.MaxCrashLoops {
		rollback = append(rollback, "worker crash-loop threshold exceeded")
	}
	candidateFailureRate := ratio(health.InfrastructureFailures, health.Samples)
	if candidateFailureRate > policy.MaxInfrastructureFailureRate {
		rollback = append(rollback, "infrastructure failure rate threshold exceeded")
	}
	if health.StableSamples > 0 &&
		candidateFailureRate-ratio(health.StableInfrastructureFailures, health.StableSamples) >
			policy.MaxInfrastructureRateDelta {
		rollback = append(rollback, "infrastructure failure rate regressed against stable")
	}
	if health.StableP95Duration > 0 &&
		float64(health.CandidateP95Duration-health.StableP95Duration)/float64(health.StableP95Duration) >
			policy.MaxDurationRegression {
		rollback = append(rollback, "scan duration regression threshold exceeded")
	}
	if len(rollback) > 0 {
		sort.Strings(rollback)
		return CanaryDecision{Outcome: CanaryRollback, Reasons: rollback}, nil
	}
	var pending []string
	if health.Samples < policy.MinimumSamples {
		pending = append(pending, "minimum canary sample count not reached")
	}
	if now.Sub(startedAt) < policy.MinimumObservation {
		pending = append(pending, "minimum canary observation time not reached")
	}
	if len(pending) > 0 {
		sort.Strings(pending)
		return CanaryDecision{Outcome: CanaryPending, Reasons: pending}, nil
	}
	return CanaryDecision{Outcome: CanaryPassed}, nil
}

func ratio(numerator, denominator int) float64 {
	if denominator <= 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}
