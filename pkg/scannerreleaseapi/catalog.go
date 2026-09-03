package scannerreleaseapi

// Catalog is a private or extra scanner catalog. The first-party factory
// stays in Community internal/scannerrelease. Enterprise registers certified
// third-party catalogs here.
type Catalog interface {
	Name() string
	ToolNames() []string
}

// Stub is a named empty catalog used by overlay proof modules.
type Stub struct {
	ID string
}

func (s Stub) Name() string        { return s.ID }
func (s Stub) ToolNames() []string { return nil }
