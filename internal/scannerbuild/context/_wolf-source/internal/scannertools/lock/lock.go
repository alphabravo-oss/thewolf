// Package scannerlock generates and validates the deterministic scanner release
// definition lock. The lock is deliberately independent of CI and runtime
// database state: identical definition inputs produce byte-identical output.
package scannerlock

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/alphabravocompany/thewolf/internal/scannertools/httpcache"
	"github.com/alphabravocompany/thewolf/internal/scannertools/manifest"
	"github.com/alphabravocompany/thewolf/internal/scannertools/registryauth"
	"gopkg.in/yaml.v3"
)

const (
	SchemaVersion      = "wolf.scanners/v1"
	BuildPolicyVersion = "wolf.scanners/build-policy/v1"
	DefaultLockPath    = "scanners/scanner-lock.yaml"
)

var digestRE = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

// Lock is the canonical, complete definition consumed by a release factory.
// Operational data such as candidate IDs, Git commits, generation timestamps,
// and approvals intentionally lives outside this reproducible artifact.
type Lock struct {
	SchemaVersion  string                   `json:"schemaVersion" yaml:"schemaVersion"`
	LockDigest     string                   `json:"lockDigest" yaml:"lockDigest"`
	Definition     Definition               `json:"definition" yaml:"definition"`
	ReleaseInputs  ReleaseInputs            `json:"releaseInputs" yaml:"releaseInputs"`
	BaseImages     map[string]BaseImage     `json:"baseImages" yaml:"baseImages"`
	Toolchains     map[string]Toolchain     `json:"toolchains" yaml:"toolchains"`
	Tools          map[string]Tool          `json:"tools" yaml:"tools"`
	UpstreamImages map[string]UpstreamImage `json:"upstreamImages,omitempty" yaml:"upstreamImages,omitempty"`
}

type Definition struct {
	Digest string            `json:"digest" yaml:"digest"`
	Inputs map[string]string `json:"inputs" yaml:"inputs"`
}

type ReleaseInputs struct {
	BuildPolicyDigest string                  `json:"buildPolicyDigest" yaml:"buildPolicyDigest"`
	Variants          map[string]BuildVariant `json:"variants" yaml:"variants"`
	FixerVariants     map[string]BuildVariant `json:"fixerVariants" yaml:"fixerVariants"`
}

type BuildVariant struct {
	Dockerfile   string            `json:"dockerfile" yaml:"dockerfile"`
	Context      string            `json:"context,omitempty" yaml:"context,omitempty"`
	Image        string            `json:"image,omitempty" yaml:"image,omitempty"`
	Platforms    []string          `json:"platforms" yaml:"platforms"`
	DependsOn    []string          `json:"dependsOn,omitempty" yaml:"dependsOn,omitempty"`
	BuildArgs    map[string]string `json:"buildArgs,omitempty" yaml:"buildArgs,omitempty"`
	AuthMode     string            `json:"authMode,omitempty" yaml:"authMode,omitempty"`
	SmokeCommand []string          `json:"smokeCommand,omitempty" yaml:"smokeCommand,omitempty"`
}

type BaseImage struct {
	Reference string `json:"reference" yaml:"reference"`
	Digest    string `json:"digest" yaml:"digest"`
}

type Toolchain struct {
	Values map[string]string `json:"values" yaml:"values"`
}

type Tool struct {
	Category        string          `json:"category" yaml:"category"`
	IntegrationTier string          `json:"integrationTier" yaml:"integrationTier"`
	Bucket          string          `json:"bucket,omitempty" yaml:"bucket,omitempty"`
	PinnedVersion   string          `json:"pinnedVersion,omitempty" yaml:"pinnedVersion,omitempty"`
	VersionVariable string          `json:"versionVariable,omitempty" yaml:"versionVariable,omitempty"`
	Platforms       []string        `json:"platforms" yaml:"platforms"`
	UpdateSource    UpdateSource    `json:"updateSource" yaml:"updateSource"`
	SourceIntegrity SourceIntegrity `json:"sourceIntegrity" yaml:"sourceIntegrity"`
	ParserContract  ParserContract  `json:"parserContract" yaml:"parserContract"`
	License         LicensePolicy   `json:"license" yaml:"license"`
	Risk            RiskPolicy      `json:"risk" yaml:"risk"`
	ManualUpdate    *ManualUpdate   `json:"manualUpdateException,omitempty" yaml:"manualUpdateException,omitempty"`
}

type UpdateSource struct {
	Type       string `json:"type" yaml:"type"`
	Repository string `json:"repository,omitempty" yaml:"repository,omitempty"`
	Package    string `json:"package,omitempty" yaml:"package,omitempty"`
	Module     string `json:"module,omitempty" yaml:"module,omitempty"`
	Owner      string `json:"owner,omitempty" yaml:"owner,omitempty"`
	Repo       string `json:"repo,omitempty" yaml:"repo,omitempty"`
	Channel    string `json:"channel,omitempty" yaml:"channel,omitempty"`
	TagPattern string `json:"tagPattern,omitempty" yaml:"tagPattern,omitempty"`
}

type SourceIntegrity struct {
	Status            string `json:"status" yaml:"status"`
	URL               string `json:"url,omitempty" yaml:"url,omitempty"`
	SHA256            string `json:"sha256,omitempty" yaml:"sha256,omitempty"`
	SHA256Variable    string `json:"sha256Variable,omitempty" yaml:"sha256Variable,omitempty"`
	SignatureURL      string `json:"signatureURL,omitempty" yaml:"signatureURL,omitempty"`
	SignatureIdentity string `json:"signatureIdentity,omitempty" yaml:"signatureIdentity,omitempty"`
}

type ParserContract struct {
	Status   string   `json:"status" yaml:"status"`
	Format   string   `json:"format,omitempty" yaml:"format,omitempty"`
	Fixtures []string `json:"fixtures,omitempty" yaml:"fixtures,omitempty"`
}

