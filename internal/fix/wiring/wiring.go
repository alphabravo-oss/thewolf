// Package wiring is the composition root for the autonomous fix worker: it
// assembles the orchestrator's production collaborators (writability preflight,
// workspace preparer, engine chain, verification gate, git-apply) into a single
// orchestrator.Deps the `wolf fixer` command hands to the worker.
//
// Why a package and not inline in cmd/wolf: the worker requires Workspaces,
// Engines, and Verifier to be non-nil (orchestrator.Run fails fast otherwise),
// and the verification gate needs a REAL targeted rescanner — none of which is
// trivial. Keeping it here makes the wiring unit-testable (the "all deps
// populated" guard and the rescan-scoping logic) instead of buried in main.
//
// Per-tenant resolution: Deps is built once at worker startup, but the AI
// provider (for the API engine tier) and the GitHub token (for clone-for-write)
// are per-user. The adapters therefore resolve them per-call from the job's /
// repo's UserID via the store, preserving multi-tenant secret scoping.
package wiring

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/alphabravocompany/thewolf/internal/ai"
	"github.com/alphabravocompany/thewolf/internal/db"
	fixauth "github.com/alphabravocompany/thewolf/internal/fix/auth"
	"github.com/alphabravocompany/thewolf/internal/fix/engine"
	"github.com/alphabravocompany/thewolf/internal/fix/orchestrator"
	"github.com/alphabravocompany/thewolf/internal/fix/verify"
	"github.com/alphabravocompany/thewolf/internal/fix/workspace"
	"github.com/alphabravocompany/thewolf/internal/fix/writability"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/scan/runner"
	"github.com/alphabravocompany/thewolf/internal/secrets"
)

// Deps assembles the production orchestrator dependencies for the fix worker.
// The worker overrides Store, Diffs, and Log with its own; the collaborators
// here (Writability/Workspaces/Engines/Verifier/GitApply) are what make a real
// job actually run. Passing a nil store is valid for the "deps are complete"
// guard test; at runtime the worker always supplies the real store.
func Deps(store db.Store) orchestrator.Deps {
	return orchestrator.Deps{
		Store:       store,
		Writability: writabilityChecker{store: store},
		Workspaces:  workspacePreparer{store: store},
		Engines:     engineSelector{store: store},
		Verifier:    verifier{scanner: Scanner{}},
		GitApply:    gitApplier{},
		Rescan:      branchRescanner{},
	}
}

// --- Writability preflight ---

type writabilityChecker struct{ store db.Store }

func (wc writabilityChecker) Check(ctx context.Context, repo *models.Repo) (bool, string) {
	r := writability.Check(ctx, repo, wc.store, writability.DefaultProbes(wc.store))
	return r.Writable, r.Reason
}

// --- Workspace preparer ---

type workspacePreparer struct{ store db.Store }

func (wp workspacePreparer) Prepare(ctx context.Context, repo *models.Repo, branch string) (orchestrator.Workspace, error) {
	opts := workspace.Options{Repo: repo, Branch: branch, BaseDir: workspaceRoot()}
	if repo != nil && repo.SourceType == models.SourceTypeGitHub {
		opts.Token = githubTokenForRepo(ctx, wp.store, repo)
	}
	return workspace.Prepare(ctx, opts)
}

func workspaceRoot() string {
	return strings.TrimSpace(os.Getenv("WOLF_WORKSPACE_ROOT"))
}

func (wp workspacePreparer) Open(_ context.Context, path string, repo *models.Repo) (orchestrator.Workspace, error) {
	token := ""
	if repo != nil && repo.SourceType == models.SourceTypeGitHub {
		token = githubTokenForRepo(context.Background(), wp.store, repo)
	}
	return workspace.Open(path, token)
}

// --- Engine chain selector ---

type engineSelector struct{ store db.Store }

func (e engineSelector) Select(ctx context.Context, job *models.FixJob) (orchestrator.EngineChain, error) {
	// The provider backs only the API tier; CLI tiers (claude/codex) are probed
	// by SelectEngine independently. A noop provider simply omits the API tier.
	provider := resolveProvider(ctx, e.store, job.UserID)
	creds := resolveCreds(ctx, e.store, job.UserID)
	return engine.SelectEngine(ctx, engine.ChainConfig{
		Engine:    job.Engine,
		Provider:  provider,
		CLIEnv:    creds.Env(),
		HasAPIKey: creds.HasKeyFor,
	})
}

// --- Verification gate ---

type verifier struct{ scanner verify.Scanner }

func (v verifier) Verify(ctx context.Context, ws verify.Workspace, finding models.Finding) (*verify.VerifyResult, error) {
	return verify.Gate(ctx, ws, finding, v.scanner, verify.Options{})
}

func (v verifier) VerifyBatch(ctx context.Context, ws verify.Workspace, findings []models.Finding) (map[string]*verify.VerifyResult, error) {
	return verify.GateBatch(ctx, ws, findings, v.scanner, verify.Options{})
}

// Scanner is the production verify.Scanner: a targeted single-tool rescan over
// the container scanner backend, scoped to one file via the runner's
// IncludePaths. It reuses the full scan pipeline (runner.Run + plugin.Global)
// rather than reimplementing container orchestration, so a rescanned finding is
// judged by exactly the same machinery that produced it.
type Scanner struct{}

// runScan is the scan entrypoint, a package var so tests stub the real backend
// (no docker/network) while still exercising the config-scoping + filtering.
var runScan = runner.Run

var missingImages sync.Map // tool -> error

