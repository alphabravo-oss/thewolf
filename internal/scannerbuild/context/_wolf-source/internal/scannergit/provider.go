// Package scannergit defines the narrow Git proposal boundary used by the
// scanner release factory. Definition generation is deliberately separate:
// callers hand the provider an exact base commit and complete file contents,
// while the provider owns conflict-safe branch, commit, pull-request, and
// status operations.
package scannergit

import (
	"context"
	"errors"
)

var (
	ErrConflict   = errors.New("scanner proposal branch changed")
	ErrValidation = errors.New("scanner Git proposal validation failed")
)

type File struct {
	Path    string
	Content []byte
	Mode    string
	// Delete removes Path from the proposal tree. Content must be empty.
	// Explicit deletion support keeps generated proposals complete when a
	// parity regeneration removes a file that no longer belongs in the
	// release definition.
	Delete bool
}

type Proposal struct {
	BaseBranch         string
	ExpectedBaseCommit string
	Branch             string
	ExpectedBranchHead string
	CommitMessage      string
	Title              string
	Body               string
	Files              []File
	Labels             []string
}

type ProposalResult struct {
	Branch      string `json:"branch"`
	Commit      string `json:"commit"`
	PullRequest int64  `json:"pull_request"`
	URL         string `json:"url"`
	Created     bool   `json:"created"`
}

type CommitStatus struct {
	State       string
	Context     string
	Description string
	TargetURL   string
}

type Provider interface {
	CreateProposal(context.Context, Proposal) (ProposalResult, error)
	SetCommitStatus(context.Context, string, CommitStatus) error
}
