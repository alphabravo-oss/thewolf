package scannerrelease

import (
	"context"
	"time"
)

// PolicyRepository owns versioned policy and registry configuration.
type PolicyRepository interface {
	CreatePolicy(context.Context, *Policy) error
	GetPolicy(context.Context, string) (*Policy, error)
	ListPolicies(context.Context, string, bool) ([]Policy, error)
}

type RegistryRepository interface {
	CreateRegistryTarget(context.Context, *RegistryTarget) error
	GetRegistryTarget(context.Context, string) (*RegistryTarget, error)
	ListRegistryTargets(context.Context, bool) ([]RegistryTarget, error)
	UpdateRegistryTarget(context.Context, *RegistryTarget, int64) error
	UpdateRegistryObservation(context.Context, string, RegistryObservation) error
	CreateRegistryJob(context.Context, *RegistryJob, TransitionCommand) error
	GetRegistryJob(context.Context, string) (*RegistryJob, error)
	ListRegistryJobs(context.Context, RegistryJobFilter, int) ([]RegistryJob, error)
	ClaimNextRegistryJob(context.Context, string, time.Time, time.Time) (*RegistryJob, error)
	HeartbeatRegistryJob(context.Context, string, string, string, time.Time, time.Time) (RegistryJobLeaseStatus, error)
	FinalizeRegistryJob(context.Context, string, string, string, RegistryJobState, time.Time, string, string, string, time.Time) (*RegistryJob, error)
	ReclaimStaleRegistryJobs(context.Context, time.Time) (RegistryJobReclaimSummary, error)
	RetryDeadLetterRegistryJob(context.Context, string, int64, TransitionCommand, time.Time) (*RegistryJob, error)
	UpsertRegistryImageObservation(context.Context, *RegistryImageObservation) error
	ListRegistryImageObservations(context.Context, string) ([]RegistryImageObservation, error)
	UpsertRegistryQuarantineObject(context.Context, *RegistryQuarantineObject) error
	ListRegistryQuarantineObjects(context.Context, string, string, int) ([]RegistryQuarantineObject, error)
	AuthorizeRegistryQuarantineDeletion(context.Context, string, string, time.Time, time.Time) (*RegistryQuarantineObject, RegistryQuarantineDecision, error)
	CompleteRegistryQuarantineDeletion(context.Context, string, string, string, bool, string, time.Time) error
}

type SignerRepository interface {
	CreateSignerProfile(context.Context, *SignerProfile) error
	GetSignerProfile(context.Context, string) (*SignerProfile, error)
	ListSignerProfiles(context.Context, bool) ([]SignerProfile, error)
	RotateSignerProfile(context.Context, string, int64, *SignerProfile) error
	RevokeSignerProfile(context.Context, string, int64, string, string, time.Time) error
}

type DiscoveryRepository interface {
	CreateDiscoveryRun(context.Context, *DiscoveryRun, TransitionCommand) error
	GetDiscoveryRun(context.Context, string) (*DiscoveryRun, error)
	GetLatestCompletedDiscovery(context.Context, string, string, int64, string) (*DiscoveryRun, error)
	ListDiscoveryRuns(context.Context, DiscoveryFilter, PageRequest) (DiscoveryPage, error)
	ClaimNextDiscoveryRun(context.Context, string, time.Time) (*DiscoveryRun, error)
	HeartbeatDiscoveryRun(context.Context, string, string, string, time.Time) (DiscoveryLeaseStatus, error)
	RequestDiscoveryCancellation(context.Context, string, TransitionCommand, time.Time) (bool, error)
	ReclaimStaleDiscoveryRuns(context.Context, time.Time) (int, error)
	FinalizeDiscoveryRun(context.Context, *DiscoveryRun, int64, string, []UpdateItem, TransitionCommand) (*DiscoveryRun, error)
	AddUpdateItems(context.Context, string, []UpdateItem) error
	ListUpdateItems(context.Context, string) ([]UpdateItem, error)
	UpdateDiscoverySummary(context.Context, *DiscoveryRun, int64, TransitionCommand) (*DiscoveryRun, error)
	TransitionDiscovery(context.Context, string, int64, DiscoveryState, TransitionCommand) (*DiscoveryRun, error)
}

