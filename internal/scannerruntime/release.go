package scannerruntime

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/plugin/container"
	"github.com/alphabravocompany/thewolf/internal/scannerbundle"
	"github.com/alphabravocompany/thewolf/internal/scannerregistry"
	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

// ReleaseSnapshot is the immutable scanner-image assignment resolved from a
// verified release manifest. It is safe to retain for the entire lifetime of a
// queued/running scan even when a channel alias moves to another release.
type ReleaseSnapshot struct {
	ReleaseID      string
	ManifestDigest string
	DefaultImage   string
	ToolImages     map[string]ToolImage
	AllImages      map[string]string
}

// SnapshotFromInventory converts the already-verified durable release
// inventory into a per-scan runtime snapshot. imageReferences is deployment
// specific (managed registry, private mirror, or air-gap registry), but every
// value must resolve to the exact digest recorded in the immutable inventory.
//
// ReleaseTool.MetadataJSON must bind each tool to an image:
// {"image_key":"default","kind":"wolf","entrypoint":""}.
func SnapshotFromInventory(
	release scannerrelease.Release,
	tools []scannerrelease.ReleaseTool,
	images []scannerrelease.ReleaseImage,
	imageReferences map[string]string,
) (*ReleaseSnapshot, error) {
	if release.ID == "" || !validRuntimeDigest(release.ManifestDigest) ||
		!validRuntimeDigest(release.LockDigest) {
		return nil, fmt.Errorf("scanner release identity or manifest digest is invalid")
	}
	if strings.TrimSpace(release.SignerIdentity) == "" || release.PolicyRevision <= 0 {
		return nil, fmt.Errorf("scanner release trust or policy identity is incomplete")
	}
	if release.State == scannerrelease.ReleaseRevoked || release.State == scannerrelease.ReleaseDeprecated {
		return nil, fmt.Errorf("scanner release %q is %s", release.ID, release.State)
	}
	snapshot := &ReleaseSnapshot{
		ReleaseID: release.ID, ManifestDigest: release.ManifestDigest,
		ToolImages: make(map[string]ToolImage, len(tools)),
		AllImages:  make(map[string]string, len(images)),
	}
	inventoryImages := make(map[string]scannerrelease.ReleaseImage, len(images))
	for _, image := range images {
		if !scannerrelease.IsRuntimeScannerImage(image) {
			continue
		}
		if image.ImageKey == "" || !validRuntimeDigest(image.Digest) {
			return nil, fmt.Errorf("scanner release image has invalid key or digest")
		}
		if image.SignatureStatus != "verified" ||
			!validRuntimeDigest(image.ProvenanceDigest) ||
			!validRuntimeDigest(image.SBOMDigest) {
			return nil, fmt.Errorf(
				"scanner release image %q has incomplete signature, provenance, or SBOM evidence",
				image.ImageKey,
			)
		}
		var platformDigests map[string]string
		if err := json.Unmarshal([]byte(image.PlatformDigests), &platformDigests); err != nil ||
			len(platformDigests) == 0 {
			return nil, fmt.Errorf("scanner release image %q has no valid platform inventory", image.ImageKey)
		}
		for platform, digest := range platformDigests {
			if !strings.Contains(platform, "/") || !validRuntimeDigest(digest) {
				return nil, fmt.Errorf(
					"scanner release image %q has invalid platform digest for %q",
					image.ImageKey, platform,
				)
			}
		}
		if _, duplicate := inventoryImages[image.ImageKey]; duplicate {
			return nil, fmt.Errorf("scanner release has duplicate selected image key %q", image.ImageKey)
		}
		reference := imageReferences[image.ImageKey]
		parsed, err := scannerregistry.ParseReference(reference)
		if err != nil {
			return nil, fmt.Errorf("scanner release image %q reference: %w", image.ImageKey, err)
		}
		if parsed.Digest != image.Digest {
			return nil, fmt.Errorf("scanner release image %q reference digest does not match inventory", image.ImageKey)
		}
		inventoryImages[image.ImageKey] = image
		snapshot.AllImages[image.ImageKey] = reference
		if image.ImageKey == "default" {
			snapshot.DefaultImage = reference
		}
	}
	if snapshot.DefaultImage == "" {
		return nil, fmt.Errorf("scanner release %q has no selected default image", release.ID)
	}
	for _, tool := range tools {
		if strings.TrimSpace(tool.ToolKey) == "" {
			return nil, fmt.Errorf("scanner release contains a tool without a key")
		}
		if _, duplicate := snapshot.ToolImages[tool.ToolKey]; duplicate {
			return nil, fmt.Errorf("scanner release contains duplicate tool %q", tool.ToolKey)
		}
		var metadata struct {
			ImageKey   string `json:"image_key"`
			Kind       string `json:"kind"`
			Entrypoint string `json:"entrypoint"`
		}
		if err := json.Unmarshal([]byte(tool.MetadataJSON), &metadata); err != nil {
			return nil, fmt.Errorf("scanner release tool %q metadata: %w", tool.ToolKey, err)
		}
		if metadata.ImageKey == "" {
			return nil, fmt.Errorf("scanner release tool %q does not declare image_key", tool.ToolKey)
		}
		if metadata.Kind != "wolf" && metadata.Kind != "upstream" {
			return nil, fmt.Errorf("scanner release tool %q has invalid image kind %q", tool.ToolKey, metadata.Kind)
		}
		if _, exists := inventoryImages[metadata.ImageKey]; !exists {
			return nil, fmt.Errorf("scanner release tool %q references absent image %q", tool.ToolKey, metadata.ImageKey)
		}
		snapshot.ToolImages[tool.ToolKey] = ToolImage{
			Key: metadata.ImageKey, Reference: snapshot.AllImages[metadata.ImageKey],
			Kind: metadata.Kind, Entrypoint: metadata.Entrypoint,
		}
	}
	return snapshot, nil
}

