package scannerreleasebackend

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/alphabravocompany/thewolf/internal/scannerpipeline"
	"github.com/alphabravocompany/thewolf/internal/scannerregistry"
	"github.com/alphabravocompany/thewolf/internal/scannerreleaseworker"
	"github.com/alphabravocompany/thewolf/internal/scannerreleaseworkspace"
	"github.com/alphabravocompany/thewolf/internal/scannersigning"
)

// RegistryClientProvider resolves credentials behind opaque deployment-owned
// references. Implementations must return a client configured only for the two
// origins in the workspace execution context.
type RegistryClientProvider interface {
	Client(context.Context, scannerreleaseworkspace.ExecutionContext) (scannerregistry.Client, error)
}

type RegistryClientProviderFunc func(context.Context, scannerreleaseworkspace.ExecutionContext) (scannerregistry.Client, error)

func (f RegistryClientProviderFunc) Client(ctx context.Context, execution scannerreleaseworkspace.ExecutionContext) (scannerregistry.Client, error) {
	return f(ctx, execution)
}

// MirrorSigner signs the destination digest after a mirror copy. The release
// DAG has one aggregate mirror step rather than one signature step per mirror,
// so this authority is explicit and cannot be inferred from registry access.
type MirrorSigner interface {
	SignMirror(context.Context, string, Binding, scannersigning.Artifact) (MirrorSigningReceipt, error)
}

// MirrorSigningReceipt proves that the signer applied the exact deterministic
// child operation. A digest alone cannot distinguish a replay-safe sink write
// from an unrelated pre-existing signature.
type MirrorSigningReceipt struct {
	ExternalOperationID     string
	SignatureURI            string
	SignatureDigest         string
	SignatureArtifactDigest string
	SignatureMediaType      string
	SignatureArtifactSize   int64
	CertificateDigest       string
	Identity                string
	Issuer                  string
	Subject                 string
	TrustRoot               string
}

type RegistryBackend struct {
	Clients      RegistryClientProvider
	MirrorSigner MirrorSigner
	Platforms    []string
}

func (b RegistryBackend) Name() string { return "oci-registry" }

func (b RegistryBackend) Capabilities(context.Context) (Capabilities, error) {
	if b.Clients == nil {
		return Capabilities{}, errors.New("registry backend requires an opaque client provider")
	}
	actions := []string{"image-manifest/*", "candidate-publish/*", "published-verify/*"}
	if b.MirrorSigner != nil {
		actions = append(actions, "mirror-copy-verify", "mirror-release-closure-verify")
	}
	gib := int64(1 << 30)
	return Capabilities{
		Name: b.Name(), Actions: actions,
		Kinds:     []scannerpipeline.StepKind{scannerpipeline.StepPublish, scannerpipeline.StepEvidence},
		Platforms: append([]string(nil), b.Platforms...),
		MaxCPU:    8000, MaxMemory: 16 * gib, MaxDisk: 100 * gib,
		MaxTimeout: 2 * time.Hour, MaxConcurrency: 8,
		EnforcesCPU: true, EnforcesMemory: true, EnforcesDisk: true,
		EnforcesTimeout: true, EnforcesCancellation: true, Idempotent: true,
		ExternalIdempotency: true,
	}, nil
}

func (b RegistryBackend) Execute(ctx context.Context, invocation Invocation) (BackendResult, error) {
	capability, err := b.Capabilities(ctx)
	if err != nil {
		return BackendResult{}, err
	}
	if !supportsAction(capability.Actions, invocation.Action.Name) {
		return BackendResult{}, fmt.Errorf("%w: registry action %q", ErrUnsupportedStep, invocation.Action.Name)
	}
	execution, err := scannerreleaseworkspace.ReadContext(invocation.Request.Workspace)
	if err != nil {
		return BackendResult{}, fmt.Errorf("read registry execution context: %w", err)
	}
	client, err := b.Clients.Client(ctx, execution)
	if err != nil {
		return BackendResult{}, err
	}
	switch {
	case strings.HasPrefix(invocation.Action.Name, "image-manifest/"):
		return b.createImageManifest(ctx, client, execution, invocation)
	case strings.HasPrefix(invocation.Action.Name, "candidate-publish/"):
		return b.publishCandidate(ctx, client, execution, invocation)
	case strings.HasPrefix(invocation.Action.Name, "published-verify/"):
		return b.verifyPublished(ctx, client, invocation)
	case invocation.Action.Name == "mirror-copy-verify":
		return b.mirror(ctx, client, execution, invocation)
	case invocation.Action.Name == "mirror-release-closure-verify":
		return b.mirrorReleaseClosure(ctx, client, execution, invocation)
	default:
		return BackendResult{}, fmt.Errorf("%w: registry action %q", ErrUnsupportedStep, invocation.Action.Name)
	}
}

