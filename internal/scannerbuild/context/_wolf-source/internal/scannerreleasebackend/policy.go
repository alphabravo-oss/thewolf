package scannerreleasebackend

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/alphabravocompany/thewolf/internal/scannerpipeline"
)

var componentPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

type ResourcePolicy struct {
	ByKind map[scannerpipeline.StepKind]Resources
}

func DefaultResourcePolicy() ResourcePolicy {
	gib := int64(1 << 30)
	return ResourcePolicy{ByKind: map[scannerpipeline.StepKind]Resources{
		scannerpipeline.StepCheckout: {
			CPUMilli: 1000, MemoryBytes: 1 * gib, DiskBytes: 10 * gib,
			Timeout: 10 * time.Minute, MaxConcurrency: 2,
		},
		scannerpipeline.StepValidation: {
			CPUMilli: 2000, MemoryBytes: 2 * gib, DiskBytes: 10 * gib,
			Timeout: 15 * time.Minute, MaxConcurrency: 4,
		},
		scannerpipeline.StepBuild: {
			CPUMilli: 4000, MemoryBytes: 8 * gib, DiskBytes: 50 * gib,
			Timeout: 2 * time.Hour, MaxConcurrency: 2,
		},
		scannerpipeline.StepTest: {
			CPUMilli: 2000, MemoryBytes: 4 * gib, DiskBytes: 20 * gib,
			Timeout: time.Hour, MaxConcurrency: 4,
		},
		scannerpipeline.StepSecurity: {
			CPUMilli: 2000, MemoryBytes: 4 * gib, DiskBytes: 20 * gib,
			Timeout: time.Hour, MaxConcurrency: 4,
		},
		scannerpipeline.StepEvidence: {
			CPUMilli: 2000, MemoryBytes: 2 * gib, DiskBytes: 20 * gib,
			Timeout: 30 * time.Minute, MaxConcurrency: 4,
		},
		scannerpipeline.StepPublish: {
			CPUMilli: 2000, MemoryBytes: 2 * gib, DiskBytes: 10 * gib,
			Timeout: time.Hour, MaxConcurrency: 2,
		},
		scannerpipeline.StepIntegration: {
			CPUMilli: 4000, MemoryBytes: 8 * gib, DiskBytes: 30 * gib,
			Timeout: 90 * time.Minute, MaxConcurrency: 1,
		},
		scannerpipeline.StepPolicy: {
			CPUMilli: 1000, MemoryBytes: 1 * gib, DiskBytes: 2 * gib,
			Timeout: 10 * time.Minute, MaxConcurrency: 4,
		},
	}}
}

func (p ResourcePolicy) Resolve(step scannerpipeline.Step) (Action, Resources, error) {
	action, err := actionForStep(step)
	if err != nil {
		return Action{}, Resources{}, err
	}
	resources, ok := p.ByKind[step.Kind]
	if !ok {
		return Action{}, Resources{}, fmt.Errorf("%w: no resource profile for kind %q", ErrResourcePolicy, step.Kind)
	}
	if resources.CPUMilli <= 0 || resources.MemoryBytes <= 0 ||
		resources.DiskBytes <= 0 || resources.Timeout <= 0 ||
		resources.MaxConcurrency <= 0 {
		return Action{}, Resources{}, fmt.Errorf("%w: incomplete profile for kind %q", ErrResourcePolicy, step.Kind)
	}
	if step.Timeout <= 0 {
		return Action{}, Resources{}, fmt.Errorf("%w: step %q has no timeout", ErrResourcePolicy, step.Key)
	}
	if step.Timeout < resources.Timeout {
		resources.Timeout = step.Timeout
	}
	return action, resources, nil
}