type LicensePolicy struct {
	Status     string   `json:"status" yaml:"status"`
	Expression string   `json:"expression,omitempty" yaml:"expression,omitempty"`
	Files      []string `json:"files,omitempty" yaml:"files,omitempty"`
}

type RiskPolicy struct {
	Classification   string `json:"classification" yaml:"classification"`
	AutoCandidate    bool   `json:"autoCandidate" yaml:"autoCandidate"`
	ApprovalRequired bool   `json:"approvalRequired" yaml:"approvalRequired"`
}

type ManualUpdate struct {
	Owner       string `json:"owner" yaml:"owner"`
	Reason      string `json:"reason" yaml:"reason"`
	ReviewAfter string `json:"reviewAfter" yaml:"reviewAfter"`
}

type UpstreamImage struct {
	DeclaredReference string   `json:"declaredReference" yaml:"declaredReference"`
	ResolvedReference string   `json:"resolvedReference,omitempty" yaml:"resolvedReference,omitempty"`
	Digest            string   `json:"digest,omitempty" yaml:"digest,omitempty"`
	Platforms         []string `json:"platforms" yaml:"platforms"`
	ResolutionStatus  string   `json:"resolutionStatus" yaml:"resolutionStatus"`
	MutableSource     bool     `json:"mutableSource" yaml:"mutableSource"`
}

type buildPolicy struct {
	SchemaVersion string                  `yaml:"schemaVersion"`
	Variants      map[string]BuildVariant `yaml:"variants"`
	FixerVariants map[string]BuildVariant `yaml:"fixerVariants"`
}

type toolchainFile struct {
	BaseImages map[string]string            `yaml:"base_images"`
	Toolchains map[string]map[string]string `yaml:"toolchains"`
}

type qualityParserPolicy struct {
	SchemaVersion string `yaml:"schemaVersion"`
	Tools         map[string]struct {
		ParserOwned  bool   `yaml:"parserOwned"`
		ParserFormat string `yaml:"parserFormat"`
	} `yaml:"tools"`
}

// GenerateOptions controls optional online image resolution. Existing locks are
// used as a deterministic cache: an unchanged declared reference retains its
// previously verified digest unless RefreshImages is requested.
type GenerateOptions struct {
	ExistingLock     *Lock
	RefreshImages    bool
	AllowTagMutation bool
	ImageResolver    *ImageResolver
}

// TagMutationError reports a mutable source tag resolving to a different digest.
// This is a hard failure unless an explicit update operation accepts it.
type TagMutationError struct {
	Tool      string
	Reference string
	Previous  string
	Current   string
}

func (e *TagMutationError) Error() string {
	return fmt.Sprintf("upstream image tag mutation for %s (%s): %s -> %s", e.Tool, e.Reference, e.Previous, e.Current)
}

// Generate constructs a lock from the repository definition files.
func Generate(ctx context.Context, root string, opts GenerateOptions) (*Lock, error) {
	if root == "" {
		var err error
		root, err = manifest.FindRepoRoot("")
		if err != nil {
			return nil, err
		}
	}
	scannerDir := filepath.Join(root, "scanners")
	m, err := manifest.LoadFile(filepath.Join(scannerDir, "tools.yaml"))
	if err != nil {
		return nil, err
	}
	versionsData, err := os.ReadFile(filepath.Join(scannerDir, "versions.env"))
	if err != nil {
		return nil, err
	}
	versions, err := manifest.ParseVersionsEnv(versionsData)
	if err != nil {
		return nil, err
	}
	tc, err := loadToolchains(filepath.Join(scannerDir, "toolchains.yaml"))
	if err != nil {
		return nil, err
	}
	policyData, err := os.ReadFile(filepath.Join(scannerDir, "build-policy.yaml"))
	if err != nil {
		return nil, err
	}
	policy, err := loadBuildPolicy(policyData)
	if err != nil {
		return nil, err
	}
	if err := validateFixerBuildArgPins(root, policy); err != nil {
		return nil, err
	}
	parserPolicy, err := loadQualityParserPolicy(
		filepath.Join(scannerDir, "quality", "policy.yaml"), m,
	)
	if err != nil {
		return nil, err
	}
	inputs, err := definitionInputs(root, policy)
	if err != nil {
		return nil, err
	}
	definitionDigest, err := digestValue(inputs)
	if err != nil {
		return nil, err
	}
	policyDigest := sha256Digest(policyData)

	out := &Lock{
		SchemaVersion: SchemaVersion,
		Definition: Definition{
			Digest: definitionDigest,
			Inputs: inputs,
		},
		ReleaseInputs: ReleaseInputs{
			BuildPolicyDigest: policyDigest,
			Variants:          policy.Variants,
			FixerVariants:     policy.FixerVariants,
		},
		BaseImages:     make(map[string]BaseImage, len(tc.BaseImages)),
		Toolchains:     make(map[string]Toolchain, len(tc.Toolchains)),
		Tools:          make(map[string]Tool, len(m.Tools)),
		UpstreamImages: make(map[string]UpstreamImage),
	}
	for variant, ref := range tc.BaseImages {
		digest, ok := referenceDigest(ref)
		if !ok {
			return nil, fmt.Errorf("base image %s must be pinned by sha256 digest: %s", variant, ref)
		}
		out.BaseImages[variant] = BaseImage{Reference: ref, Digest: digest}
	}
	for name, values := range tc.Toolchains {
		copied := make(map[string]string, len(values))
		for key, value := range values {
			copied[key] = value
		}
		out.Toolchains[name] = Toolchain{Values: copied}
	}

	resolver := opts.ImageResolver
	if resolver == nil {
		resolver = &ImageResolver{}
	}
	for _, name := range m.Names() {
		def := m.Tools[name]
		platforms, err := toolPlatforms(def, policy)
		if err != nil {
			return nil, fmt.Errorf("tool %s: %w", name, err)
		}
		lockedTool := lockTool(def, platforms)
		parser := parserPolicy.Tools[name]
		lockedTool.ParserContract = ParserContract{
			Status: "quality_policy", Format: parser.ParserFormat,
			Fixtures: []string{
				"scanners/quality/corpus.yaml",
				"scanners/quality/goldens/family-findings.json",
			},
		}
		if def.VersionVariable != "" {
			if got := versions[def.VersionVariable]; got != def.PinnedVersion {
				return nil, fmt.Errorf("tool %s version parity failed: %s=%q, pinned=%q", name, def.VersionVariable, got, def.PinnedVersion)
			}
		}
		out.Tools[name] = lockedTool
		if def.IntegrationTier != manifest.TierUpstream {
			continue
		}
		image := UpstreamImage{
			DeclaredReference: def.Image.PinnedReference,
			Platforms:         platforms,
			ResolutionStatus:  "unresolved",
			MutableSource:     true,
		}
		if digest, ok := referenceDigest(def.Image.PinnedReference); ok {
			image.Digest = digest
			image.ResolvedReference = def.Image.PinnedReference
			image.ResolutionStatus = "digest_pinned"
			image.MutableSource = false
		} else if previous, ok := existingImage(opts.ExistingLock, name, def.Image.PinnedReference); ok && !opts.RefreshImages {
			image.Digest = previous.Digest
			image.ResolvedReference = previous.ResolvedReference
			image.ResolutionStatus = previous.ResolutionStatus
		}
		if opts.RefreshImages {
			resolved, err := resolver.Resolve(ctx, def.Image.PinnedReference)
			if err != nil {
				return nil, fmt.Errorf("resolve upstream image %s: %w", name, err)
			}
			if previous, ok := existingImage(opts.ExistingLock, name, def.Image.PinnedReference); ok &&
				previous.Digest != "" && previous.Digest != resolved.Digest && !opts.AllowTagMutation {
				return nil, &TagMutationError{
					Tool: name, Reference: def.Image.PinnedReference,
					Previous: previous.Digest, Current: resolved.Digest,
				}
			}
			image.Digest = resolved.Digest
			image.ResolvedReference = resolvedReference(def.Image.PinnedReference, resolved.Digest)
			image.ResolutionStatus = "registry_resolved"
		}
		out.UpstreamImages[name] = image
	}
	if err := out.SetDigest(); err != nil {
		return nil, err
	}
	if err := out.Validate(); err != nil {
		return nil, err
	}
	return out, nil
}

