package driver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/remediate/meter"
)

// writeFakeBinary writes an executable shell script standing in for the
// container runtime (ExecConfig.Binary), so stream()'s subprocess handling
// can be exercised without docker or a real opencode container — the task's
// constraint is "no real opencode container", not "no subprocess at all".
func writeFakeBinary(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestStreamExtractsLastTextEventNotRawLastLine is a regression test for
// Critical 1: a real session's true last NDJSON line is a step_finish
// accounting event, never prose, so stream must track "text" events
// separately rather than treating the raw last line as the agent's output.
func TestStreamExtractsLastTextEventNotRawLastLine(t *testing.T) {
	bin := writeFakeBinary(t, t.TempDir(), "docker", `
cat <<'EOF'
{"type":"step_start"}
{"type":"text","part":{"text":"reasoning happens here"}}
{"type":"step_finish","part":{"reason":"stop"}}
EOF
`)
	d := &execDriver{cfg: ExecConfig{Binary: bin}}
	m := meter.NewTurns(10) // budget not exhausted by one step_finish
	lastText, err := d.stream(context.Background(), []string{"run"}, os.Environ(), "", m, nil)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if lastText != "reasoning happens here" {
		t.Errorf("lastText = %q, want the text event's content, not the trailing step_finish line", lastText)
	}
}

// TestStreamSurfacesProcessFailureWithStderr is a regression test for
// Important 3: a non-zero exit must not be silently swallowed as success.
func TestStreamSurfacesProcessFailureWithStderr(t *testing.T) {
	bin := writeFakeBinary(t, t.TempDir(), "docker", `
echo '{"type":"step_start"}'
echo "boom: something broke" 1>&2
exit 3
`)
	d := &execDriver{cfg: ExecConfig{Binary: bin}}
	m := meter.NewTurns(10)
	_, err := d.stream(context.Background(), []string{"run"}, os.Environ(), "", m, nil)
	if err == nil {
		t.Fatal("stream succeeded despite a non-zero exit, want an error")
	}
	if !strings.Contains(err.Error(), "boom: something broke") {
		t.Errorf("error %q does not carry the captured stderr", err.Error())
	}
}

// TestStreamKillsContainerByNameOnBudgetExhaustion is a regression test for
// Important 4: canceling stream's own context only SIGKILLs the local CLI
// client process, which cannot signal-proxy into the container and does not
// trigger --rm, so stream must explicitly kill the container by name.
func TestStreamKillsContainerByNameOnBudgetExhaustion(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "killed")
	bin := writeFakeBinary(t, dir, "docker", `
if [ "$1" = "kill" ]; then
  printf '%s' "$2" > `+shQuoteForTest(marker)+`
  exit 0
fi
echo '{"type":"step_finish"}'
`)
	d := &execDriver{cfg: ExecConfig{Binary: bin}}
	m := meter.NewTurns(1) // exhausts on the first step_finish
	_, err := d.stream(context.Background(), []string{"run"}, os.Environ(), "wolf-remediate-test", m, nil)
	if !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("stream err = %v, want ErrBudgetExhausted", err)
	}

	got, readErr := os.ReadFile(marker)
	if readErr != nil {
		t.Fatalf("kill marker not written (container was never explicitly killed): %v", readErr)
	}
	if string(got) != "wolf-remediate-test" {
		t.Errorf("kill marker = %q, want the container name %q", got, "wolf-remediate-test")
	}
}

func shQuoteForTest(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
