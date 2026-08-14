// Package scannercontrol coordinates release-domain commands shared by the
// scanner supply-chain API, scheduler, CLI automation, and workers.
package scannercontrol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/scannerpipeline"
	"github.com/alphabravocompany/thewolf/internal/scannerpolicy"
	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

var (
	ErrValidation         = errors.New("scanner release command validation failed")
	ErrPolicyNotFound     = errors.New("active scanner release policy not found")
	ErrCandidateNotReady  = errors.New("scanner release candidate is not ready")
	ErrApprovalStale      = errors.New("scanner release approval is stale")
	ErrReleaseUnavailable = errors.New("scanner release is unavailable for rollout")
)

var (
	releaseNamePattern = regexp.MustCompile(`^scanner-set-[0-9]{4}\.[0-9]{2}\.[1-9][0-9]*$`)
	digestPattern      = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
)

type Service struct {
	Store               scannerrelease.Persistence
	Now                 func() time.Time
	PublicationVerifier PublicationVerifier
}

type DiscoveryCommand struct {
	Trigger          scannerrelease.DiscoveryTrigger
	SchedulePeriod   string
	DefinitionCommit string
	Actor            string
	IdempotencyKey   string
	Scope            any
}

type CandidateCommand struct {
	DiscoveryRunID   string
	DefinitionCommit string
	ProposedCommit   string
	ProposalURL      string
	LockDigest       string
	LockURI          string
	RiskSummary      any
	SelectedItems    []string
	Actor            string
	Reason           string
	IdempotencyKey   string
	Images           []scannerpipeline.Image
}

type ApprovalCommand struct {
	CandidateID          string
	LockDigest           string
	PolicyDecisionDigest string
	EvidenceDigest       string
	Actor                string
	Reason               string
	IdempotencyKey       string
}

type ExceptionCommand struct {
	CandidateID         string
	Gate                string
	OwnerID             string
	Reason              string
	CompensatingControl string
	EvidenceDigest      string
	ExpiresAt           time.Time
	Actor               string
	IdempotencyKey      string
}

type RolloutCommand struct {
	ReleaseID      string
	Target         string
	Actor          string
	Reason         string
	IdempotencyKey string
	Strategy       string
}

type PublicationCommand struct {
	CandidateID    string
	Name           string
	ReceiptDigest  string
	Actor          string
	Reason         string
	IdempotencyKey string
}

func (s Service) EnsureDefaultPolicy(ctx context.Context, actor string) (*scannerrelease.Policy, error) {
	return s.EnsureDefaultPolicyWithSchedule(ctx, actor, scannerpolicy.DefaultSchedule())
}

// EnsureDefaultPolicyWithSchedule initializes a missing policy from deployment
// bootstrap settings. Once any active revision exists, the persisted policy is
// authoritative and deployment flags cannot overwrite UI/API changes.
func (s Service) EnsureDefaultPolicyWithSchedule(
	ctx context.Context,
	actor string,
	schedule scannerpolicy.SchedulePolicy,
) (*scannerrelease.Policy, error) {
	if s.Store == nil {
		return nil, errors.New("scanner release persistence is required")
	}
	active, err := s.Store.ListPolicies(ctx, "global", true)
	if err != nil {
		return nil, err
	}
	if len(active) > 0 {
		sort.Slice(active, func(i, j int) bool { return active[i].Revision > active[j].Revision })
		return &active[0], nil
	}
	if actor == "" {
		actor = "system"
	}
	rules := scannerpolicy.Default()
	rulesJSON, err := json.Marshal(rules)
	if err != nil {
		return nil, err
	}
	if err := schedule.Normalize(); err != nil {
		return nil, fmt.Errorf("validate scanner release bootstrap schedule: %w", err)
	}
	scheduleJSON, err := json.Marshal(schedule)
	if err != nil {
		return nil, err
	}
	policy := &scannerrelease.Policy{
		ID:           uuid.NewString(),
		Scope:        "global",
		Revision:     1,
		Enabled:      true,
		ScheduleJSON: string(scheduleJSON),
		RulesJSON:    string(rulesJSON),
		CreatedBy:    actor,
	}
	if err := s.Store.CreatePolicy(ctx, policy); err != nil {
		// Another replica may have initialized the same unique policy revision.
		active, listErr := s.Store.ListPolicies(ctx, "global", true)
		if listErr == nil && len(active) > 0 {
			sort.Slice(active, func(i, j int) bool { return active[i].Revision > active[j].Revision })
			return &active[0], nil
		}
		return nil, err
	}
	return policy, nil
}

