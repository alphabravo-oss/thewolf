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
