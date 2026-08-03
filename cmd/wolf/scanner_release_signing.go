package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
	"github.com/alphabravocompany/thewolf/internal/scannerreleasebackend"
	"github.com/alphabravocompany/thewolf/internal/scannerreleaseworker"
	"github.com/alphabravocompany/thewolf/internal/scannerreleaseworkspace"
	"github.com/alphabravocompany/thewolf/internal/scannersigning"
)

const signingArtifactSchema = "wolf.scanner-signing-artifact/v1"

type signingArtifactDocument struct {
	SchemaVersion string                  `json:"schema_version"`
	Artifact      scannersigning.Artifact `json:"artifact"`
}

func executeConfiguredSigningStep(
	ctx context.Context,
	invocation scannerreleasebackend.Invocation,
) (scannerreleasebackend.BackendResult, error) {
	profilePath := strings.TrimSpace(os.Getenv("WOLF_SCANNER_SIGNER_PROFILE_FILE"))
	adapterPath := strings.TrimSpace(os.Getenv("WOLF_SCANNER_SIGNER_ADAPTER"))
	if profilePath == "" || adapterPath == "" {
		return scannerreleasebackend.BackendResult{}, errors.New(
			"WOLF_SCANNER_SIGNER_PROFILE_FILE and WOLF_SCANNER_SIGNER_ADAPTER are required for signature steps",
		)
	}
	if !filepath.IsAbs(profilePath) || !filepath.IsAbs(adapterPath) {
		return scannerreleasebackend.BackendResult{}, errors.New(
			"signer profile and adapter paths must be absolute",
		)
	}
	profile, err := scannersigning.ReadProfileFile(profilePath)
	if err != nil {
		return scannerreleasebackend.BackendResult{}, err
	}
	artifact, err := readOrDeriveSigningArtifact(invocation)
	if err != nil {
		return scannerreleasebackend.BackendResult{}, err
	}
	environment, err := selectedEnvironment([]string{
		"PATH", "SSL_CERT_FILE", "SSL_CERT_DIR",
		"AWS_REGION", "AWS_DEFAULT_REGION", "AWS_ROLE_ARN",
		"AWS_WEB_IDENTITY_TOKEN_FILE", "AWS_CONTAINER_CREDENTIALS_FULL_URI",
		"GOOGLE_APPLICATION_CREDENTIALS", "GOOGLE_CLOUD_PROJECT",
		"AZURE_CLIENT_ID", "AZURE_TENANT_ID", "AZURE_FEDERATED_TOKEN_FILE",
		"PKCS11_MODULE_PATH", "PKCS11_CONFIG",
		"SIGSTORE_ID_TOKEN_FILE", "SIGSTORE_FULCIO_URL", "SIGSTORE_REKOR_URL",
	})
	if err != nil {
		return scannerreleasebackend.BackendResult{}, err
	}
	journal := strings.TrimSpace(os.Getenv("WOLF_SCANNER_SIGNER_JOURNAL"))
	if journal == "" {
		journal = filepath.Join(
			invocation.Request.Workspace, ".wolf-signing", "journal",
		)
	}
	return executeSignerInvocation(
		ctx, invocation, profile, artifact,
		scannersigning.CommandAdapter{
			Path: adapterPath, Environment: environment,
		},
		journal,
	)
}

// readOrDeriveSigningArtifact treats the durable step ledger as authoritative.
// The descriptor file is only a same-workspace optimization: a reclaimed
// worker always receives a fresh workspace, so completed publish/manifest
// evidence must be sufficient to reconstruct the exact signing request.
func readOrDeriveSigningArtifact(
	invocation scannerreleasebackend.Invocation,
) (scannersigning.Artifact, error) {
	dependency := ""
	mediaType := ""
	switch {
	case strings.HasPrefix(invocation.Action.Name, "signature/"):
		dependency = "candidate-publish/" + strings.TrimPrefix(invocation.Action.Name, "signature/")
		mediaType = "application/vnd.oci.image.index.v1+json"
	case invocation.Action.Name == "release-manifest-signature":
		dependency = "release-manifest"
		mediaType = "application/vnd.wolf.scanner-release-manifest.v1+json"
	default:
		return scannersigning.Artifact{}, fmt.Errorf(
			"%w: action %q is not a signing step",
			scannerreleasebackend.ErrUnsupportedStep, invocation.Action.Name,
		)
	}
	binding := scannerreleaseworkspace.NewBinding(
		invocation.Request.BuildRunID, invocation.Request.CandidateID,
		invocation.Request.BuildAttempt, invocation.Binding.DefinitionCommit,
		invocation.Binding.LockDigest, invocation.Binding.PolicyID,
		invocation.Binding.PolicyRevision,
	)
	evidence, readErr := scannerreleaseworkspace.ReadEvidence(
		invocation.Request.Workspace, dependency, binding,
	)
	if readErr != nil {
		return scannersigning.Artifact{}, fmt.Errorf(
			"derive signing artifact from durable evidence %q: %w", dependency, readErr,
		)
	}
	var result scannerreleaseworker.StepResult
	if decodeErr := evidence.DecodeResult(&result); decodeErr != nil {
		return scannersigning.Artifact{}, decodeErr
	}
	if value, ok := result.Summary["media_type"].(string); ok && strings.TrimSpace(value) != "" {
		mediaType = strings.TrimSpace(value)
	}
	artifact := scannersigning.Artifact{
		URI: result.OutputURI, Digest: result.OutputDigest, MediaType: mediaType,
	}
	if artifact.URI == "" || !scannersigning.ValidDigest(artifact.Digest) {
		return scannersigning.Artifact{}, errors.New("durable signing artifact evidence is incomplete")
	}
	// Descriptor files are a same-workspace optimization only. A step Job can
	// never select its own signing subject: durable dependency evidence is
	// authoritative, and any existing descriptor must match it exactly.
	descriptor, descriptorErr := readSigningArtifact(
		invocation.Request.Workspace, invocation.Action.Name,
	)
	if descriptorErr == nil {
		if descriptor != artifact {
			return scannersigning.Artifact{}, errors.New(
				"signing artifact descriptor does not match durable dependency evidence",
			)
		}
	} else if !errors.Is(descriptorErr, os.ErrNotExist) {
		return scannersigning.Artifact{}, descriptorErr
	}
	return artifact, nil
}

