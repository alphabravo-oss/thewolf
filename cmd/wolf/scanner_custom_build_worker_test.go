package main

import (
	"strings"
	"testing"
	"time"
)

func TestScannerCustomBuildWorkerCommandExposesIsolationControls(t *testing.T) {
	t.Parallel()
	command := newScannerCustomBuildWorkerCmd()
	for _, name := range []string{
		"once", "worker-id", "poll-interval", "heartbeat-interval",
		"lease-duration", "operation-timeout", "observability-address",
	} {
		if command.Flags().Lookup(name) == nil {
			t.Fatalf("custom-build worker is missing --%s", name)
		}
	}
}

func TestApplyCustomBuildWorkerEnvironment(t *testing.T) {
	t.Setenv("WOLF_SCANNER_CUSTOM_BUILD_ONCE", "true")
	t.Setenv("WOLF_SCANNER_CUSTOM_BUILD_POLL_INTERVAL", "3s")
	t.Setenv("WOLF_SCANNER_CUSTOM_BUILD_HEARTBEAT_INTERVAL", "11s")
	t.Setenv("WOLF_SCANNER_CUSTOM_BUILD_LEASE_DURATION", "50s")
	t.Setenv("WOLF_SCANNER_CUSTOM_BUILD_OPERATION_TIMEOUT", "90m")
	command := newScannerCustomBuildWorkerCmd()
	var once bool
	poll, heartbeat, lease, timeout := time.Second, time.Second, time.Second, time.Second
	if err := applyCustomBuildWorkerEnvironment(
		command, &once, &poll, &heartbeat, &lease, &timeout,
	); err != nil {
		t.Fatal(err)
	}
	if !once || poll != 3*time.Second || heartbeat != 11*time.Second ||
		lease != 50*time.Second || timeout != 90*time.Minute {
		t.Fatalf(
			"environment once=%t poll=%s heartbeat=%s lease=%s timeout=%s",
			once, poll, heartbeat, lease, timeout,
		)
	}

	t.Setenv("WOLF_SCANNER_CUSTOM_BUILD_LEASE_DURATION", "not-duration")
	if err := applyCustomBuildWorkerEnvironment(
		command, &once, &poll, &heartbeat, &lease, &timeout,
	); err == nil || !strings.Contains(
		err.Error(), "WOLF_SCANNER_CUSTOM_BUILD_LEASE_DURATION",
	) {
		t.Fatalf("invalid duration error = %v", err)
	}
}
