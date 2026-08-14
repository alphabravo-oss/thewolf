package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	fixauth "github.com/alphabravocompany/thewolf/internal/fix/auth"
	"github.com/alphabravocompany/thewolf/internal/fix/fixstore"
	"github.com/alphabravocompany/thewolf/internal/fix/install"
	"github.com/alphabravocompany/thewolf/internal/fix/wiring"
	"github.com/alphabravocompany/thewolf/internal/fix/worker"
	"github.com/alphabravocompany/thewolf/internal/secrets"
	"github.com/alphabravocompany/thewolf/internal/wolflog"
)

// newFixerCmd builds `wolf fixer` — the stateless autonomous-fix worker. It
// claims queued fix jobs from the durable queue, runs the orchestrator (which
// prepares a worktree, drives the engine chain, and gates every change through
// the verification gate), streams its log to the artifact store for the server
// to relay over SSE, and persists the job's terminal status. One binary; in
// k8s it's a Deployment scaled to the queue, or a Job-per-task with --once.
//
// The whole subsystem stays dark until the autofix_enabled setting is on: the
// server refuses to enqueue jobs, so a worker against a flag-off install simply
// finds an empty queue.
func newFixerCmd() *cobra.Command {
	var (
		once       bool
		workerID   string
		artifacts  string
		pollEvery  time.Duration
		heartbeat  time.Duration
		staleAfter time.Duration
	)
	cmd := &cobra.Command{
		Use:   "fixer",
		Short: "Run the autonomous fix worker (claims and runs queued fix jobs)",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer cancel()

			if err := secrets.LoadMasterKey(); err != nil {
				wolflog.L().Warn().Err(err).Msg("master key not loaded; stored GitHub/LLM secrets cannot be decrypted")
			}
			install.EnsureLocalBinOnPATH()

			store, err := openStore()
			if err != nil {
				return fmt.Errorf("open store: %w", err)
			}
			defer func() { _ = store.Close() }()

			root := artifacts
			if root == "" {
				home, _ := os.UserHomeDir()
				root = envOr("WOLF_ARTIFACTS_ROOT", filepath.Join(home, ".wolf", "artifacts"))
			}
			if err := os.MkdirAll(root, 0o750); err != nil {
				return fmt.Errorf("create artifacts root: %w", err)
			}

			// The verification gate re-runs scanners against the changed file, so
			// the container backend must be ready. Best-effort: if it can't be
			// installed (e.g. no docker), the worker still starts but fixes will
			// fail verification and be rolled back — safe, just unproductive.
			if err := installScannerBackend(ctx); err != nil {
				wolflog.L().Warn().Err(err).Msg("scanner backend unavailable; fix verification will fail until it is")
			}

			w, err := worker.New(worker.Config{
				Store:        store,
				Fixstore:     fixstore.New(root),
				WorkerID:     workerID,
				Once:         once,
				PollInterval: pollEvery,
				Heartbeat:    heartbeat,
				StaleAfter:   staleAfter,
				// The production collaborators that make a real job run: writability
				// preflight, workspace preparer, engine chain, verification gate,
				// git-apply. Without these orchestrator.Run fails fast.
				Deps: wiring.Deps(store),
			})
			if err != nil {
				return err
			}

			wolflog.L().Info().Str("artifacts", root).Bool("once", once).Msg("wolf fixer")
			if err := w.Run(ctx); err != nil && err != context.Canceled {
				return err
			}
			return nil
		},
	}
	cmd.AddCommand(newFixerQualificationCmd(), newFixerLoginCmd(), newFixerInstallCmd(), newFixerStatusCmd())
	cmd.Flags().BoolVar(&once, "once", false, "process a single job then exit (k8s Job-per-task)")
	cmd.Flags().StringVar(&workerID, "worker-id", "", "worker identity recorded in claimed_by (default hostname-pid)")
	cmd.Flags().StringVar(&artifacts, "artifacts", "", "artifacts root for fix logs/diffs (default ~/.wolf/artifacts)")
	cmd.Flags().DurationVar(&pollEvery, "poll-interval", 2*time.Second, "queue poll interval when idle")
	cmd.Flags().DurationVar(&heartbeat, "heartbeat", 10*time.Second, "heartbeat interval while a job runs")
	cmd.Flags().DurationVar(&staleAfter, "stale-after", 2*time.Minute, "reclaim jobs whose worker stopped heartbeating for this long")
	return cmd
}

func newFixerLoginCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "login [claude|codex|opencode]",
		Short: "Log in a harness with OAuth; the session persists in $HOME",
		Long: `Opens the native CLI login for Claude Code, Codex, or OpenCode.

The session is written under this user's home directory and reused by
every later 'wolf fixer' run as the same user:

  Claude   ~/.claude.json  ~/.config/claude
  Codex    ~/.codex
  OpenCode ~/.local/share/opencode/auth.json

In Docker, run this inside the worker container and mount that HOME
(or /home/wolf) as a volume so restarts stay logged in.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			home, _ := os.UserHomeDir()
			fmt.Fprintf(cmd.OutOrStdout(), "Logging in %s. Session files will be stored under %s\n", args[0], home)
			if err := fixauth.Login(cmd.Context(), args[0]); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Login finished. Run 'wolf fixer status' to confirm.")
			return writeLocalEngineStatus()
		},
	}
}

func newFixerInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install [claude|codex|opencode]",
		Short: "Download a pinned fixer CLI into $HOME/.local/bin",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			install.EnsureLocalBinOnPATH()
			return install.Install(cmd.Context(), args[0], func(line string) {
				fmt.Fprintln(cmd.OutOrStdout(), line)
			})
		},
	}
}

func newFixerStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show which fixer harnesses are logged in on this host",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			engines := fixauth.ProbeAll(ctx, fixauth.Resolve(ctx, nil, ""))
			for _, e := range engines {
				fmt.Fprintf(cmd.OutOrStdout(), "%-12s  available=%v  auth=%-8s  %s\n", e.Name, e.Available, e.Auth, e.Detail)
				if e.Account != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "             account: %s\n", e.Account)
				}
				if e.Usage != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "             usage:   %s\n", e.Usage)
				}
				if len(e.SessionPaths) > 0 {
					fmt.Fprintf(cmd.OutOrStdout(), "             persist: %s\n", strings.Join(e.SessionPaths, ", "))
				}
			}
			return writeLocalEngineStatus()
		},
	}
}

func writeLocalEngineStatus() error {
	home, _ := os.UserHomeDir()
	root := envOr("WOLF_ARTIFACTS_ROOT", filepath.Join(home, ".wolf", "artifacts"))
	return fixauth.WriteStatus(root, fixauth.ProbeAll(context.Background(), fixauth.Resolve(context.Background(), nil, "")))
}