func actionForStep(step scannerpipeline.Step) (Action, error) {
	exact := map[string]scannerpipeline.StepKind{
		"checkout":                      scannerpipeline.StepCheckout,
		"manifest-validate":             scannerpipeline.StepValidation,
		"generated-parity":              scannerpipeline.StepValidation,
		"update-source-recheck":         scannerpipeline.StepValidation,
		"lock-reproducibility":          scannerpipeline.StepValidation,
		"license-metadata":              scannerpipeline.StepValidation,
		"finding-regression":            scannerpipeline.StepTest,
		"aggregate-sbom":                scannerpipeline.StepEvidence,
		"fixer-integration":             scannerpipeline.StepIntegration,
		"mirror-copy-verify":            scannerpipeline.StepPublish,
		"mirror-release-closure-verify": scannerpipeline.StepPublish,
		"compose-integration":           scannerpipeline.StepIntegration,
		"kubernetes-integration":        scannerpipeline.StepIntegration,
		"compose-scanner-integration":   scannerpipeline.StepIntegration,
		"kind-scanner-integration":      scannerpipeline.StepIntegration,
		"release-manifest":              scannerpipeline.StepEvidence,
		"release-manifest-signature":    scannerpipeline.StepEvidence,
		"policy-evaluation":             scannerpipeline.StepPolicy,
		"policy-decision-artifact":      scannerpipeline.StepEvidence,
		"candidate-evidence-summary":    scannerpipeline.StepEvidence,
	}
	if kind, ok := exact[step.Key]; ok {
		if step.Kind != kind {
			return Action{}, fmt.Errorf("%w: step %q kind is %q, expected %q", ErrUnsupportedStep, step.Key, step.Kind, kind)
		}
		return Action{Name: step.Key, Kind: step.Kind}, nil
	}
	dynamic := []struct {
		prefix string
		kind   scannerpipeline.StepKind
	}{
		{"build/", scannerpipeline.StepBuild},
		{"image-manifest/", scannerpipeline.StepEvidence},
		{"strict-version-smoke/", scannerpipeline.StepTest},
		{"invocation-smoke/", scannerpipeline.StepTest},
		{"fixer-auth-contract/", scannerpipeline.StepTest},
		{"parser-fixtures/", scannerpipeline.StepTest},
		{"normalized-golden/", scannerpipeline.StepTest},
		{"candidate-stable-comparison/", scannerpipeline.StepTest},
		{"recorded-resource-gate/", scannerpipeline.StepTest},
		{"vulnerability-scan/", scannerpipeline.StepSecurity},
		{"vulnerability-db-identity/", scannerpipeline.StepEvidence},
		{"secret-scan/", scannerpipeline.StepSecurity},
		{"license-scan/", scannerpipeline.StepSecurity},
		{"sbom/", scannerpipeline.StepEvidence},
		{"oci-annotations/", scannerpipeline.StepEvidence},
		{"provenance/", scannerpipeline.StepEvidence},
		{"candidate-publish/", scannerpipeline.StepPublish},
		{"signature/", scannerpipeline.StepEvidence},
		{"published-verify/", scannerpipeline.StepEvidence},
	}
	for _, candidate := range dynamic {
		if !strings.HasPrefix(step.Key, candidate.prefix) {
			continue
		}
		if step.Kind != candidate.kind {
			return Action{}, fmt.Errorf("%w: step %q kind is %q, expected %q", ErrUnsupportedStep, step.Key, step.Kind, candidate.kind)
		}
		remainder := strings.TrimPrefix(step.Key, candidate.prefix)
		if candidate.prefix == "build/" {
			parts := strings.Split(remainder, "/")
			if len(parts) != 2 || !componentPattern.MatchString(parts[0]) {
				return Action{}, fmt.Errorf("%w: invalid build step %q", ErrUnsupportedStep, step.Key)
			}
			platform := strings.ReplaceAll(parts[1], "-", "/")
			if !validPlatform(platform) {
				return Action{}, fmt.Errorf("%w: invalid build platform in %q", ErrUnsupportedStep, step.Key)
			}
			return Action{
				Name: step.Key, Kind: step.Kind, Image: parts[0], Platform: platform,
			}, nil
		}
		if !componentPattern.MatchString(remainder) {
			return Action{}, fmt.Errorf("%w: invalid component step %q", ErrUnsupportedStep, step.Key)
		}
		return Action{Name: step.Key, Kind: step.Kind, Image: remainder}, nil
	}
	return Action{}, fmt.Errorf("%w: %q", ErrUnsupportedStep, step.Key)
}

func validPlatform(value string) bool {
	parts := strings.Split(value, "/")
	return len(parts) >= 2 && len(parts) <= 3 &&
		componentPattern.MatchString(parts[0]) &&
		componentPattern.MatchString(parts[1])
}

// RequiresSigning identifies the two built-in action families whose external
// side effect is delegated to the signer adapter protocol.
func RequiresSigning(action string) bool {
	return strings.HasPrefix(action, "signature/") ||
		action == "release-manifest-signature"
}
