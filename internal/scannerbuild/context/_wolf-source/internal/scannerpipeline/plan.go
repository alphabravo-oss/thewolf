// Package scannerpipeline defines the complete, dependency-checked release
// evidence pipeline executed by durable scanner release workers.
package scannerpipeline

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

type StepKind string

const (
	StepCheckout    StepKind = "checkout"
	StepValidation  StepKind = "validation"
	StepBuild       StepKind = "build"
	StepTest        StepKind = "test"
	StepSecurity    StepKind = "security"
	StepEvidence    StepKind = "evidence"
	StepPublish     StepKind = "publish"
	StepIntegration StepKind = "integration"
	StepPolicy      StepKind = "policy"
)

type ImageKind string

const (
	ImageKindScanner ImageKind = "scanner"
	ImageKindFixer   ImageKind = "fixer"
)

type Image struct {
	Key       string    `json:"key"`
	Kind      ImageKind `json:"kind,omitempty"`
	Platforms []string  `json:"platforms"`
	DependsOn []string  `json:"depends_on,omitempty"`
}

type Inputs struct {
	Images []Image `json:"images"`
	// RequireCompose and RequireKubernetes are retained for persisted-input
	// compatibility. Both integration gates are mandatory for every managed
	// release plan and cannot be disabled by a caller.
	RequireCompose    bool `json:"require_compose"`
	RequireKubernetes bool `json:"require_kubernetes"`
	RequireMirror     bool `json:"require_mirror"`
}

type Step struct {
	Key            string        `json:"key"`
	Kind           StepKind      `json:"kind"`
	DependsOn      []string      `json:"depends_on,omitempty"`
	Timeout        time.Duration `json:"timeout"`
	Retryable      bool          `json:"retryable"`
	Required       bool          `json:"required"`
	ConcurrencyKey string        `json:"concurrency_key,omitempty"`
}

type Plan struct {
	Steps []Step `json:"steps"`
}

// ManagedReleaseImages returns the complete Wolf-owned release inventory.
// Callers receive a deep copy so deployment validation, plan creation, and
// receipt assembly cannot drift through mutation of shared slices.
func ManagedReleaseImages() []Image {
	images := []Image{
		{Key: "default", Kind: ImageKindScanner, Platforms: []string{"linux/amd64"}},
		{Key: "jvm", Kind: ImageKindScanner, Platforms: []string{"linux/amd64"}},
		{Key: "rust", Kind: ImageKindScanner, Platforms: []string{"linux/amd64"}},
		{Key: "codeql", Kind: ImageKindScanner, Platforms: []string{"linux/amd64"}},
		{Key: "fixer-base", Kind: ImageKindFixer, Platforms: []string{"linux/amd64"}},
		{
			Key: "fixer-api", Kind: ImageKindFixer,
			Platforms: []string{"linux/amd64"}, DependsOn: []string{"fixer-base"},
		},
		{
			Key: "fixer-claude", Kind: ImageKindFixer,
			Platforms: []string{"linux/amd64"}, DependsOn: []string{"fixer-base"},
		},
		{
			Key: "fixer-codex", Kind: ImageKindFixer,
			Platforms: []string{"linux/amd64"}, DependsOn: []string{"fixer-base"},
		},
	}
	for index := range images {
		images[index].Platforms = append([]string(nil), images[index].Platforms...)
		images[index].DependsOn = append([]string(nil), images[index].DependsOn...)
	}
	return images
}

