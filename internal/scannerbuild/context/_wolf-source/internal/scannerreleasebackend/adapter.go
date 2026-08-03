package scannerreleasebackend

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

const AdapterEvidenceSchema = "wolf.scanner-release-adapter-evidence/v1"

type AdapterLane string

const (
	AdapterLaneFixed       AdapterLane = "fixed"
	AdapterLaneQuality     AdapterLane = "quality"
	AdapterLaneIntegration AdapterLane = "integration"
)

// AdapterBackend verifies the first-party protocol implemented by dedicated,
// digest-pinned release adapter images. It never turns arbitrary commands into
// capabilities: lane/action/command IDs are compiled into this binary.
type AdapterBackend struct {
	Lane       AdapterLane
	Kubernetes KubernetesBackend
}

func (b AdapterBackend) Name() string { return "kubernetes-" + string(b.Lane) + "-adapter" }

func (b AdapterBackend) Capabilities(ctx context.Context) (Capabilities, error) {
	if strings.TrimSpace(b.Kubernetes.AdapterPath) == "" {
		return Capabilities{}, errors.New("scanner release adapter lane requires a full-invocation adapter executable")
	}
	actions, err := AdapterActionPatterns(b.Lane)
	if err != nil {
		return Capabilities{}, err
	}
	backend := b.Kubernetes
	backend.Actions = actions
	backend.ExecutionLane = string(b.Lane)
	capabilities, err := backend.Capabilities(ctx)
	if err != nil {
		return Capabilities{}, err
	}
	capabilities.Name = b.Name()
	// Adapter evidence is written to an external content-addressed store. The
	// operation acknowledgment is validated below on every success/replay.
	capabilities.ExternalIdempotency = true
	return capabilities, nil
}

func (b AdapterBackend) Execute(ctx context.Context, invocation Invocation) (BackendResult, error) {
	actions, err := AdapterActionPatterns(b.Lane)
	if err != nil {
		return BackendResult{}, err
	}
	if !supportsAction(actions, invocation.Action.Name) {
		return BackendResult{}, fmt.Errorf("%w: %s lane action %q", ErrUnsupportedStep, b.Lane, invocation.Action.Name)
	}
	backend := b.Kubernetes
	backend.Actions = actions
	backend.ExecutionLane = string(b.Lane)
	result, err := backend.Execute(ctx, invocation)
	if err != nil {
		return BackendResult{}, err
	}
	if err := validateAdapterEvidence(b.Lane, invocation, result); err != nil {
		return BackendResult{}, err
	}
	return result, nil
}

// AdapterActionPatterns is the exhaustive, immutable action map. Adding a
// release step requires an explicit code and test change; deployment values
// cannot broaden adapter authority.
func AdapterActionPatterns(lane AdapterLane) ([]string, error) {
	switch lane {
	case AdapterLaneFixed:
		return []string{
			"checkout", "manifest-validate", "generated-parity",
			"update-source-recheck", "lock-reproducibility", "license-metadata",
			"release-manifest", "policy-evaluation", "policy-decision-artifact",
			"candidate-evidence-summary",
		}, nil
	case AdapterLaneQuality:
		return []string{
			"strict-version-smoke/*", "invocation-smoke/*", "fixer-auth-contract/*",
			"parser-fixtures/*", "normalized-golden/*",
			"candidate-stable-comparison/*", "recorded-resource-gate/*",
			"vulnerability-scan/*", "vulnerability-db-identity/*",
			"secret-scan/*", "license-scan/*", "sbom/*", "oci-annotations/*",
			"provenance/*", "finding-regression", "aggregate-sbom", "fixer-integration",
		}, nil
	case AdapterLaneIntegration:
		return []string{
			"compose-integration", "compose-scanner-integration",
			"kubernetes-integration", "kind-scanner-integration",
		}, nil
	default:
		return nil, fmt.Errorf("unsupported scanner release adapter lane %q", lane)
	}
}

