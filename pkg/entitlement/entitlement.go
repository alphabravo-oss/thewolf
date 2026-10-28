// Package entitlement is capability-based licensing. Capabilities are named
// strings such as enterprise.identity. Community never grants enterprise.* .
package entitlement

import (
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
	// Remediation is autonomous AI fixing: the fixer worker, its engines and
	// the fix queue. Community scans and reports; Enterprise fixes.
	Remediation  = "enterprise.remediation"
	Compliance   = "enterprise.compliance"
	Support      = "enterprise.support"
	Packaging    = "enterprise.packaging"
	Integrations = "enterprise.integrations"
	Residency    = "enterprise.residency"
	CloudTenancy = "cloud.tenancy"
)

// Catalog is every capability the edition API reports.
func Catalog() []string {
	return []string{
		Scan, Fix, Factory,
		Identity, Intelligence, Verification, Tenancy,
		Plugins, Remediation, Compliance, Support, Packaging, Integrations, Residency,
		CloudTenancy,
	}
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