func lockTool(def manifest.Tool, platforms []string) Tool {
	integrityStatus := "unverified"
	if def.SourceIntegrity.SHA256 != "" {
		integrityStatus = "checksum_verified"
	} else if def.UpdateSource.Type == "rust_channel" && def.SourceIntegrity.URL != "" {
		// rustup authenticates each selected toolchain component against the
		// SHA-256 values in the versioned channel manifest. The bootstrap binary
		// itself is independently platform-checksummed in toolchains.yaml.
		integrityStatus = "rustup_component_manifest"
	} else if def.IntegrationTier == manifest.TierUpstream {
		integrityStatus = "oci_digest_required"
	} else if def.Install.Manager != "" {
		integrityStatus = "package_manager_pin"
	}
	parserStatus := "undeclared"
	if len(def.ParserContract.Fixtures) > 0 {
		parserStatus = "fixture_contract"
	}
	licenseStatus := "undeclared"
	if def.License.Expression != "" {
		licenseStatus = "declared"
	}
	classification := def.Risk.Classification
	if classification == "" {
		classification = "high"
	}
	approvalRequired := def.Risk.ApprovalRequired
	if def.Risk.Classification == "" || classification == "high" || classification == "critical" {
		approvalRequired = true
	}
	var manual *ManualUpdate
	if def.ManualUpdate != (manifest.ManualUpdate{}) {
		manual = &ManualUpdate{
			Owner: def.ManualUpdate.Owner, Reason: def.ManualUpdate.Reason,
			ReviewAfter: def.ManualUpdate.ReviewAfter,
		}
	}
	return Tool{
		Category:        def.Category,
		IntegrationTier: def.IntegrationTier,
		Bucket:          def.Bucket,
		PinnedVersion:   def.PinnedVersion,
		VersionVariable: def.VersionVariable,
		Platforms:       platforms,
		UpdateSource: UpdateSource{
			Type: def.UpdateSource.Type, Repository: def.UpdateSource.Repository,
			Package: def.UpdateSource.Package, Module: def.UpdateSource.Module,
			Owner: def.UpdateSource.Owner, Repo: def.UpdateSource.Repo,
			Channel: def.UpdateSource.Channel, TagPattern: def.UpdateSource.TagPattern,
		},
		SourceIntegrity: SourceIntegrity{
			Status: integrityStatus, URL: def.SourceIntegrity.URL,
			SHA256: def.SourceIntegrity.SHA256, SHA256Variable: def.SourceIntegrity.SHA256Variable,
			SignatureURL:      def.SourceIntegrity.SignatureURL,
			SignatureIdentity: def.SourceIntegrity.SignatureIdentity,
		},
		ParserContract: ParserContract{
			Status: parserStatus, Format: def.ParserContract.Format,
			Fixtures: sortedCopy(def.ParserContract.Fixtures),
		},
		License: LicensePolicy{
			Status: licenseStatus, Expression: def.License.Expression,
			Files: sortedCopy(def.License.Files),
		},
		Risk: RiskPolicy{
			Classification: classification, AutoCandidate: def.Risk.AutoCandidate,
			ApprovalRequired: approvalRequired,
		},
		ManualUpdate: manual,
	}
}

