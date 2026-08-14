package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/scanneralertworker"
	"github.com/alphabravocompany/thewolf/internal/scannercontrol"
	"github.com/alphabravocompany/thewolf/internal/scannerdiscovery"
	"github.com/alphabravocompany/thewolf/internal/scannerdiscoveryworker"
	"github.com/alphabravocompany/thewolf/internal/scannernotification"
	"github.com/alphabravocompany/thewolf/internal/scannernotificationworker"
	"github.com/alphabravocompany/thewolf/internal/scannerobservability"
	"github.com/alphabravocompany/thewolf/internal/scannerpolicy"
	"github.com/alphabravocompany/thewolf/internal/scannerproposalworker"
	"github.com/alphabravocompany/thewolf/internal/scannerregistryworker"
	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
	"github.com/alphabravocompany/thewolf/internal/scannerreleasebackend"
	"github.com/alphabravocompany/thewolf/internal/scannerreleasescheduler"
	"github.com/alphabravocompany/thewolf/internal/scannerreleaseworker"
	"github.com/alphabravocompany/thewolf/internal/scannerrollout"
	"github.com/alphabravocompany/thewolf/internal/scannertools/latest"
	scannerlock "github.com/alphabravocompany/thewolf/internal/scannertools/lock"
	"github.com/alphabravocompany/thewolf/internal/scannertools/manifest"
	"github.com/alphabravocompany/thewolf/internal/wolflog"
)

