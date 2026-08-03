package scannerreleasebackend

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/scannerpipeline"
)

type managedTestBackend struct {
	name       string
	capability Capabilities
}

func (b managedTestBackend) Name() string { return b.name }
func (b managedTestBackend) Capabilities(context.Context) (Capabilities, error) {
	return b.capability, nil
}
func (b managedTestBackend) Execute(context.Context, Invocation) (BackendResult, error) {
	return BackendResult{}, errors.New("not implemented")
}

func TestValidateCompletePlanCoverageRequiresEveryExactActionAndPlatform(t *testing.T) {
	t.Parallel()
	plan := scannerpipeline.Plan{Steps: []scannerpipeline.Step{
		{Key: "checkout", Kind: scannerpipeline.StepCheckout, Timeout: time.Minute, Required: true},
		{Key: "build/default/linux-amd64", Kind: scannerpipeline.StepBuild, DependsOn: []string{"checkout"}, Timeout: time.Minute, Required: true},
		{Key: "build/default/linux-arm64", Kind: scannerpipeline.StepBuild, DependsOn: []string{"checkout"}, Timeout: time.Minute, Required: true},
	}}
	capability := completeCapabilities(
		"complete", []string{"checkout", "build/*"},
		[]scannerpipeline.StepKind{scannerpipeline.StepCheckout, scannerpipeline.StepBuild},
	)
	capability.Platforms = []string{"linux/amd64", "linux/arm64"}
	router := Router{Backends: []Backend{managedTestBackend{name: "complete", capability: capability}}}
	if err := ValidateCompletePlanCoverage(
		context.Background(), router, plan, []string{"linux/amd64", "linux/arm64"},
	); err != nil {
		t.Fatal(err)
	}

	missing := capability
	missing.Actions = []string{"checkout"}
	if err := ValidateCompletePlanCoverage(
		context.Background(), Router{Backends: []Backend{managedTestBackend{name: "missing", capability: missing}}},
		plan, []string{"linux/amd64", "linux/arm64"},
	); err == nil || !strings.Contains(err.Error(), "build/default/linux-amd64") {
		t.Fatalf("missing-action coverage error = %v", err)
	}

	wrongFirst := capability
	wrongFirst.Platforms = []string{"linux/amd64"}
	if err := ValidateCompletePlanCoverage(
		context.Background(), Router{Backends: []Backend{
			managedTestBackend{name: "wrong-first", capability: wrongFirst},
			managedTestBackend{name: "complete", capability: capability},
		}}, plan, []string{"linux/amd64", "linux/arm64"},
	); err == nil || !strings.Contains(err.Error(), "first route") {
		t.Fatalf("first-route platform error = %v", err)
	}
}

func TestValidateCompletePlanCoverageRequiresExternalIdempotency(t *testing.T) {
	t.Parallel()
	plan := scannerpipeline.Plan{Steps: []scannerpipeline.Step{
		{Key: "image-manifest/default", Kind: scannerpipeline.StepEvidence, Timeout: time.Minute, Required: true},
	}}
	capability := completeCapabilities(
		"registry", []string{"image-manifest/*"}, []scannerpipeline.StepKind{scannerpipeline.StepEvidence},
	)
	capability.ExternalIdempotency = false
	err := ValidateCompletePlanCoverage(
		context.Background(), Router{Backends: []Backend{managedTestBackend{name: "registry", capability: capability}}},
		plan, nil,
	)
	if err == nil || !strings.Contains(err.Error(), "external idempotency") {
		t.Fatalf("external idempotency coverage error = %v", err)
	}
}