func Default(inputs Inputs) (Plan, error) {
	if len(inputs.Images) == 0 {
		return Plan{}, errors.New("scanner pipeline requires at least one image")
	}
	steps := []Step{
		required("checkout", StepCheckout, 5*time.Minute),
		requiredAfter("manifest-validate", StepValidation, 2*time.Minute, "checkout"),
		requiredAfter("generated-parity", StepValidation, 2*time.Minute, "manifest-validate"),
		requiredAfter("update-source-recheck", StepValidation, 10*time.Minute, "manifest-validate"),
		requiredAfter("lock-reproducibility", StepValidation, 2*time.Minute, "generated-parity", "update-source-recheck"),
		requiredAfter("license-metadata", StepValidation, 2*time.Minute, "manifest-validate"),
	}
	imagesByKey := make(map[string]Image, len(inputs.Images))
	for index := range inputs.Images {
		image := inputs.Images[index]
		image.Key = strings.TrimSpace(image.Key)
		if image.Key == "" {
			return Plan{}, errors.New("scanner pipeline image key is required")
		}
		if image.Kind == "" {
			image.Kind = ImageKindScanner
		}
		if image.Kind != ImageKindScanner && image.Kind != ImageKindFixer {
			return Plan{}, fmt.Errorf("scanner pipeline image %q has invalid kind %q", image.Key, image.Kind)
		}
		if _, duplicate := imagesByKey[image.Key]; duplicate {
			return Plan{}, fmt.Errorf("scanner pipeline image %q is duplicated", image.Key)
		}
		imagesByKey[image.Key] = image
		inputs.Images[index] = image
	}
	for _, image := range inputs.Images {
		seenDependencies := make(map[string]struct{}, len(image.DependsOn))
		for _, dependency := range image.DependsOn {
			if dependency == image.Key {
				return Plan{}, fmt.Errorf("scanner pipeline image %q depends on itself", image.Key)
			}
			if _, exists := imagesByKey[dependency]; !exists {
				return Plan{}, fmt.Errorf("scanner pipeline image %q has unknown image dependency %q", image.Key, dependency)
			}
			if _, duplicate := seenDependencies[dependency]; duplicate {
				return Plan{}, fmt.Errorf("scanner pipeline image %q has duplicate dependency %q", image.Key, dependency)
			}
			seenDependencies[dependency] = struct{}{}
		}
	}
	if err := validateImageDependencies(inputs.Images); err != nil {
		return Plan{}, err
	}

	imageFinalSteps := make([]string, 0, len(inputs.Images))
	scannerFinalSteps := make([]string, 0, len(inputs.Images))
	fixerFinalSteps := make([]string, 0, len(inputs.Images))
	for _, image := range inputs.Images {
		if len(image.Platforms) == 0 {
			return Plan{}, fmt.Errorf("scanner pipeline image %q has no platforms", image.Key)
		}
		platformBuilds := make([]string, 0, len(image.Platforms))
		seenPlatforms := make(map[string]struct{}, len(image.Platforms))
		for _, platform := range image.Platforms {
			if !validPlatform(platform) {
				return Plan{}, fmt.Errorf("scanner pipeline image %q has invalid platform %q", image.Key, platform)
			}
			if _, duplicate := seenPlatforms[platform]; duplicate {
				return Plan{}, fmt.Errorf("scanner pipeline image %q has duplicate platform %q", image.Key, platform)
			}
			seenPlatforms[platform] = struct{}{}
			key := "build/" + image.Key + "/" + strings.ReplaceAll(platform, "/", "-")
			buildDependencies := []string{"lock-reproducibility", "license-metadata"}
			for _, dependency := range image.DependsOn {
				buildDependencies = append(buildDependencies, "image-manifest/"+dependency)
			}
			step := requiredAfter(key, StepBuild, 90*time.Minute, buildDependencies...)
			step.Retryable = true
			step.ConcurrencyKey = "build/" + platform
			steps = append(steps, step)
			platformBuilds = append(platformBuilds, key)
		}
		manifestKey := "image-manifest/" + image.Key
		steps = append(steps, requiredAfter(manifestKey, StepEvidence, 10*time.Minute, platformBuilds...))
		versionKey := "strict-version-smoke/" + image.Key
		steps = append(steps, requiredAfter(versionKey, StepTest, 10*time.Minute, manifestKey))
		invocationKey := "invocation-smoke/" + image.Key
		steps = append(steps, requiredAfter(invocationKey, StepTest, 20*time.Minute, manifestKey))
		testGateKey := invocationKey
		if image.Kind == ImageKindScanner {
			parserKey := "parser-fixtures/" + image.Key
			steps = append(steps, requiredAfter(parserKey, StepTest, 20*time.Minute, invocationKey))
			goldenKey := "normalized-golden/" + image.Key
			steps = append(steps, requiredAfter(goldenKey, StepTest, 20*time.Minute, parserKey))
			comparisonKey := "candidate-stable-comparison/" + image.Key
			steps = append(steps, requiredAfter(comparisonKey, StepTest, 30*time.Minute, goldenKey))
			resourceKey := "recorded-resource-gate/" + image.Key
			steps = append(steps, requiredAfter(resourceKey, StepTest, 10*time.Minute, comparisonKey))
			testGateKey = resourceKey
		} else {
			authKey := "fixer-auth-contract/" + image.Key
			steps = append(steps, requiredAfter(authKey, StepTest, 10*time.Minute, invocationKey))
			testGateKey = authKey
		}
		vulnerabilityKey := "vulnerability-scan/" + image.Key
		steps = append(steps, requiredAfter(vulnerabilityKey, StepSecurity, 30*time.Minute, manifestKey))
		databaseKey := "vulnerability-db-identity/" + image.Key
		steps = append(steps, requiredAfter(databaseKey, StepEvidence, 5*time.Minute, vulnerabilityKey))
		secretKey := "secret-scan/" + image.Key
		steps = append(steps, requiredAfter(secretKey, StepSecurity, 15*time.Minute, manifestKey))
		licenseKey := "license-scan/" + image.Key
		steps = append(steps, requiredAfter(licenseKey, StepSecurity, 15*time.Minute, manifestKey))
		sbomKey := "sbom/" + image.Key
		steps = append(steps, requiredAfter(sbomKey, StepEvidence, 15*time.Minute, manifestKey))
		annotationKey := "oci-annotations/" + image.Key
		steps = append(steps, requiredAfter(annotationKey, StepEvidence, 5*time.Minute, manifestKey, sbomKey))
		provenanceKey := "provenance/" + image.Key
		steps = append(steps, requiredAfter(provenanceKey, StepEvidence, 10*time.Minute, manifestKey))
		publishKey := "candidate-publish/" + image.Key
		publish := requiredAfter(
			publishKey,
			StepPublish,
			30*time.Minute,
			versionKey,
			testGateKey,
			vulnerabilityKey,
			databaseKey,
			secretKey,
			licenseKey,
			sbomKey,
			annotationKey,
			provenanceKey,
		)
		publish.Retryable = true
		publish.ConcurrencyKey = "registry-publish"
		steps = append(steps, publish)
		signKey := "signature/" + image.Key
		steps = append(steps, requiredAfter(signKey, StepEvidence, 10*time.Minute, publishKey))
		verifyKey := "published-verify/" + image.Key
		steps = append(steps, requiredAfter(verifyKey, StepEvidence, 10*time.Minute, signKey))
		imageFinalSteps = append(imageFinalSteps, verifyKey)
		if image.Kind == ImageKindScanner {
			scannerFinalSteps = append(scannerFinalSteps, verifyKey)
		} else {
			fixerFinalSteps = append(fixerFinalSteps, verifyKey)
		}
	}

	if len(scannerFinalSteps) == 0 {
		return Plan{}, errors.New("scanner pipeline requires at least one scanner runtime image")
	}
	steps = append(steps,
		requiredAfter("finding-regression", StepTest, 45*time.Minute, scannerFinalSteps...),
		requiredAfter("aggregate-sbom", StepEvidence, 10*time.Minute, imageFinalSteps...),
	)
	if len(fixerFinalSteps) != 0 {
		steps = append(steps, requiredAfter("fixer-integration", StepIntegration, 30*time.Minute, fixerFinalSteps...))
	}
	publicationDependencies := []string{"finding-regression", "aggregate-sbom"}
	if len(fixerFinalSteps) != 0 {
		publicationDependencies = append(publicationDependencies, "fixer-integration")
	}
	if inputs.RequireMirror {
		mirror := requiredAfter("mirror-copy-verify", StepPublish, 45*time.Minute, imageFinalSteps...)
		mirror.Retryable = true
		mirror.ConcurrencyKey = "registry-mirror"
		steps = append(steps, mirror)
		publicationDependencies = append(publicationDependencies, "mirror-copy-verify")
	}
	integrationDependencies := append([]string(nil), imageFinalSteps...)
	steps = append(steps,
		requiredAfter("compose-integration", StepIntegration, 45*time.Minute, integrationDependencies...),
		requiredAfter("compose-scanner-integration", StepIntegration, 45*time.Minute, integrationDependencies...),
		requiredAfter("kubernetes-integration", StepIntegration, 60*time.Minute, integrationDependencies...),
		requiredAfter("kind-scanner-integration", StepIntegration, 60*time.Minute, integrationDependencies...),
	)
	publicationDependencies = append(
		publicationDependencies,
		"compose-integration", "compose-scanner-integration",
		"kubernetes-integration", "kind-scanner-integration",
	)
	steps = append(steps,
		requiredAfter("release-manifest", StepEvidence, 10*time.Minute, publicationDependencies...),
		requiredAfter("release-manifest-signature", StepEvidence, 10*time.Minute, "release-manifest"),
	)
	policyDependency := "release-manifest-signature"
	if inputs.RequireMirror {
		closure := requiredAfter(
			"mirror-release-closure-verify", StepPublish, 30*time.Minute,
			"mirror-copy-verify", "release-manifest-signature",
		)
		closure.Retryable = true
		closure.ConcurrencyKey = "registry-mirror"
		steps = append(steps, closure)
		policyDependency = closure.Key
	}
	steps = append(steps,
		requiredAfter("policy-evaluation", StepPolicy, 5*time.Minute, policyDependency),
		requiredAfter("policy-decision-artifact", StepEvidence, 5*time.Minute, "policy-evaluation"),
		requiredAfter("candidate-evidence-summary", StepEvidence, 5*time.Minute, "policy-decision-artifact"),
	)
	plan := Plan{Steps: steps}
	if err := plan.Validate(); err != nil {
		return Plan{}, err
	}
	return plan, nil
}

