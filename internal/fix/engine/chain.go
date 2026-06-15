package engine

import (
	"context"
	"fmt"
	"os/exec"
	"sync"
	"time"

	"github.com/alphabravocompany/thewolf/internal/ai"
	"github.com/alphabravocompany/thewolf/internal/wolflog"
)

// authProbeTimeout bounds a CLI auth check so a misconfigured CLI can't hang
// engine selection.
const authProbeTimeout = 8 * time.Second

// authProber runs a fast auth/availability probe for a CLI agent. It returns
// nil when the CLI is present AND authenticated, and an error otherwise. It is
// a package var so tests can stub it without spawning real agents.
//
// The probe is deliberately tiny: a "say ok" prompt with a hard timeout. We do
// not parse its output — a clean exit within the timeout is taken as "the CLI
// is present and a session exists." A non-zero exit (missing binary, not logged
// in, rate-limited) means "skip this tier."
var authProber func(ctx context.Context, command string) error = defaultAuthProbe

func defaultAuthProbe(ctx context.Context, command string) error {
	if _, err := exec.LookPath(command); err != nil {
		return fmt.Errorf("%s not on PATH: %w", command, err)
	}
	probeCtx, cancel := context.WithTimeout(ctx, authProbeTimeout)
	defer cancel()

	var cmd *exec.Cmd
	switch command {
	case "claude":
		// A minimal headless turn. Succeeds only with a valid session.
		// #nosec G204 -- command is a fixed tool name, prompt is a constant
		cmd = exec.CommandContext(probeCtx, command, "-p", "--max-turns", "1", "ok")
	case "codex":
		// #nosec G204 -- command is a fixed tool name, prompt is a constant
		cmd = exec.CommandContext(probeCtx, command, "--approval-mode", "full-auto", "--quiet", "ok")
	default:
		// #nosec G204 -- command is a configured tool name
		cmd = exec.CommandContext(probeCtx, command, "--version")
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s auth probe failed: %w", command, err)
	}
	return nil
}

// authCache memoizes auth-probe results for the lifetime of the process so a
// worker doesn't re-probe an agent CLI on every job. Reliability over
// freshness: a CLI's authed state rarely flips mid-process, and a stale "not
// authed" verdict only forces the (always-correct) API fallback.
type authCacheEntry struct {
	authed bool
	err    error
}

var (
	authCacheMu sync.Mutex
	authCache   = map[string]authCacheEntry{}
)

// resetAuthCache clears the per-process probe cache. Test-only.
func resetAuthCache() {
	authCacheMu.Lock()
	authCache = map[string]authCacheEntry{}
	authCacheMu.Unlock()
}

// cliAuthed reports whether the named CLI command is present and authenticated,
// caching the verdict per-process. command is the binary name ("claude"/"codex").
func cliAuthed(ctx context.Context, command string) bool {
	authCacheMu.Lock()
	if e, ok := authCache[command]; ok {
		authCacheMu.Unlock()
		return e.authed
	}
	authCacheMu.Unlock()

	err := authProber(ctx, command)
	authed := err == nil

	authCacheMu.Lock()
	authCache[command] = authCacheEntry{authed: authed, err: err}
	authCacheMu.Unlock()

	if !authed {
		wolflog.Debug().Str("cli", command).Err(err).Msg("fix CLI unavailable/unauthed; will fall back")
	}
	return authed
}

// cliCommandFor maps an engine to its probe binary, "" if it isn't a probeable CLI.
func cliCommandFor(name string) string {
	switch name {
	case "claude-code":
		return "claude"
	case "codex":
		return "codex"
	default:
		return ""
	}
}

// ChainConfig configures engine selection for a job.
type ChainConfig struct {
	// Engine is the requested engine: "auto" (or "") = CLI-first then API;
	// or an explicit engine name to pin a single tier.
	Engine string
	// Provider backs the API tier. When nil/noop, the API tier is omitted.
	Provider ai.Provider
}

// Chain is an ordered list of engine tiers. The orchestrator runs the first
// tier; on a verification failure it asks the chain for the next tier, walking
// CLI → API. It is NOT a SubprocessEngine itself — escalation is the caller's
// decision (Phase 5), driven by the verification gate, never by an engine's
// self-report.
type Chain struct {
	tiers []SubprocessEngine
	idx   int
}

// Tiers returns the ordered engines in the chain.
func (c *Chain) Tiers() []SubprocessEngine { return c.tiers }

// Len returns how many tiers the chain holds.
func (c *Chain) Len() int { return len(c.tiers) }

// Current returns the engine at the current tier, or nil when exhausted.
func (c *Chain) Current() SubprocessEngine {
	if c.idx >= len(c.tiers) {
		return nil
	}
	return c.tiers[c.idx]
}

// Next advances to the next tier and returns it, or nil when the chain is
// exhausted. The caller invokes this after a verification failure to escalate.
func (c *Chain) Next() SubprocessEngine {
	c.idx++
	return c.Current()
}

// SelectEngine builds the engine chain for a job, ordered:
//
//	auto  → [claude-code if present+authed] → [codex if present+authed] → [API]
//
// An explicit cfg.Engine pins a single tier (no fallback chain): the named CLI,
// or "api" for the diff-returning API engine. "auto"/"" yields the full
// CLI-first, API-fallback chain. The API tier is included only when a usable
// provider is configured. Returns an error if no tier is available.
func SelectEngine(ctx context.Context, cfg ChainConfig) (*Chain, error) {
	apiAvailable := cfg.Provider != nil && cfg.Provider.Name() != "noop"

	switch cfg.Engine {
	case "", "auto":
		var tiers []SubprocessEngine
		// CLI tiers first, gated on present-AND-authed.
		for _, c := range []struct {
			name string
			eng  SubprocessEngine
		}{
			{"claude-code", &ClaudeCode{}},
			{"codex", &Codex{}},
		} {
			if cmd := cliCommandFor(c.name); cmd != "" && cliAuthed(ctx, cmd) {
				tiers = append(tiers, c.eng)
			}
		}
		// API tier last (zero-auth fallback).
		if apiAvailable {
			tiers = append(tiers, NewAPIEngine(cfg.Provider))
		}
		if len(tiers) == 0 {
			return nil, fmt.Errorf("no fix engine available: no authed CLI (claude/codex) and no API provider configured")
		}
		return &Chain{tiers: tiers}, nil

	case "api":
		if !apiAvailable {
			return nil, fmt.Errorf("engine %q requested but no API provider is configured", cfg.Engine)
		}
		return &Chain{tiers: []SubprocessEngine{NewAPIEngine(cfg.Provider)}}, nil

	case "claude-code":
		return &Chain{tiers: []SubprocessEngine{&ClaudeCode{}}}, nil
	case "codex":
		return &Chain{tiers: []SubprocessEngine{&Codex{}}}, nil

	default:
		// Defer to the registry/custom syntax for anything else.
		eng, err := NewEngine(cfg.Engine)
		if err != nil {
			return nil, err
		}
		return &Chain{tiers: []SubprocessEngine{eng}}, nil
	}
}
