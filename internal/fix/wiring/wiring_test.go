package wiring

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/fix/verify"
	"github.com/alphabravocompany/thewolf/internal/github"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/scan/runner"
	"github.com/alphabravocompany/thewolf/internal/secrets"
)

func TestVerificationPropagatesScannerFailure(t *testing.T) {
	orig := runScan
	t.Cleanup(func() { runScan = orig })
	want := errors.New("analysis incomplete")
	runScan = func(context.Context, runner.RunConfig) (*runner.RunResult, error) {
		return &runner.RunResult{
			ToolsFailed: map[string]error{"semgrep": want},
			Findings:    []models.Finding{{ToolName: "semgrep", FilePath: "app.py"}},
		}, nil
	}
	findings, err := (Scanner{}).RescanFiles(t.Context(), "/repo", []string{"app.py"}, "semgrep", "rule")
	if !errors.Is(err, want) || len(findings) != 1 {
		t.Fatalf("file verification hid failure: %v, %v", findings, err)
	}
	findings, err = (branchRescanner{}).Rescan(t.Context(), "/repo", []string{"semgrep"})
	if !errors.Is(err, want) || len(findings) != 1 {
		t.Fatalf("branch verification hid failure: %v, %v", findings, err)
	}
	runScan = func(context.Context, runner.RunConfig) (*runner.RunResult, error) {
		return &runner.RunResult{ToolsSkipped: []string{"semgrep"}}, nil
	}
	if _, err := (branchRescanner{}).Rescan(t.Context(), "/repo", []string{"semgrep"}); !errors.Is(err, verify.ErrMissingImage) {
		t.Fatalf("unavailable tool passed verification: %v", err)
	}
}

func TestBranchRescanRequiresEveryRequestedTool(t *testing.T) {
	original := runScan
	t.Cleanup(func() { runScan = original })
	for _, result := range []*runner.RunResult{nil, {}, {ToolsRun: []string{"gosec"}}} {
		runScan = func(_ context.Context, cfg runner.RunConfig) (*runner.RunResult, error) {
			if !cfg.ToolsExplicit || len(cfg.IncludePaths) != 0 {
				t.Fatal("final scanner scope was narrowed or auto-selected")
			}
			return result, nil
		}
		if _, err := (branchRescanner{}).Rescan(t.Context(), "/repo", []string{"gosec", "semgrep"}); err == nil {
			t.Fatalf("incomplete tool execution accepted: %+v", result)
		}
	}
	runScan = func(context.Context, runner.RunConfig) (*runner.RunResult, error) {
		return &runner.RunResult{ToolsRun: []string{"gosec", "semgrep"}}, nil
	}
	if _, err := (branchRescanner{}).Rescan(t.Context(), "/repo", []string{"gosec", "semgrep"}); err != nil {
		t.Fatal(err)
	}
}

// TestDepsArePopulated is the regression guard for the worker-had-nil-deps bug:
// orchestrator.Run fails fast unless Workspaces, Engines, and Verifier (plus the
// other collaborators) are non-nil. If a refactor drops one, this fails before
// a real job ever would.
func TestDepsArePopulated(t *testing.T) {
	d := Deps(nil)
	if d.PublicationReady == nil || d.GitHubToken == nil {
		t.Fatal("publication credentials/readiness not wired")
	}
	if d.Writability == nil {
		t.Error("Writability dep is nil")
	}
	if d.Workspaces == nil {
		t.Error("Workspaces dep is nil")
	}
	if d.Engines == nil {
		t.Error("Engines dep is nil")
	}
	if d.Verifier == nil {
		t.Error("Verifier dep is nil")
	}
	if d.GitApply == nil {
		t.Error("GitApply dep is nil")
	}
}

// TestScannerScopesToToolAndFile verifies the targeted rescan builds a run
// config scoped to the single tool + file and returns only that file's findings
// (the gate does the per-rule matching). No real docker — runScan is stubbed.
func TestScannerScopesToToolAndFile(t *testing.T) {
	orig := runScan
	defer func() { runScan = orig }()

	var got runner.RunConfig
	runScan = func(_ context.Context, cfg runner.RunConfig) (*runner.RunResult, error) {
		got = cfg
		return &runner.RunResult{Findings: []models.Finding{
			{ToolName: "gosec", FilePath: "internal/x.go", RuleID: "G101"},
			{ToolName: "gosec", FilePath: "internal/x.go", RuleID: "G404"},
			{ToolName: "gosec", FilePath: "other/y.go", RuleID: "G102"},
		}}, nil
	}

	out, err := Scanner{}.RescanFile(context.Background(), "/repo", "internal/x.go", "gosec", "G101")
	if err != nil {
		t.Fatalf("RescanFile: %v", err)
	}
	if len(got.Tools) != 1 || got.Tools[0] != "gosec" {
		t.Errorf("run not scoped to the one tool: %v", got.Tools)
	}
	if len(got.IncludePaths) != 1 || got.IncludePaths[0] != "internal/x.go" {
		t.Errorf("run not scoped to the changed file: %v", got.IncludePaths)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 findings for internal/x.go, got %d: %+v", len(out), out)
	}
	for _, f := range out {
		if f.FilePath != "internal/x.go" {
			t.Errorf("leaked a finding from another file: %+v", f)
		}
	}
}

