// Package scannerreleaseworker executes durable scanner release build plans.
//
// It is intentionally separate from the code-scan worker. Release builds use
// their own queue, lease, evidence, and executor contracts so enabling this
// package cannot change existing scan execution behavior.
package scannerreleaseworker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/alphabravocompany/thewolf/internal/scannerobservability"
	"github.com/alphabravocompany/thewolf/internal/scannerpipeline"
	"github.com/alphabravocompany/thewolf/internal/scannerpolicy"
	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

var (
	ErrLeaseLost              = errors.New("scanner release build lease lost")
	ErrCancellationRequested  = errors.New("scanner release build cancellation requested")
	ErrDrainDeadline          = errors.New("scanner release worker drain deadline exceeded")
	ErrReconciliationRequired = errors.New("scanner release external operation requires reconciliation")
)

// Executor runs one already-authorized DAG step in an ephemeral workspace.
// Implementations must honor ctx cancellation and must not retain Request
// values, workspace contents, or credentials after Execute returns.
type Executor interface {
	Execute(context.Context, StepRequest) (StepResult, error)
}

// StepRequest binds execution to the immutable candidate inputs selected by
// the control plane. Lease credentials are deliberately not exposed.
type StepRequest struct {
	BuildRunID         string                        `json:"build_run_id"`
	CandidateID        string                        `json:"candidate_id"`
	BuildAttempt       int                           `json:"build_attempt"`
	Step               scannerpipeline.Step          `json:"step"`
	StepAttempt        int                           `json:"step_attempt"`
	LogicalOperationID string                        `json:"logical_operation_id"`
	Workspace          string                        `json:"workspace"`
	DefinitionCommit   string                        `json:"definition_commit"`
	LockDigest         string                        `json:"lock_digest"`
	PolicyID           string                        `json:"policy_id"`
	PolicyRevision     int64                         `json:"policy_revision"`
	PlatformsJSON      string                        `json:"platforms_json"`
	Dependencies       map[string]DependencyEvidence `json:"dependencies,omitempty"`
}

// DeriveLogicalOperationID returns the durable sink identity for one logical
// build step. StepAttempt is deliberately excluded: attempts are diagnostic
// audit records, while retries and replacement workers must reconcile the
// same external registry, Job, mirror, or signing operation.
func DeriveLogicalOperationID(request StepRequest) string {
	value, _ := json.Marshal(struct {
		BuildRunID       string `json:"build_run_id"`
		CandidateID      string `json:"candidate_id"`
		BuildAttempt     int    `json:"build_attempt"`
		Step             string `json:"step"`
		DefinitionCommit string `json:"definition_commit"`
		LockDigest       string `json:"lock_digest"`
		PolicyID         string `json:"policy_id"`
		PolicyRevision   int64  `json:"policy_revision"`
	}{
		request.BuildRunID, request.CandidateID, request.BuildAttempt,
		request.Step.Key, request.DefinitionCommit, request.LockDigest,
		request.PolicyID, request.PolicyRevision,
	})
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// DependencyEvidence is the immutable output of a completed direct DAG
// dependency. Executors receive only direct dependencies so they can bind
// derivative work (for example fixer engines) to exact upstream digests.
type DependencyEvidence struct {
	OutputURI    string `json:"output_uri,omitempty"`
	OutputDigest string `json:"output_digest,omitempty"`
}

// Verification is executor-supplied evidence for trust-boundary steps. The
// worker checks it against the candidate snapshot before accepting the step.
type Verification struct {
	DefinitionCommit     string `json:"definition_commit,omitempty"`
	LockDigest           string `json:"lock_digest,omitempty"`
	PolicyID             string `json:"policy_id,omitempty"`
	PolicyRevision       int64  `json:"policy_revision,omitempty"`
	PolicyDecisionDigest string `json:"policy_decision_digest,omitempty"`
}

// StepResult is persistence-safe structured evidence. Summary is recursively
// redacted before storage. OutputDigest, when present, must be sha256.
type StepResult struct {
	OutputURI      string         `json:"output_uri,omitempty"`
	OutputDigest   string         `json:"output_digest,omitempty"`
	Summary        map[string]any `json:"summary,omitempty"`
	RetentionClass string         `json:"retention_class,omitempty"`
	RetainUntil    *time.Time     `json:"retain_until,omitempty"`
	Protected      bool           `json:"protected,omitempty"`
	Verification   Verification   `json:"verification,omitempty"`
	// PolicyInput is required only for the policy-evaluation step. The
	// executor reports normalized gate evidence; the trusted worker evaluates
	// it against the immutable policy snapshot and overwrites PolicyDecision.
	PolicyInput    *PolicyInput            `json:"policy_input,omitempty"`
	PolicyDecision *scannerpolicy.Decision `json:"policy_decision,omitempty"`
}

// PolicyInput is the bounded, schema-checked portion of build evidence used
// for an authorization decision. Large raw reports remain content-addressed
// step artifacts and are represented here only by gate evidence digests.
type PolicyInput struct {
	Risk       scannerpolicy.Risk        `json:"risk"`
	Changes    []scannerpolicy.Change    `json:"changes"`
	Gates      []scannerpolicy.Gate      `json:"gates"`
	Exceptions []scannerpolicy.Exception `json:"exceptions,omitempty"`
	Evidence   *scannerpolicy.Evidence   `json:"evidence,omitempty"`
}

// Config controls one release worker replica.
type Config struct {
	Store              scannerrelease.Persistence
	Executor           Executor
	WorkerID           string
	SupportedPlatforms []string
	MaxParallelSteps   int
	MaxStepAttempts    int
	PollInterval       time.Duration
	HeartbeatInterval  time.Duration
	LeaseDuration      time.Duration
	DrainTimeout       time.Duration
	WorkspaceRoot      string
	Once               bool
	Observer           scannerobservability.Observer

	// Test hooks. Production callers leave these nil.
	Now       func() time.Time
	Sleep     func(context.Context, time.Duration) error
	TempDir   func(string, string) (string, error)
	RemoveAll func(string) error
}
