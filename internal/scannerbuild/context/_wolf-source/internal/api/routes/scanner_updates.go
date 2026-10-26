package routes

import (
	"context"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/alphabravocompany/thewolf/internal/plugin/container"
	"github.com/alphabravocompany/thewolf/internal/wolflog"
)

// Scanner images follow a moving channel (`stable`), but a host keeps whatever
// it pulled first. Wolf checks for newer images in the background and tells
// administrators; pulling stays their decision (Settings → Scanners).
//
// ponytail: the result lives in memory, so a restart re-checks instead of
// remembering; persist it if notifications ever need read state across restarts.
var scannerUpdates struct {
	sync.Mutex
	images []string
	since  time.Time
}

// StartScannerUpdateChecks checks once shortly after startup, then every
// interval. WOLF_SCANNER_UPDATE_CHECK_INTERVAL (a Go duration) overrides the
// interval; "0" disables the check, e.g. for hosts without registry access.
func StartScannerUpdateChecks(ctx context.Context) {
	interval := 6 * time.Hour
	if raw := os.Getenv("WOLF_SCANNER_UPDATE_CHECK_INTERVAL"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			wolflog.Warn().Str("value", raw).Msg("invalid WOLF_SCANNER_UPDATE_CHECK_INTERVAL; using 6h")
		} else {
			interval = d
		}
	}
	if interval <= 0 {
		return
	}
	go func() {
		// Let scanner configuration finish initializing before the first check.
		timer := time.NewTimer(time.Minute)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				checkScannerUpdates(ctx)
				timer.Reset(interval)
			}
		}
	}()
}

func checkScannerUpdates(ctx context.Context) {
	cfg := container.Default()
	if cfg == nil {
		return // Kubernetes or no Docker: nothing local to compare.
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	var outdated []string
	for _, s := range probeScannerImages(ctx, cfg) {
		if s.UpdatesAvailable {
			outdated = append(outdated, s.Image)
		}
	}
	recordScannerUpdates(outdated, time.Now().UTC())
}

// recordScannerUpdates keeps the first-seen time while the outdated set is
// unchanged, so the notification does not jump to the top on every check.
func recordScannerUpdates(outdated []string, now time.Time) {
	slices.Sort(outdated)
	scannerUpdates.Lock()
	defer scannerUpdates.Unlock()
	if !slices.Equal(outdated, scannerUpdates.images) {
		scannerUpdates.since = now
	}
	scannerUpdates.images = outdated
}

func pendingScannerUpdates() (int, time.Time) {
	scannerUpdates.Lock()
	defer scannerUpdates.Unlock()
	return len(scannerUpdates.images), scannerUpdates.since
}
