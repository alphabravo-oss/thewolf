package scannerrelease

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var publicationDigestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

// RequiredReleaseToolKeys is the complete scanner tool inventory that a
// publication receipt must bind. Keep this contract in the release domain so
// workers and control-plane publication validate the same immutable set.
var RequiredReleaseToolKeys = []string{
	"bandit", "bearer", "brakeman", "cargo-audit", "cargo-deny", "checkov", "clippy", "codeql",
	"conftest", "cppcheck", "detect-secrets", "detekt", "dockle", "eslint", "gitleaks", "gokart",
	"gosec", "govulncheck", "grype", "hadolint", "infer", "kics", "kube-linter", "kubescape",
	"markdownlint", "mypy", "npm-audit", "nuclei", "osv-scanner", "phpstan", "pip-audit", "pluto",
	"pmd", "poutine", "radon", "renovate", "rubocop", "ruff", "scorecard", "semgrep", "shellcheck",
	"spectral", "sqlfluff", "staticcheck", "swiftlint", "syft", "tflint", "trivy", "trufflehog",
	"vale", "vulture", "yamllint", "zizmor",
}

// RequiredPublicationArtifactTypes is the transitive evidence inventory that
// must be retained in every final publication receipt. Per-image SBOM,
// provenance, and signature identities are carried by ReleaseImage.
var RequiredPublicationArtifactTypes = []string{
	"aggregate-sbom", "finding-regression", "mirror-copy-verify",
	"mirror-release-closure-verify",
	"compose-integration", "compose-scanner-integration",
	"kubernetes-integration", "kind-scanner-integration",
	"release-manifest", "release-manifest-signature", "policy-decision-artifact",
}

type requiredReleaseImage struct {
	Kind      string
	Platforms []string
}

var requiredReleaseImages = map[string]requiredReleaseImage{
	"default":      {Kind: ReleaseImageScanner, Platforms: []string{"linux/amd64", "linux/arm64"}},
	"jvm":          {Kind: ReleaseImageScanner, Platforms: []string{"linux/amd64", "linux/arm64"}},
	"rust":         {Kind: ReleaseImageScanner, Platforms: []string{"linux/amd64", "linux/arm64"}},
	"codeql":       {Kind: ReleaseImageScanner, Platforms: []string{"linux/amd64"}},
	"fixer-base":   {Kind: ReleaseImageFixer, Platforms: []string{"linux/amd64", "linux/arm64"}},
	"fixer-api":    {Kind: ReleaseImageFixer, Platforms: []string{"linux/amd64", "linux/arm64"}},
	"fixer-claude": {Kind: ReleaseImageFixer, Platforms: []string{"linux/amd64", "linux/arm64"}},
	"fixer-codex":  {Kind: ReleaseImageFixer, Platforms: []string{"linux/amd64", "linux/arm64"}},
}