func toolPlatforms(tool manifest.Tool, policy buildPolicy) ([]string, error) {
	if len(tool.Platforms) > 0 {
		return sortedUnique(tool.Platforms), nil
	}
	if len(tool.Image.Platforms) > 0 {
		return sortedUnique(tool.Image.Platforms), nil
	}
	var variant string
	switch tool.IntegrationTier {
	case manifest.TierDefault, manifest.TierUpstream:
		variant = "default"
	case manifest.TierBucket:
		variant = tool.Bucket
	default:
		return nil, fmt.Errorf("unsupported integration tier %q", tool.IntegrationTier)
	}
	def, ok := policy.Variants[variant]
	if !ok {
		return nil, fmt.Errorf("build policy has no variant %q", variant)
	}
	return sortedUnique(def.Platforms), nil
}

func loadToolchains(path string) (toolchainFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return toolchainFile{}, err
	}
	var out toolchainFile
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&out); err != nil {
		return toolchainFile{}, fmt.Errorf("decode %s: %w", path, err)
	}
	if len(out.BaseImages) == 0 || len(out.Toolchains) == 0 {
		return toolchainFile{}, fmt.Errorf("%s must declare base_images and toolchains", path)
	}
	return out, nil
}

func loadBuildPolicy(data []byte) (buildPolicy, error) {
	var out buildPolicy
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&out); err != nil {
		return buildPolicy{}, fmt.Errorf("decode scanner build policy: %w", err)
	}
	if out.SchemaVersion != BuildPolicyVersion {
		return buildPolicy{}, fmt.Errorf("scanner build policy schemaVersion=%q, want %q", out.SchemaVersion, BuildPolicyVersion)
	}
	if len(out.Variants) == 0 {
		return buildPolicy{}, fmt.Errorf("scanner build policy has no variants")
	}
	if len(out.FixerVariants) == 0 {
		return buildPolicy{}, fmt.Errorf("scanner build policy has no fixer variants")
	}
	if err := normalizeBuildVariants("scanner", out.Variants, false); err != nil {
		return buildPolicy{}, err
	}
	if err := normalizeBuildVariants("fixer", out.FixerVariants, true); err != nil {
		return buildPolicy{}, err
	}
	for name, variant := range out.FixerVariants {
		for _, dependency := range variant.DependsOn {
			if dependency == name {
				return buildPolicy{}, fmt.Errorf("fixer build variant %s depends on itself", name)
			}
			if _, ok := out.FixerVariants[dependency]; !ok {
				return buildPolicy{}, fmt.Errorf(
					"fixer build variant %s has unknown dependency %s", name, dependency,
				)
			}
		}
	}
	if buildVariantCycle(out.FixerVariants) {
		return buildPolicy{}, fmt.Errorf("fixer build variants contain a dependency cycle")
	}
	return out, nil
}

func loadQualityParserPolicy(path string, definition *manifest.Manifest) (qualityParserPolicy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return qualityParserPolicy{}, fmt.Errorf("read scanner quality parser policy: %w", err)
	}
	var policy qualityParserPolicy
	if err := yaml.Unmarshal(data, &policy); err != nil {
		return qualityParserPolicy{}, fmt.Errorf("decode scanner quality parser policy: %w", err)
	}
	if policy.SchemaVersion != "wolf.scanners/quality-policy/v1" || len(policy.Tools) == 0 {
		return qualityParserPolicy{}, errors.New("scanner quality parser policy is incomplete")
	}
	if definition == nil || len(policy.Tools) != len(definition.Tools) {
		return qualityParserPolicy{}, errors.New("scanner quality parser policy does not exactly cover the tool manifest")
	}
	for name := range definition.Tools {
		contract, ok := policy.Tools[name]
		if !ok || !contract.ParserOwned || strings.TrimSpace(contract.ParserFormat) == "" {
			return qualityParserPolicy{}, fmt.Errorf(
				"scanner quality parser policy for %s is absent or not owned", name,
			)
		}
	}
	for name := range policy.Tools {
		if _, ok := definition.Tools[name]; !ok {
			return qualityParserPolicy{}, fmt.Errorf("scanner quality parser policy has unknown tool %s", name)
		}
	}
	return policy, nil
}

func normalizeBuildVariants(
	kind string,
	variants map[string]BuildVariant,
	requireMetadata bool,
) error {
	for name, variant := range variants {
		if name == "" || variant.Dockerfile == "" ||
			filepath.IsAbs(variant.Dockerfile) ||
			strings.Contains(filepath.Clean(variant.Dockerfile), "..") {
			return fmt.Errorf("%s build variant %s has invalid dockerfile", kind, name)
		}
		if variant.Context != "" &&
			(filepath.IsAbs(variant.Context) ||
				strings.Contains(filepath.Clean(variant.Context), "..")) {
			return fmt.Errorf("%s build variant %s has invalid context", kind, name)
		}
		if len(variant.Platforms) == 0 {
			return fmt.Errorf("%s build variant %s has no platforms", kind, name)
		}
		variant.Platforms = sortedUnique(variant.Platforms)
		variant.DependsOn = sortedUnique(variant.DependsOn)
		if requireMetadata {
			if !regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`).MatchString(variant.Image) {
				return fmt.Errorf("fixer build variant %s has invalid image", name)
			}
			switch variant.AuthMode {
			case "none", "api-key", "interactive-session", "injected":
			default:
				return fmt.Errorf("fixer build variant %s has invalid auth mode", name)
			}
			if len(variant.SmokeCommand) == 0 {
				return fmt.Errorf("fixer build variant %s has no smoke command", name)
			}
			for key, value := range variant.BuildArgs {
				if !regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`).MatchString(key) ||
					strings.TrimSpace(value) == "" {
					return fmt.Errorf("fixer build variant %s has invalid build argument", name)
				}
			}
		}
		variants[name] = variant
	}
	return nil
}

