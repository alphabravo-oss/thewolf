package container

// DefaultBucketImages returns the canonical per-tool image map for the
// 4-image split architecture (PLAN.md §5.1). The keys are tool names as
// returned by Plugin.Name(); values are wolf-built bucket-image references.
//
// All images in this map are expected to have wolf-tool-entry as their
// entrypoint, so the shim invokes them as:
//
//	docker run <image> <tool> <args...>
//
// Tools NOT in this map and NOT in DefaultUpstreamTools fall through to
// Config.Image (the default wolf-scanners image).
//
// Example: with bucketBase="ghcr.io/alphabravocompany/wolf-scanners",
// version="1.0":
//
//	DefaultBucketImages("ghcr.io/alphabravocompany/wolf-scanners", "1.0")
//	  → map[string]string{
//	      "infer":   "ghcr.io/alphabravocompany/wolf-scanners-jvm:1.0",
//	      "pmd":     "ghcr.io/alphabravocompany/wolf-scanners-jvm:1.0",
//	      "clippy":  "ghcr.io/alphabravocompany/wolf-scanners-rust:1.0",
//	      "codeql":  "ghcr.io/alphabravocompany/wolf-scanners-codeql:1.0",
//	  }
//
// Operators can override or extend this map via wolf.yaml's
// scan.container.image_overrides.
func DefaultBucketImages(bucketBase, version string) map[string]string {
	if bucketBase == "" || version == "" {
		return nil
	}
	jvm := bucketBase + "-jvm:" + version
	rust := bucketBase + "-rust:" + version
	codeql := bucketBase + "-codeql:" + version
	return map[string]string{
		"infer":  jvm,
		"pmd":    jvm,
		"clippy": rust,
		"codeql": codeql,
	}
}

// DefaultUpstreamTools returns the curated per-tool map of upstream-official
// images, for tools where the maintainer publishes a multi-arch image that
// we trust and don't need to rebuild ourselves.
//
// Each entry routes one tool to a specific upstream image — bypassing the
// wolf-built `wolf-scanners` image. The wolf shim handles the entrypoint
// difference (upstream images expect args directly, not via wolf-tool-entry).
//
// Why this matters: it dramatically shrinks the wolf-built default image
// (no more bundling trivy/semgrep/gitleaks/etc.), and means we're not
// chasing arm64 release tarballs ourselves — upstream maintainers do that.
//
// The cost: more image registries the operator's network must reach.
// Behind a corporate proxy, allowlist:
//   - docker.io (most maintainers publish here)
//   - ghcr.io (terraform-linters and a few others)
//   - quay.io (kubescape)
//
// Tags are pinned to the versions in scanners/versions.env so swapping a
// tool between upstream and bundled is just a config change, not a version
// renegotiation.
//
// Operators can override or empty this map via wolf.yaml's
// scan.container.upstream_tools.
func DefaultUpstreamTools() map[string]ToolImageSpec {
	// NOTE: The image tags below assume the upstream maintainer publishes
	// using the same version we pin in versions.env. If a tag doesn't exist
	// in the registry, EnsureImage will surface that at startup as a clear
	// "image not found" error and the operator can override in wolf.yaml.
	return map[string]ToolImageSpec{
		// SCA / vuln scanners — all multi-arch, maintainer-built.
		"trivy":       {Image: "aquasec/trivy:0.57.0"},
		"grype":       {Image: "anchore/grype:v0.84.0"},
		"syft":        {Image: "anchore/syft:v1.17.0"},
		"osv-scanner": {Image: "ghcr.io/google/osv-scanner:v1.9.1"},

		// Secrets — Gitleaks, TruffleHog official images are multi-arch.
		"gitleaks":   {Image: "zricethezav/gitleaks:v8.21.2"},
		"trufflehog": {Image: "trufflesecurity/trufflehog:3.83.5"},

		// Container / IaC.
		"hadolint":    {Image: "hadolint/hadolint:v2.12.0-alpine"},
		"dockle":      {Image: "goodwithtech/dockle:v0.4.14"},
		"checkov":     {Image: "bridgecrew/checkov:3.2.297"},
		"tflint":      {Image: "ghcr.io/terraform-linters/tflint:v0.54.0"},
		"kubescape":   {Image: "quay.io/kubescape/kubescape-cli:v3.0.22"},
		"kube-linter": {Image: "stackrox/kube-linter:v0.7.1"},

		// SAST — semgrep's official image is multi-arch.
		"semgrep": {Image: "semgrep/semgrep:1.92.0"},

		// DAST.
		"nuclei": {Image: "projectdiscovery/nuclei:v3.3.5"},

		// Docs / specs.
		"vale":     {Image: "jdkato/vale:v3.9.1"},
		"spectral": {Image: "stoplight/spectral:6.13.1"},

		// License scanner — ScanCode's official image is large but
		// avoids the pyicu/libicu-dev build complexity entirely.
		"scancode": {Image: "ghcr.io/nexb/scancode-toolkit:v32.3.0"},

		// Repo-hygiene (new in 2.0).
		"scorecard": {Image: "gcr.io/openssf/scorecard:v5.0.0", Entrypoint: "scorecard"},

		// Stale/insecure-dependency detection across many ecosystems
		// (npm, pip, gem, composer, cargo, go.mod, helm, github-actions,
		// Dockerfile base images, terraform modules, …). Used in
		// dry-run / detect-only mode — wolf never opens PRs from it.
		"renovate": {Image: "ghcr.io/renovatebot/renovate:39.55.0", Entrypoint: "renovate"},

		// IaC SAST. KICS covers Terraform/K8s/Dockerfile/CloudFormation/
		// Ansible/Helm/ARM/OpenAPI/Pulumi with ~3k rules — broader than
		// Trivy/Checkov on those formats.
		"kics": {Image: "checkmarx/kics:v2.1.3"},

		// Policy-as-code. Operator writes Rego policies; conftest evaluates
		// any YAML/JSON/HCL/Dockerfile against them. Apache-2.0.
		"conftest": {Image: "openpolicyagent/conftest:v0.56.0"},

		// Detects deprecated Kubernetes API versions — critical before a
		// cluster upgrade. Apache-2.0.
		"pluto": {Image: "us-docker.pkg.dev/fairwinds-ops/oss/pluto:v5.20.4"},

		// Kotlin static analysis — fills the gap left by Java-focused infer/pmd.
		"detekt": {Image: "detekt/detekt:v1.23.7"},

		// Data-flow / PII / privacy scanner. Different category from
		// CVE-focused scanners. ELv2.
		"bearer": {Image: "bearer/bearer:1.49.0", Entrypoint: "bearer"},
	}
}