func newScannerReleaseWorkerCmd() *cobra.Command {
	var (
		role              string
		once              bool
		workerID          string
		platforms         []string
		executorBackend   string
		executorPath      string
		executorArgs      []string
		executorEnvNames  []string
		maxExecutorOutput int64
		maxParallel       int
		maxAttempts       int
		pollInterval      time.Duration
		heartbeat         time.Duration
		leaseDuration     time.Duration
		drainTimeout      time.Duration
		workspaceRoot     string

		schedulePoll     time.Duration
		scheduleLease    time.Duration
		scheduleBeat     time.Duration
		timezone         string
		dailyEnabled     bool
		dailyTime        string
		weeklyEnabled    bool
		weeklyTime       string
		weeklyDay        string
		scheduleJitter   time.Duration
		policyScope      string
		definitionCommit string

		discoveryPoll            time.Duration
		discoveryHeartbeat       time.Duration
		discoveryLease           time.Duration
		discoveryDrain           time.Duration
		discoveryManifest        string
		discoveryLock            string
		discoveryMaxConcurrency  int
		discoveryHostConcurrency int
		discoveryItemTimeout     time.Duration
		discoverySourceAttempts  int

		proposalExecutorPath string
		proposalExecutorArgs []string
		proposalEnvNames     []string
		proposalMaxOutput    int64
		proposalPoll         time.Duration
		proposalHeartbeat    time.Duration
		proposalLease        time.Duration
		proposalDrain        time.Duration

		notificationWebhookPath string
		notificationWebhookArgs []string
		notificationEmailPath   string
		notificationEmailArgs   []string
		notificationSIEMPath    string
		notificationSIEMArgs    []string
		notificationEnvNames    []string
		notificationMaxOutput   int64
		notificationPoll        time.Duration
		notificationHeartbeat   time.Duration
		notificationLease       time.Duration
		notificationDelivery    time.Duration
		notificationDrain       time.Duration
		notificationBaseBackoff time.Duration
		notificationMaxBackoff  time.Duration

		alertInterval  time.Duration
		alertHeartbeat time.Duration
		alertLease     time.Duration

		rolloutPoll          time.Duration
		rolloutReconcile     time.Duration
		rolloutHeartbeat     time.Duration
		rolloutLease         time.Duration
		rolloutCohortTimeout time.Duration
		rolloutWorkerActive  time.Duration
		rolloutBackend       string

		rolloutComposeStateRoot  string
		rolloutComposeAdapter    string
		rolloutComposeArgs       []string
		rolloutComposeEnvNames   []string
		rolloutComposeMaxOutput  int64
		rolloutDockerPath        string
		rolloutKubernetesAPI     string
		rolloutKubernetesNS      string
		rolloutKubernetesToken   string
		rolloutKubernetesCA      string
		rolloutKubernetesPoll    time.Duration
		rolloutKubernetesTimeout time.Duration
		rolloutSyntheticAdapter  string
		rolloutSyntheticArgs     []string
		rolloutSyntheticEnvNames []string
		rolloutSyntheticMax      int64
		rolloutSyntheticTimeout  time.Duration

		registryPoll         time.Duration
		registryHeartbeat    time.Duration
		registryLease        time.Duration
		registryTimeout      time.Duration
		registryDrain        time.Duration
		registryBaseBackoff  time.Duration
		registryMaxBackoff   time.Duration
		observabilityAddress string
	)
	command := &cobra.Command{
		Use:   "scanner-release-worker",
		Short: "Run durable scanner discovery, builds, rollouts, and update schedules",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer cancel()
			role = strings.ToLower(strings.TrimSpace(role))
			if role != "alert" && role != "build" && role != "discovery" && role != "proposal" &&
				role != "notification" && role != "registry" && role != "rollout" &&
				role != "scheduler" && role != "all" {
				return fmt.Errorf("unsupported scanner release worker role %q (alert, build, discovery, notification, proposal, registry, rollout, scheduler, or all)", role)
			}
			if workerID == "" {
				hostname, _ := os.Hostname()
				workerID = hostname + "-" + strconv.Itoa(os.Getpid())
			}
			store, err := openStore()
			if err != nil {
				return fmt.Errorf("open scanner release store: %w", err)
			}
			defer func() { _ = store.Close() }()
			persistence := store.ScannerReleases()
			observer := scannerobservability.NewRegistry()
			observer.SetDatabaseCheck(store.Ping)
			observer.SetMaintenanceCheck(func(ctx context.Context) (bool, error) {
				status, err := persistence.GetReleaseMaintenanceStatus(ctx)
				if err != nil {
					return false, err
				}
				return status.RestoreActive(time.Now()), nil
			})
			configureScannerReleaseComponents(observer, role)
			if !once {
				stopObservability, err := serveScannerReleaseObservability(
					ctx, observabilityAddress, observer,
				)
				if err != nil {
					return err
				}
				defer stopObservability()
			}

			if role == "scheduler" || role == "discovery" || role == "all" {
				if definitionCommit == "" {
					definitionCommit = commit
				}
				if definitionCommit == "" || definitionCommit == "unknown" {
					return errors.New("--definition-commit or WOLF_SCANNER_DEFINITION_COMMIT is required for scheduler and discovery roles")
				}
			}

			var buildWorker *scannerreleaseworker.Worker
			if role == "build" || role == "all" {
				if workspaceRoot != "" {
					if err := os.MkdirAll(workspaceRoot, 0o750); err != nil {
						return fmt.Errorf("create scanner release workspace root: %w", err)
					}
				}
				stepExecutor, err := scannerReleaseBuildExecutor(
					store, persistence,
					executorBackend, executorPath, executorArgs, executorEnvNames,
					maxExecutorOutput, platforms,
				)
				if err != nil {
					return err
				}
				buildWorker, err = scannerreleaseworker.New(scannerreleaseworker.Config{
					Store:    persistence,
					Executor: stepExecutor,
					WorkerID: workerID, SupportedPlatforms: platforms,
					MaxParallelSteps: maxParallel, MaxStepAttempts: maxAttempts,
					PollInterval: pollInterval, HeartbeatInterval: heartbeat,
					LeaseDuration: leaseDuration, DrainTimeout: drainTimeout,
					WorkspaceRoot: workspaceRoot, Once: once, Observer: observer,
				})
				if err != nil {
					return err
				}
			}

			var scheduler *scannerreleasescheduler.Scheduler
			var jobProvider scannerreleasescheduler.JobProvider
			if role == "scheduler" || role == "all" {
				bootstrapWeekday, err := parseWeekday(weeklyDay)
				if err != nil {
					return err
				}
				bootstrapSchedule := scannerpolicy.DefaultSchedule()
				bootstrapSchedule.Timezone = timezone
				bootstrapSchedule.DailyDiscovery.Enabled = &dailyEnabled
				bootstrapSchedule.DailyDiscovery.At = dailyTime
				bootstrapSchedule.DailyDiscovery.Jitter = scheduleJitter.String()
				bootstrapSchedule.WeeklyCandidate.Enabled = &weeklyEnabled
				bootstrapSchedule.WeeklyCandidate.At = weeklyTime
				bootstrapSchedule.WeeklyCandidate.Weekday = bootstrapWeekday.String()
				bootstrapSchedule.WeeklyCandidate.Jitter = scheduleJitter.String()
				if _, err := (scannercontrol.Service{Store: persistence}).EnsureDefaultPolicyWithSchedule(
					ctx, "scanner-scheduler:"+workerID, bootstrapSchedule,
				); err != nil {
					return err
				}
				jobProvider = scannerreleasescheduler.ActivePolicyJobs{
					Store: persistence, Scope: policyScope,
				}
				enqueuer := scannerreleasescheduler.PersistentEnqueuer{
					Store: persistence,
					Policies: scannerreleasescheduler.LatestPolicy{
						Store: persistence, Scope: policyScope,
					},
					Definition: scannerreleasescheduler.StaticDefinition(definitionCommit),
				}
				scheduler, err = scannerreleasescheduler.New(scannerreleasescheduler.Config{
					Store: persistence, Enqueuer: enqueuer, Owner: workerID,
					LeaseDuration: scheduleLease, HeartbeatInterval: scheduleBeat,
					Observer: observer,
				})
				if err != nil {
					return err
				}
			}

			var discoveryWorker *scannerdiscoveryworker.Worker
			if role == "discovery" || role == "all" {
				discoveryManifestDefinition, err := loadDiscoveryManifest(discoveryManifest)
				if err != nil {
					return err
				}
				discoveryLockDefinition, err := loadDiscoveryLock(discoveryLock)
				if err != nil {
					return err
				}
				discoveryWorker, err = scannerdiscoveryworker.New(scannerdiscoveryworker.Config{
					Store: persistence,
					Runner: scannerdiscoveryworker.EngineRunner{
						ExpectedDefinitionCommit: definitionCommit,
						Engine: scannerdiscovery.Engine{
							Manifest: discoveryManifestDefinition,
							Lock:     discoveryLockDefinition,
							Resolvers: scannerdiscovery.DefaultResolvers(
								latest.Checker{}, nil,
							),
							Config: scannerdiscovery.Config{
								MaxConcurrency:     discoveryMaxConcurrency,
								PerHostConcurrency: discoveryHostConcurrency,
								PerItemTimeout:     discoveryItemTimeout,
								MaxAttempts:        discoverySourceAttempts,
							},
						},
					},
					WorkerID: workerID, PollInterval: discoveryPoll,
					HeartbeatInterval: discoveryHeartbeat,
					LeaseDuration:     discoveryLease, DrainTimeout: discoveryDrain,
					Once: once, Observer: observer,
				})
				if err != nil {
					return err
				}
			}

			var rolloutController *scannerrollout.Controller
			if role == "rollout" || role == "all" {
				rolloutRuntime, runtimeErr := scannerReleaseRolloutRuntime(
					persistence,
					scannerRolloutRuntimeConfig{
						Backend: rolloutBackend, WorkerActive: rolloutWorkerActive,
						ComposeStateRoot:  rolloutComposeStateRoot,
						ComposeAdapter:    rolloutComposeAdapter,
						ComposeArgs:       rolloutComposeArgs,
						ComposeEnvNames:   rolloutComposeEnvNames,
						ComposeMaxOutput:  rolloutComposeMaxOutput,
						DockerPath:        rolloutDockerPath,
						KubernetesAPI:     rolloutKubernetesAPI,
						KubernetesNS:      rolloutKubernetesNS,
						KubernetesToken:   rolloutKubernetesToken,
						KubernetesCA:      rolloutKubernetesCA,
						KubernetesPoll:    rolloutKubernetesPoll,
						KubernetesTimeout: rolloutKubernetesTimeout,
						SyntheticAdapter:  rolloutSyntheticAdapter,
						SyntheticArgs:     rolloutSyntheticArgs,
						SyntheticEnvNames: rolloutSyntheticEnvNames,
						SyntheticMax:      rolloutSyntheticMax,
						SyntheticTimeout:  rolloutSyntheticTimeout,
					},
				)
				if runtimeErr != nil {
					return runtimeErr
				}
				rolloutController, err = scannerrollout.NewController(scannerrollout.Config{
					Store: persistence, Runtime: rolloutRuntime,
					WorkerID: workerID, PollInterval: rolloutPoll,
					ReconcileInterval: rolloutReconcile,
					HeartbeatInterval: rolloutHeartbeat,
					LeaseDuration:     rolloutLease, CohortTimeout: rolloutCohortTimeout,
					Once: once, Observer: observer,
				})
				if err != nil {
					return err
				}
			}

			var proposalWorker *scannerproposalworker.Worker
			if role == "proposal" || role == "all" {
				if proposalExecutorPath == "" {
					return errors.New("--proposal-executor is required for scanner release proposal workers")
				}
				environment, err := selectedEnvironment(proposalEnvNames)
				if err != nil {
					return err
				}
				proposalWorker, err = scannerproposalworker.New(scannerproposalworker.Config{
					Store: persistence,
					Proposer: scannerproposalworker.CommandProposer{
						Path: proposalExecutorPath, Args: proposalExecutorArgs,
						Environment: environment, MaxOutputBytes: proposalMaxOutput,
					},
					WorkerID: workerID, PollInterval: proposalPoll,
					HeartbeatInterval: proposalHeartbeat,
					LeaseDuration:     proposalLease,
					DrainTimeout:      proposalDrain,
					Once:              once,
					Observer:          observer,
				})
				if err != nil {
					return err
				}
			}

			var notificationWorker *scannernotificationworker.Worker
			if role == "notification" || role == "all" {
				environment, err := selectedEnvironment(notificationEnvNames)
				if err != nil {
					return err
				}
				commandAdapter := func(path string, args []string) scannernotification.Adapter {
					if strings.TrimSpace(path) == "" {
						return nil
					}
					return scannernotification.CommandAdapter{
						Path: path, Args: args, Environment: environment,
						MaxOutputBytes: notificationMaxOutput,
					}
				}
				notificationWorker, err = scannernotificationworker.New(
					scannernotificationworker.Config{
						Store: persistence,
						Dispatcher: scannernotification.Dispatcher{
							Webhook: commandAdapter(notificationWebhookPath, notificationWebhookArgs),
							Email:   commandAdapter(notificationEmailPath, notificationEmailArgs),
							SIEM:    commandAdapter(notificationSIEMPath, notificationSIEMArgs),
						},
						WorkerID: workerID, PollInterval: notificationPoll,
						HeartbeatInterval: notificationHeartbeat,
						LeaseDuration:     notificationLease,
						DeliveryTimeout:   notificationDelivery,
						DrainTimeout:      notificationDrain,
						BaseBackoff:       notificationBaseBackoff,
						MaxBackoff:        notificationMaxBackoff,
						Once:              once,
						Observer:          observer,
					},
				)
				if err != nil {
					return err
				}
			}

			var alertWorker *scanneralertworker.Worker
			if role == "alert" || role == "all" {
				alertWorker, err = scanneralertworker.New(scanneralertworker.Config{
					Store: persistence, WorkerID: workerID,
					PolicyScope: policyScope, Interval: alertInterval,
					HeartbeatInterval: alertHeartbeat, LeaseDuration: alertLease,
					Once: once, Observer: observer,
				})
				if err != nil {
					return err
				}
			}

			var registryWorker *scannerregistryworker.Worker
			if role == "registry" || role == "all" {
				registryWorker, err = scannerregistryworker.New(scannerregistryworker.Config{
					Store: persistence, Clients: releaseRegistryClientFactory{store: store},
					WorkerID: workerID, PollInterval: registryPoll,
					HeartbeatInterval: registryHeartbeat, LeaseDuration: registryLease,
					OperationTimeout: registryTimeout, DrainTimeout: registryDrain,
					BaseBackoff: registryBaseBackoff, MaxBackoff: registryMaxBackoff,
					Once: once, Observer: observer,
				})
				if err != nil {
					return err
				}
			}

			if once {
				if scheduler != nil {
					jobs, err := jobProvider.Jobs(ctx)
					if err != nil {
						return err
					}
					if err := scheduler.Tick(ctx, jobs); err != nil {
						return err
					}
				}
				if discoveryWorker != nil {
					if err := discoveryWorker.Run(ctx); err != nil {
						return ignoreWorkerCancellation(err)
					}
				}
				if proposalWorker != nil {
					if err := proposalWorker.Run(ctx); err != nil {
						return ignoreWorkerCancellation(err)
					}
				}
				if notificationWorker != nil {
					if err := notificationWorker.Run(ctx); err != nil {
						return ignoreWorkerCancellation(err)
					}
				}
				if alertWorker != nil {
					if err := alertWorker.Run(ctx); err != nil {
						return ignoreWorkerCancellation(err)
					}
				}
				if registryWorker != nil {
					if err := registryWorker.Run(ctx); err != nil {
						return ignoreWorkerCancellation(err)
					}
				}
				if buildWorker != nil {
					if err := buildWorker.Run(ctx); err != nil {
						return ignoreWorkerCancellation(err)
					}
				}
				if rolloutController != nil {
					return ignoreWorkerCancellation(rolloutController.Run(ctx))
				}
				return nil
			}
			switch role {
			case "alert":
				return ignoreWorkerCancellation(alertWorker.Run(ctx))
			case "build":
				return ignoreWorkerCancellation(buildWorker.Run(ctx))
			case "discovery":
				return ignoreWorkerCancellation(discoveryWorker.Run(ctx))
			case "proposal":
				return ignoreWorkerCancellation(proposalWorker.Run(ctx))
			case "notification":
				return ignoreWorkerCancellation(notificationWorker.Run(ctx))
			case "registry":
				return ignoreWorkerCancellation(registryWorker.Run(ctx))
			case "rollout":
				return ignoreWorkerCancellation(rolloutController.Run(ctx))
			case "scheduler":
				return ignoreWorkerCancellation(runReleaseScheduler(ctx, scheduler, jobProvider, schedulePoll))
			default:
				runContext, stop := context.WithCancel(ctx)
				defer stop()
				failures := make(chan error, 8)
				go func() { failures <- alertWorker.Run(runContext) }()
				go func() { failures <- buildWorker.Run(runContext) }()
				go func() { failures <- discoveryWorker.Run(runContext) }()
				go func() { failures <- proposalWorker.Run(runContext) }()
				go func() { failures <- notificationWorker.Run(runContext) }()
				go func() { failures <- registryWorker.Run(runContext) }()
				go func() { failures <- runReleaseScheduler(runContext, scheduler, jobProvider, schedulePoll) }()
				go func() { failures <- rolloutController.Run(runContext) }()
				first := <-failures
				stop()
				second := <-failures
				third := <-failures
				fourth := <-failures
				fifth := <-failures
				sixth := <-failures
				seventh := <-failures
				eighth := <-failures
				return joinWorkerFailures(first, second, third, fourth, fifth, sixth, seventh, eighth)
			}
		},
	}
	flags := command.Flags()
	flags.StringVar(&role, "role", envOr("WOLF_SCANNER_RELEASE_ROLE", "all"), "process role: alert, build, discovery, notification, proposal, registry, rollout, scheduler, or all")
	flags.BoolVar(&once, "once", false, "perform one schedule/claim pass and exit")
	flags.StringVar(&workerID, "worker-id", os.Getenv("WOLF_SCANNER_RELEASE_WORKER_ID"), "durable worker identity")
	flags.StringSliceVar(
		&platforms, "platform",
		[]string{runtime.GOOS + "/" + runtime.GOARCH},
		"build platforms supported by this worker (repeatable)",
	)
	flags.StringVar(
		&executorBackend, "executor-backend",
		envOr("WOLF_SCANNER_RELEASE_EXECUTOR_BACKEND", "command"),
		"step backend: command, local, buildx, kubernetes-job, or managed",
	)
	flags.StringVar(&executorPath, "executor", os.Getenv("WOLF_SCANNER_RELEASE_EXECUTOR"), "shell-free JSON step executor path")
	flags.StringSliceVar(&executorArgs, "executor-arg", nil, "argument passed to the step executor (repeatable)")
	flags.StringSliceVar(
		&executorEnvNames, "executor-env", []string{"PATH", "SSL_CERT_FILE", "SSL_CERT_DIR"},
		"explicit host environment variable allowed into executor (repeatable)",
	)
	flags.Int64Var(&maxExecutorOutput, "executor-max-output", 4<<20, "maximum executor JSON response bytes")
	flags.IntVar(&maxParallel, "max-parallel-steps", 2, "maximum concurrent dependency-ready build steps")
	flags.IntVar(&maxAttempts, "max-step-attempts", 2, "maximum attempts for retryable build steps")
	flags.DurationVar(&pollInterval, "poll-interval", 2*time.Second, "empty build queue poll interval")
	flags.DurationVar(&heartbeat, "heartbeat", 10*time.Second, "build lease heartbeat interval")
	flags.DurationVar(&leaseDuration, "lease-duration", 45*time.Second, "build claim visibility timeout")
	flags.DurationVar(&drainTimeout, "drain-timeout", 2*time.Minute, "graceful shutdown claim drain limit")
	flags.StringVar(&workspaceRoot, "workspace-root", os.Getenv("WOLF_SCANNER_RELEASE_WORKSPACE"), "ephemeral release workspace parent")

	flags.DurationVar(&schedulePoll, "schedule-poll", time.Minute, "logical schedule evaluation interval")
	flags.DurationVar(&scheduleLease, "schedule-lease", 5*time.Minute, "schedule ownership lease")
	flags.DurationVar(&scheduleBeat, "schedule-heartbeat", time.Minute, "schedule lease heartbeat interval")
	flags.StringVar(&timezone, "schedule-timezone", envOr("WOLF_SCANNER_RELEASE_TIMEZONE", "UTC"), "IANA timezone for release schedules")
	flags.BoolVar(&dailyEnabled, "daily-discovery", true, "enable daily complete update discovery")
	flags.StringVar(&dailyTime, "daily-time", envOr("WOLF_SCANNER_RELEASE_DAILY_TIME", "02:00"), "daily discovery local time (HH:MM)")
	flags.BoolVar(&weeklyEnabled, "weekly-candidate", true, "enable weekly complete candidate operation")
	flags.StringVar(&weeklyTime, "weekly-time", envOr("WOLF_SCANNER_RELEASE_WEEKLY_TIME", "03:00"), "weekly candidate local time (HH:MM)")
	flags.StringVar(&weeklyDay, "weekly-day", envOr("WOLF_SCANNER_RELEASE_WEEKLY_DAY", "Sunday"), "weekly candidate weekday")
	flags.DurationVar(&scheduleJitter, "schedule-jitter", 20*time.Minute, "deterministic maximum schedule jitter")
	flags.StringVar(&policyScope, "policy-scope", envOr("WOLF_SCANNER_RELEASE_POLICY_SCOPE", "global"), "enabled release policy scope")
	flags.StringVar(&definitionCommit, "definition-commit", os.Getenv("WOLF_SCANNER_DEFINITION_COMMIT"), "immutable scanner definition commit")
	flags.DurationVar(&discoveryPoll, "discovery-poll", 2*time.Second, "empty discovery queue poll interval")
	flags.DurationVar(&discoveryHeartbeat, "discovery-heartbeat", 10*time.Second, "discovery lease heartbeat interval")
	flags.DurationVar(&discoveryLease, "discovery-lease-duration", 45*time.Second, "discovery claim visibility timeout")
	flags.DurationVar(&discoveryDrain, "discovery-drain-timeout", time.Minute, "graceful discovery shutdown drain limit")
	flags.StringVar(&discoveryManifest, "discovery-manifest", os.Getenv("WOLF_SCANNER_DISCOVERY_MANIFEST"), "scanner tools manifest path (embedded default when empty)")
	flags.StringVar(&discoveryLock, "discovery-lock", os.Getenv("WOLF_SCANNER_DISCOVERY_LOCK"), "scanner definition lock path")
	flags.IntVar(&discoveryMaxConcurrency, "discovery-max-concurrency", 8, "maximum concurrent discovery items")
	flags.IntVar(&discoveryHostConcurrency, "discovery-per-host-concurrency", 2, "maximum concurrent requests to one discovery host")
	flags.DurationVar(&discoveryItemTimeout, "discovery-item-timeout", 30*time.Second, "timeout for one discovery source attempt")
	flags.IntVar(&discoverySourceAttempts, "discovery-source-attempts", 3, "maximum attempts for a retryable discovery source")
	flags.StringVar(&proposalExecutorPath, "proposal-executor", os.Getenv("WOLF_SCANNER_PROPOSAL_EXECUTOR"), "shell-free JSON proposal executor path")
	flags.StringSliceVar(&proposalExecutorArgs, "proposal-executor-arg", nil, "argument passed to the proposal executor (repeatable)")
	flags.StringSliceVar(
		&proposalEnvNames, "proposal-executor-env",
		executorEnvironmentAllowlist("WOLF_SCANNER_PROPOSAL_EXECUTOR_ENV"),
		"explicit host environment variable allowed into proposal executor (repeatable)",
	)
	flags.Int64Var(&proposalMaxOutput, "proposal-executor-max-output", 4<<20, "maximum proposal executor JSON response bytes")
	flags.DurationVar(&proposalPoll, "proposal-poll", 3*time.Second, "empty proposal queue poll interval")
	flags.DurationVar(&proposalHeartbeat, "proposal-heartbeat", 10*time.Second, "proposal claim heartbeat interval")
	flags.DurationVar(&proposalLease, "proposal-lease-duration", 45*time.Second, "proposal claim visibility timeout")
	flags.DurationVar(&proposalDrain, "proposal-drain-timeout", time.Minute, "graceful proposal shutdown drain limit")
	flags.StringVar(&notificationWebhookPath, "notification-webhook-adapter", os.Getenv("WOLF_SCANNER_NOTIFICATION_WEBHOOK_ADAPTER"), "shell-free webhook adapter path")
	flags.StringSliceVar(&notificationWebhookArgs, "notification-webhook-adapter-arg", nil, "argument passed to the webhook adapter (repeatable)")
	flags.StringVar(&notificationEmailPath, "notification-email-adapter", os.Getenv("WOLF_SCANNER_NOTIFICATION_EMAIL_ADAPTER"), "shell-free email adapter path")
	flags.StringSliceVar(&notificationEmailArgs, "notification-email-adapter-arg", nil, "argument passed to the email adapter (repeatable)")
	flags.StringVar(&notificationSIEMPath, "notification-siem-adapter", os.Getenv("WOLF_SCANNER_NOTIFICATION_SIEM_ADAPTER"), "shell-free SIEM adapter path")
	flags.StringSliceVar(&notificationSIEMArgs, "notification-siem-adapter-arg", nil, "argument passed to the SIEM adapter (repeatable)")
	flags.StringSliceVar(
		&notificationEnvNames, "notification-adapter-env",
		executorEnvironmentAllowlist("WOLF_SCANNER_NOTIFICATION_ADAPTER_ENV"),
		"explicit host environment variable allowed into notification adapters (repeatable)",
	)
	flags.Int64Var(&notificationMaxOutput, "notification-adapter-max-output", 64<<10, "maximum notification adapter response bytes")
	flags.DurationVar(&notificationPoll, "notification-poll", 2*time.Second, "empty notification queue poll interval")
	flags.DurationVar(&notificationHeartbeat, "notification-heartbeat", 10*time.Second, "notification claim heartbeat interval")
	flags.DurationVar(&notificationLease, "notification-lease-duration", 45*time.Second, "notification claim visibility timeout")
	flags.DurationVar(&notificationDelivery, "notification-delivery-timeout", 30*time.Second, "one notification adapter delivery timeout")
	flags.DurationVar(&notificationDrain, "notification-drain-timeout", time.Minute, "graceful notification shutdown drain limit")
	flags.DurationVar(&notificationBaseBackoff, "notification-base-backoff", 15*time.Second, "first notification retry delay before deterministic jitter")
	flags.DurationVar(&notificationMaxBackoff, "notification-max-backoff", 30*time.Minute, "maximum notification retry delay")
	flags.DurationVar(&alertInterval, "alert-interval", 5*time.Minute, "durable scanner alert evaluation interval")
	flags.DurationVar(&alertHeartbeat, "alert-heartbeat", 30*time.Second, "scanner alert evaluator lease heartbeat interval")
	flags.DurationVar(&alertLease, "alert-lease-duration", 2*time.Minute, "scanner alert evaluator ownership lease")
	flags.DurationVar(&rolloutPoll, "rollout-poll", 2*time.Second, "empty rollout queue poll interval")
	flags.DurationVar(&rolloutReconcile, "rollout-reconcile", 15*time.Second, "delay between rollout health reconciliation passes")
	flags.DurationVar(&rolloutHeartbeat, "rollout-heartbeat", 10*time.Second, "rollout claim heartbeat interval")
	flags.DurationVar(&rolloutLease, "rollout-lease-duration", 45*time.Second, "rollout claim visibility timeout")
	flags.DurationVar(&rolloutCohortTimeout, "rollout-cohort-timeout", time.Hour, "deadline for one cohort to converge")
	flags.DurationVar(&rolloutWorkerActive, "rollout-worker-active-within", 2*time.Minute, "maximum worker heartbeat age included in rollout health")
	flags.StringVar(
		&rolloutBackend, "rollout-backend",
		envOr("WOLF_SCANNER_ROLLOUT_BACKEND", "worker-status"),
		"rollout deployment backend: worker-status, compose, or kubernetes",
	)
	flags.StringVar(
		&rolloutComposeStateRoot, "rollout-compose-state-root",
		os.Getenv("WOLF_SCANNER_ROLLOUT_COMPOSE_STATE_ROOT"),
		"absolute durable Compose cohort desired/observed state directory",
	)
	flags.StringVar(
		&rolloutComposeAdapter, "rollout-compose-adapter",
		os.Getenv("WOLF_SCANNER_ROLLOUT_COMPOSE_ADAPTER"),
		"shell-free JSON Compose reload/readback adapter path",
	)
	flags.StringSliceVar(
		&rolloutComposeArgs, "rollout-compose-adapter-arg", nil,
		"argument passed to the Compose adapter (repeatable)",
	)
	flags.StringSliceVar(
		&rolloutComposeEnvNames, "rollout-compose-adapter-env",
		executorEnvironmentAllowlist("WOLF_SCANNER_ROLLOUT_COMPOSE_ADAPTER_ENV"),
		"explicit host environment variable allowed into Compose and Docker commands",
	)
	flags.Int64Var(
		&rolloutComposeMaxOutput, "rollout-compose-adapter-max-output", 1<<20,
		"maximum Compose adapter JSON response bytes",
	)
	flags.StringVar(
		&rolloutDockerPath, "rollout-docker-path",
		envOr("WOLF_SCANNER_ROLLOUT_DOCKER_PATH", "docker"),
		"Docker CLI path used for exact-digest pre-pull and cache readback",
	)
	flags.StringVar(
		&rolloutKubernetesAPI, "rollout-kubernetes-api",
		envOr("WOLF_SCANNER_ROLLOUT_KUBERNETES_API", "https://kubernetes.default.svc"),
		"Kubernetes API server URL for cohort assignments",
	)
	flags.StringVar(
		&rolloutKubernetesNS, "rollout-kubernetes-namespace",
		envOr("WOLF_SCANNER_ROLLOUT_KUBERNETES_NAMESPACE", "default"),
		"Kubernetes namespace containing scanner worker cohorts",
	)
	flags.StringVar(
		&rolloutKubernetesToken, "rollout-kubernetes-token-file",
		envOr(
			"WOLF_SCANNER_ROLLOUT_KUBERNETES_TOKEN_FILE",
			"/var/run/secrets/kubernetes.io/serviceaccount/token",
		),
		"Kubernetes service-account bearer-token file",
	)
	flags.StringVar(
		&rolloutKubernetesCA, "rollout-kubernetes-ca-file",
		envOr(
			"WOLF_SCANNER_ROLLOUT_KUBERNETES_CA_FILE",
			"/var/run/secrets/kubernetes.io/serviceaccount/ca.crt",
		),
		"Kubernetes API CA bundle",
	)
	flags.DurationVar(
		&rolloutKubernetesPoll, "rollout-kubernetes-poll", time.Second,
		"Kubernetes Deployment and image pre-pull readback interval",
	)
	flags.DurationVar(
		&rolloutKubernetesTimeout, "rollout-kubernetes-timeout", 10*time.Minute,
		"Kubernetes Deployment convergence and image pre-pull timeout",
	)
	flags.StringVar(
		&rolloutSyntheticAdapter, "rollout-synthetic-adapter",
		os.Getenv("WOLF_SCANNER_ROLLOUT_SYNTHETIC_ADAPTER"),
		"shell-free JSON signed-fixture synthetic verification adapter path",
	)
	flags.StringSliceVar(
		&rolloutSyntheticArgs, "rollout-synthetic-adapter-arg", nil,
		"argument passed to the synthetic verification adapter (repeatable)",
	)
	flags.StringSliceVar(
		&rolloutSyntheticEnvNames, "rollout-synthetic-adapter-env",
		executorEnvironmentAllowlist("WOLF_SCANNER_ROLLOUT_SYNTHETIC_ADAPTER_ENV"),
		"explicit host environment variable allowed into synthetic verification",
	)
	flags.Int64Var(
		&rolloutSyntheticMax, "rollout-synthetic-adapter-max-output", 4<<20,
		"maximum synthetic verification adapter JSON response bytes",
	)
	flags.DurationVar(
		&rolloutSyntheticTimeout, "rollout-synthetic-timeout", 15*time.Minute,
		"maximum duration of one signed-fixture synthetic verification pass",
	)
	flags.DurationVar(&registryPoll, "registry-poll", 2*time.Second, "empty registry job queue poll interval")
	flags.DurationVar(&registryHeartbeat, "registry-heartbeat", 10*time.Second, "registry job claim heartbeat interval")
	flags.DurationVar(&registryLease, "registry-lease-duration", 45*time.Second, "registry job claim visibility timeout")
	flags.DurationVar(&registryTimeout, "registry-operation-timeout", 30*time.Minute, "maximum duration of one registry repair or cleanup attempt")
	flags.DurationVar(&registryDrain, "registry-drain-timeout", 2*time.Minute, "graceful registry worker shutdown drain limit")
	flags.DurationVar(&registryBaseBackoff, "registry-base-backoff", 15*time.Second, "first registry job retry delay")
	flags.DurationVar(&registryMaxBackoff, "registry-max-backoff", 30*time.Minute, "maximum registry job retry delay")
	flags.StringVar(
		&observabilityAddress,
		"observability-address",
		envOr("WOLF_SCANNER_RELEASE_OBSERVABILITY_ADDR", ":9091"),
		"health, readiness, and Prometheus listen address (set to off to disable)",
	)
	return command
}