func validateFixerBuildArgPins(root string, policy buildPolicy) error {
	data, err := os.ReadFile(filepath.Join(root, "fixer", "versions.env"))
	if err != nil {
		return fmt.Errorf("read fixer version pins: %w", err)
	}
	pins, err := manifest.ParseVersionsEnv(data)
	if err != nil {
		return fmt.Errorf("parse fixer version pins: %w", err)
	}
	for variantName, variant := range policy.FixerVariants {
		for name, value := range variant.BuildArgs {
			pinned, ok := pins[name]
			if !ok {
				return fmt.Errorf(
					"fixer build variant %s argument %s has no canonical fixer/versions.env pin",
					variantName, name,
				)
			}
			if value != pinned {
				return fmt.Errorf(
					"fixer build variant %s argument %s=%s does not match fixer/versions.env value %s",
					variantName, name, value, pinned,
				)
			}
		}
	}
	return nil
}

func buildVariantCycle(variants map[string]BuildVariant) bool {
	visiting := make(map[string]bool, len(variants))
	visited := make(map[string]bool, len(variants))
	var visit func(string) bool
	visit = func(name string) bool {
		if visiting[name] {
			return true
		}
		if visited[name] {
			return false
		}
		visiting[name] = true
		for _, dependency := range variants[name].DependsOn {
			if visit(dependency) {
				return true
			}
		}
		visiting[name] = false
		visited[name] = true
		return false
	}
	for name := range variants {
		if visit(name) {
			return true
		}
	}
	return false
}

func definitionInputs(root string, policy buildPolicy) (map[string]string, error) {
	paths := []string{
		".dockerignore",
		"scanners/.dockerignore",
		"scanners/build-policy.yaml",
		"scanners/os-packages.lock.yaml",
		"scanners/os-packages.yaml",
		"scanners/smoke-test.sh",
		"scanners/toolchains.yaml",
		"scanners/tools.yaml",
		"scanners/trufflehog-excludes.txt",
		"scanners/versions.env",
		"scanners/wolf-tool-entry",
	}
	for _, variant := range policy.Variants {
		paths = append(paths, filepath.ToSlash(variant.Dockerfile))
	}
	for _, variant := range policy.FixerVariants {
		paths = append(paths, filepath.ToSlash(variant.Dockerfile))
	}
	paths = append(paths, "fixer/versions.env", "go.mod", "go.sum")
	for _, tree := range []string{"cmd", "fixer", "internal", "plugins", "scanners"} {
		err := filepath.WalkDir(filepath.Join(root, tree), func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() && path == filepath.Join(root, "internal", "scannerbuild", "context") {
				// This generated mirror is checked byte-for-byte by
				// scanners-context-check. Hash canonical inputs once instead of
				// duplicating hundreds of generated copies in the release lock.
				return filepath.SkipDir
			}
			if entry.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if rel == DefaultLockPath {
				return nil
			}
			paths = append(paths, rel)
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("walk scanner definition tree %s: %w", tree, err)
		}
	}
	dockerfiles, err := filepath.Glob(filepath.Join(root, "scanners", "Dockerfile*"))
	if err != nil {
		return nil, err
	}
	for _, dockerfile := range dockerfiles {
		rel, err := filepath.Rel(root, dockerfile)
		if err != nil {
			return nil, err
		}
		paths = append(paths, filepath.ToSlash(rel))
	}
	installRoot := filepath.Join(root, "scanners", "install")
	err = filepath.WalkDir(installRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		paths = append(paths, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	osPackageRoot := filepath.Join(root, "scanners", "os-packages")
	err = filepath.WalkDir(osPackageRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		paths = append(paths, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	paths = sortedUnique(paths)
	out := make(map[string]string, len(paths))
	for _, rel := range paths {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			return nil, fmt.Errorf("read scanner definition input %s: %w", rel, err)
		}
		out[rel] = sha256Digest(data)
	}
	return out, nil
}

// SetDigest calculates the self-authenticating digest over canonical JSON with
// lockDigest omitted.
func (l *Lock) SetDigest() error {
	digest, err := l.CanonicalDigest()
	if err != nil {
		return err
	}
	l.LockDigest = digest
	return nil
}

// CanonicalDigest is stable across YAML formatting and Go map insertion order.
func (l Lock) CanonicalDigest() (string, error) {
	l.LockDigest = ""
	return digestValue(l)
}

func digestValue(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return sha256Digest(data), nil
}

func sha256Digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// MarshalYAML returns stable generated YAML. yaml.v3 sorts string map keys; the
// golden test guards this property as part of the artifact contract.
func (l Lock) MarshalYAML() ([]byte, error) {
	if err := l.Validate(); err != nil {
		return nil, err
	}
	data, err := yaml.Marshal(l)
	if err != nil {
		return nil, err
	}
	header := "# Code generated by `go run ./cmd/scannertools lock`; DO NOT EDIT.\n"
	return append([]byte(header), data...), nil
}

// JSONBytes renders the validated lock for machine-to-machine consumers.
func (l Lock) JSONBytes() ([]byte, error) {
	if err := l.Validate(); err != nil {
		return nil, err
	}
	return json.MarshalIndent(l, "", "  ")
}

// Parse reads and structurally validates a lock.
func Parse(data []byte) (*Lock, error) {
	var out Lock
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&out); err != nil {
		return nil, fmt.Errorf("decode scanner lock: %w", err)
	}
	if err := out.Validate(); err != nil {
		return nil, err
	}
	return &out, nil
}

// ParseGenerationCache decodes a previous lock only for reusing immutable
// upstream-image resolutions while regenerating after an additive lock-schema
// change. It deliberately does not accept the previous lock as valid release
// evidence. Generate rebinds a cached entry only when the current declared
// reference is byte-identical.
func ParseGenerationCache(data []byte) (*Lock, error) {
	var out Lock
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&out); err != nil {
		return nil, fmt.Errorf("decode scanner lock generation cache: %w", err)
	}
	if out.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf(
			"scanner lock generation cache schemaVersion=%q, want %q",
			out.SchemaVersion, SchemaVersion,
		)
	}
	for name, image := range out.UpstreamImages {
		if image.DeclaredReference == "" {
			return nil, fmt.Errorf("scanner lock generation cache image %s has no declared reference", name)
		}
		if image.Digest != "" && !digestRE.MatchString(image.Digest) {
			return nil, fmt.Errorf("scanner lock generation cache image %s has invalid digest", name)
		}
		if image.ResolvedReference != "" && image.Digest != "" &&
			!strings.Contains(image.ResolvedReference, "@"+image.Digest) {
			return nil, fmt.Errorf("scanner lock generation cache image %s has mismatched reference", name)
		}
	}
	return &out, nil
}