func (p Plan) Validate() error {
	if len(p.Steps) == 0 {
		return errors.New("scanner pipeline plan has no steps")
	}
	steps := make(map[string]Step, len(p.Steps))
	for _, step := range p.Steps {
		if strings.TrimSpace(step.Key) == "" {
			return errors.New("scanner pipeline step key is required")
		}
		if _, duplicate := steps[step.Key]; duplicate {
			return fmt.Errorf("duplicate scanner pipeline step %q", step.Key)
		}
		if step.Timeout <= 0 {
			return fmt.Errorf("scanner pipeline step %q has no timeout", step.Key)
		}
		steps[step.Key] = step
	}
	for _, step := range p.Steps {
		for _, dependency := range step.DependsOn {
			if dependency == step.Key {
				return fmt.Errorf("scanner pipeline step %q depends on itself", step.Key)
			}
			if _, exists := steps[dependency]; !exists {
				return fmt.Errorf("scanner pipeline step %q has unknown dependency %q", step.Key, dependency)
			}
		}
	}
	visiting := make(map[string]bool)
	visited := make(map[string]bool)
	var visit func(string) error
	visit = func(key string) error {
		if visiting[key] {
			return fmt.Errorf("scanner pipeline contains a dependency cycle at %q", key)
		}
		if visited[key] {
			return nil
		}
		visiting[key] = true
		for _, dependency := range steps[key].DependsOn {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		visiting[key] = false
		visited[key] = true
		return nil
	}
	for key := range steps {
		if err := visit(key); err != nil {
			return err
		}
	}
	return nil
}

// Ready returns pending steps whose dependencies are all complete, excluding
// steps currently running. The result is sorted for deterministic claiming.
func (p Plan) Ready(completed, running map[string]bool) ([]Step, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	var ready []Step
	for _, step := range p.Steps {
		if completed[step.Key] || running[step.Key] {
			continue
		}
		dependenciesComplete := true
		for _, dependency := range step.DependsOn {
			if !completed[dependency] {
				dependenciesComplete = false
				break
			}
		}
		if dependenciesComplete {
			ready = append(ready, step)
		}
	}
	sort.Slice(ready, func(i, j int) bool { return ready[i].Key < ready[j].Key })
	return ready, nil
}

func required(key string, kind StepKind, timeout time.Duration) Step {
	return Step{Key: key, Kind: kind, Timeout: timeout, Required: true}
}

func requiredAfter(key string, kind StepKind, timeout time.Duration, dependencies ...string) Step {
	step := required(key, kind, timeout)
	step.DependsOn = append([]string(nil), dependencies...)
	return step
}

func validPlatform(platform string) bool {
	parts := strings.Split(platform, "/")
	return len(parts) >= 2 && len(parts) <= 3 && parts[0] != "" && parts[1] != ""
}

func validateImageDependencies(images []Image) error {
	byKey := make(map[string]Image, len(images))
	for _, image := range images {
		byKey[image.Key] = image
	}
	visiting := make(map[string]bool, len(images))
	visited := make(map[string]bool, len(images))
	var visit func(string) error
	visit = func(key string) error {
		if visiting[key] {
			return fmt.Errorf("scanner pipeline contains an image dependency cycle at %q", key)
		}
		if visited[key] {
			return nil
		}
		visiting[key] = true
		for _, dependency := range byKey[key].DependsOn {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		visiting[key] = false
		visited[key] = true
		return nil
	}
	for key := range byKey {
		if err := visit(key); err != nil {
			return err
		}
	}
	return nil
}
