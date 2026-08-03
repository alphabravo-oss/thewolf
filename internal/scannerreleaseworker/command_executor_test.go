package scannerreleaseworker_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/scannerpipeline"
	"github.com/alphabravocompany/thewolf/internal/scannerreleaseworker"
)

func TestCommandExecutorUsesBoundedStrictJSONProtocol(t *testing.T) {
	t.Parallel()
	request := scannerreleaseworker.StepRequest{
		BuildRunID: "build", CandidateID: "candidate",
		Step: scannerpipeline.Step{
			Key: "test", Kind: scannerpipeline.StepTest,
		},
		Workspace: t.TempDir(),
	}
	executor := scannerreleaseworker.CommandExecutor{
		Path: os.Args[0],
		Args: []string{"-test.run=TestCommandExecutorHelperProcess"},
		Environment: []string{
			"GO_WANT_SCANNER_RELEASE_EXECUTOR_HELPER=1",
			"SCANNER_RELEASE_EXECUTOR_HELPER_MODE=success",
		},
	}
	result, err := executor.Execute(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.OutputDigest != testOutputDigest || result.Summary["status"] != "passed" {
		t.Fatalf("executor result = %#v", result)
	}

	executor.MaxOutputBytes = 8
	executor.Environment[1] = "SCANNER_RELEASE_EXECUTOR_HELPER_MODE=oversized"
	if _, err := executor.Execute(context.Background(), request); err == nil ||
		!strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized executor response error = %v", err)
	}

	executor.MaxOutputBytes = 1024
	executor.Environment[1] = "SCANNER_RELEASE_EXECUTOR_HELPER_MODE=failure"
	if _, err := executor.Execute(context.Background(), request); err == nil ||
		strings.Contains(err.Error(), "raw-executor-secret") ||
		!strings.Contains(err.Error(), "[REDACTED]") {
		t.Fatalf("executor stderr redaction error = %v", err)
	}
}

func TestCommandExecutorHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_SCANNER_RELEASE_EXECUTOR_HELPER") != "1" {
		return
	}
	payload, err := io.ReadAll(os.Stdin)
	if err != nil {
		os.Exit(90)
	}
	var request scannerreleaseworker.StepRequest
	if err := json.Unmarshal(payload, &request); err != nil || request.BuildRunID == "" {
		os.Exit(91)
	}
	switch os.Getenv("SCANNER_RELEASE_EXECUTOR_HELPER_MODE") {
	case "success":
		_ = json.NewEncoder(os.Stdout).Encode(scannerreleaseworker.StepResult{
			OutputDigest: testOutputDigest,
			Summary:      map[string]any{"status": "passed"},
		})
		os.Exit(0)
	case "oversized":
		_, _ = fmt.Fprint(os.Stdout, `{"summary":{"padding":"xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"}}`)
		os.Exit(0)
	default:
		_, _ = fmt.Fprint(os.Stderr, "token=raw-executor-secret")
		os.Exit(2)
	}
}
