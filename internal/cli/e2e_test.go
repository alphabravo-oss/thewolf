package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/alphabravocompany/thewolf/internal/api"
	"github.com/alphabravocompany/thewolf/internal/auth"
	"github.com/alphabravocompany/thewolf/internal/auth/apikey"
	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/models"
)

// startServerFull brings up a real wolf API server backed by an in-memory
// DB, exposed over httptest, and returns its URL, a JWT for a fresh user,
// that user's ID, and the store (so tests can seed records directly).
func startServerFull(t *testing.T) (url, jwt, userID string, store db.Store) {
	t.Helper()
	st, err := db.NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	auth.SetJWTSecret([]byte("test-secret-key-for-jwt-signing"))
	srv := api.NewServer(st, ":0")
	ts := httptest.NewServer(srv.Router)
	t.Cleanup(ts.Close)

	c := NewClient(ts.URL, "")
	env, err := c.Do(context.Background(), "POST", "/auth/register",
		map[string]string{"email": "cli@example.com", "password": "password1234"})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	var data struct {
		AccessToken string `json:"access_token"`
		User        struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil || data.AccessToken == "" {
		t.Fatalf("register did not return a token: %v", err)
	}
	return ts.URL, data.AccessToken, data.User.ID, st
}

// startServer is the lightweight variant for tests that need only a URL
// and a JWT.
func startServer(t *testing.T) (url, jwt string) {
	t.Helper()
	u, j, _, _ := startServerFull(t)
	return u, j
}

// newRootForTest builds a root command wired with the CLI command groups.
func newRootForTest() *cobra.Command {
	root := &cobra.Command{Use: "wolf", SilenceUsage: true, SilenceErrors: true}
	AddGlobalFlags(root)
	scan := &cobra.Command{Use: "scan"}
	AddScanSubcommands(scan)
	root.AddCommand(scan)
	root.AddCommand(NewCommandGroups()...)
	return root
}

// run executes the CLI with args and returns stdout and any error.
func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := newRootForTest()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(args)
	err := root.Execute()
	return buf.String(), err
}

func TestE2ERepoCRUDOverCLI(t *testing.T) {
	url, jwt := startServer(t)
	common := []string{"--server", url, "--token", jwt, "-o", "json"}

	// Create a repo.
	out, err := run(t, append([]string{"repo", "create", "--name", "acme", "--path", "/tmp/acme"}, common...)...)
	if err != nil {
		t.Fatalf("repo create: %v\n%s", err, out)
	}
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &created); err != nil || created.Data.ID == "" {
		t.Fatalf("repo create returned no id: %v\n%s", err, out)
	}

	// List repos — the new repo must appear.
	out, err = run(t, append([]string{"repo", "list"}, common...)...)
	if err != nil {
		t.Fatalf("repo list: %v\n%s", err, out)
	}
	if !strings.Contains(out, created.Data.ID) || !strings.Contains(out, "acme") {
		t.Errorf("repo list missing the created repo:\n%s", out)
	}

	// Get the repo by id.
	out, err = run(t, append([]string{"repo", "get", created.Data.ID}, common...)...)
	if err != nil || !strings.Contains(out, "acme") {
		t.Fatalf("repo get: %v\n%s", err, out)
	}

	// Delete the repo.
	if _, err := run(t, append([]string{"repo", "delete", created.Data.ID}, common...)...); err != nil {
		t.Fatalf("repo delete: %v", err)
	}
}

func TestE2ETokenMintAndUse(t *testing.T) {
	url, jwt := startServer(t)

	// Mint a read-only scan token via the CLI.
	out, err := run(t, "auth", "token", "create",
		"--name", "ci", "--scope", apikey.ScopeReadScans,
		"--server", url, "--token", jwt, "-o", "json")
	if err != nil {
		t.Fatalf("token create: %v\n%s", err, out)
	}
	var tok struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &tok); err != nil || tok.Data.Token == "" {
		t.Fatalf("token create returned no secret: %v\n%s", err, out)
	}

	// The token can list scans (read:scans).
	if _, err := run(t, "scan", "list", "--server", url, "--token", tok.Data.Token, "-o", "json"); err != nil {
		t.Errorf("scan list with read:scans token failed: %v", err)
	}

	// The token cannot create a repo (no write:repos) -> APIError -> ExitAuth.
	_, err = run(t, "repo", "create", "--name", "x", "--path", "/tmp/x",
		"--server", url, "--token", tok.Data.Token, "-o", "json")
	if err == nil {
		t.Fatal("expected a scope-denied error creating a repo")
	}
	if code := ExitCodeFor(err); code != ExitAuth {
		t.Errorf("expected ExitAuth (%d) for a 403, got %d", ExitAuth, code)
	}
}

func TestScanGateFailExitCode(t *testing.T) {
	url, jwt, userID, store := startServerFull(t)
	repoID := uuid.New().String()
	if err := store.CreateRepo(context.Background(), &models.Repo{
		ID:            repoID,
		UserID:        userID,
		Name:          "gate-repo",
		SourceType:    models.SourceTypeLocal,
		SourcePath:    "/tmp/gate-repo",
		DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("CreateRepo: %v", err)
	}
	scanID := uuid.New().String()
	if err := store.CreateScan(context.Background(), &models.Scan{
		ID:              scanID,
		UserID:          userID,
		RepoID:          repoID,
		Branch:          "main",
		Status:          models.ScanStatusCompleted,
		ToolsSelected:   `["gitleaks"]`,
		ToolsCompleted:  `["gitleaks"]`,
		ToolsFailed:     "[]",
		CoverageSummary: "{}",
	}); err != nil {
		t.Fatalf("CreateScan: %v", err)
	}
	if err := store.CreateFinding(context.Background(), &models.Finding{
		ID:                uuid.New().String(),
		ScanID:            scanID,
		RepoID:            repoID,
		Fingerprint:       "secret-fp",
		StableFingerprint: "secret-fp",
		ToolName:          "gitleaks",
		Category:          models.CategorySecrets,
		Severity:          models.SeverityHigh,
		Title:             "Hardcoded secret",
		FilePath:          "app/config.go",
		RuleID:            "generic-api-key",
		Status:            models.StatusOpen,
	}); err != nil {
		t.Fatalf("CreateFinding: %v", err)
	}

	out, err := run(t, "scan", "gate", scanID, "--fail-exit-code", "--server", url, "--token", jwt, "-o", "json")
	if err == nil {
		t.Fatalf("expected gate failure error, got nil\n%s", out)
	}
	if code := ExitCodeFor(err); code != ExitGateFail {
		t.Fatalf("ExitCodeFor(gate failure) = %d, want %d; err=%v\n%s", code, ExitGateFail, err, out)
	}
	if !strings.Contains(out, `"status": "fail"`) {
		t.Fatalf("gate output did not include failed status:\n%s", out)
	}
}

func TestE2EContextResolution(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	url, jwt := startServer(t)

	// Save a context, then use it (no --server/--token on the call).
	if _, err := run(t, "config", "set-context", "ci",
		"--server", url, "--token", jwt); err != nil {
		t.Fatalf("set-context: %v", err)
	}
	out, err := run(t, "repo", "list", "--context", "ci", "-o", "json")
	if err != nil {
		t.Fatalf("repo list via context: %v\n%s", err, out)
	}
}

func TestClientMapsErrorStatus(t *testing.T) {
	url, _ := startServer(t)
	c := NewClient(url, "") // no credential
	_, err := c.Do(context.Background(), "GET", "/scans", nil)
	ae, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if ae.StatusCode != 401 {
		t.Errorf("expected 401, got %d", ae.StatusCode)
	}
}