func (s Service) CreateDiscovery(ctx context.Context, command DiscoveryCommand) (*scannerrelease.DiscoveryRun, error) {
	if command.Actor == "" || command.IdempotencyKey == "" || command.DefinitionCommit == "" {
		return nil, fmt.Errorf("%w: actor, idempotency key, and definition commit are required", ErrValidation)
	}
	switch command.Trigger {
	case scannerrelease.DiscoveryScheduled, scannerrelease.DiscoveryOnDemand, scannerrelease.DiscoverySecurity:
	default:
		return nil, fmt.Errorf("%w: invalid discovery trigger %q", ErrValidation, command.Trigger)
	}
	policy, err := s.EnsureDefaultPolicy(ctx, command.Actor)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(map[string]any{"scope": command.Scope})
	if err != nil {
		return nil, fmt.Errorf("%w: encode discovery scope: %v", ErrValidation, err)
	}
	scope := command.Scope
	if scope == nil {
		scope = map[string]any{"mode": "complete"}
	}
	scopeJSON, err := json.Marshal(scope)
	if err != nil {
		return nil, fmt.Errorf("%w: encode durable discovery scope: %v", ErrValidation, err)
	}
	run := &scannerrelease.DiscoveryRun{
		ID:               uuid.NewString(),
		Trigger:          command.Trigger,
		SchedulePeriod:   command.SchedulePeriod,
		DefinitionCommit: command.DefinitionCommit,
		PolicyID:         policy.ID,
		PolicyRevision:   policy.Revision,
		ScopeJSON:        string(scopeJSON),
		State:            scannerrelease.DiscoveryQueued,
		Actor:            command.Actor,
		IdempotencyKey:   command.IdempotencyKey,
	}
	err = s.Store.CreateDiscoveryRun(ctx, run, scannerrelease.TransitionCommand{
		Actor: command.Actor, Reason: "scanner update discovery requested",
		PolicyRevision: policy.Revision, IdempotencyKey: command.IdempotencyKey,
		PayloadJSON: string(payload),
	})
	if err != nil {
		return nil, err
	}
	return run, nil
}

