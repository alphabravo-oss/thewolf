package reportingapi

// Reporter is an extra report sink. Community writes artifacts via
// internal/scan/report. Enterprise may register PDF/SIEM reporters.
type Reporter interface {
	Name() string
}

type Stub struct{ ID string }

func (s Stub) Name() string { return s.ID }
