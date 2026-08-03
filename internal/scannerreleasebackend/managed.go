package scannerreleasebackend

import (
	"context"
	"errors"
	"fmt"

	"github.com/alphabravocompany/thewolf/internal/scannerpipeline"
	"github.com/alphabravocompany/thewolf/internal/scannerreleaseworker"
	"github.com/alphabravocompany/thewolf/internal/scannerreleaseworkspace"
)

// ExecutionContextProvider resolves deployment-owned source and registry
// coordinates for one immutable build. Implementations return no raw
// credentials; registry and Git backends resolve opaque references privately.
type ExecutionContextProvider interface {
	ExecutionContext(context.Context, scannerreleaseworker.StepRequest) (scannerreleaseworkspace.ExecutionContext, error)
}

type ExecutionContextProviderFunc func(context.Context, scannerreleaseworker.StepRequest) (scannerreleaseworkspace.ExecutionContext, error)

func (f ExecutionContextProviderFunc) ExecutionContext(
	ctx context.Context,
	request scannerreleaseworker.StepRequest,
) (scannerreleaseworkspace.ExecutionContext, error) {
	return f(ctx, request)
}

// ManagedBackend is an opt-in, first-party composition boundary. It provisions
// a non-secret context in every fresh workspace and refuses to start unless
// the complete required plan is covered by the composed backends.
type ManagedBackend struct {
	Router            Router
	Contexts          ExecutionContextProvider
	Sources           SourceMaterializer
	RequiredPlan      scannerpipeline.Plan
	RequiredPlatforms []string
}

func (b ManagedBackend) Name() string { return "managed" }

func (b ManagedBackend) Capabilities(ctx context.Context) (Capabilities, error) {
	if b.Contexts == nil {
		return Capabilities{}, errors.New("managed backend requires an execution context provider")
	}
	if b.Sources == nil {
		return Capabilities{}, errors.New("managed backend requires a trusted source materializer")
	}
	if err := ValidateCompletePlanCoverage(ctx, b.Router, b.RequiredPlan, b.RequiredPlatforms); err != nil {
		return Capabilities{}, err
	}
	capabilities, err := b.Router.Capabilities(ctx)
	if err != nil {
		return Capabilities{}, err
	}
	capabilities.Name = b.Name()
	return capabilities, nil
}

func (b ManagedBackend) Execute(
	ctx context.Context,
	invocation Invocation,
) (BackendResult, error) {
	if _, err := b.Capabilities(ctx); err != nil {
		return BackendResult{}, err
	}
	execution, err := b.Contexts.ExecutionContext(ctx, invocation.Request)
	if err != nil {
		return BackendResult{}, fmt.Errorf("resolve managed execution context: %w", err)
	}
	if err := scannerreleaseworkspace.WriteContext(invocation.Request.Workspace, execution); err != nil {
		return BackendResult{}, fmt.Errorf("bind managed execution context: %w", err)
	}
	// Materialize before every action, including checkout. Managed step Jobs
	// receive a read-only source snapshot; checkout is therefore the first
	// verification/evidence action, not a pod-authorized workspace mutation.
	if err := b.Sources.Materialize(ctx, execution, invocation.Request); err != nil {
		return BackendResult{}, fmt.Errorf("materialize managed release source: %w", err)
	}
	return b.Router.Execute(ctx, invocation)
}

// ValidateCompletePlanCoverage resolves every exact action in the durable
// plan and verifies that the first matching route can execute its kind and
// platform. Wildcard advertisements cannot hide a missing image or platform.
func ValidateCompletePlanCoverage(
	ctx context.Context,
	router Router,
	plan scannerpipeline.Plan,
	requiredPlatforms []string,
) error {
	if err := plan.Validate(); err != nil {
		return fmt.Errorf("validate managed release plan: %w", err)
	}
	if len(router.Backends) == 0 {
		return errors.New("managed release router is empty")
	}
	coveredPlatforms := make(map[string]bool, len(requiredPlatforms))
	for _, step := range plan.Steps {
		action, err := actionForStep(step)
		if err != nil {
			return err
		}
		routed := false
		for _, backend := range router.Backends {
			capability, capabilityErr := backend.Capabilities(ctx)
			if capabilityErr != nil {
				return fmt.Errorf("managed backend %s capabilities: %w", backend.Name(), capabilityErr)
			}
			if !supportsAction(capability.Actions, action.Name) ||
				!containsKind(capability.Kinds, action.Kind) {
				continue
			}
			if action.Platform != "" && len(capability.Platforms) != 0 &&
				!contains(capability.Platforms, action.Platform) {
				return fmt.Errorf(
					"%w: first route %q cannot execute %s on %s",
					ErrUnsupportedStep, backend.Name(), action.Name, action.Platform,
				)
			}
			if externalSideEffect(action.Name) && !capability.ExternalIdempotency {
				return fmt.Errorf(
					"%w: route %q cannot prove external idempotency for %s",
					ErrResourcePolicy, backend.Name(), action.Name,
				)
			}
			if action.Platform != "" {
				coveredPlatforms[action.Platform] = true
			}
			routed = true
			break
		}
		if !routed {
			return fmt.Errorf("%w: managed plan action %q has no route", ErrUnsupportedStep, action.Name)
		}
	}
	for _, platform := range requiredPlatforms {
		if !coveredPlatforms[platform] {
			return fmt.Errorf("%w: managed plan does not cover platform %q", ErrUnsupportedStep, platform)
		}
	}
	return nil
}
