package scannernotificationworker

import (
	"context"
	"time"

	"github.com/alphabravocompany/thewolf/internal/scannernotification"
	"github.com/alphabravocompany/thewolf/internal/scannerobservability"
	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

type Dispatcher interface {
	Deliver(context.Context, scannernotification.Delivery) error
}

type Config struct {
	Store             scannerrelease.NotificationRepository
	Dispatcher        Dispatcher
	WorkerID          string
	PollInterval      time.Duration
	HeartbeatInterval time.Duration
	LeaseDuration     time.Duration
	DeliveryTimeout   time.Duration
	DrainTimeout      time.Duration
	BaseBackoff       time.Duration
	MaxBackoff        time.Duration
	Once              bool
	Observer          scannerobservability.Observer

	Now   func() time.Time
	Sleep func(context.Context, time.Duration) error
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