func (s Service) CreateCandidate(ctx context.Context, command CandidateCommand) (*scannerrelease.Candidate, error) {
	if command.Actor == "" || command.IdempotencyKey == "" || command.DefinitionCommit == "" {
		return nil, fmt.Errorf("%w: actor, idempotency key, and definition commit are required", ErrValidation)
	}
	command.Reason = strings.TrimSpace(command.Reason)
	if command.Reason == "" || len(command.Reason) > 2_048 {
		return nil, fmt.Errorf("%w: candidate reason is required and must be at most 2048 characters", ErrValidation)
	}
	policy, err := s.EnsureDefaultPolicy(ctx, command.Actor)
	if err != nil {
		return nil, err
	}
	// UI and API callers may omit a run identifier. Bind those requests to the
	// exact latest complete snapshot for this definition and policy so the
	// durable candidate remains reproducible and cannot consume partial data.
	if command.DiscoveryRunID == "" && command.LockDigest == "" {
		latest, err := s.Store.GetLatestCompletedDiscovery(
			ctx, command.DefinitionCommit, policy.ID, policy.Revision, `{"mode":"complete"}`,
		)
		if err != nil {
			return nil, err
		}
		if latest == nil {
			return nil, fmt.Errorf("%w: no complete discovery snapshot is available", ErrCandidateNotReady)
		}
		command.DiscoveryRunID = latest.ID
	}
	if command.DiscoveryRunID != "" {
		discovery, err := s.Store.GetDiscoveryRun(ctx, command.DiscoveryRunID)
		if err != nil {
			return nil, err
		}
		if !scannerrelease.DiscoveryEligibleForCandidate(discovery) {
			return nil, fmt.Errorf("%w: discovery %s does not have complete source coverage", ErrCandidateNotReady, command.DiscoveryRunID)
		}
		if discovery.DefinitionCommit != command.DefinitionCommit ||
			discovery.PolicyID != policy.ID || discovery.PolicyRevision != policy.Revision {
			return nil, fmt.Errorf("%w: discovery %s does not match the candidate definition and policy snapshot", ErrCandidateNotReady, discovery.ID)
		}
	}
	if command.RiskSummary == nil {
		command.RiskSummary = map[string]any{}
	}
	riskJSON, err := json.Marshal(command.RiskSummary)
	if err != nil {
		return nil, fmt.Errorf("%w: encode risk summary: %v", ErrValidation, err)
	}
	requiredGatesJSON, err := requiredGates(policy)
	if err != nil {
		return nil, err
	}
	state := scannerrelease.CandidateAwaitingDefinition
	if command.LockDigest != "" {
		state = scannerrelease.CandidateQueued
	}
	selectionMode := "explicit"
	if len(command.SelectedItems) == 0 {
		selectionMode = "complete"
	}
	selectionJSON, err := json.Marshal(map[string]any{
		"mode":             selectionMode,
		"discovery_run_id": command.DiscoveryRunID,
		"items":            command.SelectedItems,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: encode candidate selection: %v", ErrValidation, err)
	}
	candidate := &scannerrelease.Candidate{
		ID:                uuid.NewString(),
		DiscoveryRunID:    command.DiscoveryRunID,
		SelectionJSON:     string(selectionJSON),
		DefinitionCommit:  command.DefinitionCommit,
		ProposedCommit:    command.ProposedCommit,
		ProposalURL:       command.ProposalURL,
		LockDigest:        command.LockDigest,
		LockURI:           command.LockURI,
		RiskSummaryJSON:   string(riskJSON),
		State:             state,
		RequiredGatesJSON: requiredGatesJSON,
		PolicyID:          policy.ID,
		PolicyRevision:    policy.Revision,
		Actor:             command.Actor,
		IdempotencyKey:    command.IdempotencyKey,
	}
	payload := selectionJSON
	if err := s.Store.CreateCandidate(ctx, candidate, scannerrelease.TransitionCommand{
		Actor: command.Actor, Reason: command.Reason,
		PolicyRevision: policy.Revision, IdempotencyKey: command.IdempotencyKey,
		PayloadJSON: string(payload),
	}); err != nil {
		return nil, err
	}
	if state == scannerrelease.CandidateQueued {
		if err := EnqueueCandidateBuildPlan(ctx, s.Store, candidate, command.Images); err != nil {
			return candidate, fmt.Errorf("candidate created but build plan failed: %w", err)
		}
	}
	return candidate, nil
}

// EnqueueCandidateBuildPlan creates the complete durable evidence DAG for an
// immutable candidate. Proposal workers and direct API candidates share this
// function so neither path can silently omit a release gate.
func EnqueueCandidateBuildPlan(
	ctx context.Context,
	store scannerrelease.Persistence,
	candidate *scannerrelease.Candidate,
	images []scannerpipeline.Image,
) error {
	return EnqueueCandidateBuildPlanAttempt(ctx, store, candidate, images, 1)
}

// EnqueueCandidateBuildPlanAttempt persists a complete fresh DAG while
// retaining prior attempts as immutable audit evidence.
func EnqueueCandidateBuildPlanAttempt(
	ctx context.Context,
	store scannerrelease.Persistence,
	candidate *scannerrelease.Candidate,
	images []scannerpipeline.Image,
	attempt int,
) error {
	if attempt <= 0 {
		return errors.New("candidate build attempt must be positive")
	}
	if len(images) == 0 {
		images = defaultImages()
	} else if err := validateCompleteImageSet(images, defaultImages()); err != nil {
		return fmt.Errorf("candidate image set is incomplete: %w", err)
	}
	plan, err := scannerpipeline.Default(scannerpipeline.Inputs{
		Images: images, RequireCompose: true, RequireKubernetes: true, RequireMirror: true,
	})
	if err != nil {
		return err
	}
	platforms, _ := json.Marshal(images)
	build := &scannerrelease.BuildRun{
		ID:            uuid.NewString(),
		CandidateID:   candidate.ID,
		Attempt:       attempt,
		State:         scannerrelease.BuildQueued,
		PlatformsJSON: string(platforms),
	}
	command := scannerrelease.TransitionCommand{
		Actor: candidate.Actor, Reason: "candidate build enqueued",
		PolicyRevision: candidate.PolicyRevision,
		IdempotencyKey: fmt.Sprintf("%s/build/%d", candidate.IdempotencyKey, attempt),
		PayloadJSON:    "{}",
	}
	steps := make([]scannerrelease.BuildStep, 0, len(plan.Steps))
	for index, step := range plan.Steps {
		summary, _ := json.Marshal(map[string]any{
			"kind": step.Kind, "depends_on": step.DependsOn,
			"timeout": step.Timeout.String(), "retryable": step.Retryable,
			"concurrency_key": step.ConcurrencyKey, "ordinal": index,
		})
		steps = append(steps, scannerrelease.BuildStep{
			ID:             uuid.NewString(),
			BuildRunID:     build.ID,
			StepKey:        step.Key,
			State:          scannerrelease.BuildQueued,
			Attempt:        1,
			SummaryJSON:    string(summary),
			RetentionClass: "candidate-evidence",
		})
	}
	return store.CreateBuildPlan(ctx, build, steps, command)
}

func (s Service) ApproveCandidate(ctx context.Context, command ApprovalCommand) (*scannerrelease.Candidate, error) {
	if command.CandidateID == "" || command.Actor == "" || command.Reason == "" || command.IdempotencyKey == "" {
		return nil, fmt.Errorf("%w: candidate, actor, reason, and idempotency key are required", ErrValidation)
	}
	candidate, err := s.Store.GetCandidate(ctx, command.CandidateID)
	if err != nil {
		return nil, err
	}
	if candidate.State != scannerrelease.CandidateAwaitingApproval {
		return nil, fmt.Errorf("%w: candidate state is %s", ErrCandidateNotReady, candidate.State)
	}
	if command.LockDigest != candidate.LockDigest ||
		command.PolicyDecisionDigest == "" ||
		command.PolicyDecisionDigest != candidate.PolicyDecision {
		return nil, ErrApprovalStale
	}
	verified, err := s.publicationVerifier().VerifyPublication(ctx, candidate, command.EvidenceDigest)
	if err != nil || verified.Digest != command.EvidenceDigest {
		return nil, fmt.Errorf("%w: approval does not reference the completed build receipt", ErrApprovalStale)
	}
	approval := &scannerrelease.Approval{
		ID:             uuid.NewString(),
		CandidateID:    candidate.ID,
		Actor:          command.Actor,
		Action:         "approve",
		Reason:         command.Reason,
		EvidenceDigest: command.EvidenceDigest,
		PolicyDecision: command.PolicyDecisionDigest,
		IdempotencyKey: command.IdempotencyKey,
	}
	if err := s.Store.AddApproval(ctx, approval); err != nil {
		return nil, err
	}
	policy, err := s.Store.GetPolicy(ctx, candidate.PolicyID)
	if err != nil {
		return nil, err
	}
	var rules scannerpolicy.Policy
	if err := json.Unmarshal([]byte(policy.RulesJSON), &rules); err != nil {
		return nil, fmt.Errorf("decode scanner policy: %w", err)
	}
	if err := rules.Normalize(); err != nil {
		return nil, fmt.Errorf("validate scanner policy: %w", err)
	}
	approvals, err := s.Store.ListApprovals(ctx, "candidate", candidate.ID)
	if err != nil {
		return nil, err
	}
	actors := make(map[string]struct{})
	for _, item := range approvals {
		if item.Action != "approve" || item.PolicyDecision != candidate.PolicyDecision {
			continue
		}
		if rules.SeparateCreator && item.Actor == candidate.Actor {
			continue
		}
		actors[item.Actor] = struct{}{}
	}
	if len(actors) < rules.RequiredApprovals {
		return candidate, nil
	}
	return s.Store.TransitionCandidate(ctx, candidate.ID, candidate.Version, scannerrelease.CandidateApproved, scannerrelease.TransitionCommand{
		Actor: command.Actor, Reason: command.Reason, PolicyRevision: candidate.PolicyRevision,
		IdempotencyKey: command.IdempotencyKey + "/transition", PayloadJSON: "{}",
	})
}

// AddCandidateException appends a complete, expiring policy exception. It
// does not mutate prior evidence or a prior decision; a subsequent trusted
// policy-evaluation step is the only component allowed to consume it.
func (s Service) AddCandidateException(
	ctx context.Context,
	command ExceptionCommand,
) (*scannerrelease.Approval, error) {
	if command.CandidateID == "" || command.Actor == "" || command.OwnerID == "" ||
		strings.TrimSpace(command.Gate) == "" || strings.TrimSpace(command.Reason) == "" ||
		strings.TrimSpace(command.CompensatingControl) == "" || command.IdempotencyKey == "" {
		return nil, fmt.Errorf(
			"%w: candidate, gate, owner, reason, compensating control, actor, and idempotency key are required",
			ErrValidation,
		)
	}
	if !digestPattern.MatchString(command.EvidenceDigest) {
		return nil, fmt.Errorf("%w: exception evidence digest is invalid", ErrValidation)
	}
	now := s.now()
	if !command.ExpiresAt.After(now) || command.ExpiresAt.After(now.Add(90*24*time.Hour)) {
		return nil, fmt.Errorf("%w: exception expiration must be within the next 90 days", ErrValidation)
	}
	if command.Actor == command.OwnerID {
		return nil, fmt.Errorf("%w: exception owner and approver must be distinct", ErrValidation)
	}
	candidate, err := s.Store.GetCandidate(ctx, command.CandidateID)
	if err != nil {
		return nil, err
	}
	switch candidate.State {
	case scannerrelease.CandidateQueued, scannerrelease.CandidateBuilding,
		scannerrelease.CandidateBlocked, scannerrelease.CandidateAwaitingApproval:
	default:
		return nil, fmt.Errorf("%w: candidate state is %s", ErrCandidateNotReady, candidate.State)
	}
	if candidate.Actor == command.Actor {
		return nil, fmt.Errorf("%w: candidate creator cannot approve its exception", ErrValidation)
	}
	policy, err := s.Store.GetPolicy(ctx, candidate.PolicyID)
	if err != nil {
		return nil, err
	}
	var rules scannerpolicy.Policy
	if err := json.Unmarshal([]byte(policy.RulesJSON), &rules); err != nil {
		return nil, fmt.Errorf("%w: decode candidate policy: %v", ErrValidation, err)
	}
	if err := rules.Normalize(); err != nil {
		return nil, fmt.Errorf("%w: validate candidate policy: %v", ErrValidation, err)
	}
	gateRequired := false
	for _, gate := range rules.RequiredGates {
		if gate == command.Gate {
			gateRequired = true
			break
		}
	}
	if !gateRequired {
		return nil, fmt.Errorf("%w: exception gate %q is not required by the candidate policy", ErrValidation, command.Gate)
	}
	if !scannerpolicy.ExceptionEligible(command.Gate) {
		return nil, fmt.Errorf("%w: hard gate %q cannot be bypassed", ErrValidation, command.Gate)
	}
	if !rules.AllowExceptions[command.Gate] {
		return nil, fmt.Errorf("%w: policy does not allow exceptions for gate %q", ErrValidation, command.Gate)
	}
	if rules.ExceptionMaxAge <= 0 || command.ExpiresAt.After(now.Add(rules.ExceptionMaxAge)) {
		return nil, fmt.Errorf(
			"%w: exception expiration exceeds policy maximum age %s",
			ErrValidation, rules.ExceptionMaxAge,
		)
	}
	expires := command.ExpiresAt.UTC()
	approval := &scannerrelease.Approval{
		ID: uuid.NewString(), CandidateID: candidate.ID, Actor: command.Actor,
		Action: "exception", Reason: strings.TrimSpace(command.Reason),
		ExceptionScope: strings.TrimSpace(command.Gate), ExceptionOwner: command.OwnerID,
		CompensatingControl: strings.TrimSpace(command.CompensatingControl),
		EvidenceDigest:      command.EvidenceDigest, PolicyDecision: candidate.PolicyDecision,
		ExpiresAt: &expires, IdempotencyKey: command.IdempotencyKey, CreatedAt: now,
	}
	if err := s.Store.AddApproval(ctx, approval); err != nil {
		return nil, err
	}
	return approval, nil
}

func (s Service) RejectCandidate(ctx context.Context, command ApprovalCommand) (*scannerrelease.Candidate, error) {
	if command.CandidateID == "" || command.Actor == "" || command.Reason == "" || command.IdempotencyKey == "" {
		return nil, fmt.Errorf("%w: candidate, actor, reason, and idempotency key are required", ErrValidation)
	}
	candidate, err := s.Store.GetCandidate(ctx, command.CandidateID)
	if err != nil {
		return nil, err
	}
	if candidate.State == scannerrelease.CandidatePublished ||
		candidate.State == scannerrelease.CandidateRejected ||
		candidate.State == scannerrelease.CandidateFailed {
		return nil, fmt.Errorf("%w: candidate state is %s", ErrCandidateNotReady, candidate.State)
	}
	approval := &scannerrelease.Approval{
		ID:             uuid.NewString(),
		CandidateID:    candidate.ID,
		Actor:          command.Actor,
		Action:         "reject",
		Reason:         command.Reason,
		EvidenceDigest: command.EvidenceDigest,
		PolicyDecision: candidate.PolicyDecision,
		IdempotencyKey: command.IdempotencyKey,
	}
	if err := s.Store.AddApproval(ctx, approval); err != nil {
		return nil, err
	}
	return s.Store.TransitionCandidate(ctx, candidate.ID, candidate.Version, scannerrelease.CandidateRejected, scannerrelease.TransitionCommand{
		Actor: command.Actor, Reason: command.Reason, PolicyRevision: candidate.PolicyRevision,
		IdempotencyKey: command.IdempotencyKey + "/transition", PayloadJSON: "{}",
	})
}

func (s Service) PromoteRelease(ctx context.Context, command RolloutCommand) (*scannerrelease.Rollout, error) {
	if command.ReleaseID == "" || command.Target == "" || command.Actor == "" ||
		command.Reason == "" || command.IdempotencyKey == "" {
		return nil, fmt.Errorf("%w: release, target, actor, reason, and idempotency key are required", ErrValidation)
	}
	release, err := s.Store.GetRelease(ctx, command.ReleaseID)
	if err != nil {
		return nil, err
	}
	if release.State == scannerrelease.ReleaseRevoked ||
		release.State == scannerrelease.ReleaseDeprecated {
		return nil, fmt.Errorf("%w: release state is %s", ErrReleaseUnavailable, release.State)
	}
	policy, err := s.EnsureDefaultPolicy(ctx, command.Actor)
	if err != nil {
		return nil, err
	}
	fromReleaseID := ""
	stable, err := s.Store.ListReleases(ctx, scannerrelease.ReleaseFilter{
		State: scannerrelease.ReleaseStable,
	}, scannerrelease.PageRequest{Limit: 100})
	if err != nil {
		return nil, err
	}
	for _, existing := range stable.Items {
		if existing.ID != release.ID && existing.PublishedAt.After(time.Time{}) {
			if fromReleaseID == "" {
				fromReleaseID = existing.ID
			}
		}
	}
	if command.Strategy == "" {
		command.Strategy = "canary_then_stable"
	}
	policySnapshot, _ := json.Marshal(policy)
	rollout := &scannerrelease.Rollout{
		ID:                 uuid.NewString(),
		Target:             command.Target,
		FromReleaseID:      fromReleaseID,
		ToReleaseID:        release.ID,
		Strategy:           command.Strategy,
		State:              scannerrelease.RolloutPending,
		PolicySnapshotJSON: string(policySnapshot),
		Actor:              command.Actor,
		IdempotencyKey:     command.IdempotencyKey,
	}
	cohorts := []scannerrelease.RolloutCohort{
		{
			ID: uuid.NewString(), RolloutID: rollout.ID, Name: "canary", Ordinal: 0,
			DesiredReleaseID: release.ID, State: "pending", HealthSummaryJSON: "{}",
		},
		{
			ID: uuid.NewString(), RolloutID: rollout.ID, Name: "stable", Ordinal: 1,
			DesiredReleaseID: release.ID, State: "pending", HealthSummaryJSON: "{}",
		},
	}
	if err := s.Store.CreateRollout(ctx, rollout, cohorts, scannerrelease.TransitionCommand{
		Actor: command.Actor, Reason: command.Reason, PolicyRevision: policy.Revision,
		IdempotencyKey: command.IdempotencyKey, PayloadJSON: "{}",
	}); err != nil {
		return nil, err
	}
	return rollout, nil
}

// PublishCandidate records an immutable release inventory and advances the
// exact approved candidate. Registry upload, signing, and re-read verification
// happen in the build worker before this command; the required digests make
// that evidence explicit rather than trusting a mutable tag.
func (s Service) PublishCandidate(ctx context.Context, command PublicationCommand) (*scannerrelease.Release, error) {
	if command.CandidateID == "" || command.ReceiptDigest == "" || command.Actor == "" ||
		command.Reason == "" || command.IdempotencyKey == "" {
		return nil, fmt.Errorf("%w: candidate, receipt digest, actor, reason, and idempotency key are required", ErrValidation)
	}
	candidate, err := s.Store.GetCandidate(ctx, command.CandidateID)
	if err != nil {
		return nil, err
	}
	if candidate.State != scannerrelease.CandidateApproved &&
		candidate.State != scannerrelease.CandidatePublishing &&
		candidate.State != scannerrelease.CandidatePublished {
		return nil, fmt.Errorf("%w: candidate state is %s", ErrCandidateNotReady, candidate.State)
	}
	verified, err := s.publicationVerifier().VerifyPublication(ctx, candidate, command.ReceiptDigest)
	if err != nil {
		return nil, err
	}
	receipt := verified.Receipt
	if err := validatePublication(candidate, command.Name, receipt); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrValidation, err)
	}
	approvals, err := s.Store.ListApprovals(ctx, "candidate", candidate.ID)
	if err != nil {
		return nil, err
	}
	boundApproval := false
	for _, approval := range approvals {
		if approval.Action == "approve" &&
			approval.PolicyDecision == candidate.PolicyDecision &&
			approval.EvidenceDigest == verified.Digest {
			boundApproval = true
			break
		}
	}
	if !boundApproval {
		return nil, fmt.Errorf("%w: candidate has no approval bound to receipt %s", ErrApprovalStale, verified.Digest)
	}
	publicationTime := s.now()
	release := scannerrelease.Release{
		ID:               uuid.NewString(),
		Name:             command.Name,
		CandidateID:      candidate.ID,
		LockDigest:       candidate.LockDigest,
		ManifestDigest:   receipt.ManifestDigest,
		ManifestURI:      receipt.ManifestURI,
		State:            scannerrelease.ReleasePublished,
		SignerIdentity:   receipt.SignerIdentity,
		PolicyID:         candidate.PolicyID,
		PolicyRevision:   candidate.PolicyRevision,
		DefinitionCommit: scannerrelease.EffectiveDefinitionCommit(candidate),
		Protected:        true,
		RollbackEligible: true,
		RetentionClass:   "published",
		PublishedAt:      publicationTime,
		CreatedAt:        publicationTime,
		UpdatedAt:        publicationTime,
	}
	inventory := &scannerrelease.ReleaseInventory{
		Release: release, Tools: receipt.Tools, Images: receipt.Images, Artifacts: receipt.Artifacts,
	}
	persisted, err := s.Store.CommitCandidatePublication(
		ctx, candidate.ID, candidate.Version, inventory, scannerrelease.TransitionCommand{
			Actor: command.Actor, Reason: command.Reason, PolicyRevision: candidate.PolicyRevision,
			IdempotencyKey: command.IdempotencyKey, PayloadJSON: "{}",
		},
	)
	if err != nil {
		return nil, fmt.Errorf("atomically publish scanner release: %w", err)
	}
	return persisted, nil
}

