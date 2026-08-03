// Package scannerfeature defines the staged scanner release-management
// enablement contract. It is intentionally independent from HTTP so the API,
// CLI, workers, and deployment validation can make the same decision.
package scannerfeature

import (
	"fmt"
	"strings"
)

const EnvironmentVariable = "WOLF_SCANNER_RELEASE_MODE"

type Mode string

const (
	ModeDisabled      Mode = "disabled"
	ModeReadOnly      Mode = "read_only"
	ModeCandidate     Mode = "candidate"
	ModeCanary        Mode = "canary"
	ModeStableControl Mode = "stable_control"
)

type Capability string

const (
	CapabilityRead      Capability = "read"
	CapabilityCandidate Capability = "candidate"
	CapabilityCanary    Capability = "canary"
	CapabilityStable    Capability = "stable_control"
)

// Parse defaults to read-only observation. Stable assignment is an explicit
// production enablement decision made only after the release definition of
// done passes; an absent environment variable must never grant it implicitly.
func Parse(value string) (Mode, error) {
	switch normalized := strings.ToLower(strings.TrimSpace(value)); normalized {
	case "":
		return ModeReadOnly, nil
	case "stable", "stable-control", "stable_control", "enabled", "true":
		return ModeStableControl, nil
	case "disabled", "off", "false":
		return ModeDisabled, nil
	case "read", "readonly", "read-only", "read_only", "observe", "observe-only":
		return ModeReadOnly, nil
	case "candidate", "candidates":
		return ModeCandidate, nil
	case "canary":
		return ModeCanary, nil
	default:
		return "", fmt.Errorf("unsupported scanner release mode %q", value)
	}
}

func (m Mode) Allows(capability Capability) bool {
	level := map[Mode]int{
		ModeDisabled: 0, ModeReadOnly: 1, ModeCandidate: 2,
		ModeCanary: 3, ModeStableControl: 4,
	}[m]
	required := map[Capability]int{
		CapabilityRead: 1, CapabilityCandidate: 2,
		CapabilityCanary: 3, CapabilityStable: 4,
	}[capability]
	return level >= required && required > 0
}

type Capabilities struct {
	Mode          Mode `json:"mode"`
	Read          bool `json:"read"`
	Candidates    bool `json:"candidates"`
	Canary        bool `json:"canary"`
	StableControl bool `json:"stable_control"`
}

func (m Mode) Capabilities() Capabilities {
	return Capabilities{
		Mode: m, Read: m.Allows(CapabilityRead),
		Candidates:    m.Allows(CapabilityCandidate),
		Canary:        m.Allows(CapabilityCanary),
		StableControl: m.Allows(CapabilityStable),
	}
}
