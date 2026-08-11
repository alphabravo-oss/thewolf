package models

import (
	"context"
	"time"
)

// Plugin defines the interface that all analysis tool plugins must implement.
//
// After the containerization migration (docs/PLAN-containerized-scanner-execution.md), CheckAvailable should
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

	// OnRawOutput, when set, is called by a plugin with the verbatim bytes
	// of the tool's output (typically JSON or SARIF) before parsing. ext is
	// a hint at the canonical extension ("json", "sarif", "txt", "xml").
	// Used to persist raw tool output for audit/reprocessing without
	// requiring the runner to re-invoke the tool.
	OnRawOutput func(data []byte, ext string)

	// OnParseError reports a malformed record that a streaming parser can
	// safely skip while continuing to process later records. Callers use this
	// to distinguish a fully parsed successful run from partial data loss.
	OnParseError func(error)

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
