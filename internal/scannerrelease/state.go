package scannerrelease

import (
	"errors"
	"fmt"
)

var (
	ErrVersionConflict      = errors.New("scanner release aggregate version conflict")
	ErrInvalidTransition    = errors.New("invalid scanner release state transition")
	ErrIdempotencyConflict  = errors.New("idempotency key already represents a different command")
	ErrImmutable            = errors.New("published scanner release evidence is immutable")
	ErrLeaseNotOwned        = errors.New("scanner schedule lease is not owned by caller")
	ErrCustomBuildLogBudget = errors.New("custom build log budget exhausted")
)

type DiscoveryState string

const (
	DiscoveryQueued    DiscoveryState = "queued"
	DiscoveryResolving DiscoveryState = "resolving"
	DiscoveryComparing DiscoveryState = "comparing"
	DiscoveryProposing DiscoveryState = "proposing"
	DiscoveryCompleted DiscoveryState = "completed"
	DiscoveryFailed    DiscoveryState = "failed"
	DiscoveryCancelled DiscoveryState = "cancelled"
)

type CandidateState string

const (
	CandidateDraft              CandidateState = "draft"
	CandidateAwaitingDefinition CandidateState = "awaiting_definition"
	CandidateQueued             CandidateState = "queued"
	CandidateBuilding           CandidateState = "building"
	CandidateTesting            CandidateState = "testing"
	CandidateSecurityReview     CandidateState = "security_review"
	CandidateAwaitingApproval   CandidateState = "awaiting_approval"
	CandidateApproved           CandidateState = "approved"
	CandidatePublishing         CandidateState = "publishing"
	CandidatePublished          CandidateState = "published"
	CandidateBlocked            CandidateState = "blocked"
	CandidateRejected           CandidateState = "rejected"
	CandidateFailed             CandidateState = "failed"
)

type BuildState string

const (
	BuildQueued    BuildState = "queued"
	BuildClaimed   BuildState = "claimed"
	BuildRunning   BuildState = "running"
	BuildCompleted BuildState = "completed"
	BuildFailed    BuildState = "failed"
	BuildCancelled BuildState = "cancelled"
)

type ReleaseState string

const (
	ReleasePublished        ReleaseState = "published"
	ReleaseCandidateChannel ReleaseState = "candidate_channel"
	ReleaseCanary           ReleaseState = "canary"
	ReleaseStable           ReleaseState = "stable"
	ReleaseDeprecated       ReleaseState = "deprecated"
	ReleaseRevoked          ReleaseState = "revoked"
)

type RolloutState string

const (
	RolloutPending     RolloutState = "pending"
	RolloutPreparing   RolloutState = "preparing"
	RolloutCanary      RolloutState = "canary"
	RolloutVerifying   RolloutState = "verifying"
	RolloutRollingOut  RolloutState = "rolling_out"
	RolloutCompleted   RolloutState = "completed"
	RolloutFailed      RolloutState = "failed"
	RolloutPaused      RolloutState = "paused"
	RolloutRollingBack RolloutState = "rolling_back"
	RolloutRolledBack  RolloutState = "rolled_back"
)

var discoveryTransitions = transitionMap[DiscoveryState]{
	DiscoveryQueued:    {DiscoveryResolving, DiscoveryFailed, DiscoveryCancelled},
	DiscoveryResolving: {DiscoveryComparing, DiscoveryFailed, DiscoveryCancelled},
	DiscoveryComparing: {DiscoveryProposing, DiscoveryFailed, DiscoveryCancelled},
	DiscoveryProposing: {DiscoveryCompleted, DiscoveryFailed, DiscoveryCancelled},
}

