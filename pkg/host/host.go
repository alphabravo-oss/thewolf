// Package host is the overlay-safe process entry for serving Community Wolf.
// Overlay binaries call ListenAndServe after registering edition modules.
package host

import (
	"context"

	"github.com/alphabravocompany/thewolf/internal/serve"
)

// Options is the public serve configuration. Overlay must not import internal/serve.
type Options struct {
	Addr         string
	SkipScanInit bool
	APIOnly      bool
	Version      string
	Commit       string
	BuildDate    string
}

func ListenAndServe(ctx context.Context, addr string) error {
	return Serve(ctx, Options{Addr: addr})
}

func Serve(ctx context.Context, opt Options) error {
	return serve.Run(ctx, serve.Options{
		Addr: opt.Addr, SkipScanInit: opt.SkipScanInit, APIOnly: opt.APIOnly,
		Version: opt.Version, Commit: opt.Commit, BuildDate: opt.BuildDate,
	})
}
