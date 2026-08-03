package main

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/scannerobservability"
	"github.com/alphabravocompany/thewolf/internal/scannerreleaseworker"
)

func TestScannerReleaseWorkerCommandExposesReleaseFactoryControls(t *testing.T) {
	t.Parallel()
	command := newScannerReleaseWorkerCmd()
	for _, name := range []string{
		"proposal-executor", "proposal-executor-arg", "proposal-executor-env",
		"proposal-executor-max-output", "proposal-poll", "proposal-heartbeat",
		"proposal-lease-duration", "proposal-drain-timeout",
		"notification-webhook-adapter", "notification-webhook-adapter-arg",
		"notification-email-adapter", "notification-email-adapter-arg",
		"notification-siem-adapter", "notification-siem-adapter-arg",
		"notification-adapter-env", "notification-adapter-max-output",
		"notification-poll", "notification-heartbeat",
		"notification-lease-duration", "notification-delivery-timeout",
		"notification-drain-timeout", "notification-base-backoff",
		"notification-max-backoff",
		"role", "rollout-poll", "rollout-reconcile", "rollout-heartbeat",
		"executor-backend",
		"rollout-lease-duration", "rollout-cohort-timeout",
		"rollout-worker-active-within", "observability-address",
		"rollout-backend",
		"rollout-compose-state-root", "rollout-compose-adapter",
		"rollout-compose-adapter-arg", "rollout-compose-adapter-env",
		"rollout-compose-adapter-max-output", "rollout-docker-path",
		"rollout-kubernetes-api", "rollout-kubernetes-namespace",
		"rollout-kubernetes-token-file", "rollout-kubernetes-ca-file",
		"rollout-kubernetes-poll", "rollout-kubernetes-timeout",
		"rollout-synthetic-adapter", "rollout-synthetic-adapter-arg",
		"rollout-synthetic-adapter-env",
		"rollout-synthetic-adapter-max-output", "rollout-synthetic-timeout",
	} {
		if command.Flags().Lookup(name) == nil {
			t.Fatalf("scanner release worker is missing --%s", name)
		}
	}
}

func TestScannerReleaseBuildExecutorPreservesCommandCompatibility(t *testing.T) {
	t.Parallel()
	executor, err := scannerReleaseBuildExecutor(
		nil, nil,
		"command", "/usr/local/bin/release-executor", []string{"--fixed"},
		nil, 1024, []string{"linux/amd64"},
	)
	if err != nil {
		t.Fatal(err)
	}
	command, ok := executor.(scannerreleaseworker.CommandExecutor)
	if !ok || command.Path != "/usr/local/bin/release-executor" ||
		len(command.Args) != 1 || command.Args[0] != "--fixed" {
		t.Fatalf("command executor = %#v", executor)
	}
	if _, err := scannerReleaseBuildExecutor(
		nil, nil,
		"command", "", nil, nil, 1024, nil,
	); err == nil {
		t.Fatal("command backend accepted a missing --executor")
	}
}

func TestConfigureScannerReleaseComponentsReportsEnabledAndDisabledRoles(t *testing.T) {
	t.Parallel()
	registry := scannerobservability.NewRegistry()
	configureScannerReleaseComponents(registry, "scheduler")

	snapshot := registry.Snapshot(context.Background())
	var scheduler, build scannerobservability.ComponentStatus
	for _, component := range snapshot.Components {
		switch component.Component {
		case scannerobservability.ComponentScheduler:
			scheduler = component
		case scannerobservability.ComponentBuild:
			build = component
		}
	}
	if !scheduler.Enabled || scheduler.Status != "active" || !scheduler.Ready {
		t.Fatalf("scheduler health = %#v", scheduler)
	}
	if build.Enabled || build.Status != "disabled" || !build.Ready {
		t.Fatalf("build health = %#v", build)
	}
}

func TestJoinWorkerFailuresDoesNotHideActionableFailure(t *testing.T) {
	t.Parallel()
	actionable := errors.New("rollout controller failed")
	if err := joinWorkerFailures(context.Canceled, actionable, nil); !errors.Is(err, actionable) {
		t.Fatalf("joined failure = %v", err)
	}
	if err := joinWorkerFailures(context.Canceled, nil); err != nil {
		t.Fatalf("cancellation-only failures = %v", err)
	}
}

func TestExecutorEnvironmentAllowlistAddsOnlyNamedVariables(t *testing.T) {
	t.Setenv("WOLF_TEST_EXECUTOR_ENV_NAMES", "GITHUB_TOKEN_FILE, KMS_KEY_FILE, GITHUB_TOKEN_FILE")
	names := executorEnvironmentAllowlist("WOLF_TEST_EXECUTOR_ENV_NAMES")
	for _, expected := range []string{"PATH", "SSL_CERT_FILE", "SSL_CERT_DIR", "GITHUB_TOKEN_FILE", "KMS_KEY_FILE"} {
		if !slices.Contains(names, expected) {
			t.Fatalf("executor environment allowlist %v does not contain %q", names, expected)
		}
	}
}
