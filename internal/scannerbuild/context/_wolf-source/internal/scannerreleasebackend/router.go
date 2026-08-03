package scannerreleasebackend

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alphabravocompany/thewolf/internal/scannerpipeline"
)

// Router composes narrowly capable backends without weakening their
// advertisements. The first backend that explicitly advertises the action is
// selected; unsupported actions fail rather than falling through to a shell.
type Router struct {
	Backends []Backend
}

func (r Router) Name() string { return "routed" }

func (r Router) Capabilities(ctx context.Context) (Capabilities, error) {
	if len(r.Backends) == 0 {
		return Capabilities{}, errors.New("scanner release backend router is empty")
	}
	out := Capabilities{
		Name: r.Name(), MaxTimeout: 24 * time.Hour, MaxConcurrency: 1 << 20,
		EnforcesCPU: true, EnforcesMemory: true, EnforcesDisk: true,
		EnforcesTimeout: true, EnforcesCancellation: true, Idempotent: true,
		ExternalIdempotency: true,
	}
	for _, backend := range r.Backends {
		capability, err := backend.Capabilities(ctx)
		if err != nil {
			return Capabilities{}, fmt.Errorf("%s capabilities: %w", backend.Name(), err)
		}
		out.Actions = appendUnique(out.Actions, capability.Actions...)
		out.Kinds = appendUniqueKind(out.Kinds, capability.Kinds...)
		out.Platforms = appendUnique(out.Platforms, capability.Platforms...)
		out.MaxCPU = max(out.MaxCPU, capability.MaxCPU)
		out.MaxMemory = max(out.MaxMemory, capability.MaxMemory)
		out.MaxDisk = max(out.MaxDisk, capability.MaxDisk)
		out.MaxTimeout = max(out.MaxTimeout, capability.MaxTimeout)
		out.MaxConcurrency = max(out.MaxConcurrency, capability.MaxConcurrency)
		out.EnforcesCPU = out.EnforcesCPU && capability.EnforcesCPU
		out.EnforcesMemory = out.EnforcesMemory && capability.EnforcesMemory
		out.EnforcesDisk = out.EnforcesDisk && capability.EnforcesDisk
		out.EnforcesTimeout = out.EnforcesTimeout && capability.EnforcesTimeout
		out.EnforcesCancellation = out.EnforcesCancellation && capability.EnforcesCancellation
		out.Idempotent = out.Idempotent && capability.Idempotent
		if advertisesExternalAction(capability.Actions) {
			out.ExternalIdempotency =
				out.ExternalIdempotency && capability.ExternalIdempotency
		}
	}
	return out, nil
}

func advertisesExternalAction(actions []string) bool {
	for _, action := range actions {
		if externalSideEffect(strings.TrimSuffix(action, "*")) {
			return true
		}
	}
	return false
}

func (r Router) Execute(ctx context.Context, invocation Invocation) (BackendResult, error) {
	for _, backend := range r.Backends {
		capability, err := backend.Capabilities(ctx)
		if err != nil {
			return BackendResult{}, err
		}
		if !supportsAction(capability.Actions, invocation.Action.Name) ||
			!containsKind(capability.Kinds, invocation.Action.Kind) {
			continue
		}
		if err := authorizeCapability(capability, invocation.Action, invocation.Resources); err != nil {
			return BackendResult{}, err
		}
		return backend.Execute(ctx, invocation)
	}
	return BackendResult{}, fmt.Errorf("%w: router action %q", ErrUnsupportedStep, invocation.Action.Name)
}

func appendUnique(values []string, additions ...string) []string {
	for _, addition := range additions {
		if !contains(values, addition) {
			values = append(values, addition)
		}
	}
	return values
}

func appendUniqueKind(
	values []scannerpipeline.StepKind,
	additions ...scannerpipeline.StepKind,
) []scannerpipeline.StepKind {
	for _, addition := range additions {
		if !containsKind(values, addition) {
			values = append(values, addition)
		}
	}
	return values
}