func validatePublication(
	candidate *scannerrelease.Candidate,
	name string,
	receipt scannerrelease.PublicationReceipt,
) error {
	if err := scannerrelease.ValidatePublicationReceiptInventory(receipt); err != nil {
		return err
	}
	switch {
	case name != "" && !releaseNamePattern.MatchString(name):
		return errors.New("release name must use scanner-set-YYYY.WW.N")
	case !digestPattern.MatchString(candidate.LockDigest):
		return errors.New("candidate lock digest is invalid")
	case !digestPattern.MatchString(candidate.PolicyDecision):
		return errors.New("candidate policy decision digest is invalid")
	case !digestPattern.MatchString(receipt.ManifestDigest):
		return errors.New("release manifest digest is invalid")
	case !strings.Contains(receipt.ManifestURI, receipt.ManifestDigest):
		return errors.New("release manifest URI must contain its immutable digest")
	case strings.TrimSpace(receipt.SignerIdentity) == "":
		return errors.New("release signer identity is required")
	case len(receipt.Tools) != len(requiredReleaseToolKeys):
		return fmt.Errorf("release inventory must contain exactly %d tools", len(requiredReleaseToolKeys))
	case len(receipt.Images) == 0:
		return errors.New("release inventory must contain images")
	}
	runtimeImageKeys := make(map[string]struct{})
	imageRecords := make(map[string]struct{})
	imageIdentities := make(map[string]string)
	hasDefault := false
	for index, image := range receipt.Images {
		recordKey := image.RegistryTargetID + "\x00" + image.ImageKey
		if _, duplicate := imageRecords[recordKey]; duplicate {
			return fmt.Errorf("release image %q is duplicated for registry target %q", image.ImageKey, image.RegistryTargetID)
		}
		imageRecords[recordKey] = struct{}{}
		if image.ImageKey == "default" && scannerrelease.IsRuntimeScannerImage(image) {
			hasDefault = true
		}
		if scannerrelease.IsRuntimeScannerImage(image) {
			runtimeImageKeys[image.ImageKey] = struct{}{}
		}
		switch {
		case image.ImageKey == "" || image.RegistryTargetID == "" || image.Repository == "":
			return fmt.Errorf("release image %d identity is incomplete", index)
		case scannerrelease.NormalizedImageKind(image) != scannerrelease.ReleaseImageScanner &&
			scannerrelease.NormalizedImageKind(image) != scannerrelease.ReleaseImageFixer:
			return fmt.Errorf("release image %q kind must be scanner or fixer", image.ImageKey)
		case !digestPattern.MatchString(image.Digest):
			return fmt.Errorf("release image %q digest is invalid", image.ImageKey)
		case image.SignatureStatus != "verified":
			return fmt.Errorf("release image %q signature is not verified", image.ImageKey)
		case !digestPattern.MatchString(image.ProvenanceDigest):
			return fmt.Errorf("release image %q provenance digest is invalid", image.ImageKey)
		case !digestPattern.MatchString(image.SBOMDigest):
			return fmt.Errorf("release image %q SBOM digest is invalid", image.ImageKey)
		case image.SizeBytes < 0:
			return fmt.Errorf("release image %q size is invalid", image.ImageKey)
		}
		var platforms map[string]string
		if err := json.Unmarshal([]byte(image.PlatformDigests), &platforms); err != nil || len(platforms) == 0 {
			return fmt.Errorf("release image %q platform digests are invalid", image.ImageKey)
		}
		for platform, digest := range platforms {
			if !strings.HasPrefix(platform, "linux/") || !digestPattern.MatchString(digest) {
				return fmt.Errorf("release image %q platform %q digest is invalid", image.ImageKey, platform)
			}
		}
		canonicalPlatforms, _ := json.Marshal(platforms)
		identity := scannerrelease.NormalizedImageKind(image) + "\x00" + image.Digest + "\x00" + string(canonicalPlatforms)
		if existing := imageIdentities[image.ImageKey]; existing != "" && existing != identity {
			return fmt.Errorf("release image %q differs across registry targets", image.ImageKey)
		}
		imageIdentities[image.ImageKey] = identity
	}
	if !hasDefault {
		return errors.New("release inventory must contain a default image")
	}
	for key, expected := range requiredOwnedReleaseImages() {
		identity, exists := imageIdentities[key]
		if !exists {
			return fmt.Errorf("release inventory is missing required owned image %q", key)
		}
		parts := strings.SplitN(identity, "\x00", 3)
		if parts[0] != string(expected.Kind) {
			return fmt.Errorf("release image %q has kind %q, expected %q", key, parts[0], expected.Kind)
		}
		var platforms map[string]string
		if err := json.Unmarshal([]byte(parts[2]), &platforms); err != nil {
			return fmt.Errorf("release image %q platform identity is invalid", key)
		}
		if len(platforms) != len(expected.Platforms) {
			return fmt.Errorf("release image %q does not cover its complete platform set", key)
		}
		for _, platform := range expected.Platforms {
			if platforms[platform] == "" {
				return fmt.Errorf("release image %q is missing platform %q", key, platform)
			}
		}
	}
	toolKeys := make(map[string]struct{}, len(receipt.Tools))
	for index, tool := range receipt.Tools {
		if tool.ToolKey == "" || tool.Version == "" || tool.SourceReference == "" ||
			tool.ParserCompatibility == "" {
			return fmt.Errorf("release tool %d identity or compatibility is incomplete", index)
		}
		if _, duplicate := toolKeys[tool.ToolKey]; duplicate {
			return fmt.Errorf("release tool %q is duplicated", tool.ToolKey)
		}
		toolKeys[tool.ToolKey] = struct{}{}
		var metadata struct {
			ImageKey string `json:"image_key"`
			Kind     string `json:"kind"`
		}
		if err := json.Unmarshal([]byte(tool.MetadataJSON), &metadata); err != nil {
			return fmt.Errorf("release tool %q metadata is invalid", tool.ToolKey)
		}
		if _, exists := runtimeImageKeys[metadata.ImageKey]; !exists {
			return fmt.Errorf("release tool %q references absent scanner runtime image %q", tool.ToolKey, metadata.ImageKey)
		}
		if metadata.Kind != "wolf" && metadata.Kind != "upstream" {
			return fmt.Errorf("release tool %q image kind is invalid", tool.ToolKey)
		}
	}
	for _, key := range requiredReleaseToolKeys {
		if _, exists := toolKeys[key]; !exists {
			return fmt.Errorf("release inventory is missing required tool %q", key)
		}
	}
	for index, artifact := range receipt.Artifacts {
		if artifact.ArtifactType == "" || artifact.MediaType == "" || artifact.URI == "" ||
			!digestPattern.MatchString(artifact.Digest) || artifact.SizeBytes < 0 {
			return fmt.Errorf("release artifact %d is incomplete or invalid", index)
		}
	}
	return nil
}

