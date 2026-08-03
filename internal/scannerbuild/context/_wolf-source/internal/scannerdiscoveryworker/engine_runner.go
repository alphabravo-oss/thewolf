package scannerdiscoveryworker

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/scannerdiscovery"
	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

// EngineRunner adapts the persistence-neutral discovery engine to a claimed
// durable run. ExpectedDefinitionCommit binds mounted definitions to the
// immutable identity recorded when the run was enqueued.
type EngineRunner struct {
	Engine                   scannerdiscovery.Engine
	ExpectedDefinitionCommit string
}

func (r EngineRunner) Discover(
	ctx context.Context,
	run scannerrelease.DiscoveryRun,
) (scannerdiscovery.Run, error) {
	if expected := strings.TrimSpace(r.ExpectedDefinitionCommit); expected != "" &&
		run.DefinitionCommit != expected {
		return scannerdiscovery.Run{}, fmt.Errorf(
			"claimed definition commit %q does not match mounted definition commit %q",
			run.DefinitionCommit, expected,
		)
	}
	scope, err := DecodeScope(run.ScopeJSON)
	if err != nil {
		return scannerdiscovery.Run{}, err
	}
	return r.Engine.Discover(ctx, scope)
}

// DecodeScope accepts the canonical v1 discovery scope plus the legacy
// complete/all representations already accepted by the API and scheduler.
func DecodeScope(encoded string) (scannerdiscovery.Scope, error) {
	encoded = strings.TrimSpace(encoded)
	if encoded == "" || encoded == "null" {
		return scannerdiscovery.CompleteScope(), nil
	}
	var scope scannerdiscovery.Scope
	if err := json.Unmarshal([]byte(encoded), &scope); err == nil && scope.Mode != "" {
		return validateScope(scope)
	}
	var mode string
	if err := json.Unmarshal([]byte(encoded), &mode); err == nil {
		return scopeFromMode(mode)
	}
	var legacy struct {
		Type       string                         `json:"type"`
		Mode       string                         `json:"mode"`
		Tools      []string                       `json:"tools"`
		Components []scannerdiscovery.ComponentID `json:"components"`
	}
	if err := json.Unmarshal([]byte(encoded), &legacy); err != nil {
		return scannerdiscovery.Scope{}, fmt.Errorf("decode discovery scope: %w", err)
	}
	if legacy.Mode != "" {
		scope.Mode = scannerdiscovery.ScopeMode(legacy.Mode)
	} else {
		switch strings.ToLower(strings.TrimSpace(legacy.Type)) {
		case "", "all", "complete":
			scope.Mode = scannerdiscovery.ScopeComplete
		case "selected":
			scope.Mode = scannerdiscovery.ScopeSelected
		default:
			return scannerdiscovery.Scope{}, fmt.Errorf("unsupported discovery scope type %q", legacy.Type)
		}
	}
	scope.Tools = legacy.Tools
	scope.Components = legacy.Components
	return validateScope(scope)
}

func scopeFromMode(mode string) (scannerdiscovery.Scope, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "all", "complete":
		return scannerdiscovery.CompleteScope(), nil
	default:
		return scannerdiscovery.Scope{}, fmt.Errorf("unsupported discovery scope mode %q", mode)
	}
}

func validateScope(scope scannerdiscovery.Scope) (scannerdiscovery.Scope, error) {
	switch scope.Mode {
	case scannerdiscovery.ScopeComplete:
		if len(scope.Tools) != 0 || len(scope.Components) != 0 {
			return scannerdiscovery.Scope{}, fmt.Errorf("complete discovery scope cannot select components")
		}
	case scannerdiscovery.ScopeSelected:
		if len(scope.Tools) == 0 && len(scope.Components) == 0 {
			return scannerdiscovery.Scope{}, fmt.Errorf("selected discovery scope is empty")
		}
	default:
		return scannerdiscovery.Scope{}, fmt.Errorf("unsupported discovery scope mode %q", scope.Mode)
	}
	return scope, nil
}
