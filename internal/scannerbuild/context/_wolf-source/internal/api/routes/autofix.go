package routes

import (
	"context"

	"github.com/alphabravocompany/thewolf/internal/db"
)

// autofixEnabled reports whether the autonomous fix engine is turned on. The
// entire fix-execution surface (job enqueue, the worker, the UI actions) is
// gated by this single setting, which defaults to "false" (migration 021).
// Mirrors fleetModeEnabled. On error or absence, returns false — fail safe.
func autofixEnabled(ctx context.Context, store db.Store) bool {
	v, err := store.GetSetting(ctx, "autofix_enabled")
	return err == nil && v == "true"
}