var requiredReleaseToolKeys = []string{
	"bandit", "bearer", "brakeman", "cargo-audit", "cargo-deny", "checkov", "clippy", "codeql",
	"conftest", "cppcheck", "detect-secrets", "detekt", "dockle", "eslint", "gitleaks", "gokart",
	"gosec", "govulncheck", "grype", "hadolint", "infer", "kics", "kube-linter", "kubescape",
	"markdownlint", "mypy", "npm-audit", "nuclei", "osv-scanner", "phpstan", "pip-audit", "pluto",
	"pmd", "poutine", "radon", "renovate", "rubocop", "ruff", "scorecard", "semgrep", "shellcheck",
	"spectral", "sqlfluff", "staticcheck", "swiftlint", "syft", "tflint", "trivy", "trufflehog",
	"vale", "vulture", "yamllint", "zizmor",
}

func (s Service) publicationVerifier() PublicationVerifier {
	if s.PublicationVerifier != nil {
		return s.PublicationVerifier
	}
	return DurablePublicationVerifier{Store: s.Store}
}

func requiredOwnedReleaseImages() map[string]scannerpipeline.Image {
	result := make(map[string]scannerpipeline.Image)
	for _, image := range defaultImages() {
		result[image.Key] = image
	}
	return result
}

func requiredGates(policy *scannerrelease.Policy) (string, error) {
	var rules scannerpolicy.Policy
	if err := json.Unmarshal([]byte(policy.RulesJSON), &rules); err != nil {
		return "", fmt.Errorf("decode scanner policy: %w", err)
	}
	if err := rules.Normalize(); err != nil {
		return "", fmt.Errorf("validate scanner policy: %w", err)
	}
	value, err := json.Marshal(rules.RequiredGates)
	return string(value), err
}

