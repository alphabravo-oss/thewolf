package container

import (
	"strings"
	"testing"
)

func TestResolveNetworkDenyByDefault(t *testing.T) {
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

func TestNetworkPolicyIsPerScan(t *testing.T) {
	online := &Config{Network: "none", AllowNetwork: "scanner-egress", NetworkRequirements: map[string]bool{"semgrep": true}}
	offline := &Config{Network: "none", NetworkRequirements: map[string]bool{}}
	for _, cfg := range []*Config{online, offline, online} {
		want := "none"
		if cfg == online {
			want = "scanner-egress"
		}
		if got := ResolveNetwork(cfg, "semgrep"); got != want {
			t.Fatalf("scan policies interfered: %s", got)
		}
	}
	if got := ResolveNetwork(&Config{Network: "none"}, "semgrep"); got != "bridge" {
		t.Fatalf("direct verification ignored manifest: %s", got)
	}
}

func TestAppendIsolationFlags(t *testing.T) {
	args := appendIsolationFlags(nil, &Config{Network: "none"}, "bandit")
	joined := argList(args).String()
	for _, want := range []string{"--network none", "--cap-drop ALL", "--security-opt no-new-privileges"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in %s", want, joined)
		}
	}
}
