package scannerrollout

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

type WorkerStatusStore interface {
	scannerrelease.WorkerStatusRepository
}

// WorkerStatusRuntime is the built-in Compose/persistence adapter. Production
// Kubernetes controllers can replace it with an adapter that patches workload
// assignments and reads cluster-native health while preserving this contract.
type WorkerStatusRuntime struct {
	Store        WorkerStatusStore
	ActiveWithin time.Duration
	Now          func() time.Time
}

func (r WorkerStatusRuntime) Assign(ctx context.Context, request AssignmentRequest) error {
	if r.Store == nil {
		return errors.New("rollout worker-status store is required")
	}
	if strings.TrimSpace(request.OperationID) == "" {
		return errors.New("rollout assignment operation ID is required")
	}
	now := r.now()
	_, err := r.Store.AssignWorkerReleaseStatuses(
		ctx, request.CohortName, request.DesiredReleaseID, request.OperationID,
		now.Add(-r.activeWithin()), now,
	)
	return err
}

func (r WorkerStatusRuntime) Health(ctx context.Context, request HealthRequest) (HealthSnapshot, error) {
	if r.Store == nil {
		return HealthSnapshot{}, errors.New("rollout worker-status store is required")
	}
	if strings.TrimSpace(request.OperationID) == "" {
		return HealthSnapshot{}, errors.New("rollout health assignment operation ID is required")
	}
	now := r.now()
	activeAfter := now.Add(-r.activeWithin())
	statuses, err := r.Store.ListWorkerReleaseStatuses(ctx, request.CohortName, activeAfter)
	if err != nil {
		return HealthSnapshot{}, err
	}
	snapshot := HealthSnapshot{TotalWorkers: len(statuses), ObservedAt: now}
	allObserved := len(statuses) > 0
	for _, status := range statuses {
		currentEvidence := workerEvidenceCurrent(status, request.OperationID)
		if currentEvidence {
			addWorkerMetrics(&snapshot.Canary, status.CapabilitiesJSON, false)
		}
		verified := currentEvidence && verificationReady(status.VerificationState) &&
			status.ObservedReleaseID == request.DesiredReleaseID
		if verified {
			snapshot.ReadyWorkers++
		}
		if !currentEvidence || status.ObservedReleaseID != request.DesiredReleaseID {
			allObserved = false
		}
		if currentEvidence &&
			(verificationFailed(status.VerificationState) || status.VerificationError != "") {
			snapshot.FailedWorkers++
			classifyVerificationFailure(&snapshot.Canary, status.VerificationError)
		}
	}
	if allObserved {
		snapshot.ObservedReleaseID = request.DesiredReleaseID
	}
	if request.StableCohortName != "" && request.StableCohortName != request.CohortName {
		stable, err := r.Store.ListWorkerReleaseStatuses(
			ctx, request.StableCohortName, activeAfter,
		)
		if err != nil {
			return HealthSnapshot{}, err
		}
		for _, status := range stable {
			if workerEvidenceCurrent(status, "") {
				addWorkerMetrics(&snapshot.Canary, status.CapabilitiesJSON, true)
			}
		}
	}
	realScans := realScanHealthEvidence(snapshot, now)
	snapshot.RealScans = &realScans
	if err := snapshot.Validate(); err != nil {
		return HealthSnapshot{}, err
	}
	return snapshot, nil
}