var candidateTransitions = transitionMap[CandidateState]{
	CandidateDraft:              {CandidateAwaitingDefinition, CandidateQueued, CandidateRejected, CandidateFailed},
	CandidateAwaitingDefinition: {CandidateQueued, CandidateRejected, CandidateFailed},
	CandidateQueued:             {CandidateBuilding, CandidateBlocked, CandidateRejected, CandidateFailed},
	CandidateBuilding:           {CandidateTesting, CandidateBlocked, CandidateFailed},
	CandidateTesting:            {CandidateSecurityReview, CandidateBlocked, CandidateFailed},
	CandidateSecurityReview:     {CandidateAwaitingApproval, CandidateBlocked, CandidateRejected, CandidateFailed},
	CandidateAwaitingApproval:   {CandidateApproved, CandidateRejected, CandidateBlocked, CandidateFailed},
	CandidateApproved:           {CandidatePublishing, CandidateFailed},
	CandidatePublishing:         {CandidatePublished, CandidateFailed},
	// A blocked candidate is resumable after its blocking evidence/policy is
	// resolved. It returns to the appropriate executable or review queue.
	CandidateBlocked: {CandidateQueued, CandidateSecurityReview, CandidateAwaitingApproval, CandidateRejected, CandidateFailed},
}

var buildTransitions = transitionMap[BuildState]{
	BuildQueued:  {BuildClaimed, BuildRunning, BuildCancelled, BuildFailed},
	BuildClaimed: {BuildRunning, BuildQueued, BuildCancelled, BuildFailed},
	BuildRunning: {BuildCompleted, BuildFailed, BuildCancelled},
}

var releaseTransitions = transitionMap[ReleaseState]{
	ReleasePublished:        {ReleaseCandidateChannel, ReleaseDeprecated, ReleaseRevoked},
	ReleaseCandidateChannel: {ReleaseCanary, ReleaseStable, ReleaseDeprecated, ReleaseRevoked},
	ReleaseCanary:           {ReleaseStable, ReleaseDeprecated, ReleaseRevoked},
	ReleaseStable:           {ReleaseDeprecated, ReleaseRevoked},
	ReleaseDeprecated:       {ReleaseRevoked},
}

var rolloutTransitions = transitionMap[RolloutState]{
	RolloutPending:     {RolloutPreparing, RolloutPaused, RolloutFailed, RolloutRollingBack},
	RolloutPreparing:   {RolloutCanary, RolloutPaused, RolloutFailed, RolloutRollingBack},
	RolloutCanary:      {RolloutVerifying, RolloutPaused, RolloutFailed, RolloutRollingBack},
	RolloutVerifying:   {RolloutRollingOut, RolloutPaused, RolloutFailed, RolloutRollingBack},
	RolloutRollingOut:  {RolloutCompleted, RolloutPaused, RolloutFailed, RolloutRollingBack},
	RolloutPaused:      {RolloutPreparing, RolloutCanary, RolloutVerifying, RolloutRollingOut, RolloutFailed, RolloutRollingBack},
	RolloutFailed:      {RolloutRollingBack},
	RolloutRollingBack: {RolloutRolledBack, RolloutFailed},
}

type transitionMap[S ~string] map[S][]S

func (m transitionMap[S]) allows(from, to S) bool {
	for _, allowed := range m[from] {
		if allowed == to {
			return true
		}
	}
	return false
}

func validateTransition[S ~string](aggregate string, m transitionMap[S], from, to S) error {
	if from == to || !m.allows(from, to) {
		return fmt.Errorf("%w: %s %q -> %q", ErrInvalidTransition, aggregate, from, to)
	}
	return nil
}

func ValidateDiscoveryTransition(from, to DiscoveryState) error {
	return validateTransition("discovery", discoveryTransitions, from, to)
}

func ValidateCandidateTransition(from, to CandidateState) error {
	return validateTransition("candidate", candidateTransitions, from, to)
}

func ValidateBuildTransition(from, to BuildState) error {
	return validateTransition("build", buildTransitions, from, to)
}

func ValidateReleaseTransition(from, to ReleaseState) error {
	return validateTransition("release", releaseTransitions, from, to)
}

func ValidateRolloutTransition(from, to RolloutState) error {
	return validateTransition("rollout", rolloutTransitions, from, to)
}

func IsTerminalDiscoveryState(state DiscoveryState) bool {
	return state == DiscoveryCompleted || state == DiscoveryFailed || state == DiscoveryCancelled
}

func IsTerminalCandidateState(state CandidateState) bool {
	return state == CandidatePublished || state == CandidateRejected || state == CandidateFailed
}

func IsTerminalBuildState(state BuildState) bool {
	return state == BuildCompleted || state == BuildFailed || state == BuildCancelled
}

func IsTerminalRolloutState(state RolloutState) bool {
	return state == RolloutCompleted || state == RolloutRolledBack
}