func (Scanner) RescanFile(ctx context.Context, repoPath, file, tool, rule string) ([]models.Finding, error) {
	if strings.TrimSpace(file) == "" {
		return nil, nil
	}
	return Scanner{}.RescanFiles(ctx, repoPath, []string{file}, tool, rule)
}

func (Scanner) RescanFiles(ctx context.Context, repoPath string, files []string, tool, rule string) ([]models.Finding, error) {
	if tool == "" {
		return nil, nil
	}
	if err := rememberedMissing(tool); err != nil {
		return nil, err
	}
	var include []string
	for _, f := range files {
		if strings.TrimSpace(f) != "" {
			include = append(include, f)
		}
	}
	if len(include) == 0 {
		return nil, nil
	}
	res, err := runScan(ctx, runner.RunConfig{
		RepoPath:     repoPath,
		Registry:     plugin.Global,
		Tools:        []string{tool},
		IncludePaths: include,
		Concurrency:  1,
		ContainerCfg: nil,
	})
	if err != nil {
		if verify.IsMissingImage(err) {
			wrapped := fmt.Errorf("%w: %s: %v", verify.ErrMissingImage, tool, err)
			rememberMissing(tool, wrapped)
			return nil, wrapped
		}
		return nil, err
	}
	if skippedAsMissing(res, tool) {
		wrapped := fmt.Errorf("%w: %s", verify.ErrMissingImage, tool)
		rememberMissing(tool, wrapped)
		return nil, wrapped
	}
	_ = rule
	out := make([]models.Finding, 0, len(res.Findings))
	for _, f := range res.Findings {
		for _, file := range include {
			if sameFile(f.FilePath, file) {
				out = append(out, f)
				break
			}
		}
	}
	return out, nil
}

func rememberedMissing(tool string) error {
	if v, ok := missingImages.Load(tool); ok {
		if err, _ := v.(error); err != nil {
			return err
		}
	}
	return nil
}

func rememberMissing(tool string, err error) {
	missingImages.Store(tool, err)
}

func skippedAsMissing(res *runner.RunResult, tool string) bool {
	if res == nil {
		return false
	}
	for _, s := range res.ToolsSkipped {
		if s == tool {
			return true
		}
	}
	if res.ToolsFailed != nil {
		if err, ok := res.ToolsFailed[tool]; ok && verify.IsMissingImage(err) {
			return true
		}
	}
	return false
}

// sameFile compares two repo-relative-ish paths, tolerating a leading-dir
// mismatch between the finding's path and the changed-file path.
func sameFile(a, b string) bool {
	if a == b {
		return true
	}
	return strings.HasSuffix(a, "/"+b) || strings.HasSuffix(b, "/"+a)
}

// --- git apply (for the API engine's returned diff) ---

type gitApplier struct{}

func (gitApplier) Apply(ctx context.Context, repoPath, diff string) error {
	// #nosec G204 -- repoPath is a server-prepared workspace path; diff is on stdin.
	cmd := exec.CommandContext(ctx, "git", "-C", repoPath, "apply", "--whitespace=nowarn", "-")
	cmd.Stdin = strings.NewReader(diff)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return errors.New(msg)
		}
		return err
	}
	return nil
}

// branchRescanner re-runs the original tools against the whole worktree so a
// loop can see which findings the last round actually cleared.
type branchRescanner struct{}

func (branchRescanner) Rescan(ctx context.Context, repoPath string, tools []string) ([]models.Finding, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	res, err := runScan(ctx, runner.RunConfig{
		RepoPath:    repoPath,
		Registry:    plugin.Global,
		Tools:       tools,
		Concurrency: 1,
	})
	if err != nil {
		return nil, err
	}
	return res.Findings, nil
}

// --- per-tenant secret resolution ---

// resolveProvider picks the best available API provider for the API engine tier,
// preferring the user's stored key, then the process env. A noop provider means
// "no API tier" — the chain then relies on the CLI engines.
func resolveProvider(ctx context.Context, store db.Store, userID string) ai.Provider {
	creds := resolveCreds(ctx, store, userID)
	if creds.AnthropicKey != "" {
		return ai.NewAnthropicProvider(creds.AnthropicKey)
	}
	if creds.XAIKey != "" {
		return ai.NewXAIProvider(creds.XAIKey)
	}
	if creds.OpenAIKey != "" {
		return ai.NewOpenAIProvider(creds.OpenAIKey)
	}
	return ai.NewNoopProvider()
}

func resolveCreds(ctx context.Context, store db.Store, userID string) fixauth.Credentials {
	return fixauth.Resolve(ctx, store, userID)
}

func githubTokenForRepo(ctx context.Context, store db.Store, repo *models.Repo) string {
	if store == nil || repo == nil {
		return ""
	}
	if id := strings.TrimSpace(repo.CredentialSecretID); id != "" {
		if sec, err := store.GetSecretByID(ctx, id); err == nil && sec != nil {
			if dec, derr := secrets.Decrypt(sec.EncryptedValue); derr == nil {
				return dec
			}
		}
	}
	return githubToken(ctx, store, repo.UserID)
}

func githubToken(ctx context.Context, store db.Store, userID string) string {
	if store == nil || userID == "" {
		return ""
	}
	list, err := store.ListSecretsByUser(ctx, userID)
	if err != nil {
		return ""
	}
	for _, s := range list {
		if s.KeyType == models.KeyTypeGitHubToken {
			if dec, derr := secrets.Decrypt(s.EncryptedValue); derr == nil {
				return dec
			}
		}
	}
	return ""
}