func defaultImages() []scannerpipeline.Image {
	return []scannerpipeline.Image{
		{Key: "default", Kind: scannerpipeline.ImageKindScanner, Platforms: []string{"linux/amd64", "linux/arm64"}},
		{Key: "jvm", Kind: scannerpipeline.ImageKindScanner, Platforms: []string{"linux/amd64", "linux/arm64"}},
		{Key: "rust", Kind: scannerpipeline.ImageKindScanner, Platforms: []string{"linux/amd64", "linux/arm64"}},
		{Key: "codeql", Kind: scannerpipeline.ImageKindScanner, Platforms: []string{"linux/amd64"}},
		{Key: "fixer-base", Kind: scannerpipeline.ImageKindFixer, Platforms: []string{"linux/amd64", "linux/arm64"}},
		{
			Key: "fixer-api", Kind: scannerpipeline.ImageKindFixer,
			Platforms: []string{"linux/amd64", "linux/arm64"}, DependsOn: []string{"fixer-base"},
		},
		{
			Key: "fixer-claude", Kind: scannerpipeline.ImageKindFixer,
			Platforms: []string{"linux/amd64", "linux/arm64"}, DependsOn: []string{"fixer-base"},
		},
		{
			Key: "fixer-codex", Kind: scannerpipeline.ImageKindFixer,
			Platforms: []string{"linux/amd64", "linux/arm64"}, DependsOn: []string{"fixer-base"},
		},
	}
}