func scannerReleaseBuildExecutor(
	rawStore db.Store,
	persistence scannerrelease.Persistence,
	backendName, executorPath string,
	executorArgs, executorEnvNames []string,
	maxExecutorOutput int64,
	platforms []string,
) (scannerreleaseworker.Executor, error) {
	switch strings.ToLower(strings.TrimSpace(backendName)) {
	case "", "command":
		if strings.TrimSpace(executorPath) == "" {
			return nil, errors.New("--executor is required when --executor-backend=command")
		}
		environment, err := selectedEnvironment(executorEnvNames)
		if err != nil {
			return nil, err
		}
		return scannerreleaseworker.CommandExecutor{
			Path: executorPath, Args: executorArgs, Environment: environment,
			MaxOutputBytes: maxExecutorOutput,
		}, nil
	case "local", "offline", "buildkit", "buildx", "kubernetes", "kubernetes-job":
		backend, err := scannerreleasebackend.FromEnvironment(backendName, platforms)
		if err != nil {
			return nil, fmt.Errorf("configure scanner release %s backend: %w", backendName, err)
		}
		executor, err := scannerreleasebackend.NewExecutor(
			backend, scannerreleasebackend.DefaultResourcePolicy(),
		)
		if err != nil {
			return nil, err
		}
		return executor, nil
	case "managed":
		backend, err := managedScannerReleaseBackend(rawStore, persistence, platforms)
		if err != nil {
			return nil, fmt.Errorf("configure managed scanner release backend: %w", err)
		}
		executor, err := scannerreleasebackend.NewExecutor(
			backend, scannerreleasebackend.DefaultResourcePolicy(),
		)
		if err != nil {
			return nil, err
		}
		return executor, nil
	default:
		return nil, fmt.Errorf("unsupported scanner release executor backend %q", backendName)
	}
}

