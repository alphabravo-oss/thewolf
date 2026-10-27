// Package host is the overlay-safe process entry for serving Community Wolf.
// Overlay binaries call ListenAndServe after registering edition modules.
package host

import (
	"context"

	"github.com/alphabravocompany/thewolf/internal/serve"
	"github.com/alphabravocompany/thewolf/internal/wolfcli"
)

// Options is the public serve configuration. Overlay must not import internal/serve.
type Options struct {
	Addr           string
	SkipScanInit   bool
	APIOnly        bool
	Version        string
	Commit         string
	BuildDate      string
	CoreCommit     string
	OverlayVersion string
	OverlayCommit  string
}

func ListenAndServe(ctx context.Context, addr string) error {
	return Serve(ctx, Options{Addr: addr})
}

func Serve(ctx context.Context, opt Options) error {
	return serve.Run(ctx, serve.Options{
		Addr: opt.Addr, SkipScanInit: opt.SkipScanInit, APIOnly: opt.APIOnly,
		Version: opt.Version, Commit: opt.Commit, BuildDate: opt.BuildDate,
		CoreCommit: opt.CoreCommit, OverlayVersion: opt.OverlayVersion, OverlayCommit: opt.OverlayCommit,
	})
}

// CLIOptions configure the embedded wolf command tree (see Execute).
type CLIOptions = wolfcli.Options

// Execute runs the complete wolf command tree - serve, scan, every worker -
// and exits with the command's exit code on error. An overlay passes Setup
// (register edition modules, grant entitlements; it runs before every
// command), identity (overlay and core revisions) and extra Commands, so its
// binary is a full wolf superset instead of a reduced server.
func Execute(opt CLIOptions) { wolfcli.Execute(opt) }
