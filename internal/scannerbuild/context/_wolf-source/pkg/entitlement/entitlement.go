// Package entitlement is capability-based licensing. Capabilities are named
// strings such as enterprise.identity. Community never grants enterprise.* .
package entitlement

import (
	"os"
	"strings"
	"sync"
)

const (
	Scan         = "community.scan"
	Fix          = "community.fix"
	Factory      = "community.factory"
	Identity     = "enterprise.identity"
	Intelligence = "enterprise.intelligence"
	Verification = "enterprise.verification"
	Tenancy      = "enterprise.tenancy"
	Plugins      = "enterprise.plugins"
	Compliance   = "enterprise.compliance"
	Support      = "enterprise.support"
	Packaging    = "enterprise.packaging"
	Integrations = "enterprise.integrations"
	Residency    = "enterprise.residency"
	CloudTenancy = "cloud.tenancy"

	// Synthetic BSL evaluation ceilings. Counsel/business owns production
	// numbers; these exist so the registry and tests have one source.
	SyntheticRepos   = 5
	SyntheticUsers   = 3
	SyntheticWorkers = 1
	SourceSynthetic  = "synthetic"
	LimitsEnv        = "WOLF_COMMUNITY_LIMITS"
)

// Catalog is every capability the edition API reports.
func Catalog() []string {
	return []string{
		Scan, Fix, Factory,
		Identity, Intelligence, Verification, Tenancy,
		Plugins, Compliance, Support, Packaging, Integrations, Residency,
		CloudTenancy,
	}
}

// Limits is the Community evaluation ceiling. Production values are
// configuration, not scattered literals.
type Limits struct {
	Repos    int    `json:"repos"`
	Users    int    `json:"users"`
	Workers  int    `json:"workers"`
	Source   string `json:"source"`
	Enforced bool   `json:"enforced"`
}

func CommunityLimits() Limits {
	return Limits{
		Repos:    SyntheticRepos,
		Users:    SyntheticUsers,
		Workers:  SyntheticWorkers,
		Source:   SourceSynthetic,
		Enforced: EnforceCommunityLimits(),
	}
}

func LimitsEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(LimitsEnv)))
	return v == "1" || v == "true" || v == "yes"
}

// EnforceCommunityLimits is opt-in and only applies to the Community checker.
func EnforceCommunityLimits() bool {
	if !LimitsEnabled() {
		return false
	}
	_, ok := Active().(Community)
	return ok
}

// Checker answers whether a capability is granted.
type Checker interface {
	Allows(capability string) bool
}

// Community grants community.* and denies enterprise.* and cloud.*.
// No commercial license is installed in this binary.
type Community struct{}

func (Community) Allows(capability string) bool {
	c := strings.ToLower(strings.TrimSpace(capability))
	if c == "" {
		return false
	}
	if strings.HasPrefix(c, "enterprise.") || strings.HasPrefix(c, "cloud.") {
		return false
	}
	return true
}

func (Community) Licensed() bool { return false }

func (Community) Product() string { return "Wolf Community" }

var (
	activeMu sync.RWMutex
	active   Checker = Community{}
)

// SetActive replaces the process-wide checker. Overlay license code sets this.
// nil resets to Community.
func SetActive(c Checker) {
	if c == nil {
		c = Community{}
	}
	activeMu.Lock()
	active = c
	activeMu.Unlock()
}

func Active() Checker {
	activeMu.RLock()
	defer activeMu.RUnlock()
	return active
}

// Licensed reports whether the active checker is a signed commercial grant.
func Licensed() bool {
	type licensed interface{ Licensed() bool }
	l, ok := Active().(licensed)
	return ok && l.Licensed()
}
