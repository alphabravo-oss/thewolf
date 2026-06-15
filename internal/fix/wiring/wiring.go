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
	"os"
	"os/exec"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/ai"
	"github.com/alphabravocompany/thewolf/internal/db"
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
	opts := workspace.Options{Repo: repo, Branch: branch}
	if repo != nil && repo.SourceType == models.SourceTypeGitHub {
		// Clone-for-write needs a push-capable token; resolve the owning user's.
		opts.Token = githubToken(ctx, wp.store, repo.UserID)
	}
	return workspace.Prepare(ctx, opts)
}

// --- Engine chain selector ---

type engineSelector struct{ store db.Store }

func (e engineSelector) Select(ctx context.Context, job *models.FixJob) (orchestrator.EngineChain, error) {
	// The provider backs only the API tier; CLI tiers (claude/codex) are probed
	// by SelectEngine independently. A noop provider simply omits the API tier.
	provider := resolveProvider(ctx, e.store, job.UserID)
	return engine.SelectEngine(ctx, engine.ChainConfig{
		Engine:   job.Engine,
		Provider: provider,
	})
}

// --- Verification gate ---

type verifier struct{ scanner verify.Scanner }

func (v verifier) Verify(ctx context.Context, ws verify.Workspace, finding models.Finding) (*verify.VerifyResult, error) {
	return verify.Gate(ctx, ws, finding, v.scanner, verify.Options{})
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

func (Scanner) RescanFile(ctx context.Context, repoPath, file, tool, rule string) ([]models.Finding, error) {
	if tool == "" {
		return nil, nil // nothing to re-run
	}
	res, err := runScan(ctx, runner.RunConfig{
		RepoPath:     repoPath,
		Registry:     plugin.Global,
		Tools:        []string{tool}, // the one tool that produced the finding
		IncludePaths: []string{file}, // scope to the changed file only
		Concurrency:  1,
		ContainerCfg: nil, // shim falls back to container.Default()
	})
	if err != nil {
		return nil, err
	}
	if file == "" {
		return res.Findings, nil
	}
	// Defensive re-filter to the file: some tools ignore include-path scoping, so
	// we return only this file's findings (the gate then matches by rule/line for
	// "finding cleared" and counts any others as regressions). rule is a hint we
	// don't restrict on — returning the file's full set is the correct superset.
	out := make([]models.Finding, 0, len(res.Findings))
	for _, f := range res.Findings {
		if sameFile(f.FilePath, file) {
			out = append(out, f)
		}
	}
	return out, nil
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

// --- per-tenant secret resolution ---

// resolveProvider picks the best available API provider for the API engine tier,
// preferring the user's stored key, then the process env. A noop provider means
// "no API tier" — the chain then relies on the CLI engines.
func resolveProvider(ctx context.Context, store db.Store, userID string) ai.Provider {
	if key := apiKey(ctx, store, userID, models.KeyTypeAnthropicKey, "ANTHROPIC_API_KEY"); key != "" {
		return ai.NewAnthropicProvider(key)
	}
	if key := apiKey(ctx, store, userID, models.KeyTypeOpenAIKey, "OPENAI_API_KEY"); key != "" {
		return ai.NewOpenAIProvider(key)
	}
	return ai.NewNoopProvider()
}

func apiKey(ctx context.Context, store db.Store, userID string, kt models.KeyType, env string) string {
	if store != nil && userID != "" {
		if list, err := store.ListSecretsByUser(ctx, userID); err == nil {
			for _, s := range list {
				if s.KeyType == kt {
					if dec, derr := secrets.Decrypt(s.EncryptedValue); derr == nil && dec != "" {
						return dec
					}
				}
			}
		}
	}
	return os.Getenv(env)
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
