package scannerreleasebackend

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/scannerregistry"
	scannerlock "github.com/alphabravocompany/thewolf/internal/scannertools/lock"
)

const (
	wolfImageSource     = "https://github.com/alphabravocompany/thewolf"
	wolfBuildxBuilderID = "https://github.com/alphabravocompany/thewolf/builders/scanner-release-buildkit/v1"

	annotationSource    = "org.opencontainers.image.source"
	annotationRevision  = "org.opencontainers.image.revision"
	annotationVersion   = "org.opencontainers.image.version"
	annotationCandidate = "dev.wolf.release.id"
	annotationImageKind = "dev.wolf.release.image-kind"
	annotationVariant   = "dev.wolf.release.variant"
)

type imageTrustBinding struct {
	Source           string
	DefinitionCommit string
	LockDigest       string
	CandidateID      string
	ImageKind        string
	Variant          string
}

func resolveImageTrustBinding(invocation Invocation, imageKey string) (imageTrustBinding, error) {
	image, err := imageSnapshot(invocation.Request.PlatformsJSON, imageKey)
	if err != nil {
		return imageTrustBinding{}, err
	}
	lock, err := scannerlock.LoadFile(filepath.Join(
		invocation.Request.Workspace, scannerlock.DefaultLockPath,
	))
	if err != nil {
		return imageTrustBinding{}, fmt.Errorf("load image trust lock: %w", err)
	}
	if lock.LockDigest != invocation.Binding.LockDigest ||
		lock.LockDigest != invocation.Request.LockDigest {
		return imageTrustBinding{}, fmt.Errorf("%w: image trust lock does not match invocation", ErrBinding)
	}
	selection, err := resolveBuildSelection(lock, imageKey)
	if err != nil {
		return imageTrustBinding{}, err
	}
	if selection.Kind != string(image.Kind) {
		return imageTrustBinding{}, fmt.Errorf(
			"%w: image %q kind %q does not match locked kind %q",
			ErrBinding, imageKey, image.Kind, selection.Kind,
		)
	}
	if strings.TrimSpace(invocation.Request.CandidateID) == "" ||
		strings.TrimSpace(invocation.Request.DefinitionCommit) == "" {
		return imageTrustBinding{}, fmt.Errorf("%w: image trust invocation is incomplete", ErrBinding)
	}
	return imageTrustBinding{
		Source: wolfImageSource, DefinitionCommit: invocation.Request.DefinitionCommit,
		LockDigest: lock.LockDigest, CandidateID: invocation.Request.CandidateID,
		ImageKind: selection.Kind, Variant: image.Key,
	}, nil
}

func requiredOCIAnnotations(binding imageTrustBinding) map[string]string {
	return map[string]string{
		annotationSource: binding.Source, annotationRevision: binding.DefinitionCommit,
		annotationVersion: binding.CandidateID, annotationCandidate: binding.CandidateID,
		annotationImageKind: binding.ImageKind, annotationVariant: binding.Variant,
	}
}

type adapterImageManifestEvidence struct {
	Digest        string `json:"digest"`
	MediaType     string `json:"media_type"`
	PayloadBase64 string `json:"payload_base64"`
}

const maxAnnotationManifestBytes = 16 << 20

func validateAnnotatedOCIManifest(
	evidence adapterImageManifestEvidence,
	subjectDigest string,
	binding imageTrustBinding,
) error {
	if evidence.Digest != subjectDigest || !digestPattern.MatchString(subjectDigest) {
		return errors.New("OCI annotation evidence is not bound to the exact image manifest digest")
	}
	payload, err := base64.StdEncoding.Strict().DecodeString(evidence.PayloadBase64)
	if err != nil || len(payload) == 0 || len(payload) > maxAnnotationManifestBytes {
		return errors.New("OCI annotation evidence has no bounded exact manifest payload")
	}
	sum := sha256.Sum256(payload)
	if "sha256:"+hex.EncodeToString(sum[:]) != subjectDigest {
		return errors.New("OCI annotation evidence payload does not match the image manifest digest")
	}
	annotations, err := decodeOCIAnnotations(payload, evidence.MediaType)
	if err != nil {
		return err
	}
	for key, expected := range requiredOCIAnnotations(binding) {
		if annotations[key] != expected {
			return fmt.Errorf("OCI image annotation %q is not bound to the release invocation", key)
		}
	}
	return nil
}

type strictOCIDescriptor struct {
	MediaType    string                    `json:"mediaType"`
	Digest       string                    `json:"digest"`
	Size         int64                     `json:"size"`
	URLs         []string                  `json:"urls,omitempty"`
	Annotations  map[string]string         `json:"annotations,omitempty"`
	Data         string                    `json:"data,omitempty"`
	Platform     *scannerregistry.Platform `json:"platform,omitempty"`
	ArtifactType string                    `json:"artifactType,omitempty"`
}