func (b RegistryBackend) mirrorReleaseClosure(
	ctx context.Context,
	client scannerregistry.Client,
	execution scannerreleaseworkspace.ExecutionContext,
	invocation Invocation,
) (BackendResult, error) {
	manifestResult, err := readStepResult(invocation, "release-manifest")
	if err != nil {
		return BackendResult{}, err
	}
	source, err := immutableReference(manifestResult.OutputURI, manifestResult.OutputDigest)
	if err != nil {
		return BackendResult{}, fmt.Errorf("resolve signed release manifest: %w", err)
	}
	signature, err := readStepResult(invocation, "release-manifest-signature")
	if err != nil {
		return BackendResult{}, errors.New("release manifest signature evidence is incomplete")
	}
	expected, err := signingClosureEvidence(signature, "release_manifest")
	if err != nil {
		return BackendResult{}, fmt.Errorf("release manifest signature evidence is incomplete: %w", err)
	}
	if err := requireOCIReferrerEvidence(ctx, client, source, expected, "primary release closure"); err != nil {
		return BackendResult{}, err
	}
	repository := path.Join(
		strings.Trim(execution.Mirror.Repository, "/"), "wolf-release-manifests",
	)
	if repository == "." || strings.HasPrefix(repository, "../") {
		return BackendResult{}, errors.New("mirror release manifest repository is invalid")
	}
	destination := scannerregistry.Reference{
		Registry: execution.Mirror.Host, Repository: repository, Digest: source.Digest,
	}
	if err := client.CopyManifestGraph(ctx, source, destination); err != nil {
		return BackendResult{}, fmt.Errorf("copy signed release closure: %w", err)
	}
	if err := client.EnsureManifestAlias(
		ctx, destination, operationAlias(invocation.OperationID),
	); err != nil {
		return BackendResult{}, err
	}
	readback, err := client.FetchManifest(ctx, destination)
	if err != nil || readback.Digest != source.Digest {
		return BackendResult{}, errors.New("mirror release manifest exact-digest readback failed")
	}
	if err := requireOCIReferrerEvidence(
		ctx, client, destination, expected, "mirror release closure",
	); err != nil {
		return BackendResult{}, err
	}
	return BackendResult{
		Binding: invocation.Binding, ExternalOperationID: invocation.OperationID,
		Result: scannerreleaseworkerResult(
			"oci://"+destination.String(), destination.Digest,
			map[string]any{
				"schema_version":                    "wolf.scanner-mirror-release-closure/v1",
				"release_manifest_digest":           destination.Digest,
				"release_manifest_signature_digest": signature.OutputDigest,
				"registry_target_id":                execution.Mirror.ID,
				"read_back_verified":                true,
			},
		),
	}, nil
}

func (b RegistryBackend) createImageManifest(
	ctx context.Context,
	client scannerregistry.Client,
	execution scannerreleaseworkspace.ExecutionContext,
	invocation Invocation,
) (BackendResult, error) {
	image, err := imageSnapshot(invocation.Request.PlatformsJSON, invocation.Action.Image)
	if err != nil {
		return BackendResult{}, err
	}
	trustBinding, err := resolveImageTrustBinding(invocation, image.Key)
	if err != nil {
		return BackendResult{}, err
	}
	repository, err := imageRepository(execution.Primary.Repository, image.Key)
	if err != nil {
		return BackendResult{}, err
	}
	descriptors := make([]scannerregistry.Descriptor, 0, len(image.Platforms))
	platformDigests := make(map[string]string, len(image.Platforms))
	buildAttestations := make(map[string]map[string]buildxAttestationEvidence, len(image.Platforms))
	for _, platform := range image.Platforms {
		buildKey := "build/" + image.Key + "/" + strings.ReplaceAll(platform, "/", "-")
		built, readErr := readStepResult(invocation, buildKey)
		if readErr != nil {
			return BackendResult{}, readErr
		}
		source, referenceErr := immutableReference(built.OutputURI, built.OutputDigest)
		if referenceErr != nil {
			return BackendResult{}, fmt.Errorf("resolve %s: %w", buildKey, referenceErr)
		}
		destination := scannerregistry.Reference{
			Registry: execution.Primary.Host, Repository: repository, Digest: source.Digest,
		}
		if source != destination {
			if err := client.CopyManifestGraph(ctx, source, destination); err != nil {
				return BackendResult{}, fmt.Errorf("copy %s platform manifest: %w", platform, err)
			}
		}
		manifest, fetchErr := client.FetchManifest(ctx, destination)
		if fetchErr != nil || manifest.Digest != source.Digest {
			return BackendResult{}, fmt.Errorf("read back %s platform manifest", platform)
		}
		runnable, attestations, evidence, selectErr := selectBuildxPlatformClosure(
			ctx, client, destination, manifest, platform,
			provenanceExpectation{imageTrustBinding: trustBinding, Platform: platform},
		)
		if selectErr != nil {
			return BackendResult{}, fmt.Errorf("validate %s Buildx output: %w", platform, selectErr)
		}
		descriptors = append(descriptors, runnable)
		descriptors = append(descriptors, attestations...)
		platformDigests[platform] = runnable.Digest
		buildAttestations[platform] = evidence
	}
	const indexMediaType = "application/vnd.oci.image.index.v1+json"
	content, err := json.Marshal(struct {
		SchemaVersion int                          `json:"schemaVersion"`
		MediaType     string                       `json:"mediaType"`
		Manifests     []scannerregistry.Descriptor `json:"manifests"`
		Annotations   map[string]string            `json:"annotations"`
	}{
		SchemaVersion: 2, MediaType: indexMediaType, Manifests: descriptors,
		Annotations: requiredOCIAnnotations(trustBinding),
	})
	if err != nil {
		return BackendResult{}, err
	}
	sum := sha256.Sum256(content)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	reference := scannerregistry.Reference{
		Registry: execution.Primary.Host, Repository: repository, Digest: digest,
	}
	if err := client.PutManifest(ctx, reference, indexMediaType, content); err != nil {
		return BackendResult{}, err
	}
	if err := client.EnsureManifestAlias(ctx, reference, operationAlias(invocation.OperationID)); err != nil {
		return BackendResult{}, err
	}
	readback, err := client.FetchManifest(ctx, reference)
	if err != nil || readback.Digest != digest || len(readback.Descriptors) != len(descriptors) {
		return BackendResult{}, errors.New("multi-platform image index readback failed")
	}
	return BackendResult{
		Binding: invocation.Binding, ExternalOperationID: invocation.OperationID,
		Result: scannerreleaseworkerResult("oci://"+reference.String(), digest, map[string]any{
			"image": image.Key, "image_kind": image.Kind,
			"platform_digests":   platformDigests,
			"build_attestations": buildAttestations,
			"registry_target_id": execution.Primary.ID,
			"read_back_verified": true,
		}),
	}, nil
}