func LoadFile(path string) (*Lock, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Validate checks schema, canonical digests, definition coverage, and internal
// references. It does not claim mutable upstream tags have been resolved.
func (l Lock) Validate() error {
	var errs []string
	if l.SchemaVersion != SchemaVersion {
		errs = append(errs, fmt.Sprintf("schemaVersion=%q, want %q", l.SchemaVersion, SchemaVersion))
	}
	if !digestRE.MatchString(l.LockDigest) {
		errs = append(errs, "lockDigest must be a sha256 digest")
	} else if got, err := l.CanonicalDigest(); err != nil {
		errs = append(errs, "calculate lock digest: "+err.Error())
	} else if got != l.LockDigest {
		errs = append(errs, fmt.Sprintf("lockDigest=%s, calculated %s", l.LockDigest, got))
	}
	if len(l.Definition.Inputs) == 0 {
		errs = append(errs, "definition.inputs is empty")
	} else {
		for path, digest := range l.Definition.Inputs {
			if path == "" || filepath.IsAbs(path) || strings.Contains(filepath.Clean(path), "..") {
				errs = append(errs, "definition input has invalid path "+path)
			}
			if !digestRE.MatchString(digest) {
				errs = append(errs, "definition input "+path+" has invalid digest")
			}
		}
		if got, err := digestValue(l.Definition.Inputs); err != nil {
			errs = append(errs, "calculate definition digest: "+err.Error())
		} else if got != l.Definition.Digest {
			errs = append(errs, fmt.Sprintf("definition.digest=%s, calculated %s", l.Definition.Digest, got))
		}
	}
	if !digestRE.MatchString(l.ReleaseInputs.BuildPolicyDigest) {
		errs = append(errs, "releaseInputs.buildPolicyDigest must be a sha256 digest")
	} else if inputDigest := l.Definition.Inputs["scanners/build-policy.yaml"]; inputDigest != l.ReleaseInputs.BuildPolicyDigest {
		errs = append(errs, "releaseInputs.buildPolicyDigest does not match definition input scanners/build-policy.yaml")
	}
	if len(l.ReleaseInputs.Variants) == 0 {
		errs = append(errs, "releaseInputs.variants is empty")
	}
	for name, variant := range l.ReleaseInputs.Variants {
		if variant.Dockerfile == "" || len(variant.Platforms) == 0 {
			errs = append(errs, "release variant "+name+" must declare dockerfile and platforms")
		}
		if !isSortedUnique(variant.Platforms) {
			errs = append(errs, "release variant "+name+" platforms must be sorted and unique")
		}
	}
	for name, base := range l.BaseImages {
		if !digestRE.MatchString(base.Digest) || !strings.Contains(base.Reference, "@"+base.Digest) {
			errs = append(errs, "base image "+name+" must use its declared sha256 digest")
		}
		if _, ok := l.ReleaseInputs.Variants[name]; !ok {
			errs = append(errs, "base image "+name+" has no release variant")
		}
	}
	for name := range l.ReleaseInputs.Variants {
		if _, ok := l.BaseImages[name]; !ok {
			errs = append(errs, "release variant "+name+" has no base image")
		}
	}
	if len(l.ReleaseInputs.FixerVariants) == 0 {
		errs = append(errs, "releaseInputs.fixerVariants is empty")
	}
	for name, variant := range l.ReleaseInputs.FixerVariants {
		if variant.Dockerfile == "" || variant.Image == "" ||
			len(variant.Platforms) == 0 || len(variant.SmokeCommand) == 0 {
			errs = append(errs, "fixer release variant "+name+" has incomplete build metadata")
		}
		if !isSortedUnique(variant.Platforms) ||
			(len(variant.DependsOn) > 0 && !isSortedUnique(variant.DependsOn)) {
			errs = append(errs, "fixer release variant "+name+" platforms and dependencies must be sorted and unique")
		}
		for _, dependency := range variant.DependsOn {
			if _, ok := l.ReleaseInputs.FixerVariants[dependency]; !ok {
				errs = append(errs, "fixer release variant "+name+" has unknown dependency "+dependency)
			}
		}
	}
	if buildVariantCycle(l.ReleaseInputs.FixerVariants) {
		errs = append(errs, "fixer release variants contain a dependency cycle")
	}
	if len(l.Tools) == 0 {
		errs = append(errs, "tools is empty")
	}
	for name, tool := range l.Tools {
		if tool.UpdateSource.Type == "" {
			errs = append(errs, "tool "+name+" has no update source")
		}
		if !validLockUpdateSource(tool.UpdateSource.Type) && tool.ManualUpdate == nil {
			errs = append(errs, "tool "+name+" has an unsupported update source without a manual exception")
		}
		if len(tool.Platforms) == 0 || !isSortedUnique(tool.Platforms) {
			errs = append(errs, "tool "+name+" platforms must be non-empty, sorted, and unique")
		}
		if tool.ParserContract.Status != "quality_policy" ||
			strings.TrimSpace(tool.ParserContract.Format) == "" ||
			len(tool.ParserContract.Fixtures) != 2 ||
			!isSortedUnique(tool.ParserContract.Fixtures) {
			errs = append(errs, "tool "+name+" has no exact quality parser contract")
		}
		switch tool.Risk.Classification {
		case "low", "medium", "high", "critical":
		default:
			errs = append(errs, "tool "+name+" has invalid risk classification")
		}
		if tool.SourceIntegrity.SHA256 != "" && !digestRE.MatchString("sha256:"+strings.TrimPrefix(tool.SourceIntegrity.SHA256, "sha256:")) {
			errs = append(errs, "tool "+name+" has invalid source integrity digest")
		}
		if tool.ManualUpdate != nil {
			if tool.ManualUpdate.Owner == "" || tool.ManualUpdate.Reason == "" {
				errs = append(errs, "tool "+name+" has an incomplete manual update exception")
			}
			if _, err := time.Parse("2006-01-02", tool.ManualUpdate.ReviewAfter); err != nil {
				errs = append(errs, "tool "+name+" has an invalid manual update review date")
			}
		}
		image, hasImage := l.UpstreamImages[name]
		if tool.IntegrationTier == manifest.TierUpstream && !hasImage {
			errs = append(errs, "upstream tool "+name+" has no upstream image")
		}
		if tool.IntegrationTier != manifest.TierUpstream && hasImage {
			errs = append(errs, "non-upstream tool "+name+" has an upstream image")
		}
		if hasImage {
			if image.DeclaredReference == "" || !isSortedUnique(image.Platforms) {
				errs = append(errs, "upstream image "+name+" has invalid reference or platforms")
			}
			if strings.Join(image.Platforms, ",") != strings.Join(tool.Platforms, ",") {
				errs = append(errs, "upstream image "+name+" platforms do not match its tool")
			}
			if image.Digest != "" && !digestRE.MatchString(image.Digest) {
				errs = append(errs, "upstream image "+name+" has invalid digest")
			}
			if image.ResolvedReference != "" && !strings.Contains(image.ResolvedReference, "@"+image.Digest) {
				errs = append(errs, "upstream image "+name+" resolvedReference does not match digest")
			}
			if image.Digest == "" && image.ResolutionStatus != "unresolved" {
				errs = append(errs, "upstream image "+name+" is unresolved but has status "+image.ResolutionStatus)
			}
			if image.Digest != "" && image.ResolutionStatus == "unresolved" {
				errs = append(errs, "upstream image "+name+" has a digest but is marked unresolved")
			}
			if !image.MutableSource && image.ResolutionStatus != "digest_pinned" {
				errs = append(errs, "upstream image "+name+" immutable source must be digest_pinned")
			}
		}
	}
	for name := range l.UpstreamImages {
		if _, ok := l.Tools[name]; !ok {
			errs = append(errs, "upstream image has no tool "+name)
		}
	}
	if len(errs) > 0 {
		sort.Strings(errs)
		return fmt.Errorf("invalid scanner lock:\n- %s", strings.Join(errs, "\n- "))
	}
	return nil
}

func validLockUpdateSource(sourceType string) bool {
	for _, supported := range manifest.SupportedUpdateSourceTypes() {
		if sourceType == supported {
			return true
		}
	}
	return false
}

// ValidateResolved applies the publication-time invariant that every upstream
// image has been converted to an immutable digest reference.
func (l Lock) ValidateResolved() error {
	if err := l.Validate(); err != nil {
		return err
	}
	var unresolved []string
	for name, image := range l.UpstreamImages {
		if image.Digest == "" || image.ResolvedReference == "" {
			unresolved = append(unresolved, name+" ("+image.DeclaredReference+")")
		}
	}
	sort.Strings(unresolved)
	if len(unresolved) > 0 {
		return fmt.Errorf("scanner lock has unresolved upstream images:\n- %s", strings.Join(unresolved, "\n- "))
	}
	return nil
}

// ValidateManifestCoverage proves every runtime scanner is represented exactly
// once and that the lock still matches its definition tier and version.
func (l Lock) ValidateManifestCoverage(m *manifest.Manifest) error {
	var errs []string
	for name, def := range m.Tools {
		tool, ok := l.Tools[name]
		if !ok {
			errs = append(errs, "manifest tool missing from lock: "+name)
			continue
		}
		if tool.IntegrationTier != def.IntegrationTier {
			errs = append(errs, fmt.Sprintf("%s integrationTier=%s, manifest=%s", name, tool.IntegrationTier, def.IntegrationTier))
		}
		if tool.PinnedVersion != def.PinnedVersion {
			errs = append(errs, fmt.Sprintf("%s pinnedVersion=%s, manifest=%s", name, tool.PinnedVersion, def.PinnedVersion))
		}
	}
	for name := range l.Tools {
		if _, ok := m.Tools[name]; !ok {
			errs = append(errs, "lock tool missing from manifest: "+name)
		}
	}
	if len(errs) > 0 {
		sort.Strings(errs)
		return fmt.Errorf("scanner lock manifest coverage failed:\n- %s", strings.Join(errs, "\n- "))
	}
	return nil
}

func existingImage(lock *Lock, name, declared string) (UpstreamImage, bool) {
	if lock == nil {
		return UpstreamImage{}, false
	}
	image, ok := lock.UpstreamImages[name]
	return image, ok && image.DeclaredReference == declared && image.Digest != ""
}

func referenceDigest(ref string) (string, bool) {
	_, digest, ok := strings.Cut(ref, "@")
	return digest, ok && digestRE.MatchString(digest)
}

func resolvedReference(ref, digest string) string {
	if base, _, ok := strings.Cut(ref, "@"); ok {
		return base + "@" + digest
	}
	slash := strings.LastIndex(ref, "/")
	colon := strings.LastIndex(ref, ":")
	if colon > slash {
		ref = ref[:colon]
	}
	return ref + "@" + digest
}

func sortedCopy(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	return sortedUnique(in)
}

func sortedUnique(in []string) []string {
	set := make(map[string]struct{}, len(in))
	for _, value := range in {
		if value != "" {
			set[value] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func isSortedUnique(values []string) bool {
	for i, value := range values {
		if value == "" || (i > 0 && values[i-1] >= value) {
			return false
		}
	}
	return true
}

// ImageResolver resolves an OCI/Docker tag to the registry content digest.
// RegistryBase is a test/private-registry hook mapping registry host to API base.
type ImageResolver struct {
	Client       *http.Client
	RegistryBase func(registry string) string
	LookupIP     registryauth.LookupIPFunc
	Cache        httpcache.Store
	CacheMaxAge  time.Duration
	Now          func() time.Time
}

type ResolvedImage struct {
	Digest string
}

const maxManifestResponseBytes int64 = 16 << 20

func (r *ImageResolver) Resolve(ctx context.Context, ref string) (ResolvedImage, error) {
	registry, repository, identifier, err := parseImageReference(ref)
	if err != nil {
		return ResolvedImage{}, err
	}
	base := "https://" + registry
	if r.RegistryBase != nil {
		base = strings.TrimSuffix(r.RegistryBase(registry), "/")
	}
	manifestURL := base + "/v2/" + repository + "/manifests/" + url.PathEscape(identifier)
	digest, err := r.resolveManifest(ctx, manifestURL, registry, repository, "")
	if err != nil {
		return ResolvedImage{}, err
	}
	return ResolvedImage{Digest: digest}, nil
}

func (r *ImageResolver) resolveManifest(
	ctx context.Context,
	manifestURL, registry, repository, token string,
) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, manifestURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", strings.Join([]string{
		"application/vnd.oci.image.index.v1+json",
		"application/vnd.oci.image.manifest.v1+json",
		"application/vnd.docker.distribution.manifest.list.v2+json",
		"application/vnd.docker.distribution.manifest.v2+json",
	}, ", "))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := httpcache.Do(ctx, r.httpClient(), req, httpcache.Options{
		Store: r.Cache, MaxBodyBytes: 0, MaxAge: r.CacheMaxAge, Now: r.Now,
	})
	if err != nil {
		return "", err
	}
	if resp.StatusCode == http.StatusUnauthorized && token == "" {
		nextToken, err := r.bearerToken(ctx, resp.Header.Get("WWW-Authenticate"), registry, repository)
		if err != nil {
			return "", err
		}
		return r.resolveManifest(ctx, manifestURL, registry, repository, nextToken)
	}
	if resp.StatusCode == http.StatusMethodNotAllowed {
		return r.resolveManifestGET(ctx, manifestURL, registry, repository, token)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("HEAD %s: %s", manifestURL, resp.Status)
	}
	digest := resp.Header.Get("Docker-Content-Digest")
	if !digestRE.MatchString(digest) {
		return r.resolveManifestGET(ctx, manifestURL, registry, repository, token)
	}
	return digest, nil
}

