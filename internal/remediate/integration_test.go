//go:build integration

package remediate

import (
	"context"
	"os"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/remediate/driver"
)

// TestRealOpenCodeRun exercises one genuine opencode run against a fixture
// repo, driving the pinned opencode-ai@1.18.11 CLI end to end (see
// docs/superpowers/specs/2026-08-03-opencode-spike-findings.md for what the
// spike validated: step_finish as the turn signal, OPENCODE_CONFIG as the
// permission-document env var, and that opencode run hangs unless stdin is
// closed — driver.NewExec already accounts for all three). Skipped unless
// both credential and image are present, so CI without them stays green.
//
// Run with: go test -tags integration ./internal/remediate/ -run TestRealOpenCodeRun -v
func TestRealOpenCodeRun(t *testing.T) {
	auth := os.Getenv("WOLF_TEST_OPENCODE_AUTH")
	if auth == "" {
		t.Skip("WOLF_TEST_OPENCODE_AUTH not set")
	}
	image := os.Getenv("WOLF_TEST_OPENCODE_IMAGE")
	if image == "" {
		t.Skip("WOLF_TEST_OPENCODE_IMAGE not set")
	}

	d := driver.NewExec(driver.ExecConfig{Image: image})
	p, usage, err := d.Plan(context.Background(), driver.PlanRequest{
		WorktreePath: t.TempDir(),
		MaxTurns:     5,
		AuthContent:  auth,
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if usage.Turns == 0 {
		t.Error("Usage.Turns = 0 — the meter did not recognize any turn")
	}
	if p == nil || len(p.Items) == 0 {
		t.Error("plan is empty")
	}
}
