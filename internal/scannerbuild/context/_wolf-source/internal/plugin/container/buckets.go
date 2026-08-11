package container

import (
	"strings"

	"github.com/alphabravocompany/thewolf/internal/scannertools/manifest"
)

// localOnlyImageSuffixes are repo-name suffixes for scanner buckets that must
// NEVER be pulled from (or pushed to) a registry. CodeQL is the only one: its
// license forbids redistribution and only permits local builds for analyzing
// open-source code, so wolf builds it locally on demand rather than shipping a
// published image. See scanners/LICENSES.md.
var localOnlyImageSuffixes = []string{"-codeql"}

// IsLocalOnlyImage reports whether an image reference belongs to a
// local-build-only bucket. Such images are never auto-pulled, offered for
// registry pull/push, or probed for a remote digest — the operator builds them
// locally if their license permits.
func IsLocalOnlyImage(ref string) bool {
	repo := ref
	// Strip a trailing :tag (but not a registry host:port, which is followed
	// by a path segment).
	if i := strings.LastIndex(ref, ":"); i >= 0 && !strings.Contains(ref[i+1:], "/") {
		repo = ref[:i]
	}
	for _, suf := range localOnlyImageSuffixes {
		if strings.HasSuffix(repo, suf) {
			return true
		}
	}
	return false
}

// DefaultBucketImages returns the canonical per-tool image map for the
// 4-image split architecture (docs/PLAN-containerized-scanner-execution.md §5.1). The keys are tool names as
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
	definition, err := manifest.LoadDefault()
	if err != nil {
		return nil
	}
	return BucketImagesFromManifest(definition, bucketBase, version)
}

// BucketImagesFromManifest is the deterministic runtime router. Keeping this
// derived from the scanner definition removes a second mutable version map
// that proposal automation could otherwise forget to update.
func BucketImagesFromManifest(
	definition *manifest.Manifest,
	bucketBase, version string,
) map[string]string {
	if definition == nil || bucketBase == "" || version == "" {
		return nil
	}
	out := make(map[string]string)
	for name, tool := range definition.Tools {
		if tool.IntegrationTier != manifest.TierBucket || tool.Bucket == "" {
			continue
		}
		out[name] = bucketBase + "-" + tool.Bucket + ":" + version
	}
	return out
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
	definition, err := manifest.LoadDefault()
	if err != nil {
		return nil
	}
	return UpstreamToolsFromManifest(definition)
}

// UpstreamToolsFromManifest derives routing from the same definition used for
// discovery, locking, documentation, and release builds.
func UpstreamToolsFromManifest(definition *manifest.Manifest) map[string]ToolImageSpec {
	if definition == nil {
		return nil
	}
	out := make(map[string]ToolImageSpec)
	for name, tool := range definition.Tools {
		if tool.IntegrationTier != manifest.TierUpstream {
			continue
		}
		out[name] = ToolImageSpec{
			Image: tool.Image.PinnedReference, Entrypoint: tool.Image.Entrypoint,
		}
	}
	return out
}
