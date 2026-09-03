// Package fixengine is the public Fix Engine Protocol.
//
// Harnesses stay in internal/fix. This package is the replaceable surface:
// Name/Available/Fix, with independent verification remaining authoritative
// in internal/fix/verify. Repo-local agent files are untrusted input.
package fixengine

import (
	"context"
	"time"
)

// Engine is one fix harness (Claude, Codex, OpenCode, API, ToolDefinition).
type Engine interface {
	Name() string
	Available() bool
	Fix(ctx context.Context, req Request) (*Result, error)
}

// Finding is the protocol-level finding. It is not the persistence model.
type Finding struct {
	ID       string
	Tool     string
	Title    string
	FilePath string
	RuleID   string
	Severity string
	Line     int
}

type Request struct {
	Findings     []Finding
	RepoPath     string
	Timeout      time.Duration
	Model        string
	Effort       string
	Instructions string
	Phase        string
}

type Result struct {
	Success      bool
	FilesChanged []string
	Diff         string
	Output       string
	Error        string
	EditsInPlace bool
	Skipped      bool
	SkipReason   string
}

// UntrustedRepoFiles documents the threat: CLAUDE.md, AGENTS.md, and other
// in-repo agent files are attacker-controlled. Engines must not treat them as
// Wolf policy. Verification (internal/fix/verify) is the authority.
const UntrustedRepoFiles = "in-repo agent instruction files are untrusted"

// CommunityHarnesses stay in the public tree. Enterprise adds governance,
// not a reduced harness set.
func CommunityHarnesses() []string {
	return []string{"claude-code", "codex", "opencode", "api", "api-patch", "auto"}
}