func (r *ImageResolver) resolveManifestGET(
	ctx context.Context,
	manifestURL, registry, repository, token string,
) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.docker.distribution.manifest.v2+json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := httpcache.Do(ctx, r.httpClient(), req, httpcache.Options{
		Store: r.Cache, MaxBodyBytes: maxManifestResponseBytes,
		MaxAge: r.CacheMaxAge, Now: r.Now,
	})
	if err != nil {
		return "", err
	}
	if resp.StatusCode == http.StatusUnauthorized && token == "" {
		nextToken, err := r.bearerToken(ctx, resp.Header.Get("WWW-Authenticate"), registry, repository)
		if err != nil {
			return "", err
		}
		return r.resolveManifestGET(ctx, manifestURL, registry, repository, nextToken)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("GET %s: %s", manifestURL, resp.Status)
	}
	if digest := resp.Header.Get("Docker-Content-Digest"); digestRE.MatchString(digest) {
		return digest, nil
	}
	return sha256Digest(resp.Body), nil
}

func (r *ImageResolver) bearerToken(
	ctx context.Context,
	challenge, registry, repository string,
) (string, error) {
	allowLoopbackHTTP := false
	if r.Client != nil && r.RegistryBase != nil {
		baseURL, err := url.Parse(r.RegistryBase(registry))
		allowLoopbackHTTP = err == nil &&
			strings.EqualFold(baseURL.Scheme, "http") &&
			isLoopbackHost(baseURL.Hostname())
	}
	return registryauth.FetchBearerToken(ctx, challenge, registryauth.FetchOptions{
		Client:            r.httpClient(),
		Registry:          registry,
		Repository:        repository,
		LookupIP:          r.LookupIP,
		AllowLoopbackHTTP: allowLoopbackHTTP,
	})
}

