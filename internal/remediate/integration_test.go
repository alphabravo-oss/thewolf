//go:build integration

package remediate

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/remediate/driver"
)

// setupFixtureRepo creates a throwaway git repository with one obvious flaw
// committed to it — a real repo for a real agent to triage, not an empty
// directory. Without a git history and a real finding to reason about, the
// agent has nothing to triage: gitCommonDirMount silently no-ops on a
// worktree with no .git (see exec.go), so an empty dir would not even
// surface as a setup error, and a correct agent given zero findings would
// legitimately return an empty plan or prose — failing this test for
// reasons that have nothing to do with the driver code it's meant to cover.
func setupFixtureRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Wolf Integration Test")

	// A plain, unambiguous SQL injection sink: string-concatenated user
	// input into a query. Any triage-capable agent should recognize it
	// without needing project context beyond this one file.
	const vulnerable = `package main

import (
	"database/sql"
	"fmt"
)

func buildQuery(db *sql.DB, username string) (*sql.Rows, error) {
	query := "SELECT * FROM users WHERE username = '" + username + "'"
	return db.Query(query)
}

func main() {
	fmt.Println("fixture")
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(vulnerable), 0o644); err != nil {
		t.Fatalf("write fixture file: %v", err)
	}
	run("add", "main.go")
	run("commit", "-m", "seed fixture with a SQL injection sink")
	return dir
}

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
		WorktreePath: setupFixtureRepo(t),
		MaxTurns:     5,
		AuthContent:  auth,
		Findings: []models.Finding{{
			ID:        "f-1",
			Severity:  models.SeverityHigh,
			Category:  models.CategorySAST,
			Title:     "SQL injection via string concatenation",
			FilePath:  "main.go",
			LineStart: 9,
			LineEnd:   9,
		}},
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
