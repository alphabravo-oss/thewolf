package models

import (
	"context"
	"time"
)

// Plugin defines the interface that all analysis tool plugins must implement.
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
	Target       string           // URL or host for DAST scanners (nuclei, etc.)
	OnOutput     func(line string) // callback for streaming tool stderr/progress lines
}
