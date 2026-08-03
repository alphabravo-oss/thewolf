package scannerrollout

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// SnapshotProgressGate supports a fail-safe maintenance extension in the
// immutable rollout policy snapshot:
//
//	{
//	  "maintenance": {
//	    "required": true,
//	    "window_open": true,
//	    "emergency_override_until": "2026-07-30T22:00:00Z"
//	  }
//	}
//
// Existing policy snapshots omit this extension and remain allowed. Once
// required=true, absence of an open window is denied.
type SnapshotProgressGate struct{}

func (SnapshotProgressGate) Evaluate(
	_ context.Context,
	request GateRequest,
) (GateDecision, error) {
	if request.Rollout == nil {
		return GateDecision{}, errors.New("rollout gate requires a rollout")
	}
	var control struct {
		Maintenance struct {
			Required               bool       `json:"required"`
			WindowOpen             *bool      `json:"window_open"`
			EmergencyOverrideUntil *time.Time `json:"emergency_override_until"`
		} `json:"maintenance"`
	}
	if err := json.Unmarshal([]byte(request.Rollout.PolicySnapshotJSON), &control); err != nil {
		return GateDecision{}, fmt.Errorf("decode rollout maintenance policy: %w", err)
	}
	if control.Maintenance.EmergencyOverrideUntil != nil &&
		request.Now.Before(control.Maintenance.EmergencyOverrideUntil.UTC()) {
		return GateDecision{
			Allowed: true,
			Reason:  "authorized emergency maintenance override is active",
		}, nil
	}
	if control.Maintenance.WindowOpen != nil {
		if *control.Maintenance.WindowOpen {
			return GateDecision{Allowed: true, Reason: "maintenance window is open"}, nil
		}
		return GateDecision{Reason: "maintenance window is closed"}, nil
	}
	if control.Maintenance.Required {
		return GateDecision{Reason: "maintenance window evidence is required"}, nil
	}
	return GateDecision{Allowed: true, Reason: "no maintenance restriction in policy snapshot"}, nil
}
