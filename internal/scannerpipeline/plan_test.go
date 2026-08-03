package scannerpipeline

import (
	"strings"
	"testing"
)

func TestDefaultPlanContainsCompleteRequiredEvidencePipeline(t *testing.T) {
	t.Parallel()
	plan, err := Default(Inputs{
		Images: []Image{
			{Key: "default", Platforms: []string{"linux/amd64", "linux/arm64"}},
			{Key: "codeql", Platforms: []string{"linux/amd64"}},
		},
		RequireCompose: true, RequireKubernetes: true, RequireMirror: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	keys := make(map[string]Step)
	for _, step := range plan.Steps {
		keys[step.Key] = step
		if !step.Required {
			t.Fatalf("default pipeline step %q is not required", step.Key)
		}
	}
	for _, required := range []string{
		"checkout",
		"lock-reproducibility",
		"build/default/linux-amd64",
		"build/default/linux-arm64",
		"build/codeql/linux-amd64",
		"strict-version-smoke/default",
		"parser-fixtures/default",
		"normalized-golden/default",
		"candidate-stable-comparison/default",
		"recorded-resource-gate/default",
		"vulnerability-scan/default",
		"vulnerability-db-identity/default",
		"secret-scan/default",
		"license-scan/default",
		"sbom/default",
		"provenance/default",
		"candidate-publish/default",
		"signature/default",
		"finding-regression",
		"aggregate-sbom",
		"mirror-copy-verify",
		"mirror-release-closure-verify",
		"compose-integration",
		"kubernetes-integration",
		"compose-scanner-integration",
		"kind-scanner-integration",
		"release-manifest",
		"release-manifest-signature",
		"policy-evaluation",
		"candidate-evidence-summary",
	} {
		if _, exists := keys[required]; !exists {
			t.Errorf("default pipeline missing %q", required)
		}
	}
	if _, exists := keys["build/codeql/linux-arm64"]; exists {
		t.Fatal("plan added an undeclared CodeQL arm64 build")
	}
	if !keys["candidate-publish/default"].Retryable {
		t.Fatal("registry publication should be retryable and idempotent")
	}
	closure := keys["mirror-release-closure-verify"]
	if strings.Join(closure.DependsOn, ",") != "mirror-copy-verify,release-manifest-signature" ||
		strings.Join(keys["policy-evaluation"].DependsOn, ",") != "mirror-release-closure-verify" {
		t.Fatalf("signed mirror closure dependencies = %#v, policy = %#v", closure.DependsOn, keys["policy-evaluation"].DependsOn)
	}
}

func TestPlanReadyRespectsDependencies(t *testing.T) {
	t.Parallel()
	plan, err := Default(Inputs{Images: []Image{{Key: "default", Platforms: []string{"linux/amd64"}}}})
	if err != nil {
		t.Fatal(err)
	}
	ready, err := plan.Ready(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(ready) != 1 || ready[0].Key != "checkout" {
		t.Fatalf("initial ready = %#v", ready)
	}
	completed := map[string]bool{"checkout": true}
	ready, err = plan.Ready(completed, nil)
	if err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(ready))
	for _, step := range ready {
		keys = append(keys, step.Key)
	}
	if strings.Join(keys, ",") != "manifest-validate" {
		t.Fatalf("ready after checkout = %v", keys)
	}
}

func TestDefaultPlanCannotDisableRuntimeIntegrationGates(t *testing.T) {
	t.Parallel()
	plan, err := Default(Inputs{
		Images: []Image{{Key: "default", Platforms: []string{"linux/amd64"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	steps := make(map[string]Step, len(plan.Steps))
	for _, step := range plan.Steps {
		steps[step.Key] = step
	}
	for _, key := range []string{
		"compose-integration", "compose-scanner-integration",
		"kubernetes-integration", "kind-scanner-integration",
	} {
		if !steps[key].Required {
			t.Fatalf("mandatory runtime integration gate %q is absent or optional", key)
		}
	}
}

func TestPlanValidationRejectsCyclesUnknownDependenciesAndInvalidPlatforms(t *testing.T) {
	t.Parallel()
	tests := []Plan{
		{Steps: []Step{
			{Key: "a", Kind: StepTest, Timeout: 1, Required: true, DependsOn: []string{"b"}},
			{Key: "b", Kind: StepTest, Timeout: 1, Required: true, DependsOn: []string{"a"}},
		}},
		{Steps: []Step{
			{Key: "a", Kind: StepTest, Timeout: 1, Required: true, DependsOn: []string{"missing"}},
		}},
	}
	for _, plan := range tests {
		if err := plan.Validate(); err == nil {
			t.Fatalf("invalid plan unexpectedly passed: %#v", plan)
		}
	}
	if _, err := Default(Inputs{
		Images: []Image{{Key: "default", Platforms: []string{"amd64"}}},
	}); err == nil {
		t.Fatal("invalid platform unexpectedly passed")
	}
}

func TestDefaultPlanModelsFixerDependencyAndDistinctQualityGates(t *testing.T) {
	t.Parallel()
	plan, err := Default(Inputs{Images: []Image{
		{Key: "default", Kind: ImageKindScanner, Platforms: []string{"linux/amd64"}},
		{Key: "fixer-base", Kind: ImageKindFixer, Platforms: []string{"linux/amd64", "linux/arm64"}},
		{
			Key: "fixer-codex", Kind: ImageKindFixer,
			Platforms: []string{"linux/amd64", "linux/arm64"},
			DependsOn: []string{"fixer-base"},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	steps := make(map[string]Step, len(plan.Steps))
	for _, step := range plan.Steps {
		steps[step.Key] = step
	}
	build := steps["build/fixer-codex/linux-amd64"]
	if !containsString(build.DependsOn, "image-manifest/fixer-base") {
		t.Fatalf("fixer engine build dependencies = %v", build.DependsOn)
	}
	for _, key := range []string{
		"strict-version-smoke/fixer-codex",
		"invocation-smoke/fixer-codex",
		"fixer-auth-contract/fixer-codex",
		"vulnerability-scan/fixer-codex",
		"sbom/fixer-codex",
		"provenance/fixer-codex",
		"fixer-integration",
	} {
		if _, exists := steps[key]; !exists {
			t.Errorf("fixer pipeline missing %q", key)
		}
	}
	for _, key := range []string{
		"parser-fixtures/fixer-codex",
		"normalized-golden/fixer-codex",
		"candidate-stable-comparison/fixer-codex",
	} {
		if _, exists := steps[key]; exists {
			t.Errorf("fixer pipeline incorrectly includes scanner-only gate %q", key)
		}
	}
	if containsString(steps["finding-regression"].DependsOn, "published-verify/fixer-codex") {
		t.Fatal("scanner finding regression depends on a fixer image")
	}
	if !containsString(steps["aggregate-sbom"].DependsOn, "published-verify/fixer-codex") {
		t.Fatal("aggregate SBOM does not include fixer evidence")
	}
}

func TestDefaultPlanRejectsInvalidImageGraph(t *testing.T) {
	t.Parallel()
	for name, images := range map[string][]Image{
		"fixer-only": {
			{Key: "fixer-base", Kind: ImageKindFixer, Platforms: []string{"linux/amd64"}},
		},
		"unknown dependency": {
			{Key: "default", Platforms: []string{"linux/amd64"}, DependsOn: []string{"missing"}},
		},
		"cycle": {
			{Key: "default", Platforms: []string{"linux/amd64"}, DependsOn: []string{"fixer"}},
			{Key: "fixer", Kind: ImageKindFixer, Platforms: []string{"linux/amd64"}, DependsOn: []string{"default"}},
		},
		"invalid kind": {
			{Key: "default", Kind: "job", Platforms: []string{"linux/amd64"}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Default(Inputs{Images: images}); err == nil {
				t.Fatalf("invalid image graph unexpectedly passed: %#v", images)
			}
		})
	}
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