func executeSignerInvocation(
	ctx context.Context,
	invocation scannerreleasebackend.Invocation,
	profile scannerrelease.SignerProfile,
	artifact scannersigning.Artifact,
	adapter scannersigning.Adapter,
	journal string,
) (scannerreleasebackend.BackendResult, error) {
	if err := scannerreleasebackend.ValidateInvocation(invocation); err != nil {
		return scannerreleasebackend.BackendResult{}, err
	}
	if !scannerreleasebackend.RequiresSigning(invocation.Action.Name) {
		return scannerreleasebackend.BackendResult{}, fmt.Errorf(
			"%w: action %q is not a signing step",
			scannerreleasebackend.ErrUnsupportedStep, invocation.Action.Name,
		)
	}
	evidence, result, err := (scannersigning.Service{
		Adapter: adapter, JournalRoot: journal, RequireDurableArtifact: true,
	}).Sign(
		ctx, profile, artifact, scannersigning.Binding{
			DefinitionCommit: invocation.Binding.DefinitionCommit,
			LockDigest:       invocation.Binding.LockDigest,
			PolicyID:         invocation.Binding.PolicyID,
			PolicyRevision:   invocation.Binding.PolicyRevision,
		}, invocation.OperationID,
	)
	if err != nil {
		return scannerreleasebackend.BackendResult{}, err
	}
	evidenceValue, err := json.Marshal(evidence)
	if err != nil {
		return scannerreleasebackend.BackendResult{}, err
	}
	var evidenceObject map[string]any
	if err := json.Unmarshal(evidenceValue, &evidenceObject); err != nil {
		return scannerreleasebackend.BackendResult{}, err
	}
	return scannerreleasebackend.BackendResult{
		Binding:             invocation.Binding,
		ExternalOperationID: invocation.OperationID,
		Result: scannerreleaseworker.StepResult{
			OutputURI: result.SignatureURI,
			// The durable step output is the exact OCI/object signature artifact.
			// Raw signature bytes remain separately bound in signing_evidence.
			OutputDigest: evidence.SignatureArtifactDigest,
			Summary: map[string]any{
				"signing_evidence": evidenceObject,
				"adapter_evidence": map[string]any{
					"output_identity": "storage",
					"artifact": map[string]any{
						"uri": result.SignatureURI, "digest": evidence.SignatureArtifactDigest,
						"payload_digest":     evidence.SignatureDigest,
						"storage_media_type": evidence.SignatureMediaType,
						"storage_size_bytes": evidence.SignatureArtifactSize,
					},
				},
			},
		},
	}, nil
}

func readSigningArtifact(workspace, action string) (scannersigning.Artifact, error) {
	if !filepath.IsAbs(workspace) {
		return scannersigning.Artifact{}, errors.New("signing workspace must be absolute")
	}
	name := strings.ReplaceAll(action, "/", "--") + ".json"
	path := filepath.Join(workspace, ".wolf-signing", "requests", name)
	file, err := os.Open(path)
	if err != nil {
		return scannersigning.Artifact{}, fmt.Errorf(
			"open signing artifact descriptor %s: %w", path, err,
		)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return scannersigning.Artifact{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > scannerReleaseStepInputLimit {
		return scannersigning.Artifact{}, errors.New(
			"signing artifact descriptor must be a bounded regular file",
		)
	}
	value, err := io.ReadAll(io.LimitReader(file, scannerReleaseStepInputLimit+1))
	if err != nil {
		return scannersigning.Artifact{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	var document signingArtifactDocument
	if err := decoder.Decode(&document); err != nil {
		return scannersigning.Artifact{}, fmt.Errorf(
			"decode signing artifact descriptor: %w", err,
		)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return scannersigning.Artifact{}, errors.New(
			"signing artifact descriptor has trailing JSON",
		)
	}
	if document.SchemaVersion != signingArtifactSchema {
		return scannersigning.Artifact{}, fmt.Errorf(
			"unsupported signing artifact schema %q", document.SchemaVersion,
		)
	}
	return document.Artifact, nil
}