type CandidateRepository interface {
	CreateCandidate(context.Context, *Candidate, TransitionCommand) error
	GetCandidate(context.Context, string) (*Candidate, error)
	ListCandidates(context.Context, CandidateFilter, PageRequest) (CandidatePage, error)
	ClaimNextCandidateProposal(context.Context, string, time.Time) (*Candidate, error)
	HeartbeatCandidateProposal(context.Context, string, string, string, time.Time) (CandidateProposalLeaseStatus, error)
	ReleaseCandidateProposal(context.Context, string, string, string, string, string, TransitionCommand) (*Candidate, error)
	FinalizeCandidateProposalNoOp(context.Context, string, string, string, string, string, TransitionCommand) (*Candidate, error)
	FinalizeCandidateProposal(context.Context, *Candidate, int64, string, TransitionCommand) (*Candidate, error)
	ReclaimStaleCandidateProposals(context.Context, time.Time) (int, error)
	UpdateCandidateProposal(context.Context, *Candidate, int64, TransitionCommand) (*Candidate, error)
	TransitionCandidate(context.Context, string, int64, CandidateState, TransitionCommand) (*Candidate, error)
}

type BuildRepository interface {
	CreateBuildRun(context.Context, *BuildRun, TransitionCommand) error
	CreateBuildPlan(context.Context, *BuildRun, []BuildStep, TransitionCommand) error
	GetBuildRun(context.Context, string) (*BuildRun, error)
	ListBuildRuns(context.Context, string) ([]BuildRun, error)
	ClaimNextBuildRun(context.Context, string, []string, time.Time) (*BuildRun, error)
	HeartbeatBuildRun(context.Context, string, string, string, time.Time) (BuildLeaseStatus, error)
	RequestBuildCancellation(context.Context, string, TransitionCommand, time.Time) (bool, error)
	ReclaimStaleBuildRuns(context.Context, time.Time) (int, error)
	TransitionBuildRun(context.Context, string, int64, BuildState, TransitionCommand) (*BuildRun, error)
	CreateBuildStep(context.Context, *BuildStep, TransitionCommand) error
	ListBuildSteps(context.Context, string) ([]BuildStep, error)
	UpdateBuildStepEvidence(context.Context, *BuildStep, int64, TransitionCommand) (*BuildStep, error)
	TransitionBuildStep(context.Context, string, int64, BuildState, TransitionCommand) (*BuildStep, error)
}

type ReleaseRepository interface {
	CreateRelease(context.Context, *ReleaseInventory, TransitionCommand) error
	// CommitCandidatePublication atomically advances an approved/publishing
	// candidate, inserts its immutable inventory, and records both aggregate
	// events. Retrying the same candidate and manifest returns the original
	// release; conflicting evidence fails closed.
	CommitCandidatePublication(context.Context, string, int64, *ReleaseInventory, TransitionCommand) (*Release, error)
	GetRelease(context.Context, string) (*Release, error)
	GetReleaseInventory(context.Context, string) (*ReleaseInventory, error)
	ListReleases(context.Context, ReleaseFilter, PageRequest) (ReleasePage, error)
	TransitionRelease(context.Context, string, int64, ReleaseState, TransitionCommand) (*Release, error)
	AddArtifact(context.Context, *ReleaseArtifact) error
	ListArtifacts(context.Context, string, string) ([]ReleaseArtifact, error)
	AddApproval(context.Context, *Approval) error
	ListApprovals(context.Context, string, string) ([]Approval, error)
}

type RolloutRepository interface {
	CreateRollout(context.Context, *Rollout, []RolloutCohort, TransitionCommand) error
	GetRollout(context.Context, string) (*Rollout, error)
	ListRollouts(context.Context, RolloutFilter, PageRequest) (RolloutPage, error)
	TransitionRollout(context.Context, string, int64, RolloutState, TransitionCommand) (*Rollout, error)
	ListRolloutCohorts(context.Context, string) ([]RolloutCohort, error)
	UpdateRolloutCohort(context.Context, *RolloutCohort, int64, TransitionCommand) error
}

// RolloutLeaseRepository provides replica-safe, opaque-token ownership for
// rollout reconciliation without changing the rollout business version.
type RolloutLeaseRepository interface {
	ClaimNextRollout(context.Context, string, time.Time, time.Time) (*RolloutClaim, error)
	HeartbeatRollout(context.Context, string, string, string, time.Time, time.Time) (RolloutLeaseStatus, error)
	ReleaseRolloutClaim(context.Context, string, string, string, time.Time, time.Time, TransitionCommand) (bool, error)
}

type WorkerStatusRepository interface {
	AssignWorkerReleaseStatuses(context.Context, string, string, string, time.Time, time.Time) (int64, error)
	UpsertWorkerReleaseStatus(context.Context, *WorkerReleaseStatus) error
	ListWorkerReleaseStatuses(context.Context, string, time.Time) ([]WorkerReleaseStatus, error)
}

