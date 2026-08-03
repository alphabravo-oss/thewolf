package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/scannerbuild"
	"github.com/alphabravocompany/thewolf/internal/scannercustombuildworker"
	"github.com/alphabravocompany/thewolf/internal/scannerobservability"
	"github.com/alphabravocompany/thewolf/internal/secrets"
)

func newScannerCustomBuildWorkerCmd() *cobra.Command {
	var (
		once                 bool
		workerID             string
		pollInterval         time.Duration
		heartbeatInterval    time.Duration
		leaseDuration        time.Duration
		operationTimeout     time.Duration
		observabilityAddress string
	)
	command := &cobra.Command{
		Use:   "scanner-custom-build-worker",
		Short: "Run the Docker-isolated durable custom scanner-image builder",
		RunE: func(command *cobra.Command, _ []string) error {
			if err := applyCustomBuildWorkerEnvironment(
				command,
				&once,
				&pollInterval,
				&heartbeatInterval,
				&leaseDuration,
				&operationTimeout,
			); err != nil {
				return err
			}
			if strings.TrimSpace(workerID) == "" {
				hostname, _ := os.Hostname()
				workerID = hostname + "-" + strconv.Itoa(os.Getpid())
			}
			ctx, cancel := signal.NotifyContext(
				command.Context(), syscall.SIGINT, syscall.SIGTERM,
			)
			defer cancel()
			store, err := openStore()
			if err != nil {
				return fmt.Errorf("open custom-build store: %w", err)
			}
			defer store.Close()

			observer := scannerobservability.NewRegistry()
			observer.SetDatabaseCheck(store.Ping)
			observer.Enable(scannerobservability.ComponentBuild, true)
			observer.SetState(scannerobservability.ComponentBuild, "idle")
			if !once {
				stopObservability, err := serveScannerReleaseObservability(
					ctx, observabilityAddress, observer,
				)
				if err != nil {
					return err
				}
				defer stopObservability()
			}

			worker, err := scannercustombuildworker.New(
				scannercustombuildworker.Config{
					Store:    store.ScannerReleases(),
					Executor: scannercustombuildworker.ExecutorFunc(scannerbuild.Build),
					Credentials: scannercustombuildworker.CredentialResolverFunc(
						func(
							ctx context.Context,
							reference, userID string,
						) (string, string, error) {
							secret, err := store.GetSecretByID(ctx, reference)
							if err != nil {
								return "", "", errors.New("registry credential is unavailable")
							}
							if secret.UserID != userID ||
								secret.KeyType != models.KeyTypeDockerHubToken {
								return "", "", errors.New("registry credential is unavailable")
							}
							if err := secrets.LoadMasterKey(); err != nil {
								return "", "", errors.New("registry credential is unavailable")
							}
							value, err := secrets.Decrypt(secret.EncryptedValue)
							if err != nil {
								return "", "", errors.New("registry credential is unavailable")
							}
							return secret.KeyName, value, nil
						},
					),
					WorkerID: workerID, PollInterval: pollInterval,
					HeartbeatInterval: heartbeatInterval,
					LeaseDuration:     leaseDuration,
					OperationTimeout:  operationTimeout,
					Once:              once,
				},
			)
			if err != nil {
				return err
			}
			err = worker.Run(ctx)
			observer.SetState(scannerobservability.ComponentBuild, "stopped")
			if errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		},
	}
	command.Flags().BoolVar(
		&once, "once", false,
		"reclaim stale work and execute at most one available operation",
	)
	command.Flags().StringVar(
		&workerID, "worker-id",
		os.Getenv("WOLF_SCANNER_CUSTOM_BUILD_WORKER_ID"),
		"stable worker identity",
	)
	command.Flags().DurationVar(
		&pollInterval, "poll-interval", 2*time.Second,
		"empty queue poll interval",
	)
	command.Flags().DurationVar(
		&heartbeatInterval, "heartbeat-interval", 10*time.Second,
		"active operation heartbeat interval",
	)
	command.Flags().DurationVar(
		&leaseDuration, "lease-duration", 45*time.Second,
		"operation claim visibility timeout",
	)
	command.Flags().DurationVar(
		&operationTimeout, "operation-timeout", 2*time.Hour,
		"hard timeout for one aggregate custom build",
	)
	command.Flags().StringVar(
		&observabilityAddress, "observability-address",
		envOr("WOLF_SCANNER_CUSTOM_BUILD_OBSERVABILITY_ADDR", ":9092"),
		"health, readiness, and Prometheus listen address (set to off to disable)",
	)
	return command
}

func applyCustomBuildWorkerEnvironment(
	command *cobra.Command,
	once *bool,
	pollInterval, heartbeatInterval, leaseDuration,
	operationTimeout *time.Duration,
) error {
	if !command.Flags().Changed("once") {
		if value := strings.TrimSpace(
			os.Getenv("WOLF_SCANNER_CUSTOM_BUILD_ONCE"),
		); value != "" {
			parsed, err := strconv.ParseBool(value)
			if err != nil {
				return fmt.Errorf("WOLF_SCANNER_CUSTOM_BUILD_ONCE: %w", err)
			}
			*once = parsed
		}
	}
	return applyDurationEnvironment(command, []struct {
		flagName    string
		environment string
		destination *time.Duration
	}{
		{"poll-interval", "WOLF_SCANNER_CUSTOM_BUILD_POLL_INTERVAL", pollInterval},
		{"heartbeat-interval", "WOLF_SCANNER_CUSTOM_BUILD_HEARTBEAT_INTERVAL", heartbeatInterval},
		{"lease-duration", "WOLF_SCANNER_CUSTOM_BUILD_LEASE_DURATION", leaseDuration},
		{"operation-timeout", "WOLF_SCANNER_CUSTOM_BUILD_OPERATION_TIMEOUT", operationTimeout},
	})
}

func applyDurationEnvironment(
	command *cobra.Command,
	values []struct {
		flagName    string
		environment string
		destination *time.Duration
	},
) error {
	for _, value := range values {
		if command.Flags().Changed(value.flagName) {
			continue
		}
		raw := strings.TrimSpace(os.Getenv(value.environment))
		if raw == "" {
			continue
		}
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return fmt.Errorf("%s: %w", value.environment, err)
		}
		*value.destination = parsed
	}
	return nil
}
