// Package driver runs the two phases of a remediation session. The Driver
// boundary exists so session orchestration is testable without containers.
package driver

import (
	"context"
	"errors"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/remediate/meter"
	"github.com/alphabravocompany/thewolf/internal/remediate/plan"
)

// ErrBudgetExhausted is returned when a run hits its turn budget before
// producing a usable result.
var ErrBudgetExhausted = errors.New("turn budget exhausted")

// ErrUnparseablePlan is wrapped into Plan's error when the agent's output
// could not be parsed as a plan.Plan — the agent ran, spent turns, and
// produced text, but that text was not the JSON object triagePrompt asked
// for. Session's retry logic checks for this via errors.Is rather than
// matching on the error string, so it does not silently stop retrying if the
// wrapped message ever changes wording.
var ErrUnparseablePlan = errors.New("unparseable plan")

// PlanRequest is the read-only triage run.
type PlanRequest struct {
	WorktreePath string
	Findings     []models.Finding
	MaxTurns     int
	Provider     string
	Model        string
	// AuthContent is the OPENCODE_AUTH_CONTENT payload. It is passed by
	// environment only — never as a command-line argument.
	AuthContent string
	// RepairHint, when non-empty, is appended to the triage prompt — a
	// second attempt after the agent's first response failed to parse as a
	// plan, telling it plainly what went wrong instead of silently repeating
	// the exact same prompt and risking the exact same malformed output.
	RepairHint string
	// OnEvent, when set, receives every decoded event for persistence and SSE.
	OnEvent func(meter.Event)
}

// ExecuteRequest is the scoped-write fix run.
type ExecuteRequest struct {
	WorktreePath string
	Plan         *plan.Plan
	Findings     []models.Finding
	MaxTurns     int
	Provider     string
	Model        string
	AuthContent  string
	OnEvent      func(meter.Event)
}

// Patch is one commit the agent produced.
type Patch struct {
	CommitSHA    string
	FilesChanged []string
	FindingIDs   []string
	Message      string
	// Unattributed is true when the commit's Finding-IDs trailer was
	// missing, malformed, or named no ID in the run's actual finding set —
	// so FindingIDs is empty. This is a visibility signal, not a failure:
	// collectPatches keeps the patch (a single missed trailer must not
	// discard an otherwise-successful run) and only errors the whole
	// series when every commit in it is unattributed, which is the only
	// case that plausibly means the agent ignored the trailer contract
	// entirely rather than missing it once.
	Unattributed bool
}

// PatchSeries is the ordered output of an execute run.
type PatchSeries struct {
	Patches []Patch
}

// Driver runs the two phases of a remediation session.
type Driver interface {
	Plan(ctx context.Context, req PlanRequest) (*plan.Plan, meter.Usage, error)
	Execute(ctx context.Context, req ExecuteRequest) (*PatchSeries, meter.Usage, error)
}