func decodeOCIAnnotations(payload []byte, declaredMediaType string) (map[string]string, error) {
	decode := func(target any) error {
		decoder := json.NewDecoder(bytes.NewReader(payload))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(target); err != nil {
			return err
		}
		return ensureEOF(decoder)
	}
	switch declaredMediaType {
	case "application/vnd.oci.image.index.v1+json":
		var document struct {
			SchemaVersion int                   `json:"schemaVersion"`
			MediaType     string                `json:"mediaType"`
			ArtifactType  string                `json:"artifactType,omitempty"`
			Manifests     []strictOCIDescriptor `json:"manifests"`
			Subject       *strictOCIDescriptor  `json:"subject,omitempty"`
			Annotations   map[string]string     `json:"annotations,omitempty"`
		}
		if err := decode(&document); err != nil {
			return nil, fmt.Errorf("strictly decode OCI image index annotations: %w", err)
		}
		if document.SchemaVersion != 2 || document.MediaType != declaredMediaType ||
			len(document.Manifests) == 0 {
			return nil, errors.New("OCI annotation payload is not a valid image index")
		}
		return document.Annotations, nil
	case "application/vnd.oci.image.manifest.v1+json":
		var document struct {
			SchemaVersion int                   `json:"schemaVersion"`
			MediaType     string                `json:"mediaType"`
			ArtifactType  string                `json:"artifactType,omitempty"`
			Config        strictOCIDescriptor   `json:"config"`
			Layers        []strictOCIDescriptor `json:"layers"`
			Subject       *strictOCIDescriptor  `json:"subject,omitempty"`
			Annotations   map[string]string     `json:"annotations,omitempty"`
		}
		if err := decode(&document); err != nil {
			return nil, fmt.Errorf("strictly decode OCI image manifest annotations: %w", err)
		}
		if document.SchemaVersion != 2 || document.MediaType != declaredMediaType ||
			!digestPattern.MatchString(document.Config.Digest) || document.Config.Size <= 0 {
			return nil, errors.New("OCI annotation payload is not a valid image manifest")
		}
		return document.Annotations, nil
	default:
		return nil, errors.New("OCI annotation evidence media type is not an image manifest or index")
	}
}

type buildxAttestationEvidence struct {
	ManifestDigest string `json:"manifest_digest"`
	PayloadDigest  string `json:"payload_digest"`
	PredicateType  string `json:"predicate_type"`
	SubjectDigest  string `json:"subject_digest"`
	BuilderID      string `json:"builder_id,omitempty"`
}

type inTotoStatement struct {
	Type          string          `json:"_type"`
	Subject       []inTotoSubject `json:"subject"`
	PredicateType string          `json:"predicateType"`
	Predicate     json.RawMessage `json:"predicate"`
}

type inTotoSubject struct {
	Name   string            `json:"name"`
	Digest map[string]string `json:"digest"`
}

type slsaMaterial struct {
	URI    string            `json:"uri"`
	Digest map[string]string `json:"digest"`
}

type provenanceExpectation struct {
	imageTrustBinding
	Platform string
}

func inspectInTotoPayload(
	payload []byte,
	runnableDigest string,
	expectation provenanceExpectation,
) (string, string, error) {
	var statement inTotoStatement
	decoder := json.NewDecoder(bytes.NewReader(payload))
	if err := decoder.Decode(&statement); err != nil || ensureEOF(decoder) != nil {
		return "", "", errors.New("decode exact in-toto attestation statement")
	}
	if statement.Type != "https://in-toto.io/Statement/v0.1" &&
		statement.Type != "https://in-toto.io/Statement/v1" {
		return "", "", errors.New("Buildx attestation has an untrusted in-toto statement type")
	}
	if len(statement.Subject) != 1 || strings.TrimSpace(statement.Subject[0].Name) == "" ||
		statement.Subject[0].Digest["sha256"] != strings.TrimPrefix(runnableDigest, "sha256:") {
		return "", "", errors.New("Buildx attestation statement is not bound to the runnable manifest")
	}
	switch statement.PredicateType {
	case "https://spdx.dev/Document", "https://cyclonedx.org/bom":
		return "sbom", "", nil
	case "https://slsa.dev/provenance/v0.2":
		builderID, err := validateSLSA02Predicate(statement.Predicate, expectation)
		return "provenance", builderID, err
	case "https://slsa.dev/provenance/v1":
		builderID, err := validateSLSA1Predicate(statement.Predicate, expectation)
		return "provenance", builderID, err
	default:
		return "", "", errors.New("Buildx attestation predicate type is not trusted")
	}
}