func selectBuildxPlatformClosure(
	ctx context.Context,
	client scannerregistry.Client,
	root scannerregistry.Reference,
	manifest *scannerregistry.Manifest,
	platform string,
	expectation provenanceExpectation,
) (scannerregistry.Descriptor, []scannerregistry.Descriptor, map[string]buildxAttestationEvidence, error) {
	expected, err := parseOCIPlatform(platform)
	if err != nil {
		return scannerregistry.Descriptor{}, nil, nil, err
	}
	if manifest == nil || len(manifest.Descriptors) == 0 {
		return scannerregistry.Descriptor{}, nil, nil, errors.New(
			"Buildx provenance/SBOM output must be an attestation-bearing OCI index",
		)
	}
	var runnable scannerregistry.Descriptor
	attestations := make([]scannerregistry.Descriptor, 0, len(manifest.Descriptors)-1)
	for _, descriptor := range manifest.Descriptors {
		if buildxAttestationDescriptor(descriptor) {
			attestations = append(attestations, descriptor)
			continue
		}
		if descriptor.Platform != expected {
			return scannerregistry.Descriptor{}, nil, nil, fmt.Errorf(
				"unexpected runnable platform %q in single-platform Buildx output",
				descriptor.Platform.String(),
			)
		}
		if runnable.Digest != "" {
			return scannerregistry.Descriptor{}, nil, nil, errors.New(
				"Buildx output contains multiple runnable manifests for one platform",
			)
		}
		runnable = descriptor
	}
	if !digestPattern.MatchString(runnable.Digest) || runnable.Size <= 0 {
		return scannerregistry.Descriptor{}, nil, nil, errors.New(
			"Buildx output has no exact runnable platform manifest",
		)
	}
	runnableManifest, err := client.FetchManifest(ctx, scannerregistry.Reference{
		Registry: root.Registry, Repository: root.Repository, Digest: runnable.Digest,
	})
	if err != nil || runnableManifest.Digest != runnable.Digest ||
		int64(len(runnableManifest.Content)) != runnable.Size {
		return scannerregistry.Descriptor{}, nil, nil, errors.New(
			"Buildx runnable platform manifest failed exact-digest readback",
		)
	}
	evidence := make(map[string]buildxAttestationEvidence, 2)
	for _, descriptor := range attestations {
		if !digestPattern.MatchString(descriptor.Digest) || descriptor.Size <= 0 {
			return scannerregistry.Descriptor{}, nil, nil, errors.New(
				"Buildx attestation descriptor is invalid",
			)
		}
		attestation, err := client.FetchManifest(ctx, scannerregistry.Reference{
			Registry: root.Registry, Repository: root.Repository, Digest: descriptor.Digest,
		})
		if err != nil || attestation.Digest != descriptor.Digest ||
			int64(len(attestation.Content)) != descriptor.Size {
			return scannerregistry.Descriptor{}, nil, nil, errors.New(
				"Buildx attestation manifest failed exact-digest readback",
			)
		}
		items, err := inspectBuildxAttestation(
			ctx, client, root, descriptor, attestation.Content, runnable.Digest, expectation,
		)
		if err != nil {
			return scannerregistry.Descriptor{}, nil, nil, err
		}
		for kind, item := range items {
			if previous := evidence[kind]; previous.ManifestDigest != "" {
				return scannerregistry.Descriptor{}, nil, nil, fmt.Errorf(
					"Buildx output contains multiple %s attestation manifests", kind,
				)
			}
			evidence[kind] = item
		}
	}
	if evidence["sbom"].ManifestDigest == "" || evidence["provenance"].ManifestDigest == "" {
		return scannerregistry.Descriptor{}, nil, nil, errors.New(
			"Buildx output does not contain subject-bound SBOM and provenance attestations",
		)
	}
	return runnable, attestations, evidence, nil
}

