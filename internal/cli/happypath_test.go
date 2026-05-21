package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/secrets"
)

// mustRun runs a CLI command and fails the test on any error.
func mustRun(t *testing.T, args ...string) string {
	t.Helper()
	out, err := run(t, args...)
	if err != nil {
		t.Fatalf("`wolf %s` failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

// softRun runs a command that is data-dependent: success or a well-formed
// API error is acceptable, but a crash or non-API error is a failure.
func softRun(t *testing.T, args ...string) {
	t.Helper()
	if _, err := run(t, args...); err != nil {
		if _, ok := err.(*APIError); !ok {
			t.Fatalf("`wolf %s` crashed: %v", strings.Join(args, " "), err)
		}
	}
}

// dataID extracts data.id from a JSON CLI response.
func dataID(t *testing.T, out string) string {
	t.Helper()
	var r struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil || r.Data.ID == "" {
		t.Fatalf("expected data.id in response: %v\n%s", err, out)
	}
	return r.Data.ID
}

// TestHappyPathRepoAndCollection exercises the full repo + collection
// lifecycle end to end through the CLI.
func TestHappyPathRepoAndCollection(t *testing.T) {
	url, jwt, _, _ := startServerFull(t)
	cli := func(a ...string) []string {
		return append(a, "--server", url, "--token", jwt, "-o", "json")
	}

	repoID := dataID(t, mustRun(t, cli("repo", "create", "--name", "acme", "--path", "/tmp/acme")...))
	mustRun(t, cli("repo", "get", repoID)...)
	mustRun(t, cli("repo", "update", repoID, "--name", "acme-renamed")...)
	mustRun(t, cli("repo", "list")...)

	colID := dataID(t, mustRun(t, cli("collection", "create", "--name", "team-a")...))
	mustRun(t, cli("collection", "get", colID)...)
	mustRun(t, cli("collection", "update", colID, "--name", "team-a2", "--description", "desc")...)
	mustRun(t, cli("collection", "add-repo", colID, "--repo", repoID)...)
	mustRun(t, cli("collection", "tools", colID)...)
	mustRun(t, cli("collection", "metrics", colID)...)
	mustRun(t, cli("collection", "remove-repo", colID, "--repo", repoID)...)
	mustRun(t, cli("collection", "list")...)
	mustRun(t, cli("collection", "delete", colID)...)
	mustRun(t, cli("repo", "delete", repoID)...)
}

// TestHappyPathConfigResources exercises users, secrets, tokens, settings,
// prompts, and providers.
func TestHappyPathConfigResources(t *testing.T) {
	// Secret encryption needs a 32-byte master key.
	t.Setenv("WOLF_MASTER_KEY", strings.Repeat("ab", 32))
	if err := secrets.LoadMasterKey(); err != nil {
		t.Fatalf("LoadMasterKey: %v", err)
	}
	url, jwt, _, _ := startServerFull(t)
	cli := func(a ...string) []string {
		return append(a, "--server", url, "--token", jwt, "-o", "json")
	}

	// Users.
	userID := dataID(t, mustRun(t, cli("user", "create", "--email", "u2@example.com", "--password", "password123")...))
	mustRun(t, cli("user", "list")...)
	mustRun(t, cli("user", "delete", userID)...)

	// Secrets.
	secretID := dataID(t, mustRun(t, cli("secret", "create", "--name", "API_KEY", "--value", "s3cr3t")...))
	mustRun(t, cli("secret", "list")...)
	mustRun(t, cli("secret", "delete", secretID)...)

	// API tokens.
	tokenID := dataID(t, mustRun(t, cli("auth", "token", "create", "--name", "ci", "--scope", "read:scans")...))
	mustRun(t, cli("auth", "token", "list")...)
	mustRun(t, cli("auth", "token", "revoke", tokenID)...)

	// Settings.
	mustRun(t, cli("settings", "set", "--set", "ai_enabled=true")...)
	mustRun(t, cli("settings", "get")...)

	// AI prompts.
	softRun(t, cli("prompt", "set", "--type", "tool_assess", "--section", "system", "--content", "hello")...)
	mustRun(t, cli("prompt", "list")...)
	mustRun(t, cli("prompt", "defaults")...)
	softRun(t, cli("prompt", "preview", "--type", "tool_assess")...)

	// AI providers.
	mustRun(t, cli("provider", "list")...)
}

// TestHappyPathScanAndFindings seeds scan and finding records directly in
// the store (real scans need a container backend) and exercises every scan
// and finding read/triage command against that real data.
func TestHappyPathScanAndFindings(t *testing.T) {
	url, jwt, userID, store := startServerFull(t)
	cli := func(a ...string) []string {
		return append(a, "--server", url, "--token", jwt, "-o", "json")
	}
	ctx := context.Background()
	now := time.Now().UTC()

	repoID := dataID(t, mustRun(t, cli("repo", "create", "--name", "r", "--path", "/tmp/r")...))

	scan1 := &models.Scan{ID: uuid.New().String(), UserID: userID, RepoID: repoID, Branch: "main", Status: models.ScanStatusCompleted, CreatedAt: now, UpdatedAt: now}
	scan2 := &models.Scan{ID: uuid.New().String(), UserID: userID, RepoID: repoID, Branch: "main", Status: models.ScanStatusCompleted, CreatedAt: now, UpdatedAt: now}
	for _, s := range []*models.Scan{scan1, scan2} {
		if err := store.CreateScan(ctx, s); err != nil {
			t.Fatalf("seed scan: %v", err)
		}
	}
	finding := &models.Finding{
		ID: uuid.New().String(), ScanID: scan1.ID, RepoID: repoID,
		ToolName: "semgrep", Severity: models.SeverityHigh, Title: "SQL Injection",
		Status: models.StatusOpen, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.CreateFinding(ctx, finding); err != nil {
		t.Fatalf("seed finding: %v", err)
	}

	// Scan read commands.
	mustRun(t, cli("scan", "list")...)
	mustRun(t, cli("scan", "get", scan1.ID)...)
	mustRun(t, cli("scan", "findings", scan1.ID)...)
	mustRun(t, cli("scan", "stats", scan1.ID)...)
	mustRun(t, cli("scan", "tools", scan1.ID)...)
	mustRun(t, cli("scan", "ai-logs", scan1.ID)...)
	mustRun(t, cli("scan", "tool-summaries", scan1.ID)...)
	mustRun(t, cli("scan", "recommendations", scan1.ID)...)
	mustRun(t, cli("scan", "trends", "--repo", repoID)...)
	// Artifact-backed reads depend on on-disk scan output.
	softRun(t, cli("scan", "report", scan1.ID)...)
	softRun(t, cli("scan", "sarif", scan1.ID)...)
	softRun(t, cli("scan", "coverage", scan1.ID)...)
	softRun(t, cli("scan", "compare", scan1.ID, scan2.ID)...)

	// Cancel a pending scan.
	pending := &models.Scan{ID: uuid.New().String(), UserID: userID, RepoID: repoID, Status: models.ScanStatusPending, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateScan(ctx, pending); err != nil {
		t.Fatalf("seed pending scan: %v", err)
	}
	mustRun(t, cli("scan", "cancel", pending.ID)...)

	// Finding commands.
	mustRun(t, cli("finding", "list")...)
	mustRun(t, cli("finding", "get", finding.ID)...)
	mustRun(t, cli("finding", "set-status", finding.ID, "--status", "false_positive")...)
	mustRun(t, cli("finding", "trends")...)
	softRun(t, cli("finding", "export")...)
	softRun(t, cli("finding", "trends-export")...)
}

// TestHappyPathFixAndLoop exercises fix creation (no execution backend
// needed) and loop commands against a seeded loop record.
func TestHappyPathFixAndLoop(t *testing.T) {
	url, jwt, userID, store := startServerFull(t)
	cli := func(a ...string) []string {
		return append(a, "--server", url, "--token", jwt, "-o", "json")
	}
	ctx := context.Background()
	now := time.Now().UTC()

	repoID := dataID(t, mustRun(t, cli("repo", "create", "--name", "r", "--path", "/tmp/r")...))
	scan := &models.Scan{ID: uuid.New().String(), UserID: userID, RepoID: repoID, Status: models.ScanStatusCompleted, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateScan(ctx, scan); err != nil {
		t.Fatalf("seed scan: %v", err)
	}

	// Fix create just records a pending fix — no execution goroutine.
	fixID := dataID(t, mustRun(t, cli("fix", "create", "--scan", scan.ID)...))
	mustRun(t, cli("fix", "list")...)
	mustRun(t, cli("fix", "get", fixID)...)
	mustRun(t, cli("fix", "cancel", fixID)...)

	// Loops normally run a controller goroutine; seed one directly.
	loop := &models.Loop{ID: uuid.New().String(), UserID: userID, RepoID: repoID, Status: models.LoopStatusRunning, MaxIterations: 3, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateLoop(ctx, loop); err != nil {
		t.Fatalf("seed loop: %v", err)
	}
	mustRun(t, cli("loop", "list")...)
	mustRun(t, cli("loop", "get", loop.ID)...)
	// pause/resume/stop act on a live controller that the seeded loop lacks;
	// a graceful API error is an acceptable, proper response.
	softRun(t, cli("loop", "pause", loop.ID)...)
	softRun(t, cli("loop", "resume", loop.ID)...)
	softRun(t, cli("loop", "stop", loop.ID)...)
}

// TestHappyPathSystemAndAuth exercises system probes, audit log, and the
// remaining auth commands.
func TestHappyPathSystemAndAuth(t *testing.T) {
	// Allow-list a directory so `system browse` has a legal target.
	browseRoot := t.TempDir()
	t.Setenv("WOLF_BROWSE_ROOTS", browseRoot)
	url, jwt, _, _ := startServerFull(t)
	cli := func(a ...string) []string {
		return append(a, "--server", url, "--token", jwt, "-o", "json")
	}

	mustRun(t, cli("system", "health")...)
	mustRun(t, cli("system", "ready")...)
	mustRun(t, cli("system", "version")...)
	mustRun(t, cli("system", "setup-status")...)
	mustRun(t, cli("system", "browse", browseRoot)...)
	softRun(t, cli("system", "git-info", t.TempDir())...)

	mustRun(t, cli("audit", "list")...)
	mustRun(t, cli("auth", "whoami")...)
	mustRun(t, cli("auth", "passwd", "--current", "password123", "--new", "password456")...)
}