// TestScannerNoToolIsNoop: a finding with no tool can't be rescanned.
func TestScannerNoToolIsNoop(t *testing.T) {
	out, err := Scanner{}.RescanFile(context.Background(), "/repo", "x.go", "", "")
	if err != nil || out != nil {
		t.Errorf("empty tool should be a no-op, got %v / %v", out, err)
	}
}

func TestScannerEmptyFileIsNoop(t *testing.T) {
	orig := runScan
	defer func() { runScan = orig }()
	called := false
	runScan = func(context.Context, runner.RunConfig) (*runner.RunResult, error) {
		called = true
		return &runner.RunResult{}, nil
	}
	out, err := Scanner{}.RescanFile(context.Background(), "/repo", "", "scorecard", "Branch-Protection")
	if err != nil || out != nil {
		t.Errorf("empty file should not rescan, got %v / %v", out, err)
	}
	if called {
		t.Fatal("empty file_path must not invoke the scanner")
	}
}

func TestScannerMissingImage(t *testing.T) {
	orig := runScan
	defer func() { runScan = orig }()
	missingImages.Range(func(k, _ any) bool {
		missingImages.Delete(k)
		return true
	})
	runScan = func(context.Context, runner.RunConfig) (*runner.RunResult, error) {
		return nil, errors.New("image wolf-scanners:stable is not present locally")
	}
	_, err := Scanner{}.RescanFile(context.Background(), "/repo", "main.go", "bearer", "x")
	if !verify.IsMissingImage(err) {
		t.Fatalf("want missing image, got %v", err)
	}
	runScan = func(context.Context, runner.RunConfig) (*runner.RunResult, error) {
		t.Fatal("missing image should be remembered")
		return nil, nil
	}
	_, err = Scanner{}.RescanFiles(context.Background(), "/repo", []string{"a.go", "b.go"}, "bearer", "")
	if !verify.IsMissingImage(err) {
		t.Fatalf("memoized missing image: %v", err)
	}
}

type publicationSecrets struct {
	db.Store
	secret models.Secret
}

func (s publicationSecrets) GetSecretByID(context.Context, string) (*models.Secret, error) {
	return &s.secret, nil
}
func (s publicationSecrets) ListSecretsByUser(context.Context, string) ([]models.Secret, error) {
	return nil, nil
}

func TestPublicationReadinessUsesOwnerTokenAndPermissions(t *testing.T) {
	secrets.SetMasterKey(bytes.Repeat([]byte{0x53}, 32))
	t.Cleanup(func() { secrets.SetMasterKey(nil) })
	const token = "owner-test-credential"
	encrypted, err := secrets.Encrypt(token)
	if err != nil {
		t.Fatal(err)
	}
	store := publicationSecrets{secret: models.Secret{ID: "selected", UserID: "owner", KeyType: models.KeyTypeGitHubToken, EncryptedValue: encrypted}}
	repo := &models.Repo{SourceType: models.SourceTypeGitHub, SourcePath: "example/repo", UserID: "owner", CredentialSecretID: "selected"}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte("fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	var canPush, archived, rejected bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/example/repo" || r.Header.Get("Authorization") != "Bearer "+token {
			t.Errorf("wrong repository/token boundary")
			w.WriteHeader(403)
			return
		}
		if rejected {
			w.WriteHeader(403)
			fmt.Fprint(w, token)
			return
		}
		fmt.Fprintf(w, `{"permissions":{"push":%t},"archived":%t}`, canPush, archived)
	}))
	defer server.Close()
	old := github.DefaultBaseURL
	github.DefaultBaseURL = server.URL
	t.Cleanup(func() { github.DefaultBaseURL = old })
	for _, state := range []string{"ready", "read-only", "archived", "rejected", "wrong-owner", "missing-cli"} {
		canPush, archived, rejected = state != "read-only", state == "archived", state == "rejected"
		store.secret.UserID = "owner"
		if state == "wrong-owner" {
			store.secret.UserID = "another-owner"
		}
		if state == "missing-cli" {
			t.Setenv("PATH", t.TempDir())
		}
		ready, reason := Deps(store).PublicationReady(t.Context(), repo)
		if ready != (state == "ready") || (!ready && reason == "") || strings.Contains(reason, token) {
			t.Fatalf("%s: ready=%v reason=%q", state, ready, reason)
		}
	}
}
