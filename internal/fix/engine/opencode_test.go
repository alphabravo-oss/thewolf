package engine

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStripRepoOpenCodeConfig(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "opencode.json"), []byte(`{"*":"allow"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, ".opencode"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := stripRepoOpenCodeConfig(dir)
	if len(got) != 2 {
		t.Fatalf("stripped = %v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "opencode.json")); !os.IsNotExist(err) {
		t.Fatal("opencode.json should have been removed")
	}
}

func TestParseOpenCodeUsage(t *testing.T) {
	raw := []byte(`{"type":"step_start"}
{"type":"step_finish","part":{"tokens":{"total":10,"input":8,"output":2},"cost":0.1}}
{"type":"step_finish","part":{"tokens":{"total":5,"input":3,"output":2},"cost":0.05}}
`)
	u := parseOpenCodeUsage(raw)
	if u.Turns != 2 || u.InputTokens != 11 || u.CostUSD < 0.14 {
		t.Fatalf("usage = %+v", u)
	}
}

func TestFormatOpenCodeEvent(t *testing.T) {
	got := formatOpenCodeEvent(`{"type":"tool_use","part":{"tool":"read","state":{"status":"completed","input":{"filePath":"pkg/auth.go"}}}}`)
	if got != "tool read pkg/auth.go" {
		t.Fatalf("tool = %q", got)
	}
	if formatOpenCodeEvent(`{"type":"step_start"}`) != "step start" {
		t.Fatal("step_start")
	}
	if formatOpenCodeEvent(`{"type":"text","part":{"text":"looking at isolation.go"}}`) != "say looking at isolation.go" {
		t.Fatal("text")
	}
	if formatOpenCodeEvent("not json") != "" {
		t.Fatal("garbage")
	}
}

func TestWolfOpenCodeConfigAllowsTaskDeniesBash(t *testing.T) {
	if !strings.Contains(wolfOpenCodeConfig, `"task": "allow"`) {
		t.Fatal("task must be allowed")
	}
	if strings.Contains(wolfOpenCodeConfig, `"bash": "allow"`) {
		t.Fatal("bash must not be allowed")
	}
	if !strings.Contains(wolfOpenCodeConfig, `"*": "deny"`) {
		t.Fatal("default deny required")
	}
}

func TestRunOpenCodeStreaming_StallKillsMuteProcess(t *testing.T) {
	cmd := exec.Command("sh", "-c", `printf '%s\n' '{"type":"step_start"}'; sleep 30`)
	var heartbeats int
	start := time.Now()
	_, err := runOpenCodeStreamingOpts(context.Background(), cmd, func(msg string) {
		if strings.Contains(msg, "still running") {
			heartbeats++
		}
	}, streamOpts{StallAfter: 200 * time.Millisecond, Heartbeat: 50 * time.Millisecond})
	elapsed := time.Since(start)
	if !IsStall(err) {
		t.Fatalf("want stall, got %v", err)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("stall took %s, want well under 5s", elapsed)
	}
	if heartbeats == 0 {
		t.Fatal("expected heartbeat while mute")
	}
	if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		// Wait already ran; process should be reaped.
		if cmd.ProcessState == nil {
			t.Fatal("process group should have been waited on")
		}
	}
}

func TestIsStall(t *testing.T) {
	if !IsStall(ErrStall) || !IsStall(errors.New("stall: no opencode event (last: tool bash)")) {
		t.Fatal("IsStall should match sentinel and wrapped messages")
	}
	if IsStall(nil) || IsStall(errors.New("signal: killed")) {
		t.Fatal("IsStall false positives")
	}
	if !IsStallMessage("stall: no opencode event (last: tool bash)") {
		t.Fatal("IsStallMessage")
	}
}

func TestCountTaskToolEvents(t *testing.T) {
	raw := `{"type":"tool_use","part":{"tool":"read"}}
{"type":"tool_use","part":{"tool":"task","state":{"input":{"description":"fix a.go"}}}}
{"type":"tool_use","part":{"name":"task"}}
`
	if n := CountTaskToolEvents(raw); n != 2 {
		t.Fatalf("task events = %d, want 2", n)
	}
}

func TestCountOpenCodeTurns(t *testing.T) {
	raw := []byte(`{"type":"step_start"}
{"type":"text"}
{"type":"step_finish","part":{"reason":"stop"}}
{"type":"step_start"}
{"type":"step_finish"}
not json
`)
	if n := countOpenCodeTurns(raw); n != 2 {
		t.Fatalf("turns = %d, want 2", n)
	}
}