type scannerRolloutRuntimeConfig struct {
	Backend           string
	WorkerActive      time.Duration
	ComposeStateRoot  string
	ComposeAdapter    string
	ComposeArgs       []string
	ComposeEnvNames   []string
	ComposeMaxOutput  int64
	DockerPath        string
	KubernetesAPI     string
	KubernetesNS      string
	KubernetesToken   string
	KubernetesCA      string
	KubernetesPoll    time.Duration
	KubernetesTimeout time.Duration
	SyntheticAdapter  string
	SyntheticArgs     []string
	SyntheticEnvNames []string
	SyntheticMax      int64
	SyntheticTimeout  time.Duration
}

func scannerReleaseRolloutRuntime(
	store scannerrelease.Persistence,
	config scannerRolloutRuntimeConfig,
) (scannerrollout.Runtime, error) {
	if store == nil {
		return nil, errors.New("scanner rollout persistence is required")
	}
	status := scannerrollout.WorkerStatusRuntime{
		Store: store, ActiveWithin: config.WorkerActive,
	}
	backend := strings.ToLower(strings.TrimSpace(config.Backend))
	var runtimeAdapter scannerrollout.Runtime = status
	switch backend {
	case "", "worker-status":
	case "compose":
		if strings.TrimSpace(config.ComposeStateRoot) == "" ||
			strings.TrimSpace(config.ComposeAdapter) == "" {
			return nil, errors.New(
				"Compose rollout requires --rollout-compose-state-root and --rollout-compose-adapter",
			)
		}
		if !filepath.IsAbs(config.ComposeStateRoot) ||
			strings.TrimSpace(config.DockerPath) == "" ||
			config.ComposeMaxOutput <= 0 {
			return nil, errors.New(
				"Compose rollout requires an absolute state root, Docker path, and positive adapter output limit",
			)
		}
		if err := os.MkdirAll(config.ComposeStateRoot, 0o750); err != nil {
			return nil, fmt.Errorf("create Compose rollout state root: %w", err)
		}
		environment, err := selectedEnvironment(config.ComposeEnvNames)
		if err != nil {
			return nil, err
		}
		runtimeAdapter = scannerrollout.CohortDeploymentRuntime{
			Name: "compose", Store: store, Status: status,
			Cache: scannerrollout.DockerImageCache{
				Path: config.DockerPath, Environment: environment,
			},
			Control: scannerrollout.ComposeControl{
				StateRoot: config.ComposeStateRoot,
				Runner: scannerrollout.CommandComposeRunner{
					Path: config.ComposeAdapter, Args: config.ComposeArgs,
					Environment: environment, MaxOutputBytes: config.ComposeMaxOutput,
				},
			},
		}
	case "kubernetes":
		kubernetes := scannerrollout.KubernetesConfig{
			BaseURL: config.KubernetesAPI, Namespace: config.KubernetesNS,
			TokenFile: config.KubernetesToken, CAFile: config.KubernetesCA,
			PollInterval: config.KubernetesPoll, PullTimeout: config.KubernetesTimeout,
		}
		if config.KubernetesPoll <= 0 || config.KubernetesTimeout <= 0 {
			return nil, errors.New(
				"Kubernetes rollout poll interval and timeout must be positive",
			)
		}
		if err := scannerrollout.ValidateKubernetesConfig(kubernetes); err != nil {
			return nil, fmt.Errorf("configure Kubernetes rollout: %w", err)
		}
		runtimeAdapter = scannerrollout.CohortDeploymentRuntime{
			Name: "kubernetes", Store: store, Status: status,
			Cache:   scannerrollout.KubernetesImageCache{Config: kubernetes},
			Control: scannerrollout.KubernetesControl{Config: kubernetes},
		}
	default:
		return nil, fmt.Errorf("unsupported scanner rollout backend %q", config.Backend)
	}

	if strings.TrimSpace(config.SyntheticAdapter) == "" {
		if backend == "compose" || backend == "kubernetes" {
			return nil, errors.New(
				"managed deployment rollouts require --rollout-synthetic-adapter",
			)
		}
		return runtimeAdapter, nil
	}
	if config.SyntheticMax <= 0 || config.SyntheticTimeout <= 0 {
		return nil, errors.New(
			"synthetic verification output limit and timeout must be positive",
		)
	}
	environment, err := selectedEnvironment(config.SyntheticEnvNames)
	if err != nil {
		return nil, err
	}
	corpus, err := scannerrollout.DefaultFixtureCorpus()
	if err != nil {
		return nil, fmt.Errorf("load signed synthetic fixture corpus: %w", err)
	}
	return scannerrollout.DurableSyntheticRuntime{
		Base: runtimeAdapter, Store: store, Corpus: corpus,
		Executor: scannerrollout.CommandSyntheticExecutor{
			Path: config.SyntheticAdapter, Args: config.SyntheticArgs,
			Environment: environment, Timeout: config.SyntheticTimeout,
			MaxOutputBytes: config.SyntheticMax,
		},
	}, nil
}

