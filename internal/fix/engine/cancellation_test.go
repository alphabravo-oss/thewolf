package engine

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A CLI wrapper can exit while its child keeps the output pipe and checkout
// open. Cancellation must stop the child before the worker acknowledges it.
func TestEngineCancellationStopsChildren(t *testing.T) {
	for _, name := range []string{"claude", "codex", "custom", "configured", "opencode"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, name)
			if err := os.WriteFile(bin, []byte("#!/bin/sh\nsleep 30 &\necho $! > child.pid\nwait\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			var eng SubprocessEngine
			switch name {
			case "claude":
				eng = &ClaudeCode{}
			case "codex":
				eng = &Codex{}
			case "opencode":
				eng = &OpenCode{}
			case "configured":
				eng = NewConfigEngine(ToolDefinition{Name: name, Command: bin})
			default:
				eng = &Custom{Command: bin}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan struct{})
			go func() {
				defer close(done)
				_, _ = eng.Fix(ctx, FixRequest{RepoPath: dir, Timeout: 10 * time.Second})
			}()
			var pid int
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				data, _ := os.ReadFile(filepath.Join(dir, "child.pid"))
				pid, _ = strconv.Atoi(strings.TrimSpace(string(data)))
				if pid > 0 {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if pid <= 0 {
				t.Fatal("engine did not start child")
			}
			defer func() {
				_ = syscall.Kill(pid, syscall.SIGKILL)
				<-done
			}()
			cancel()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("cancelled engine is still waiting on its child process")
			}
		})
	}
}
