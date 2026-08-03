package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/alphabravocompany/thewolf/internal/scannerruntime"
	kubernetesruntime "github.com/alphabravocompany/thewolf/internal/scannerruntime/kubernetes"
)

func newScannerJobExecCmd() *cobra.Command {
	var encoded string
	cmd := &cobra.Command{
		Use:    "scanner-job-exec",
		Short:  "Internal: execute one scanner through a Kubernetes Job",
		Hidden: true,
		Run: func(cmd *cobra.Command, args []string) {
			data, err := base64.RawURLEncoding.DecodeString(encoded)
			if err != nil {
				fmt.Fprintln(os.Stderr, "decode scanner invocation:", err)
				os.Exit(1)
			}
			var invocation scannerruntime.Invocation
			if err := json.Unmarshal(data, &invocation); err != nil {
				fmt.Fprintln(os.Stderr, "parse scanner invocation:", err)
				os.Exit(1)
			}
			cfg, err := kubernetesruntime.ConfigFromEnv()
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			os.Exit(kubernetesruntime.Execute(cmd.Context(), cfg, invocation, os.Stdout, os.Stderr))
		},
	}
	cmd.Flags().StringVar(&encoded, "invocation", "", "base64url-encoded scanner invocation")
	_ = cmd.MarkFlagRequired("invocation")
	return cmd
}

func newScannerToolWrapperCmd() *cobra.Command {
	var stdinPath, stdoutPath, stderrPath, exitPath string
	var maxOutputBytes int64
	cmd := &cobra.Command{
		Use:                "scanner-tool-wrapper -- <command> [args...]",
		Short:              "Internal: capture scanner stdout, stderr, and real exit code",
		Hidden:             true,
		DisableFlagParsing: false,
		Args:               cobra.MinimumNArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			code := runScannerToolWrapper(
				cmd.Context(),
				stdinPath,
				stdoutPath,
				stderrPath,
				exitPath,
				maxOutputBytes,
				args,
			)
			os.Exit(code)
		},
	}
	cmd.Flags().StringVar(&stdinPath, "stdin", "", "optional stdin source path")
	cmd.Flags().StringVar(&stdoutPath, "stdout", "", "stdout result path")
	cmd.Flags().StringVar(&stderrPath, "stderr", "", "stderr result path")
	cmd.Flags().StringVar(&exitPath, "exit-code", "", "exit-code result path")
	cmd.Flags().Int64Var(
		&maxOutputBytes,
		"max-output-bytes",
		kubernetesruntime.DefaultScannerOutputMaxBytes,
		"maximum captured bytes per output stream",
	)
	_ = cmd.MarkFlagRequired("stdout")
	_ = cmd.MarkFlagRequired("stderr")
	_ = cmd.MarkFlagRequired("exit-code")
	return cmd
}

const scannerOutputLimitExitCode = 74

func runScannerToolWrapper(
	ctx context.Context,
	stdinPath, stdoutPath, stderrPath, exitPath string,
	maxOutputBytes int64,
	command []string,
) int {
	if len(command) == 0 {
		return 127
	}
	if maxOutputBytes <= 0 {
		maxOutputBytes = kubernetesruntime.DefaultScannerOutputMaxBytes
	}
	for _, resultPath := range []string{stdoutPath, stderrPath, exitPath} {
		if err := os.MkdirAll(filepath.Dir(resultPath), 0o770); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	stdoutFile, err := os.OpenFile(stdoutPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o660)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer stdoutFile.Close()
	stderrFile, err := os.OpenFile(stderrPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o660)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer stderrFile.Close()

	child := exec.CommandContext(ctx, command[0], command[1:]...) // #nosec G204 -- internal scanner manifest/runtime command
	child.Stdin = os.Stdin
	if stdinPath != "" {
		stdinFile, openErr := os.Open(stdinPath)
		if openErr != nil {
			fmt.Fprintln(os.Stderr, openErr)
			return 1
		}
		defer stdinFile.Close()
		child.Stdin = stdinFile
	}
	stdoutCapture := newBoundedCaptureWriter(maxOutputBytes, os.Stdout, stdoutFile)
	stderrCapture := newBoundedCaptureWriter(maxOutputBytes, os.Stderr, stderrFile)
	child.Stdout = stdoutCapture
	child.Stderr = stderrCapture
	child.Cancel = func() error {
		if child.Process == nil {
			return nil
		}
		return child.Process.Signal(syscall.SIGTERM)
	}
	code := 0
	if err := child.Run(); err != nil {
		code = 1
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		}
	}
	if stdoutCapture.Exceeded() || stderrCapture.Exceeded() {
		code = scannerOutputLimitExitCode
		diagnostic := fmt.Sprintf(
			"wolf: scanner output exceeded the %d-byte per-stream limit\n",
			maxOutputBytes,
		)
		_, _ = stderrCapture.Write([]byte(diagnostic))
		if stderrCapture.Exceeded() {
			_, _ = io.WriteString(os.Stderr, diagnostic)
		}
	}
	_ = stdoutFile.Sync()
	_ = stderrFile.Sync()
	_ = os.WriteFile(exitPath, []byte(strconv.Itoa(code)+"\n"), 0o660)
	return code
}

type boundedCaptureWriter struct {
	destinations []io.Writer
	remaining    int64
	exceeded     bool
}

func newBoundedCaptureWriter(limit int64, destinations ...io.Writer) *boundedCaptureWriter {
	return &boundedCaptureWriter{destinations: destinations, remaining: limit}
}

func (w *boundedCaptureWriter) Write(payload []byte) (int, error) {
	originalLength := len(payload)
	if int64(len(payload)) > w.remaining {
		payload = payload[:max(w.remaining, 0)]
		w.exceeded = true
	}
	for _, destination := range w.destinations {
		if len(payload) == 0 {
			break
		}
		written, err := destination.Write(payload)
		if err != nil {
			return written, err
		}
		if written != len(payload) {
			return written, io.ErrShortWrite
		}
	}
	w.remaining -= int64(len(payload))
	return originalLength, nil
}

func (w *boundedCaptureWriter) Exceeded() bool {
	return w.exceeded
}
