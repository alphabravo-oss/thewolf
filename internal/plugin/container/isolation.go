package container

import (
	"os"
	"strings"
	"sync"

	"github.com/alphabravocompany/thewolf/internal/scannertools/manifest"
)

const (
	IsolationStandard = "standard" // deny-by-default network; cap-drop ALL
	IsolationStrict   = "strict"   // same as standard (reserved for tighter profiles)
	IsolationRelaxed  = "relaxed"  // all tools on bridge (legacy)
)

// Direct plugin executions (including fix verification) use the manifest too.
var defaultNetworkRequirements = sync.OnceValue(func() map[string]bool {
	result := map[string]bool{}
	if m, err := manifest.LoadDefault(); err == nil {
		for name, tool := range m.Tools {
			result[name] = tool.NetworkRequired || tool.ResourceClass == "network"
		}
	}
	return result
})

// IsolationFromEnv reads WOLF_SCAN_ISOLATION (standard|strict|relaxed).
func IsolationFromEnv() string {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("WOLF_SCAN_ISOLATION"))) {
	case IsolationRelaxed, "legacy", "bridge":
		return IsolationRelaxed
	case IsolationStrict:
		return IsolationStrict
	default:
		return IsolationStandard
	}
}

// ResolveNetwork is deny-by-default: none unless the tool declared
// network_required (then AllowNetwork, default bridge) or isolation is relaxed.
func ResolveNetwork(cfg *Config, tool string) string {
	if cfg != nil && strings.EqualFold(cfg.Isolation, IsolationRelaxed) {
		if cfg.Network != "" {
			return cfg.Network
		}
		return "bridge"
	}
	requirements := defaultNetworkRequirements()
	if cfg != nil && cfg.NetworkRequirements != nil {
		requirements = cfg.NetworkRequirements
	}
	if requirements[tool] {
		if cfg != nil && strings.TrimSpace(cfg.AllowNetwork) != "" {
			return cfg.AllowNetwork
		}
		return "bridge"
	}
	if cfg != nil && cfg.Network != "" {
		return cfg.Network
	}
	return "none"
}

func appendIsolationFlags(args []string, cfg *Config, tool string) []string {
	args = append(args, "--network", ResolveNetwork(cfg, tool))
	if cfg == nil || !strings.EqualFold(cfg.Isolation, IsolationRelaxed) {
		args = append(args, "--cap-drop", "ALL", "--security-opt", "no-new-privileges")
	}
	return args
}
