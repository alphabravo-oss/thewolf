package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestScannerCustomBuildCLIEndToEnd(t *testing.T) {
	server, token := startServer(t)
	common := []string{"--server", server, "--token", token, "--output", "json"}
	output, err := run(t, append([]string{
		"scanner", "custom-build", "create",
		"--variant", "default",
		"--platform", "linux/amd64",
		"--reason", "CLI custom-build contract",
		"--idempotency-key", "cli-custom-build",
	}, common...)...)
	if err != nil {
		t.Fatalf("create: %v\n%s", err, output)
	}
	var created struct {
		Data struct {
			ID    string `json:"id"`
			State string `json:"state"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(output), &created); err != nil ||
		created.Data.ID == "" || created.Data.State != "queued" {
		t.Fatalf("create output: err=%v body=%s", err, output)
	}

	output, err = run(t, append([]string{
		"scanner", "custom-build", "show", created.Data.ID,
	}, common...)...)
	if err != nil || !strings.Contains(output, created.Data.ID) ||
		strings.Contains(output, "secret_reference") ||
		strings.Contains(output, "lease_token") {
		t.Fatalf("show: %v\n%s", err, output)
	}
	output, err = run(t, append([]string{
		"scanner", "custom-build", "list", "--state", "queued",
	}, common...)...)
	if err != nil || !strings.Contains(output, created.Data.ID) {
		t.Fatalf("list: %v\n%s", err, output)
	}
	output, err = run(t, append([]string{
		"scanner", "custom-build", "cancel", created.Data.ID,
		"--if-match", "1",
		"--reason", "CLI cancellation contract",
		"--idempotency-key", "cli-custom-build-cancel",
	}, common...)...)
	if err != nil || !strings.Contains(output, `"state": "cancelled"`) {
		t.Fatalf("cancel: %v\n%s", err, output)
	}
	output, err = run(t, append([]string{
		"scanner", "custom-build", "events", created.Data.ID,
	}, common...)...)
	if err != nil || !strings.Contains(output, `"state":"cancelled"`) {
		t.Fatalf("events: %v\n%s", err, output)
	}
}

func TestScannerCustomBuildCLIValidatesUnsafeRequestsLocally(t *testing.T) {
	if _, err := run(
		t, "scanner", "custom-build", "create",
		"--variant", "default", "--push",
		"--reason", "missing credential",
	); err == nil || !strings.Contains(err.Error(), "--credential-secret-id") {
		t.Fatalf("push validation error = %v", err)
	}
	if _, err := run(
		t, "scanner", "custom-build", "cancel", "build-id",
		"--reason", "missing revision",
	); err == nil || !strings.Contains(err.Error(), "--if-match") {
		t.Fatalf("cancel validation error = %v", err)
	}
}
