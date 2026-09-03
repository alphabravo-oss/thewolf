// Package cloud is the public contract for Wolf Cloud operations.
// Cloud modules are not part of self-hosted Enterprise.
package cloud

type Status struct {
	Offering  string `json:"offering"`
	Tenancy   bool   `json:"tenancy"`
	Metering  bool   `json:"metering"`
	Regions   bool   `json:"regions"`
	Quotas    bool   `json:"quotas"`
	Abuse     bool   `json:"abuse"`
	ScanZones bool   `json:"scan_zones"`
}

type Reporter interface {
	Status() Status
}

// Meter records Cloud usage. Self-hosted Enterprise does not register one.
// Billing settlement stays out of this contract.
type Meter interface {
	Record(event string, quantity int)
}

// NopMeter discards events. Cloud billing is not implemented here.
type NopMeter struct{}

func (NopMeter) Record(string, int) {}

const Tenancy = "cloud.tenancy"