func realScanHealthEvidence(
	snapshot HealthSnapshot,
	observedAt time.Time,
) RealScanHealthEvidence {
	state := "healthy"
	if snapshot.Canary.Samples == 0 {
		state = "pending"
	}
	if snapshot.FailedWorkers > 0 ||
		snapshot.Canary.InfrastructureFailures > 0 ||
		snapshot.Canary.ParserFailures > 0 ||
		snapshot.Canary.PullFailures > 0 ||
		snapshot.Canary.SignatureFailures > 0 ||
		snapshot.Canary.ManifestFailures > 0 ||
		snapshot.Canary.ExpectedFindingLosses > 0 ||
		snapshot.Canary.CrashLoops > 0 {
		state = "degraded"
	}
	return RealScanHealthEvidence{
		State:                        state,
		CandidateSamples:             snapshot.Canary.Samples,
		StableSamples:                snapshot.Canary.StableSamples,
		CandidateInfrastructureFails: snapshot.Canary.InfrastructureFailures,
		StableInfrastructureFails:    snapshot.Canary.StableInfrastructureFailures,
		ParserFailures:               snapshot.Canary.ParserFailures,
		ExpectedFindingLosses:        snapshot.Canary.ExpectedFindingLosses,
		CandidateP95DurationMS:       snapshot.Canary.CandidateP95Duration.Milliseconds(),
		StableP95DurationMS:          snapshot.Canary.StableP95Duration.Milliseconds(),
		WorkersTotal:                 snapshot.TotalWorkers,
		WorkersReady:                 snapshot.ReadyWorkers,
		WorkersFailed:                snapshot.FailedWorkers,
		ObservedAt:                   observedAt.UTC(),
	}
}

func workerEvidenceCurrent(status scannerrelease.WorkerReleaseStatus, operationID string) bool {
	if status.AssignmentOperationID == "" ||
		(operationID != "" && status.AssignmentOperationID != operationID) ||
		status.AssignedAt == nil || status.EvidenceObservedAt == nil {
		return false
	}
	return !status.EvidenceObservedAt.Before(status.AssignedAt.UTC())
}

func (r WorkerStatusRuntime) activeWithin() time.Duration {
	if r.ActiveWithin <= 0 {
		return 2 * time.Minute
	}
	return r.ActiveWithin
}

func (r WorkerStatusRuntime) now() time.Time {
	if r.Now == nil {
		return time.Now().UTC()
	}
	return r.Now().UTC()
}

type workerMetrics struct {
	Samples                int   `json:"samples"`
	InfrastructureFailures int   `json:"infrastructure_failures"`
	ParserFailures         int   `json:"parser_failures"`
	PullFailures           int   `json:"pull_failures"`
	SignatureFailures      int   `json:"signature_failures"`
	ManifestFailures       int   `json:"manifest_failures"`
	ExpectedFindingLosses  int   `json:"expected_finding_losses"`
	CrashLoops             int   `json:"crash_loops"`
	P95DurationMS          int64 `json:"p95_duration_ms"`
}

func addWorkerMetrics(health *CanaryHealth, value string, stable bool) {
	var metrics workerMetrics
	if json.Unmarshal([]byte(value), &metrics) != nil {
		return
	}
	if stable {
		health.StableSamples += metrics.Samples
		health.StableInfrastructureFailures += metrics.InfrastructureFailures
		duration := time.Duration(metrics.P95DurationMS) * time.Millisecond
		if duration > health.StableP95Duration {
			health.StableP95Duration = duration
		}
		return
	}
	health.Samples += metrics.Samples
	health.InfrastructureFailures += metrics.InfrastructureFailures
	health.ParserFailures += metrics.ParserFailures
	health.PullFailures += metrics.PullFailures
	health.SignatureFailures += metrics.SignatureFailures
	health.ManifestFailures += metrics.ManifestFailures
	health.ExpectedFindingLosses += metrics.ExpectedFindingLosses
	health.CrashLoops += metrics.CrashLoops
	duration := time.Duration(metrics.P95DurationMS) * time.Millisecond
	if duration > health.CandidateP95Duration {
		health.CandidateP95Duration = duration
	}
}

func verificationReady(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "verified", "ready", "healthy", "passed":
		return true
	default:
		return false
	}
}

func verificationFailed(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "failed", "error", "rejected":
		return true
	default:
		return false
	}
}

func classifyVerificationFailure(health *CanaryHealth, detail string) {
	lower := strings.ToLower(detail)
	switch {
	case strings.Contains(lower, "signature"):
		health.SignatureFailures++
	case strings.Contains(lower, "manifest"), strings.Contains(lower, "digest"):
		health.ManifestFailures++
	case strings.Contains(lower, "pull"):
		health.PullFailures++
	case strings.Contains(lower, "parser"):
		health.ParserFailures++
	case strings.Contains(lower, "crash"):
		health.CrashLoops++
	case strings.Contains(lower, "finding"):
		health.ExpectedFindingLosses++
	default:
		health.InfrastructureFailures++
	}
}
