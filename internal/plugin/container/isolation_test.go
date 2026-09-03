package container

import (
	"strings"
	"testing"
)

func TestResolveNetworkDenyByDefault(t *testing.T) {
	SetNetworkRequirements(map[string]bool{"trivy": true})
	t.Cleanup(func() { SetNetworkRequirements(nil) })

	cfg := &Config{Network: "none", Isolation: IsolationStandard}
	if got := ResolveNetwork(cfg, "bandit"); got != "none" {
		t.Fatalf("offline tool network = %q", got)
	}
	if got := ResolveNetwork(cfg, "trivy"); got != "bridge" {
		t.Fatalf("network_required tool = %q", got)
	}

	relaxed := &Config{Network: "bridge", Isolation: IsolationRelaxed}
	if got := ResolveNetwork(relaxed, "bandit"); got != "bridge" {
		t.Fatalf("relaxed = %q", got)
	}
}

func TestAppendIsolationFlags(t *testing.T) {
	SetNetworkRequirements(nil)
	args := appendIsolationFlags(nil, &Config{Network: "none"}, "gosec")
	joined := argList(args).String()
	for _, want := range []string{"--network none", "--cap-drop ALL", "--security-opt no-new-privileges"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in %s", want, joined)
		}
	}
}
