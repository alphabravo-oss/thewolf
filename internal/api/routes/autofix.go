package routes

import (
	"context"

	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/fix"
)

// autofixEnabled reports whether the autonomous fix engine is turned on. The
// entire fix-execution surface (job enqueue, the worker, the UI actions) is
// gated by this single setting, which defaults to "false" (migration 021).
// Mirrors fleetModeEnabled. On error or absence, returns false — fail safe.
func autofixEnabled(ctx context.Context, store db.Store) bool {
	return fix.CheckEnabled(ctx, store) == nil
}