type EventRepository interface {
	ListEvents(context.Context, string, string, int64, int) ([]Event, error)
	ListAllEvents(context.Context, EventFilter, PageRequest) (EventPage, error)
}

type OperationCorrelationRepository interface {
	GetOperationCorrelation(context.Context, string, string) (*OperationCorrelation, error)
}

type NotificationRepository interface {
	GetNotification(context.Context, string) (*Notification, error)
	ListNotifications(context.Context, NotificationFilter, PageRequest) (NotificationPage, error)
	NotificationQueueCounts(context.Context) (NotificationCounts, error)
	ClaimNextNotification(context.Context, string, time.Time, time.Time) (*Notification, error)
	HeartbeatNotification(context.Context, string, string, string, time.Time, time.Time) (NotificationLeaseStatus, error)
	FinalizeNotification(context.Context, string, string, string, NotificationState, time.Time, string, string, time.Time) (*Notification, error)
	ReclaimStaleNotifications(context.Context, time.Time) (NotificationReclaimSummary, error)
	RetryDeadLetterNotification(context.Context, string, int64, TransitionCommand, time.Time) (*Notification, error)
}

type AlertRepository interface {
	GetAlert(context.Context, string) (*Alert, error)
	ListAlerts(context.Context, AlertFilter, PageRequest) (AlertPage, error)
	AlertCounts(context.Context) (AlertCounts, error)
	EvaluateAlerts(context.Context, AlertEvaluationRequest, time.Time) (AlertEvaluationSummary, error)
}

type ScheduleLeaseRepository interface {
	AcquireScheduleLease(context.Context, string, string, string, time.Time, time.Time) (*ScheduleLease, bool, error)
	HeartbeatScheduleLease(context.Context, string, string, string, string, time.Time, time.Time) (bool, error)
	CompleteScheduleLease(context.Context, string, string, string, string, LeaseState, string, time.Time) (bool, error)
	GetScheduleLease(context.Context, string, string) (*ScheduleLease, error)
}

type BackupRepository interface {
	ExportReleaseBackup(context.Context, BackupCommand) (*ReleaseBackup, error)
	PreflightReleaseRestore(context.Context, *ReleaseBackup) (BackupPreflight, error)
	RestoreReleaseBackup(context.Context, *ReleaseBackup, BackupCommand) (*BackupRestoreResult, error)
	ListBackupOperations(context.Context, int) ([]BackupOperation, error)
	GetReleaseMaintenanceStatus(context.Context) (*MaintenanceStatus, error)
}

type CustomBuildRepository interface {
	CreateCustomBuild(context.Context, CustomBuildCreateRequest) (*CustomBuildInventory, bool, error)
	GetCustomBuild(context.Context, string) (*CustomBuildInventory, error)
	ListCustomBuilds(context.Context, CustomBuildFilter, PageRequest) (CustomBuildPage, error)
	ClaimNextCustomBuild(context.Context, string, time.Time, time.Time) (*CustomBuild, error)
	StartCustomBuild(context.Context, string, string, time.Time) (*CustomBuild, error)
	HeartbeatCustomBuild(context.Context, string, string, time.Time, time.Time) (CustomBuildLeaseStatus, error)
	StartCustomBuildVariant(context.Context, string, string, string, time.Time) (*CustomBuildVariant, error)
	CompleteCustomBuildVariant(context.Context, string, string, string, CustomBuildVariantResult, time.Time) (*CustomBuildVariant, error)
	AppendCustomBuildLog(context.Context, string, string, string, string, bool, time.Time) (*CustomBuildLog, error)
	FinalizeCustomBuild(context.Context, string, string, time.Time) (*CustomBuild, error)
	RequestCustomBuildCancellation(context.Context, string, int64, TransitionCommand, time.Time) (*CustomBuild, error)
	RetryCustomBuild(context.Context, string, int64, TransitionCommand, time.Time) (*CustomBuild, error)
	ReclaimStaleCustomBuilds(context.Context, time.Time) (int, error)
	ListCustomBuildLogs(context.Context, string, int64, int) ([]CustomBuildLog, error)
}

// Persistence groups aggregate-specific repository contracts without exposing
// one monolithic method namespace to services.
type Persistence interface {
	PolicyRepository
	RegistryRepository
	SignerRepository
	DiscoveryRepository
	CandidateRepository
	BuildRepository
	ReleaseRepository
	RolloutRepository
	RolloutLeaseRepository
	WorkerStatusRepository
	EventRepository
	OperationCorrelationRepository
	NotificationRepository
	AlertRepository
	ScheduleLeaseRepository
	CustomBuildRepository
	BackupRepository
}
