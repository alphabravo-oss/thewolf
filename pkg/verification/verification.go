// Package verification is the public contract for governed runtime verification.
// Production is denied by default. Engines must not overwrite scanner evidence.
package verification

import (
	"context"
	"os"
	"strings"
)

const (
	EnvProduction = "production"
	EnvSandbox    = "sandbox"
	StatusDenied  = "denied"
	StatusSkipped = "skipped"
)

type Request struct {
	VulnerabilityID string `json:"vulnerability_id"`
	Class           string `json:"class,omitempty"`
	Environment     string `json:"environment,omitempty"`
}

type Result struct {
	Status      string `json:"status"`
	Reason      string `json:"reason"`
	Environment string `json:"environment"`
	Exploitable *bool  `json:"exploitable"`
}

type Engine interface {
	Verify(ctx context.Context, req Request) (Result, error)
}

// DenyEngine never runs a payload. Kill switch and production-deny are enforced here.
type DenyEngine struct{}

func (DenyEngine) Verify(_ context.Context, req Request) (Result, error) {
	env := strings.ToLower(strings.TrimSpace(req.Environment))
	if env == "" {
		env = EnvProduction
	}
	res := Result{Status: StatusDenied, Environment: env, Exploitable: nil}
	if killSwitchOn() {
		res.Reason = "verification kill switch is on"
		return res, nil
	}
	if env == EnvProduction {
		res.Reason = "production-deny default"
		return res, nil
	}
	res.Reason = "sandbox runtime verification is not implemented; deterministic evidence was not modified"
	return res, nil
}

func killSwitchOn() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("WOLF_VERIFY_KILL_SWITCH")))
	return v == "1" || v == "true" || v == "yes" || v == "on"
}
