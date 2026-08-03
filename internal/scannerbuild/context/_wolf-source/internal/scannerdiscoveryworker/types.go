// Package scannerdiscoveryworker connects the persistence-neutral discovery
// engine to the durable scanner release queue.
package scannerdiscoveryworker

import (
	"context"
	"errors"
	"time"

	"github.com/alphabravocompany/thewolf/internal/scannerdiscovery"
	"github.com/alphabravocompany/thewolf/internal/scannerobservability"
	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

const (
	defaultPollInterval      = 2 * time.Second
	defaultHeartbeatInterval = 10 * time.Second
	defaultLeaseDuration     = 45 * time.Second
	defaultDrainTimeout      = time.Minute
)

var (
	ErrLeaseLost             = errors.New("scanner discovery lease lost")
	ErrCancellationRequested = errors.New("scanner discovery cancellation requested")
	ErrDrainDeadline         = errors.New("scanner discovery graceful drain deadline exceeded")
)

// Runner is injected so queue lifecycle tests never need network access.
// Production uses EngineRunner; managed or air-gapped deployments can provide
// a runner backed by another resolver set without changing persistence.
type Runner interface {
	Discover(context.Context, scannerrelease.DiscoveryRun) (scannerdiscovery.Run, error)
}

type Config struct {
	Store             scannerrelease.DiscoveryRepository
	Runner            Runner
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

func sleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