func validateCompleteImageSet(actual, expected []scannerpipeline.Image) error {
	normalize := func(images []scannerpipeline.Image) map[string]scannerpipeline.Image {
		result := make(map[string]scannerpipeline.Image, len(images))
		for _, image := range images {
			if image.Kind == "" {
				image.Kind = scannerpipeline.ImageKindScanner
			}
			image.Platforms = append([]string(nil), image.Platforms...)
			image.DependsOn = append([]string(nil), image.DependsOn...)
			sort.Strings(image.Platforms)
			sort.Strings(image.DependsOn)
			result[image.Key] = image
		}
		return result
	}
	got := normalize(actual)
	want := normalize(expected)
	if len(got) != len(want) || len(actual) != len(got) {
		return fmt.Errorf("expected %d unique images, got %d", len(want), len(got))
	}
	for key, expectedImage := range want {
		actualImage, exists := got[key]
		if !exists {
			return fmt.Errorf("required image %q is missing", key)
		}
		if actualImage.Kind != expectedImage.Kind ||
			strings.Join(actualImage.Platforms, ",") != strings.Join(expectedImage.Platforms, ",") ||
			strings.Join(actualImage.DependsOn, ",") != strings.Join(expectedImage.DependsOn, ",") {
			return fmt.Errorf("image %q kind, platform set, or dependency set differs from the release policy", key)
		}
	}
	return nil
}

func (s Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