type adapterEvidence struct {
	SchemaVersion  string                       `json:"schema_version"`
	Lane           AdapterLane                  `json:"lane"`
	Action         string                       `json:"action"`
	OperationID    string                       `json:"operation_id"`
	CommandID      string                       `json:"command_id"`
	OutputIdentity string                       `json:"output_identity"`
	Artifact       adapterArtifact              `json:"artifact"`
	Runtime        string                       `json:"runtime,omitempty"`
	ImageDigests   map[string]string            `json:"image_digests,omitempty"`
	SubjectDigest  string                       `json:"subject_digest,omitempty"`
	ReferrerDigest string                       `json:"referrer_digest,omitempty"`
	ImageManifest  adapterImageManifestEvidence `json:"image_manifest,omitempty"`
}

type adapterArtifact struct {
	URI              string `json:"uri"`
	Digest           string `json:"digest"`
	PayloadDigest    string `json:"payload_digest"`
	MediaType        string `json:"media_type"`
	SizeBytes        int64  `json:"size_bytes"`
	StorageMediaType string `json:"storage_media_type"`
	StorageSizeBytes int64  `json:"storage_size_bytes"`
	ReadBackVerified bool   `json:"read_back_verified"`
}

// ValidateAdapterResult applies the production adapter evidence contract to a
// result emitted by a first-party scanner release adapter.
func ValidateAdapterResult(
	lane AdapterLane,
	invocation Invocation,
	result BackendResult,
) error {
	return validateAdapterEvidence(lane, invocation, result)
}

func validateAdapterEvidence(
	lane AdapterLane,
	invocation Invocation,
	result BackendResult,
) error {
	raw, exists := result.Result.Summary["adapter_evidence"]
	if !exists {
		return errors.New("scanner release adapter returned no adapter_evidence")
	}
	value, err := strictJSONRoundTrip[adapterEvidence](raw)
	if err != nil {
		return fmt.Errorf("decode scanner release adapter evidence: %w", err)
	}
	expectedCommand, err := adapterCommandID(invocation.Action.Name)
	if err != nil {
		return err
	}
	if value.SchemaVersion != AdapterEvidenceSchema || value.Lane != lane ||
		value.Action != invocation.Action.Name || value.OperationID != invocation.OperationID ||
		value.CommandID != expectedCommand {
		return errors.New("scanner release adapter evidence binding is invalid")
	}
	artifact := value.Artifact
	expectedIdentity := "payload"
	expectedDigest := artifact.PayloadDigest
	if invocation.Action.Name == "release-manifest" {
		expectedIdentity = "storage"
		expectedDigest = artifact.Digest
	}
	if value.OutputIdentity != expectedIdentity || expectedDigest != result.Result.OutputDigest ||
		artifact.URI != result.Result.OutputURI ||
		!digestPattern.MatchString(artifact.Digest) ||
		!digestPattern.MatchString(artifact.PayloadDigest) ||
		strings.TrimSpace(artifact.MediaType) == "" ||
		artifact.SizeBytes < 0 || strings.TrimSpace(artifact.StorageMediaType) == "" ||
		artifact.StorageSizeBytes <= 0 || !artifact.ReadBackVerified {
		return errors.New("scanner release adapter artifact was not exact-digest read-back verified")
	}
	parsed, err := url.Parse(artifact.URI)
	if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		!adapterArtifactScheme(parsed.Scheme) || !strings.Contains(artifact.URI, artifact.Digest) {
		return errors.New("scanner release adapter artifact URI is not immutable and retrievable")
	}
	if result.ExternalOperationID != invocation.OperationID {
		return errors.New("scanner release adapter did not acknowledge the exact external operation")
	}
	if strings.HasPrefix(invocation.Action.Name, "sbom/") ||
		strings.HasPrefix(invocation.Action.Name, "provenance/") {
		image := strings.TrimPrefix(invocation.Action.Name, "sbom/")
		if strings.HasPrefix(invocation.Action.Name, "provenance/") {
			image = strings.TrimPrefix(invocation.Action.Name, "provenance/")
		}
		subject, ok := invocation.Request.Dependencies["image-manifest/"+image]
		if !ok || !digestPattern.MatchString(subject.OutputDigest) ||
			value.SubjectDigest != subject.OutputDigest ||
			value.ReferrerDigest != artifact.Digest || parsed.Scheme != "oci" {
			return errors.New("scanner release adapter evidence is not an exact subject-bound OCI referrer")
		}
	}
	if strings.HasPrefix(invocation.Action.Name, "oci-annotations/") {
		image := strings.TrimPrefix(invocation.Action.Name, "oci-annotations/")
		subject, ok := invocation.Request.Dependencies["image-manifest/"+image]
		if !ok || !digestPattern.MatchString(subject.OutputDigest) ||
			value.SubjectDigest != subject.OutputDigest {
			return errors.New("OCI annotation evidence is not bound to the exact image subject")
		}
		binding, err := resolveImageTrustBinding(invocation, image)
		if err != nil {
			return err
		}
		if err := validateAnnotatedOCIManifest(
			value.ImageManifest, subject.OutputDigest, binding,
		); err != nil {
			return err
		}
	}
	if lane == AdapterLaneIntegration {
		expectedRuntime := map[string]string{
			"compose-integration":         "compose",
			"compose-scanner-integration": "compose",
			"kubernetes-integration":      "kubernetes",
			"kind-scanner-integration":    "kind",
		}[invocation.Action.Name]
		if value.Runtime != expectedRuntime || len(value.ImageDigests) == 0 {
			return errors.New("integration adapter evidence has no real runtime/image identity")
		}
		for image, digest := range value.ImageDigests {
			if image == "" || !digestPattern.MatchString(digest) {
				return errors.New("integration adapter image digest inventory is invalid")
			}
		}
	}
	return nil
}

