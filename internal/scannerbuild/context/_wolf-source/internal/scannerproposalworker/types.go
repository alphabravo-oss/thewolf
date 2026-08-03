// Package scannerproposalworker turns durable awaiting-definition candidates
// into immutable, buildable scanner-set proposals.
package scannerproposalworker

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/alphabravocompany/thewolf/internal/scannerobservability"
	"github.com/alphabravocompany/thewolf/internal/scannerpipeline"
	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

var (
	ErrProposalRaceLost = errors.New("scanner proposal ownership was won by another worker")
	ErrDrainDeadline    = errors.New("scanner proposal graceful drain deadline exceeded")
)

type Request struct {
	CandidateID      string           `json:"candidate_id"`
	DefinitionCommit string           `json:"definition_commit"`
	Selection        json.RawMessage  `json:"selection"`
	Updates          []SelectedUpdate `json:"updates,omitempty"`
	RiskSummary      json.RawMessage  `json:"risk_summary"`
	RequiredGates    []string         `json:"required_gates"`
	SourceDateEpoch  int64            `json:"source_date_epoch"`
	ExpectedHead     string           `json:"expected_branch_head,omitempty"`
	PolicyID         string           `json:"policy_id"`
	PolicyRevision   int64            `json:"policy_revision"`
	IdempotencyKey   string           `json:"idempotency_key"`
}

// SelectedUpdate is a redacted, server-resolved discovery result. Proposal
// executors receive these values instead of resolving client-supplied IDs or
// consulting the release database with broader credentials.
type SelectedUpdate struct {
	ID              string          `json:"id"`
	ComponentType   string          `json:"component_type"`
	ComponentName   string          `json:"component_name"`
	CurrentValue    string          `json:"current_value"`
	AvailableValue  string          `json:"available_value"`
	AvailableDigest string          `json:"available_digest,omitempty"`
	RiskClass       string          `json:"risk_class"`
	Evidence        json.RawMessage `json:"evidence"`
	Compatibility   json.RawMessage `json:"compatibility"`
}

type Result struct {
	ProposedCommit string                  `json:"proposed_commit"`
	ProposalURL    string                  `json:"proposal_url,omitempty"`
	LockDigest     string                  `json:"lock_digest"`
	LockURI        string                  `json:"lock_uri"`
	RiskSummary    json.RawMessage         `json:"risk_summary"`
	Images         []scannerpipeline.Image `json:"images,omitempty"`
}

type Proposer interface {
	Propose(context.Context, Request) (Result, error)
}

type Config struct {
	Store             scannerrelease.Persistence
	Proposer          Proposer
	WorkerID          string
	PollInterval      time.Duration
	HeartbeatInterval time.Duration
	LeaseDuration     time.Duration
	DrainTimeout      time.Duration
	Once              bool
	Observer          scannerobservability.Observer
	Now               func() time.Time
	Sleep             func(context.Context, time.Duration) error
}
