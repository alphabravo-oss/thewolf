package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/alphabravocompany/thewolf/internal/api/routes"
	"github.com/alphabravocompany/thewolf/internal/artifacts"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	scannercontainer "github.com/alphabravocompany/thewolf/internal/plugin/container"
	"github.com/alphabravocompany/thewolf/internal/scanjob"
	kubernetesruntime "github.com/alphabravocompany/thewolf/internal/scannerruntime/kubernetes"
	"github.com/alphabravocompany/thewolf/internal/scantarget"
	"github.com/alphabravocompany/thewolf/internal/secrets"
	"github.com/alphabravocompany/thewolf/internal/setup/scanners"
	"github.com/alphabravocompany/thewolf/internal/wolflog"
)

func newScanWorkerCmd() *cobra.Command {
	var (
		once         bool
		workerID     string
		backend      string
		capacity     int
		artifactsDir string
		pollEvery    time.Duration
		heartbeat    time.Duration
		lease        time.Duration
	)
	cmd := &cobra.Command{
		Use:   "scan-worker",
		Short: "Claim and execute durable code-scan jobs",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer cancel()

			store, err := openStore()
			if err != nil {
				return fmt.Errorf("open store: %w", err)
			}
			defer func() { _ = store.Close() }()
			if strings.EqualFold(envOr("WOLF_DB_DRIVER", "sqlite"), "sqlite") && capacity > 1 {
				return fmt.Errorf("SQLite supports one effective scan-worker slot; use PostgreSQL for capacity > 1")
			}
			var kubernetesConfig *kubernetesruntime.Config
			switch backend {
			case "docker":
				if err := installScannerBackend(ctx); err != nil {
					wolflog.Warn().Err(err).Msg("scanner backend unavailable; scans may fail until images/runtime are ready")
				}
			case "kubernetes":
				if !strings.EqualFold(envOr("WOLF_DB_DRIVER", "sqlite"), "postgres") {
					return fmt.Errorf("Kubernetes scan workers require PostgreSQL")
				}
				runtimeConfig, err := kubernetesruntime.ConfigFromEnv()
				if err != nil {
					return fmt.Errorf("configure Kubernetes scanner runtime: %w", err)
				}
				kubernetesConfig = &runtimeConfig
				scannerCfg, err := scanners.EnvDefaults().ToContainerConfig()
				if err != nil {
					return fmt.Errorf("configure scanner images: %w", err)
				}
				scannercontainer.SetDefault(scannerCfg)
				scannercontainer.SetRuntimeAvailable(true)
				scannercontainer.SetCommandFactory(kubernetesruntime.CommandContext)
			default:
				return fmt.Errorf("unsupported scan-worker backend %q (supported: docker, kubernetes)", backend)
			}
			if err := secrets.LoadMasterKey(); err != nil {
				wolflog.Warn().Err(err).Msg("secrets master key unavailable; private source resolution will fail")
			}
			if artifactsDir == "" {
				home, _ := os.UserHomeDir()
				artifactsDir = envOr("WOLF_ARTIFACTS_ROOT", filepath.Join(home, ".wolf", "artifacts"))
			}
			if err := artifacts.Init(artifactsDir); err != nil {
				return fmt.Errorf("initialize artifacts: %w", err)
			}
			routes.SetHandler(store, plugin.Global)
			if kubernetesConfig != nil {
				deleted, reconcileErr := kubernetesruntime.ReconcileAbandonedJobs(
					ctx,
					*kubernetesConfig,
					func(checkCtx context.Context, scanID, leaseToken string) (bool, error) {
						scan, getErr := store.GetScanByID(checkCtx, scanID)
						if errors.Is(getErr, sql.ErrNoRows) {
							return false, nil
						}
						if getErr != nil {
							return false, getErr
						}
						return scan.Status == models.ScanStatusRunning &&
							scan.LeaseToken == leaseToken &&
							scan.LeaseExpiresAt != nil &&
							scan.LeaseExpiresAt.After(time.Now().UTC()), nil
					},
				)
				if reconcileErr != nil {
					wolflog.Warn().Err(reconcileErr).Msg("Kubernetes scanner Job reconciliation incomplete")
				} else if deleted > 0 {
					wolflog.Info().Int("jobs", deleted).Msg("removed abandoned Kubernetes scanner Jobs")
				}
			}

			capabilities := fmt.Sprintf(`{"runtime":%q,"durable_events":true,"tool_cancellation":true,"native_jobs":%t}`,
				backend, backend == "kubernetes")
			worker, err := scanjob.New(scanjob.Config{
				Store:            store,
				WorkerID:         workerID,
				Backend:          backend,
				Capacity:         capacity,
				Once:             once,
				PollInterval:     pollEvery,
				Heartbeat:        heartbeat,
				Lease:            lease,
				Version:          version,
				Capabilities:     capabilities,
				CleanupWorkspace: scantarget.CleanupWorkspace,
				Executor: func(execCtx context.Context, scan *models.Scan) error {
					return routes.ExecuteQueuedScan(execCtx, routes.DefaultHandler, scan)
				},
			})
			if err != nil {
				return err
			}
			if err := worker.Run(ctx); err != nil && err != context.Canceled {
				return err
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&once, "once", false, "process one scan then exit")
	cmd.Flags().StringVar(&workerID, "worker-id", "", "worker identity (default hostname-pid)")
	cmd.Flags().StringVar(&backend, "backend", "docker", "scanner runtime backend")
	cmd.Flags().IntVar(&capacity, "capacity", 1, "maximum concurrent scans")
	cmd.Flags().StringVar(&artifactsDir, "artifacts", "", "shared artifacts root")
	cmd.Flags().DurationVar(&pollEvery, "poll-interval", 2*time.Second, "queue poll interval")
	cmd.Flags().DurationVar(&heartbeat, "heartbeat", 10*time.Second, "worker and lease heartbeat interval")
	cmd.Flags().DurationVar(&lease, "lease-duration", 45*time.Second, "scan claim lease duration")
	return cmd
}