func validRuntimeDigest(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, character := range value[len("sha256:"):] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

type ToolImage struct {
	Key        string
	Reference  string
	Kind       string
	Entrypoint string
}

// SnapshotFromManifest builds a runtime snapshot. Signature/trust verification
// must happen before this function; scannerbundle.Read performs it for offline
// bundles and registry resolvers must enforce their configured trust policy.
func SnapshotFromManifest(manifest scannerbundle.ReleaseManifest) (*ReleaseSnapshot, error) {
	if err := manifest.Validate(); err != nil {
		return nil, fmt.Errorf("validate scanner release manifest: %w", err)
	}
	digest, err := manifest.Digest()
	if err != nil {
		return nil, fmt.Errorf("digest scanner release manifest: %w", err)
	}
	snapshot := &ReleaseSnapshot{
		ReleaseID:      manifest.ReleaseID,
		ManifestDigest: digest,
		ToolImages:     make(map[string]ToolImage),
		AllImages:      make(map[string]string, len(manifest.Images)),
	}
	for _, image := range manifest.Images {
		if image.Kind == "fixer" {
			continue
		}
		snapshot.AllImages[image.Key] = image.Reference
		if image.Key == "default" {
			snapshot.DefaultImage = image.Reference
		}
		for _, tool := range image.Tools {
			snapshot.ToolImages[tool] = ToolImage{
				Key:        image.Key,
				Reference:  image.Reference,
				Kind:       image.Kind,
				Entrypoint: image.Entrypoint,
			}
		}
	}
	if snapshot.DefaultImage == "" {
		return nil, fmt.Errorf("scanner release %q has no default image", manifest.ReleaseID)
	}
	return snapshot, nil
}

func (s *ReleaseSnapshot) ImageFor(tool string) string {
	if s == nil {
		return ""
	}
	if image, ok := s.ToolImages[tool]; ok {
		return image.Reference
	}
	return s.DefaultImage
}

// Apply returns a copy of a container runtime config with all scanner image
// routing replaced by this release's immutable digest references. Resource,
// network, mount, and sandbox controls remain unchanged.
func (s *ReleaseSnapshot) Apply(base *container.Config) (*container.Config, error) {
	if s == nil || s.ReleaseID == "" || s.ManifestDigest == "" || s.DefaultImage == "" {
		return nil, fmt.Errorf("scanner release snapshot is incomplete")
	}
	if base == nil {
		return nil, fmt.Errorf("container config is nil")
	}
	cfg := *base
	cfg.Image = s.DefaultImage
	cfg.ImageOverrides = make(map[string]string)
	cfg.UpstreamTools = make(map[string]container.ToolImageSpec)

	tools := make([]string, 0, len(s.ToolImages))
	for tool := range s.ToolImages {
		tools = append(tools, tool)
	}
	sort.Strings(tools)
	for _, tool := range tools {
		image := s.ToolImages[tool]
		switch image.Kind {
		case "wolf":
			cfg.ImageOverrides[tool] = image.Reference
		case "upstream":
			cfg.UpstreamTools[tool] = container.ToolImageSpec{
				Image:      image.Reference,
				Entrypoint: image.Entrypoint,
			}
		default:
			return nil, fmt.Errorf("tool %q has unsupported image kind %q", tool, image.Kind)
		}
	}
	if len(cfg.ImageOverrides) == 0 {
		cfg.ImageOverrides = nil
	}
	if len(cfg.UpstreamTools) == 0 {
		cfg.UpstreamTools = nil
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("apply scanner release to runtime: %w", err)
	}
	return &cfg, nil
}