func loadDiscoveryManifest(path string) (*manifest.Manifest, error) {
	var (
		definition *manifest.Manifest
		err        error
	)
	if path = strings.TrimSpace(path); path != "" {
		definition, err = manifest.LoadFile(path)
	} else {
		definition, err = manifest.LoadDefault()
	}
	if err != nil {
		return nil, fmt.Errorf("load scanner discovery manifest: %w", err)
	}
	return definition, nil
}

func loadDiscoveryLock(path string) (*scannerlock.Lock, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		for _, candidate := range []string{
			scannerlock.DefaultLockPath,
			"/usr/share/wolf/scanners/scanner-lock.yaml",
		} {
			if _, err := os.Stat(candidate); err == nil {
				path = candidate
				break
			}
		}
	}
	if path == "" {
		return nil, errors.New("--discovery-lock or WOLF_SCANNER_DISCOVERY_LOCK is required")
	}
	definition, err := scannerlock.LoadFile(path)
	if err != nil {
		return nil, fmt.Errorf("load scanner discovery lock %q: %w", path, err)
	}
	return definition, nil
}

func runReleaseScheduler(
	ctx context.Context,
	scheduler *scannerreleasescheduler.Scheduler,
	provider scannerreleasescheduler.JobProvider,
	interval time.Duration,
) error {
	if scheduler == nil || provider == nil {
		return errors.New("scanner release scheduler and job provider are required")
	}
	if interval <= 0 {
		return errors.New("scanner release schedule poll interval must be positive")
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		jobs, err := provider.Jobs(ctx)
		if err == nil {
			err = scheduler.Tick(ctx, jobs)
		}
		if err != nil && ctx.Err() == nil {
			wolflog.Warn().Err(err).Msg("scanner release scheduler tick incomplete")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func selectedEnvironment(names []string) ([]string, error) {
	seen := make(map[string]struct{}, len(names))
	environment := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || strings.ContainsAny(name, "=\x00") {
			return nil, fmt.Errorf("invalid executor environment variable name %q", name)
		}
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		seen[name] = struct{}{}
		if value, exists := os.LookupEnv(name); exists {
			environment = append(environment, name+"="+value)
		}
	}
	return environment, nil
}

func executorEnvironmentAllowlist(extraVariable string) []string {
	names := []string{"PATH", "SSL_CERT_FILE", "SSL_CERT_DIR"}
	for _, name := range strings.Split(os.Getenv(extraVariable), ",") {
		if name = strings.TrimSpace(name); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func parseWeekday(value string) (time.Weekday, error) {
	for day := time.Sunday; day <= time.Saturday; day++ {
		if strings.EqualFold(value, day.String()) {
			return day, nil
		}
	}
	return 0, fmt.Errorf("invalid weekly scanner release day %q", value)
}

func ignoreWorkerCancellation(err error) error {
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func joinWorkerFailures(failures ...error) error {
	var actionable []error
	for _, failure := range failures {
		if failure == nil || errors.Is(failure, context.Canceled) {
			continue
		}
		actionable = append(actionable, failure)
	}
	return errors.Join(actionable...)
}