func adapterArtifactScheme(scheme string) bool {
	switch strings.ToLower(scheme) {
	case "oci", "https", "s3", "gs", "azblob":
		return true
	default:
		return false
	}
}

func adapterCommandID(action string) (string, error) {
	return AdapterCommandID(action)
}

// AdapterCommandID returns the immutable first-party command identity for an
// exact release action. Adapter executables use this exported lookup so the
// protocol catalog has one compiled source of truth.
func AdapterCommandID(action string) (string, error) {
	exact := map[string]string{
		"checkout": "git-checkout-exact", "manifest-validate": "scannertools-manifest-validate",
		"generated-parity": "scanner-build-context-parity", "update-source-recheck": "scanner-update-source-recheck",
		"lock-reproducibility": "scannertools-lock-reproducibility", "license-metadata": "scanner-license-metadata",
		"finding-regression": "scanner-finding-regression", "aggregate-sbom": "scanner-aggregate-sbom",
		"fixer-integration": "fixer-integration", "compose-integration": "compose-release-integration",
		"compose-scanner-integration": "compose-scanner-integration",
		"kubernetes-integration":      "kubernetes-release-integration",
		"kind-scanner-integration":    "kind-scanner-integration",
		"release-manifest":            "scanner-release-manifest", "policy-evaluation": "scanner-policy-input",
		"policy-decision-artifact":   "scanner-policy-decision",
		"candidate-evidence-summary": "scanner-publication-receipt",
	}
	if value := exact[action]; value != "" {
		return value, nil
	}
	prefixes := []struct{ prefix, command string }{
		{"strict-version-smoke/", "image-strict-version-smoke"},
		{"invocation-smoke/", "image-invocation-smoke"},
		{"fixer-auth-contract/", "fixer-auth-contract"},
		{"parser-fixtures/", "scanner-parser-fixtures"},
		{"normalized-golden/", "scanner-normalized-golden"},
		{"candidate-stable-comparison/", "scanner-candidate-stable-comparison"},
		{"recorded-resource-gate/", "scanner-recorded-resource-gate"},
		{"vulnerability-scan/", "image-vulnerability-scan"},
		{"vulnerability-db-identity/", "vulnerability-database-identity"},
		{"secret-scan/", "image-secret-scan"}, {"license-scan/", "image-license-scan"},
		{"sbom/", "image-sbom"}, {"oci-annotations/", "oci-annotation-verify"},
		{"provenance/", "image-provenance"},
	}
	for _, candidate := range prefixes {
		if strings.HasPrefix(action, candidate.prefix) {
			return candidate.command, nil
		}
	}
	return "", fmt.Errorf("%w: adapter command for %q", ErrUnsupportedStep, action)
}

func strictJSONRoundTrip[T any](input any) (T, error) {
	var result T
	value, err := json.Marshal(input)
	if err != nil {
		return result, err
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return result, err
	}
	if err := ensureEOF(decoder); err != nil {
		return result, err
	}
	return result, nil
}
