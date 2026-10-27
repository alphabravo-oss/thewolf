package routes

import (
	"context"

	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/fix"
	"github.com/alphabravocompany/thewolf/pkg/entitlement"
)

// autofixEnabled reports whether the autonomous fix engine is turned on. The
// entire fix-execution surface (job enqueue, the worker, the UI actions) is
// gated by this single setting, which defaults to "false" (migration 021).
// It also requires the enterprise.remediation entitlement: AI remediation is an
// Enterprise feature. Mirrors fleetModeEnabled. On error, absence or a missing
// entitlement, returns false — fail safe.
func autofixEnabled(ctx context.Context, store db.Store) bool {
	return entitlement.Active().Allows(entitlement.Remediation) && fix.CheckEnabled(ctx, store) == nil
}