func buildxAttestationDescriptor(descriptor scannerregistry.Descriptor) bool {
	return descriptor.Annotations["vnd.docker.reference.type"] == "attestation-manifest" ||
		(descriptor.Platform.OS == "unknown" && descriptor.Platform.Architecture == "unknown")
}

func inspectBuildxAttestation(
	ctx context.Context,
	client scannerregistry.Client,
	root scannerregistry.Reference,
	descriptor scannerregistry.Descriptor,
	content []byte,
	runnableDigest string,
	expectation provenanceExpectation,
) (map[string]buildxAttestationEvidence, error) {
	var document struct {
		ArtifactType string                       `json:"artifactType"`
		Subject      *scannerregistry.Descriptor  `json:"subject"`
		Layers       []scannerregistry.Descriptor `json:"layers"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(content)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		// OCI manifests contain config/schema/media fields outside this focused
		// view, so retry with ordinary unmarshalling while keeping linkage checks
		// below authoritative.
		if err := json.Unmarshal(content, &document); err != nil {
			return nil, fmt.Errorf("decode Buildx attestation manifest: %w", err)
		}
	}
	descriptorSubject := descriptor.Annotations["vnd.docker.reference.digest"]
	manifestSubject := ""
	if document.Subject != nil {
		manifestSubject = document.Subject.Digest
	}
	if descriptorSubject == "" && manifestSubject == "" {
		return nil, errors.New("Buildx attestation does not declare its runnable subject")
	}
	if (descriptorSubject != "" && descriptorSubject != runnableDigest) ||
		(manifestSubject != "" && manifestSubject != runnableDigest) {
		return nil, errors.New("Buildx attestation subject does not match the runnable manifest")
	}
	evidence := make(map[string]buildxAttestationEvidence, 2)
	for _, layer := range document.Layers {
		if !digestPattern.MatchString(layer.Digest) || layer.Size <= 0 ||
			layer.MediaType != "application/vnd.in-toto+json" {
			return nil, errors.New("Buildx attestation payload descriptor is invalid")
		}
		payload, err := client.FetchBlob(ctx, root, layer.Digest)
		if err != nil {
			return nil, fmt.Errorf("Buildx attestation payload failed exact-digest readback: %w", err)
		}
		if int64(len(payload)) != layer.Size {
			return nil, fmt.Errorf(
				"Buildx attestation payload size %d does not match descriptor %d",
				len(payload), layer.Size,
			)
		}
		kind, builderID, err := inspectInTotoPayload(
			payload, runnableDigest, expectation,
		)
		if err != nil {
			return nil, err
		}
		annotatedPredicate := layer.Annotations["in-toto.io/predicate-type"]
		if annotatedPredicate == "" {
			annotatedPredicate = layer.Annotations["predicate-type"]
		}
		var statement inTotoStatement
		if err := json.Unmarshal(payload, &statement); err != nil ||
			(annotatedPredicate != "" && annotatedPredicate != statement.PredicateType) {
			return nil, errors.New("Buildx attestation annotation does not match its exact predicate payload")
		}
		if previous := evidence[kind]; previous.PayloadDigest != "" {
			return nil, fmt.Errorf("Buildx attestation manifest contains multiple %s predicates", kind)
		}
		evidence[kind] = buildxAttestationEvidence{
			ManifestDigest: descriptor.Digest, PayloadDigest: layer.Digest,
			PredicateType: statement.PredicateType, SubjectDigest: runnableDigest,
			BuilderID: builderID,
		}
	}
	if len(evidence) == 0 {
		return nil, errors.New("Buildx attestation manifest has no recognized SBOM or provenance predicate")
	}
	return evidence, nil
}

func (b RegistryBackend) publishCandidate(
	ctx context.Context,
	client scannerregistry.Client,
	execution scannerreleaseworkspace.ExecutionContext,
	invocation Invocation,
) (BackendResult, error) {
	manifest, err := readStepResult(
		invocation, "image-manifest/"+invocation.Action.Image,
	)
	if err != nil {
		return BackendResult{}, err
	}
	if manifest.OutputDigest == "" {
		return BackendResult{}, errors.New("candidate image manifest has no digest")
	}
	source, err := immutableReference(manifest.OutputURI, manifest.OutputDigest)
	if err != nil {
		return BackendResult{}, err
	}
	repository, err := imageRepository(execution.Primary.Repository, invocation.Action.Image)
	if err != nil {
		return BackendResult{}, err
	}
	destination := scannerregistry.Reference{
		Registry:   execution.Primary.Host,
		Repository: repository,
		Digest:     manifest.OutputDigest,
	}
	if source != destination {
		if err := client.CopyManifestGraph(ctx, source, destination); err != nil {
			return BackendResult{}, err
		}
	}
	supplyChainEvidence, err := requiredImageEvidence(
		invocation, invocation.Action.Image, "sbom", "provenance",
	)
	if err != nil {
		return BackendResult{}, err
	}
	if err := requireOCIReferrerEvidence(
		ctx, client, destination, supplyChainEvidence, "candidate",
	); err != nil {
		return BackendResult{}, err
	}
	if err := client.EnsureManifestAlias(ctx, destination, operationAlias(invocation.OperationID)); err != nil {
		return BackendResult{}, err
	}
	readback, err := client.FetchManifest(ctx, destination)
	if err != nil || readback.Digest != destination.Digest {
		return BackendResult{}, errors.New("candidate registry readback did not preserve the image digest")
	}
	uri := "oci://" + destination.String()
	if err := writeSigningDescriptor(
		invocation.Request.Workspace, "signature/"+invocation.Action.Image,
		scannersigning.Artifact{
			URI: uri, Digest: destination.Digest, MediaType: readback.MediaType,
		},
	); err != nil {
		return BackendResult{}, err
	}
	return BackendResult{
		Binding: invocation.Binding, ExternalOperationID: invocation.OperationID,
		Result: scannerreleaseworkerResult(uri, destination.Digest, map[string]any{
			"image": invocation.Action.Image, "registry_target_id": execution.Primary.ID,
			"repository":         destination.Registry + "/" + destination.Repository,
			"media_type":         readback.MediaType,
			"read_back_verified": true,
		}),
	}, nil
}

func (b RegistryBackend) verifyPublished(
	ctx context.Context,
	client scannerregistry.Client,
	invocation Invocation,
) (BackendResult, error) {
	published, err := readStepResult(
		invocation, "candidate-publish/"+invocation.Action.Image,
	)
	if err != nil {
		return BackendResult{}, err
	}
	reference, err := immutableReference(published.OutputURI, published.OutputDigest)
	if err != nil {
		return BackendResult{}, err
	}
	manifest, err := client.FetchManifest(ctx, reference)
	if err != nil || manifest.Digest != published.OutputDigest {
		return BackendResult{}, errors.New("published image digest readback failed")
	}
	signature, err := readStepResult(
		invocation, "signature/"+invocation.Action.Image,
	)
	if err != nil || signature.OutputDigest == "" {
		return BackendResult{}, errors.New("published image has no verified signature evidence")
	}
	supplyChainEvidence, err := requiredImageEvidence(
		invocation, invocation.Action.Image, "sbom", "provenance", "signature",
	)
	if err != nil {
		return BackendResult{}, err
	}
	if err := requireOCIReferrerEvidence(
		ctx, client, reference, supplyChainEvidence, "published",
	); err != nil {
		return BackendResult{}, err
	}
	return BackendResult{
		Binding: invocation.Binding,
		Result: scannerreleaseworkerResult(published.OutputURI, published.OutputDigest, map[string]any{
			"image": invocation.Action.Image, "read_back_verified": true,
			"signature_digest": signature.OutputDigest,
		}),
	}, nil
}

func (b RegistryBackend) mirror(
	ctx context.Context,
	client scannerregistry.Client,
	execution scannerreleaseworkspace.ExecutionContext,
	invocation Invocation,
) (BackendResult, error) {
	if b.MirrorSigner == nil {
		return BackendResult{}, fmt.Errorf("%w: mirror signer is not configured", ErrUnsupportedStep)
	}
	var images []scannerpipeline.Image
	decoder := json.NewDecoder(strings.NewReader(invocation.Request.PlatformsJSON))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&images); err != nil {
		return BackendResult{}, fmt.Errorf("decode mirror image inventory: %w", err)
	}
	results := make(map[string]any, len(images))
	for _, image := range images {
		published, err := readStepResult(invocation, "published-verify/"+image.Key)
		if err != nil {
			return BackendResult{}, err
		}
		source, err := immutableReference(published.OutputURI, published.OutputDigest)
		if err != nil {
			return BackendResult{}, err
		}
		expectedEvidence, err := requiredImageReferrerEvidence(invocation, image.Key)
		if err != nil {
			return BackendResult{}, err
		}
		if err := requireOCIReferrerEvidence(
			ctx, client, source, expectedEvidence, "primary",
		); err != nil {
			return BackendResult{}, err
		}
		repository, err := imageRepository(execution.Mirror.Repository, image.Key)
		if err != nil {
			return BackendResult{}, err
		}
		destination := scannerregistry.Reference{
			Registry:   execution.Mirror.Host,
			Repository: repository,
			Digest:     source.Digest,
		}
		if err := client.CopyManifestGraph(ctx, source, destination); err != nil {
			return BackendResult{}, err
		}
		if err := client.EnsureManifestAlias(ctx, destination, operationAlias(invocation.OperationID)); err != nil {
			return BackendResult{}, err
		}
		manifest, err := client.FetchManifest(ctx, destination)
		if err != nil || manifest.Digest != source.Digest {
			return BackendResult{}, errors.New("mirror image readback failed")
		}
		if err := requireOCIReferrerEvidence(
			ctx, client, destination, expectedEvidence, "mirror",
		); err != nil {
			return BackendResult{}, err
		}
		childOperation := childOperationID(invocation.OperationID, image.Key)
		signingReceipt, err := b.MirrorSigner.SignMirror(
			ctx, childOperation, invocation.Binding,
			scannersigning.Artifact{
				URI: "oci://" + destination.String(), Digest: destination.Digest,
				MediaType: manifest.MediaType,
			},
		)
		if err != nil || signingReceipt.ExternalOperationID != childOperation ||
			!digestPattern.MatchString(signingReceipt.SignatureDigest) ||
			!digestPattern.MatchString(signingReceipt.SignatureArtifactDigest) ||
			strings.TrimSpace(signingReceipt.SignatureMediaType) == "" ||
			signingReceipt.SignatureArtifactSize <= 0 ||
			strings.TrimSpace(signingReceipt.Identity) == "" ||
			strings.TrimSpace(signingReceipt.Issuer) == "" ||
			strings.TrimSpace(signingReceipt.Subject) == "" ||
			strings.TrimSpace(signingReceipt.TrustRoot) == "" ||
			!strings.Contains(signingReceipt.SignatureURI, signingReceipt.SignatureArtifactDigest) {
			return BackendResult{}, errors.New("mirror signature did not verify")
		}
		mirrorSignatureEvidence := map[string]string{
			"mirror_signature":          signingReceipt.SignatureDigest,
			"mirror_signature_artifact": signingReceipt.SignatureArtifactDigest,
		}
		if signingReceipt.CertificateDigest != "" {
			if !digestPattern.MatchString(signingReceipt.CertificateDigest) {
				return BackendResult{}, errors.New("mirror signature certificate digest is invalid")
			}
			mirrorSignatureEvidence["mirror_certificate"] = signingReceipt.CertificateDigest
		}
		if err := requireOCIReferrerEvidence(
			ctx, client, destination, mirrorSignatureEvidence, "mirror signature",
		); err != nil {
			return BackendResult{}, err
		}
		results[image.Key] = map[string]any{
			"reference":                     "oci://" + destination.String(),
			"digest":                        destination.Digest,
			"signature_digest":              signingReceipt.SignatureDigest,
			"signature_uri":                 signingReceipt.SignatureURI,
			"signature_artifact_digest":     signingReceipt.SignatureArtifactDigest,
			"signature_media_type":          signingReceipt.SignatureMediaType,
			"signature_artifact_size_bytes": signingReceipt.SignatureArtifactSize,
			"certificate_digest":            signingReceipt.CertificateDigest,
			"signing_operation_id":          signingReceipt.ExternalOperationID,
			"signer_identity":               signingReceipt.Identity,
			"signer_issuer":                 signingReceipt.Issuer,
			"signer_subject":                signingReceipt.Subject,
			"signer_trust_root":             signingReceipt.TrustRoot,
			"registry_target_id":            execution.Mirror.ID,
			"source_evidence":               expectedEvidence,
		}
	}
	value, err := json.Marshal(struct {
		SchemaVersion string         `json:"schema_version"`
		OperationID   string         `json:"operation_id"`
		Binding       Binding        `json:"binding"`
		Images        map[string]any `json:"images"`
	}{
		SchemaVersion: "wolf.scanner-mirror-receipt/v1",
		OperationID:   invocation.OperationID, Binding: invocation.Binding,
		Images: results,
	})
	if err != nil {
		return BackendResult{}, err
	}
	configSum := sha256.Sum256(value)
	configDigest := "sha256:" + hex.EncodeToString(configSum[:])
	receiptRepository := path.Join(strings.Trim(execution.Mirror.Repository, "/"), "wolf-mirror-receipts")
	configReference := scannerregistry.Reference{
		Registry: execution.Mirror.Host, Repository: receiptRepository,
		Digest: configDigest,
	}
	if err := client.PutBlob(ctx, configReference, configDigest, value); err != nil {
		return BackendResult{}, fmt.Errorf("persist mirror receipt payload: %w", err)
	}
	const receiptMediaType = "application/vnd.wolf.scanner-mirror-receipt.v1+json"
	const manifestMediaType = "application/vnd.oci.image.manifest.v1+json"
	manifestValue, err := json.Marshal(struct {
		SchemaVersion int                          `json:"schemaVersion"`
		MediaType     string                       `json:"mediaType"`
		ArtifactType  string                       `json:"artifactType"`
		Config        scannerregistry.Descriptor   `json:"config"`
		Layers        []scannerregistry.Descriptor `json:"layers"`
	}{
		SchemaVersion: 2, MediaType: manifestMediaType, ArtifactType: receiptMediaType,
		Config: scannerregistry.Descriptor{
			MediaType: receiptMediaType, Digest: configDigest, Size: int64(len(value)),
		},
		Layers: []scannerregistry.Descriptor{},
	})
	if err != nil {
		return BackendResult{}, err
	}
	manifestSum := sha256.Sum256(manifestValue)
	digest := "sha256:" + hex.EncodeToString(manifestSum[:])
	receiptReference := scannerregistry.Reference{
		Registry: execution.Mirror.Host, Repository: receiptRepository, Digest: digest,
	}
	if err := client.PutManifest(ctx, receiptReference, manifestMediaType, manifestValue); err != nil {
		return BackendResult{}, fmt.Errorf("persist mirror receipt manifest: %w", err)
	}
	if err := client.EnsureManifestAlias(ctx, receiptReference, operationAlias(invocation.OperationID)); err != nil {
		return BackendResult{}, err
	}
	readback, err := client.FetchManifest(ctx, receiptReference)
	if err != nil || readback.Digest != digest || !strings.EqualFold(readback.MediaType, manifestMediaType) {
		return BackendResult{}, errors.New("mirror receipt exact-digest readback failed")
	}
	return BackendResult{
		Binding: invocation.Binding, ExternalOperationID: invocation.OperationID,
		Result: scannerreleaseworkerResult(
			"oci://"+receiptReference.String(),
			digest, map[string]any{
				"images": results, "read_back_verified": true,
				"receipt_payload_digest": configDigest,
			},
		),
	}, nil
}

func requiredImageReferrerEvidence(
	invocation Invocation, imageKey string,
) (map[string]string, error) {
	return requiredImageEvidence(invocation, imageKey, "sbom", "provenance", "signature")
}

func requiredImageEvidence(
	invocation Invocation, imageKey string, kinds ...string,
) (map[string]string, error) {
	expected := make(map[string]string, len(kinds))
	for _, kind := range kinds {
		if kind != "sbom" && kind != "provenance" && kind != "signature" {
			return nil, fmt.Errorf("unsupported image evidence kind %q", kind)
		}
		step := kind + "/" + imageKey
		result, err := readStepResult(invocation, step)
		if err != nil {
			return nil, err
		}
		if !digestPattern.MatchString(result.OutputDigest) {
			return nil, fmt.Errorf("%s evidence for image %q has no immutable digest", kind, imageKey)
		}
		if kind != "signature" {
			expected[kind] = result.OutputDigest
			continue
		}
		signatureEvidence, err := signingClosureEvidence(result, "signature")
		if err != nil {
			return nil, fmt.Errorf("signature evidence for image %q is incomplete: %w", imageKey, err)
		}
		for evidenceKind, digest := range signatureEvidence {
			expected[evidenceKind] = digest
		}
	}
	return expected, nil
}

func signingClosureEvidence(
	result scannerreleaseworker.StepResult, prefix string,
) (map[string]string, error) {
	raw, ok := result.Summary["signing_evidence"]
	if !ok {
		return nil, errors.New("signing evidence is missing")
	}
	evidence, err := strictJSONRoundTrip[scannersigning.Evidence](raw)
	if err != nil {
		return nil, fmt.Errorf("decode signing evidence: %w", err)
	}
	if evidence.SchemaVersion != "wolf.scanner-signing-evidence/v1" ||
		!evidence.Verified ||
		!digestPattern.MatchString(evidence.SignatureDigest) ||
		!digestPattern.MatchString(evidence.SignatureArtifactDigest) ||
		evidence.SignatureArtifactDigest != result.OutputDigest ||
		!digestPattern.MatchString(evidence.ArtifactSubjectDigest) {
		return nil, errors.New("signing evidence durable artifact binding is invalid")
	}
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return nil, errors.New("signing evidence prefix is empty")
	}
	expected := map[string]string{
		prefix + "_signature":          evidence.SignatureDigest,
		prefix + "_signature_artifact": evidence.SignatureArtifactDigest,
	}
	if evidence.CertificateDigest != "" {
		if !digestPattern.MatchString(evidence.CertificateDigest) {
			return nil, errors.New("signing evidence certificate digest is invalid")
		}
		expected[prefix+"_certificate"] = evidence.CertificateDigest
	}
	return expected, nil
}

func requireOCIReferrerEvidence(
	ctx context.Context,
	client scannerregistry.Client,
	reference scannerregistry.Reference,
	expected map[string]string,
	location string,
) error {
	verified, err := client.ReadEvidence(ctx, reference, expected)
	if err != nil {
		return fmt.Errorf("read back %s OCI referrer evidence: %w", location, err)
	}
	for kind := range expected {
		if !verified[kind] {
			return fmt.Errorf("%s OCI referrer evidence %q failed exact-digest readback", location, kind)
		}
	}
	return nil
}

func imageSnapshot(platformsJSON, imageKey string) (scannerpipeline.Image, error) {
	var images []scannerpipeline.Image
	decoder := json.NewDecoder(strings.NewReader(platformsJSON))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&images); err != nil {
		return scannerpipeline.Image{}, fmt.Errorf("decode image inventory: %w", err)
	}
	for _, image := range images {
		if image.Key == imageKey {
			if len(image.Platforms) == 0 {
				return scannerpipeline.Image{}, fmt.Errorf("image %q has no platforms", imageKey)
			}
			return image, nil
		}
	}
	return scannerpipeline.Image{}, fmt.Errorf("image %q is absent from immutable build inventory", imageKey)
}

func parseOCIPlatform(value string) (scannerregistry.Platform, error) {
	parts := strings.Split(value, "/")
	if len(parts) < 2 || len(parts) > 3 || parts[0] == "" || parts[1] == "" {
		return scannerregistry.Platform{}, fmt.Errorf("invalid OCI platform %q", value)
	}
	platform := scannerregistry.Platform{OS: parts[0], Architecture: parts[1]}
	if len(parts) == 3 {
		platform.Variant = parts[2]
	}
	return platform, nil
}

func readStepResult(invocation Invocation, stepKey string) (scannerreleaseworker.StepResult, error) {
	binding := scannerreleaseworkspace.NewBinding(
		invocation.Request.BuildRunID, invocation.Request.CandidateID,
		invocation.Request.BuildAttempt, invocation.Binding.DefinitionCommit,
		invocation.Binding.LockDigest, invocation.Binding.PolicyID,
		invocation.Binding.PolicyRevision,
	)
	evidence, err := scannerreleaseworkspace.ReadEvidence(
		invocation.Request.Workspace, stepKey, binding,
	)
	if err != nil {
		return scannerreleaseworker.StepResult{}, fmt.Errorf("read transitive evidence %q: %w", stepKey, err)
	}
	var result scannerreleaseworker.StepResult
	if err := evidence.DecodeResult(&result); err != nil {
		return scannerreleaseworker.StepResult{}, err
	}
	return result, nil
}

func immutableReference(uri, expectedDigest string) (scannerregistry.Reference, error) {
	parsed, err := url.Parse(uri)
	if err != nil || parsed.Scheme != "oci" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		return scannerregistry.Reference{}, errors.New("release evidence URI is not an immutable OCI reference")
	}
	reference, err := scannerregistry.ParseReference(parsed.Host + parsed.EscapedPath())
	if err != nil {
		// EscapedPath is intentionally tried first to preserve URL validation;
		// ordinary OCI repositories contain no characters requiring escaping.
		reference, err = scannerregistry.ParseReference(parsed.Host + parsed.Path)
	}
	if err != nil || reference.Digest != expectedDigest {
		return scannerregistry.Reference{}, errors.New("release evidence OCI digest is invalid")
	}
	return reference, nil
}

func imageRepository(prefix, image string) (string, error) {
	name := map[string]string{
		"default": "wolf-scanners", "jvm": "wolf-scanners-jvm",
		"rust": "wolf-scanners-rust", "codeql": "wolf-scanners-codeql",
		"fixer-base": "wolf-fixer", "fixer-api": "wolf-fixer-api",
		"fixer-claude": "wolf-fixer-claude", "fixer-codex": "wolf-fixer-codex",
	}[image]
	if name == "" {
		return "", fmt.Errorf("unsupported owned release image %q", image)
	}
	repository := path.Join(strings.Trim(prefix, "/"), name)
	if repository == "." || strings.HasPrefix(repository, "../") {
		return "", errors.New("registry repository prefix is invalid")
	}
	return repository, nil
}

func operationAlias(operationID string) string {
	return "wolf-operation-" + strings.TrimPrefix(operationID, "sha256:")
}

func childOperationID(parent, component string) string {
	sum := sha256.Sum256([]byte(parent + "\x00" + component))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func writeSigningDescriptor(workspace, action string, artifact scannersigning.Artifact) error {
	if !filepath.IsAbs(workspace) || artifact.URI == "" ||
		!digestPattern.MatchString(artifact.Digest) || artifact.MediaType == "" {
		return errors.New("signing artifact descriptor is incomplete")
	}
	document := struct {
		SchemaVersion string                  `json:"schema_version"`
		Artifact      scannersigning.Artifact `json:"artifact"`
	}{"wolf.scanner-signing-artifact/v1", artifact}
	value, err := json.Marshal(document)
	if err != nil {
		return err
	}
	directory := filepath.Join(workspace, ".wolf-signing", "requests")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	name := strings.ReplaceAll(action, "/", "--") + ".json"
	temporary, err := os.CreateTemp(directory, ".signing-artifact-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(value); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, filepath.Join(directory, name))
}

// WriteSigningDescriptor persists the exact immutable artifact identity that
// a later isolated signing lane is permitted to sign. First-party adapters use
// this for domain artifacts whose storage manifest is the signed subject.
func WriteSigningDescriptor(workspace, action string, artifact scannersigning.Artifact) error {
	return writeSigningDescriptor(workspace, action, artifact)
}
