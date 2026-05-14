package models

import (
	"context"
	"time"
)

// Plugin defines the interface that all analysis tool plugins must implement.
//
// After the containerization migration (PLAN.md), CheckAvailable should
// return true iff the wolf-scanners image is locally available. Plugins no
// longer probe the host PATH.
type Plugin interface {
	Name() string
	Category() Category
	Languages() []Language
	CheckAvailable() bool
	Execute(ctx context.Context, opts ExecuteOpts) ([]Finding, error)
}

// ExecuteOpts contains options for plugin execution.
type ExecuteOpts struct {
	RepoPath     string
	Branch       string
	IncludePaths []string
	ExcludePaths []string
	Timeout      time.Duration
	Target       string            // URL or host for DAST scanners (nuclei, etc.)
	OnOutput     func(line string) // callback for streaming tool stderr/progress lines

	// ContainerCfg is the runtime container backend configuration. It is
	// typed as `any` here to avoid a circular import (models → container →
	// models). Plugins assert it back to `*container.Config` at the call
	// site:
	//
	//	cfg, _ := opts.ContainerCfg.(*container.Config)
	//
	// May be nil only in unit tests that drive plugins directly.
	ContainerCfg any
}
