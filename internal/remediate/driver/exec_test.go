package driver

import (
	"strings"
	"testing"
)

func TestExecBuildsArgsWithoutCredentials(t *testing.T) {
	d := &execDriver{cfg: ExecConfig{Image: "wolf-fixer-opencode:test", Provider: "grok"}}
	args, env := d.buildInvocation(ExecuteRequest{
		WorktreePath: "/tmp/wt",
		AuthContent:  `{"grok":{"type":"api","key":"SECRET"}}`,
		Provider:     "grok",
		Model:        "grok-code-fast",
	}, "/tmp/cfg/opencode.json", "do the thing")

	joined := strings.Join(args, " ")
	if strings.Contains(joined, "SECRET") {
		t.Fatalf("credential leaked into argv: %s", joined)
	}
	if !strings.Contains(joined, "--format json") && !strings.Contains(joined, "--format") {
		t.Errorf("missing --format json: %s", joined)
	}
	if !strings.Contains(joined, "--auto") {
		t.Errorf("execute run missing --auto: %s", joined)
	}

	var found bool
	for _, kv := range env {
		if strings.HasPrefix(kv, "OPENCODE_AUTH_CONTENT=") {
			found = true
			if !strings.Contains(kv, "SECRET") {
				t.Error("OPENCODE_AUTH_CONTENT does not carry the credential")
			}
		}
	}
	if !found {
		t.Error("OPENCODE_AUTH_CONTENT not set in env")
	}
}
