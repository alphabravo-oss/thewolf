package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRunScannerToolWrapperCapturesOutputAndExitCode(t *testing.T) {
	resultDir := t.TempDir()
	stdoutPath := filepath.Join(resultDir, "stdout")
	stderrPath := filepath.Join(resultDir, "stderr")
	exitPath := filepath.Join(resultDir, "exit-code")

	code := runScannerToolWrapper(
		context.Background(),
		"",
		stdoutPath,
		stderrPath,
		exitPath,
		1024,
		[]string{"/bin/sh", "-c", "printf 'tool output'; printf 'tool error' >&2; exit 7"},
	)
	if code != 7 {
		t.Fatalf("exit code = %d", code)
	}
	assertFileContent(t, stdoutPath, "tool output")
	assertFileContent(t, stderrPath, "tool error")
	assertFileContent(t, exitPath, "7\n")
}

func TestRunScannerToolWrapperForwardsStdin(t *testing.T) {
	resultDir := t.TempDir()
	stdinPath := filepath.Join(resultDir, "stdin")
	if err := os.WriteFile(stdinPath, []byte("scanner input"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdoutPath := filepath.Join(resultDir, "stdout")
	stderrPath := filepath.Join(resultDir, "stderr")
	exitPath := filepath.Join(resultDir, "exit-code")
	code := runScannerToolWrapper(
		context.Background(),
		stdinPath,
		stdoutPath,
		stderrPath,
		exitPath,
		1024,
		[]string{"/bin/sh", "-c", "cat"},
	)
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	assertFileContent(t, stdoutPath, "scanner input")
	assertFileContent(t, exitPath, "0\n")
}

func TestRunScannerToolWrapperBoundsOutputAndFailsDeterministically(t *testing.T) {
	resultDir := t.TempDir()
	stdoutPath := filepath.Join(resultDir, "stdout")
	stderrPath := filepath.Join(resultDir, "stderr")
	exitPath := filepath.Join(resultDir, "exit-code")

	code := runScannerToolWrapper(
		context.Background(),
		"",
		stdoutPath,
		stderrPath,
		exitPath,
		8,
		[]string{"/bin/sh", "-c", "printf '0123456789abcdef'"},
	)
	if code != scannerOutputLimitExitCode {
		t.Fatalf("exit code = %d, want %d", code, scannerOutputLimitExitCode)
	}
	assertFileContent(t, stdoutPath, "01234567")
	assertFileContent(t, exitPath, "74\n")
	info, err := os.Stat(stderrPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > 8 {
		t.Fatalf("stderr size = %d, want at most 8", info.Size())
	}
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Fatalf("%s = %q, want %q", path, string(data), want)
	}
}