func (r *ImageResolver) httpClient() *http.Client {
	if r.Client != nil {
		return r.Client
	}
	return &http.Client{Timeout: 20 * time.Second}
}

func parseImageReference(ref string) (registry, repository, identifier string, err error) {
	if ref == "" {
		return "", "", "", errors.New("image reference is empty")
	}
	base := ref
	if before, digest, ok := strings.Cut(ref, "@"); ok {
		base, identifier = before, digest
	}
	parts := strings.Split(base, "/")
	if len(parts) > 1 && (strings.Contains(parts[0], ".") || strings.Contains(parts[0], ":") || parts[0] == "localhost") {
		registry = parts[0]
		repository = strings.Join(parts[1:], "/")
	} else {
		registry = "registry-1.docker.io"
		repository = base
		if len(parts) == 1 {
			repository = "library/" + base
		}
	}
	if identifier == "" {
		slash := strings.LastIndex(repository, "/")
		colon := strings.LastIndex(repository, ":")
		if colon > slash {
			identifier = repository[colon+1:]
			repository = repository[:colon]
		} else {
			identifier = "latest"
		}
	}
	if registry == "" || repository == "" || identifier == "" {
		return "", "", "", fmt.Errorf("invalid image reference %q", ref)
	}
	return registry, repository, identifier, nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}