func validateSLSA02Predicate(raw json.RawMessage, expected provenanceExpectation) (string, error) {
	var predicate struct {
		Builder struct {
			ID string `json:"id"`
		} `json:"builder"`
		Invocation struct {
			Parameters json.RawMessage `json:"parameters"`
		} `json:"invocation"`
		BuildType string         `json:"buildType"`
		Materials []slsaMaterial `json:"materials"`
	}
	if err := json.Unmarshal(raw, &predicate); err != nil {
		return "", errors.New("decode SLSA v0.2 provenance predicate")
	}
	if predicate.BuildType != "https://mobyproject.org/buildkit@v1" {
		return "", errors.New("SLSA v0.2 provenance build type is not trusted")
	}
	if err := validateProvenanceBindings(
		predicate.Builder.ID, predicate.Invocation.Parameters, predicate.Materials, expected,
	); err != nil {
		return "", err
	}
	return predicate.Builder.ID, nil
}

func validateSLSA1Predicate(raw json.RawMessage, expected provenanceExpectation) (string, error) {
	var predicate struct {
		BuildDefinition struct {
			BuildType          string `json:"buildType"`
			ExternalParameters struct {
				Request json.RawMessage `json:"request"`
			} `json:"externalParameters"`
			ResolvedDependencies []slsaMaterial `json:"resolvedDependencies"`
		} `json:"buildDefinition"`
		RunDetails struct {
			Builder struct {
				ID string `json:"id"`
			} `json:"builder"`
		} `json:"runDetails"`
	}
	if err := json.Unmarshal(raw, &predicate); err != nil {
		return "", errors.New("decode SLSA v1 provenance predicate")
	}
	if predicate.BuildDefinition.BuildType !=
		"https://github.com/moby/buildkit/blob/master/docs/attestations/slsa-definitions.md" {
		return "", errors.New("SLSA v1 provenance build type is not trusted")
	}
	if err := validateProvenanceBindings(
		predicate.RunDetails.Builder.ID,
		predicate.BuildDefinition.ExternalParameters.Request,
		predicate.BuildDefinition.ResolvedDependencies,
		expected,
	); err != nil {
		return "", err
	}
	return predicate.RunDetails.Builder.ID, nil
}

func validateProvenanceBindings(
	builderID string,
	parameters json.RawMessage,
	materials []slsaMaterial,
	expected provenanceExpectation,
) error {
	if builderID != wolfBuildxBuilderID {
		return errors.New("Buildx provenance builder identity is not trusted")
	}
	buildArgs, err := provenanceBuildArgs(parameters)
	if err != nil {
		return err
	}
	required := map[string]string{
		"WOLF_DEFINITION_COMMIT": expected.DefinitionCommit,
		"WOLF_LOCK_DIGEST":       expected.LockDigest,
		"WOLF_CANDIDATE_ID":      expected.CandidateID,
		"WOLF_IMAGE_KIND":        expected.ImageKind,
		"WOLF_IMAGE_VARIANT":     expected.Variant,
		"WOLF_BUILD_PLATFORM":    expected.Platform,
	}
	for name, value := range required {
		if buildArgs[name] != value {
			return fmt.Errorf("Buildx provenance invocation is not bound to %s", name)
		}
	}
	commitBound := false
	for _, material := range materials {
		if trustedSourceMaterialURI(material.URI, expected.Source) &&
			(material.Digest["gitCommit"] == expected.DefinitionCommit ||
				material.Digest["sha1"] == expected.DefinitionCommit) {
			commitBound = true
		}
		if !validSLSAMaterialDigest(material.Digest) {
			return errors.New("Buildx provenance contains a material without an immutable digest")
		}
	}
	if !commitBound {
		return errors.New("Buildx provenance materials do not bind the definition commit")
	}
	return nil
}

func trustedSourceMaterialURI(value, source string) bool {
	value = strings.TrimPrefix(value, "git+")
	base, _, _ := strings.Cut(value, "#")
	return strings.TrimSuffix(base, ".git") == strings.TrimSuffix(source, ".git")
}

func validSLSAMaterialDigest(value map[string]string) bool {
	for algorithm, digest := range value {
		switch algorithm {
		case "sha256":
			if len(digest) == 64 {
				if _, err := hex.DecodeString(digest); err == nil {
					return true
				}
			}
		case "sha1", "gitCommit":
			if len(digest) == 40 || len(digest) == 64 {
				if _, err := hex.DecodeString(digest); err == nil {
					return true
				}
			}
		}
	}
	return false
}

func provenanceBuildArgs(raw json.RawMessage) (map[string]string, error) {
	var parameters struct {
		Args      map[string]string `json:"args"`
		BuildArgs map[string]string `json:"buildArgs"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &parameters) != nil {
		return nil, errors.New("Buildx provenance has no decodable invocation parameters")
	}
	result := make(map[string]string, len(parameters.Args)+len(parameters.BuildArgs))
	for name, value := range parameters.BuildArgs {
		result[name] = value
	}
	for name, value := range parameters.Args {
		name = strings.TrimPrefix(name, "build-arg:")
		result[name] = value
	}
	return result, nil
}
