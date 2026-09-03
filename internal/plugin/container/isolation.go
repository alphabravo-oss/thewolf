package container

import (
	"os"
	"strings"
	"sync"
)

const (
	IsolationStandard = "standard" // deny-by-default network; cap-drop ALL
	IsolationStrict   = "strict"   // same as standard (reserved for tighter profiles)
	IsolationRelaxed  = "relaxed"  // all tools on bridge (legacy)
)

var (
	networkReqMu sync.RWMutex
	networkReq   map[string]bool
)

// SetNetworkRequirements records which tools may leave --network none.
// Called once per scan from the runner using the scanner manifest.
func SetNetworkRequirements(m map[string]bool) {
	networkReqMu.Lock()
	defer networkReqMu.Unlock()
	networkReq = m
}

func toolNeedsNetwork(tool string) bool {
	networkReqMu.RLock()
	defer networkReqMu.RUnlock()
	if networkReq == nil {
		return false
	}
	return networkReq[tool]
}

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
	if toolNeedsNetwork(tool) {
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