// ValidatePublicationReceiptInventory rejects a final receipt unless it
// contains the complete tool, owned-image/platform, and required-artifact
// inventory. It intentionally runs in the worker before the candidate can
// transition to awaiting approval, and again in the publication verifier.
func ValidatePublicationReceiptInventory(receipt PublicationReceipt) error {
	if len(receipt.Tools) != len(RequiredReleaseToolKeys) {
		return fmt.Errorf("release inventory must contain exactly %d tools", len(RequiredReleaseToolKeys))
	}
	if len(receipt.Images) == 0 {
		return errors.New("release inventory must contain images")
	}

	runtimeImageKeys := make(map[string]struct{})
	imageRecords := make(map[string]struct{})
	imageIdentities := make(map[string]string)
	for index, image := range receipt.Images {
		recordKey := image.RegistryTargetID + "\x00" + image.ImageKey
		if _, duplicate := imageRecords[recordKey]; duplicate {
			return fmt.Errorf("release image %q is duplicated for registry target %q", image.ImageKey, image.RegistryTargetID)
		}
		imageRecords[recordKey] = struct{}{}
		kind := NormalizedImageKind(image)
		if kind == ReleaseImageScanner {
			runtimeImageKeys[image.ImageKey] = struct{}{}
		}
		switch {
		case image.ImageKey == "" || image.RegistryTargetID == "" || image.Repository == "":
			return fmt.Errorf("release image %d identity is incomplete", index)
		case kind != ReleaseImageScanner && kind != ReleaseImageFixer:
			return fmt.Errorf("release image %q kind must be scanner or fixer", image.ImageKey)
		case !publicationDigestPattern.MatchString(image.Digest):
			return fmt.Errorf("release image %q digest is invalid", image.ImageKey)
		case image.SignatureStatus != "verified":
			return fmt.Errorf("release image %q signature is not verified", image.ImageKey)
		case !publicationDigestPattern.MatchString(image.SignatureDigest) ||
			!publicationDigestPattern.MatchString(image.SignatureArtifactDigest) ||
			!strings.Contains(image.SignatureArtifactURI, image.SignatureArtifactDigest) ||
			strings.TrimSpace(image.SignatureMediaType) == "" ||
			image.SignatureArtifactSizeBytes <= 0 ||
			strings.TrimSpace(image.SignatureIdentity) == "" ||
			strings.TrimSpace(image.SignatureIssuer) == "" ||
			strings.TrimSpace(image.SignatureSubject) == "" ||
			strings.TrimSpace(image.SignatureTrustRoot) == "" ||
			!publicationDigestPattern.MatchString(image.SignatureOperationID):
			return fmt.Errorf("release image %q exact signature identity is incomplete", image.ImageKey)
		case image.SignatureCertificateDigest != "" &&
			!publicationDigestPattern.MatchString(image.SignatureCertificateDigest):
			return fmt.Errorf("release image %q signature certificate digest is invalid", image.ImageKey)
		case !publicationDigestPattern.MatchString(image.ProvenanceDigest):
			return fmt.Errorf("release image %q provenance digest is invalid", image.ImageKey)
		case !publicationDigestPattern.MatchString(image.SBOMDigest):
			return fmt.Errorf("release image %q SBOM digest is invalid", image.ImageKey)
		case image.SizeBytes < 0:
			return fmt.Errorf("release image %q size is invalid", image.ImageKey)
		}
		var platforms map[string]string
		if err := json.Unmarshal([]byte(image.PlatformDigests), &platforms); err != nil || len(platforms) == 0 {
			return fmt.Errorf("release image %q platform digests are invalid", image.ImageKey)
		}
		for platform, digest := range platforms {
			if !strings.HasPrefix(platform, "linux/") || !publicationDigestPattern.MatchString(digest) {
				return fmt.Errorf("release image %q platform %q digest is invalid", image.ImageKey, platform)
			}
		}
		platformKeys := make([]string, 0, len(platforms))
		for platform := range platforms {
			platformKeys = append(platformKeys, platform)
		}
		sort.Strings(platformKeys)
		canonicalPlatforms := make(map[string]string, len(platformKeys))
		for _, platform := range platformKeys {
			canonicalPlatforms[platform] = platforms[platform]
		}
		encodedPlatforms, _ := json.Marshal(canonicalPlatforms)
		identity := kind + "\x00" + image.Digest + "\x00" + string(encodedPlatforms)
		if existing := imageIdentities[image.ImageKey]; existing != "" && existing != identity {
			return fmt.Errorf("release image %q differs across registry targets", image.ImageKey)
		}
		imageIdentities[image.ImageKey] = identity
	}
	for key, expected := range requiredReleaseImages {
		identity, exists := imageIdentities[key]
		if !exists {
			return fmt.Errorf("release inventory is missing required owned image %q", key)
		}
		parts := strings.SplitN(identity, "\x00", 3)
		if parts[0] != expected.Kind {
			return fmt.Errorf("release image %q has kind %q, expected %q", key, parts[0], expected.Kind)
		}
		var platforms map[string]string
		if err := json.Unmarshal([]byte(parts[2]), &platforms); err != nil || len(platforms) != len(expected.Platforms) {
			return fmt.Errorf("release image %q does not cover its complete platform set", key)
		}
		for _, platform := range expected.Platforms {
			if platforms[platform] == "" {
				return fmt.Errorf("release image %q is missing platform %q", key, platform)
			}
		}
	}

	toolKeys := make(map[string]struct{}, len(receipt.Tools))
	for index, tool := range receipt.Tools {
		if tool.ToolKey == "" || tool.Version == "" || tool.SourceReference == "" || tool.ParserCompatibility == "" {
			return fmt.Errorf("release tool %d identity or compatibility is incomplete", index)
		}
		for _, value := range []string{tool.Version, tool.SourceReference, tool.ParserCompatibility} {
			switch strings.ToLower(strings.TrimSpace(value)) {
			case "locked", "declared", "unknown", "unresolved", "latest":
				return fmt.Errorf("release tool %q contains placeholder identity %q", tool.ToolKey, value)
			}
		}
		if !strings.HasPrefix(tool.ParserCompatibility, "quality_policy:") ||
			strings.TrimPrefix(tool.ParserCompatibility, "quality_policy:") == "" {
			return fmt.Errorf("release tool %q parser compatibility is not an exact quality contract", tool.ToolKey)
		}
		if _, duplicate := toolKeys[tool.ToolKey]; duplicate {
			return fmt.Errorf("release tool %q is duplicated", tool.ToolKey)
		}
		toolKeys[tool.ToolKey] = struct{}{}
		var metadata struct {
			ImageKey          string   `json:"image_key"`
			Kind              string   `json:"kind"`
			IntegrationTier   string   `json:"integration_tier"`
			Platforms         []string `json:"platforms"`
			ParserFormat      string   `json:"parser_format"`
			ResolvedReference string   `json:"resolved_reference"`
			ResolvedDigest    string   `json:"resolved_digest"`
			ResolutionStatus  string   `json:"resolution_status"`
		}
		if err := json.Unmarshal([]byte(tool.MetadataJSON), &metadata); err != nil {
			return fmt.Errorf("release tool %q metadata is invalid", tool.ToolKey)
		}
		if _, exists := runtimeImageKeys[metadata.ImageKey]; !exists {
			return fmt.Errorf("release tool %q references absent scanner runtime image %q", tool.ToolKey, metadata.ImageKey)
		}
		if metadata.Kind != "wolf" && metadata.Kind != "upstream" {
			return fmt.Errorf("release tool %q image kind is invalid", tool.ToolKey)
		}
		if metadata.IntegrationTier == "" || len(metadata.Platforms) == 0 || metadata.ParserFormat == "" {
			return fmt.Errorf("release tool %q exact metadata is incomplete", tool.ToolKey)
		}
		if metadata.Kind == "upstream" {
			if metadata.IntegrationTier != "upstream" ||
				!publicationDigestPattern.MatchString(metadata.ResolvedDigest) ||
				!strings.Contains(metadata.ResolvedReference, "@"+metadata.ResolvedDigest) ||
				(metadata.ResolutionStatus != "digest_pinned" && metadata.ResolutionStatus != "registry_resolved") ||
				tool.SourceReference != metadata.ResolvedReference || tool.SourceDigest != metadata.ResolvedDigest {
				return fmt.Errorf("release tool %q upstream identity is incomplete or inconsistent", tool.ToolKey)
			}
		} else if metadata.IntegrationTier != "default" && metadata.IntegrationTier != "bucket" {
			return fmt.Errorf("release tool %q owned image tier is invalid", tool.ToolKey)
		}
	}
	for _, key := range RequiredReleaseToolKeys {
		if _, exists := toolKeys[key]; !exists {
			return fmt.Errorf("release inventory is missing required tool %q", key)
		}
	}

	artifactTypes := make(map[string]struct{}, len(receipt.Artifacts))
	for index, artifact := range receipt.Artifacts {
		if artifact.ArtifactType == "" || artifact.MediaType == "" || artifact.URI == "" ||
			!publicationDigestPattern.MatchString(artifact.Digest) || artifact.SizeBytes < 0 {
			return fmt.Errorf("release artifact %d is incomplete or invalid", index)
		}
		if _, duplicate := artifactTypes[artifact.ArtifactType]; duplicate {
			return fmt.Errorf("release artifact %q is duplicated", artifact.ArtifactType)
		}
		artifactTypes[artifact.ArtifactType] = struct{}{}
	}
	for _, artifactType := range RequiredPublicationArtifactTypes {
		if _, exists := artifactTypes[artifactType]; !exists {
			return fmt.Errorf("release inventory is missing required artifact %q", artifactType)
		}
	}
	return nil
}
