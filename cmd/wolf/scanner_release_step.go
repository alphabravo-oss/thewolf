package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/alphabravocompany/thewolf/internal/scannerreleasebackend"
	"github.com/alphabravocompany/thewolf/internal/scannerreleaseworker"
)

const scannerReleaseStepInputLimit = 4 << 20

var (
	scannerReleaseStepBearerPattern  = regexp.MustCompile(`(?i)\b(bearer\s+)[A-Za-z0-9._~+/=-]+`)
	scannerReleaseStepSecretPattern  = regexp.MustCompile(`(?i)\b(token|password|passwd|secret|api[_-]?key|authorization|cookie)\s*[:=]\s*([^\s,;]+)`)
	scannerReleaseStepURLUserPattern = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)[^/@\s]+@`)
)

func newScannerReleaseStepCmd() *cobra.Command {
	var requestPath, resultPath, executorPath, adapterPath string
	command := &cobra.Command{
		Use:    "scanner-release-step",
		Short:  "Execute one policy-bound scanner release step payload",
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			invocation, err := readScannerReleaseInvocation(cmd.InOrStdin(), requestPath)
			if err != nil {
				return err
			}
			if scannerreleasebackend.RequiresSigning(invocation.Action.Name) {
				result, err := executeConfiguredSigningStep(cmd.Context(), invocation)
				if err != nil {
					return err
				}
				return writeScannerReleaseBackendResult(
					cmd.OutOrStdout(), resultPath, result,
				)
			}
			if strings.TrimSpace(adapterPath) != "" {
				result, err := executeScannerReleaseAdapterInvocation(
					cmd.Context(), invocation, adapterPath,
				)
				if err != nil {
					return err
				}
				return writeScannerReleaseBackendResult(cmd.OutOrStdout(), resultPath, result)
			}
			if strings.TrimSpace(executorPath) == "" {
				return errors.New("--executor or WOLF_SCANNER_RELEASE_STEP_EXECUTOR is required")
			}
			environment, err := selectedEnvironment(
				[]string{"PATH", "SSL_CERT_FILE", "SSL_CERT_DIR"},
			)
			if err != nil {
				return err
			}
			result, err := executeScannerReleaseInvocation(
				cmd.Context(), invocation, scannerreleaseworker.CommandExecutor{
					Path: executorPath, Environment: environment,
					MaxOutputBytes: scannerReleaseStepInputLimit,
				},
			)
			if err != nil {
				return err
			}
			return writeScannerReleaseBackendResult(
				cmd.OutOrStdout(), resultPath, result,
			)
		},
	}
	command.Flags().StringVar(&requestPath, "request", "", "invocation JSON path (stdin when empty)")
	command.Flags().StringVar(&resultPath, "result", "", "atomic result JSON path (stdout when empty)")
	command.Flags().StringVar(
		&adapterPath, "adapter", os.Getenv("WOLF_SCANNER_RELEASE_ADAPTER"),
		"absolute full-Invocation adapter executable for a compiled managed lane",
	)
	command.Flags().StringVar(
		&executorPath, "executor",
		envOr(
			"WOLF_SCANNER_RELEASE_STEP_EXECUTOR",
			"/usr/local/bin/wolf-release-command-executor",
		),
		"shell-free legacy step executor binary inside the immutable step image",
	)
	return command
}

func executeScannerReleaseAdapterInvocation(
	ctx context.Context,
	invocation scannerreleasebackend.Invocation,
	adapterPath string,
) (scannerreleasebackend.BackendResult, error) {
	if err := scannerreleasebackend.ValidateInvocation(invocation); err != nil {
		return scannerreleasebackend.BackendResult{}, err
	}
	if !filepath.IsAbs(adapterPath) {
		return scannerreleasebackend.BackendResult{}, errors.New("scanner release adapter path must be absolute")
	}
	payload, err := json.Marshal(invocation)
	if err != nil {
		return scannerreleasebackend.BackendResult{}, err
	}
	environment, err := selectedEnvironment([]string{
		"PATH", "SSL_CERT_FILE", "SSL_CERT_DIR",
		"WOLF_SCANNER_RELEASE_REGISTRY_CREDENTIAL_DIR",
		"WOLF_SCANNER_RELEASE_ENGINE_CREDENTIAL_DIR", "AWS_REGION", "AWS_DEFAULT_REGION",
		"AWS_ROLE_ARN", "AWS_WEB_IDENTITY_TOKEN_FILE", "GOOGLE_APPLICATION_CREDENTIALS",
		"GOOGLE_CLOUD_PROJECT", "AZURE_CLIENT_ID", "AZURE_TENANT_ID",
		"AZURE_FEDERATED_TOKEN_FILE",
	})
	if err != nil {
		return scannerreleasebackend.BackendResult{}, err
	}
	command := exec.CommandContext(ctx, adapterPath) // #nosec G204 -- absolute executable is immutable deployment configuration.
	command.Dir = invocation.Request.Workspace
	command.Env = environment
	command.Stdin = bytes.NewReader(payload)
	var stdout, stderr scannerReleaseStepBuffer
	command.Stdout, command.Stderr = &stdout, &stderr
	runErr := command.Run()
	if stdout.Exceeded() {
		return scannerreleasebackend.BackendResult{}, fmt.Errorf(
			"scanner release adapter stdout exceeds %d bytes", scannerReleaseStepInputLimit,
		)
	}
	if stderr.Exceeded() {
		return scannerreleasebackend.BackendResult{}, fmt.Errorf(
			"scanner release adapter stderr exceeds %d bytes: %s",
			scannerReleaseStepInputLimit, redactScannerReleaseStepText(stderr.String()),
		)
	}
	if runErr != nil {
		return scannerreleasebackend.BackendResult{}, fmt.Errorf(
			"scanner release adapter failed: %w: %s",
			runErr, redactScannerReleaseStepText(stderr.String()),
		)
	}
	decoder := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	decoder.DisallowUnknownFields()
	var result scannerreleasebackend.BackendResult
	if err := decoder.Decode(&result); err != nil {
		return scannerreleasebackend.BackendResult{}, fmt.Errorf("decode scanner release adapter result: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return scannerreleasebackend.BackendResult{}, errors.New("scanner release adapter result has trailing JSON")
	}
	if result.Binding != invocation.Binding || result.ExternalOperationID != invocation.OperationID {
		return scannerreleasebackend.BackendResult{}, errors.New("scanner release adapter result binding is invalid")
	}
	return result, nil
}

type scannerReleaseStepBuffer struct {
	value    bytes.Buffer
	exceeded bool
}

func (b *scannerReleaseStepBuffer) Write(value []byte) (int, error) {
	original := len(value)
	if b.value.Len() >= scannerReleaseStepInputLimit {
		if len(value) > 0 {
			b.exceeded = true
		}
		return original, nil
	}
	remaining := scannerReleaseStepInputLimit - b.value.Len()
	if len(value) > remaining {
		b.exceeded = true
		value = value[:remaining]
	}
	_, err := b.value.Write(value)
	return original, err
}

func (b *scannerReleaseStepBuffer) Bytes() []byte  { return b.value.Bytes() }
func (b *scannerReleaseStepBuffer) String() string { return b.value.String() }
func (b *scannerReleaseStepBuffer) Exceeded() bool { return b.exceeded }

func redactScannerReleaseStepText(value string) string {
	value = scannerReleaseStepBearerPattern.ReplaceAllString(value, `${1}[REDACTED]`)
	value = scannerReleaseStepSecretPattern.ReplaceAllString(value, `${1}=[REDACTED]`)
	value = scannerReleaseStepURLUserPattern.ReplaceAllString(value, `${1}[REDACTED]@`)
	const maximum = 4096
	if len(value) > maximum {
		return value[:maximum] + "…"
	}
	return value
}

func executeScannerReleaseInvocation(
	ctx context.Context,
	invocation scannerreleasebackend.Invocation,
	executor scannerreleaseworker.Executor,
) (scannerreleasebackend.BackendResult, error) {
	if err := scannerreleasebackend.ValidateInvocation(invocation); err != nil {
		return scannerreleasebackend.BackendResult{}, err
	}
	if scannerreleasebackend.RequiresExternalIdempotency(invocation.Action.Name) {
		return scannerreleasebackend.BackendResult{}, fmt.Errorf(
			"%w: generic command bridge cannot prove external operation %s",
			scannerreleasebackend.ErrUnsupportedStep, invocation.OperationID,
		)
	}
	result, err := executor.Execute(ctx, invocation.Request)
	if err != nil {
		return scannerreleasebackend.BackendResult{}, err
	}
	return scannerreleasebackend.BackendResult{
		Result: result, Binding: invocation.Binding,
	}, nil
}

func readScannerReleaseInvocation(
	stdin io.Reader,
	path string,
) (scannerreleasebackend.Invocation, error) {
	var reader io.Reader = stdin
	if strings.TrimSpace(path) != "" {
		file, err := os.Open(path)
		if err != nil {
			return scannerreleasebackend.Invocation{}, err
		}
		defer file.Close()
		reader = file
	}
	value, err := io.ReadAll(io.LimitReader(reader, scannerReleaseStepInputLimit+1))
	if err != nil {
		return scannerreleasebackend.Invocation{}, err
	}
	if len(value) > scannerReleaseStepInputLimit {
		return scannerreleasebackend.Invocation{}, errors.New("scanner release invocation exceeds size limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	var invocation scannerreleasebackend.Invocation
	if err := decoder.Decode(&invocation); err != nil {
		return scannerreleasebackend.Invocation{}, fmt.Errorf("decode scanner release invocation: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return scannerreleasebackend.Invocation{}, errors.New("scanner release invocation has trailing JSON")
	}
	return invocation, nil
}

func writeScannerReleaseBackendResult(
	stdout io.Writer,
	path string,
	result scannerreleasebackend.BackendResult,
) error {
	value, err := json.Marshal(result)
	if err != nil {
		return err
	}
	value = append(value, '\n')
	if strings.TrimSpace(path) == "" {
		_, err = stdout.Write(value)
		return err
	}
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".wolf-release-result-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(value); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}
