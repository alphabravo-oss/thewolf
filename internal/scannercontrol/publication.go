package scannercontrol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/scannerpipeline"
	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

var ErrPublicationEvidence = errors.New("verified scanner publication evidence is unavailable")

// VerifiedPublication is authoritative server-side evidence recovered from a
// completed release build. Digest is the approval/publication receipt identity.
type VerifiedPublication struct {
	Receipt scannerrelease.PublicationReceipt
	Digest  string
}

// PublicationVerifier prevents HTTP and CLI callers from becoming an
// authority for manifests, signer identities, or release inventory.
type PublicationVerifier interface {
	VerifyPublication(context.Context, *scannerrelease.Candidate, string) (*VerifiedPublication, error)
}

// DurablePublicationVerifier verifies the final receipt and the full durable
// build DAG directly from the control-plane database.
type DurablePublicationVerifier struct {
	Store scannerrelease.Persistence
}

func (v DurablePublicationVerifier) VerifyPublication(
	ctx context.Context,
	candidate *scannerrelease.Candidate,
	expectedDigest string,
) (*VerifiedPublication, error) {
	if v.Store == nil || candidate == nil {
		return nil, fmt.Errorf("%w: persistence and candidate are required", ErrPublicationEvidence)
	}
	if !digestPattern.MatchString(expectedDigest) {
		return nil, fmt.Errorf("%w: a valid receipt digest is required", ErrPublicationEvidence)
	}
	builds, err := v.Store.ListBuildRuns(ctx, candidate.ID)
	if err != nil {
		return nil, fmt.Errorf("%w: list candidate builds: %v", ErrPublicationEvidence, err)
	}
	sort.Slice(builds, func(i, j int) bool { return builds[i].Attempt > builds[j].Attempt })
	for index := range builds {
		build := &builds[index]
		if build.State != scannerrelease.BuildCompleted {
			continue
		}
		verified, verifyErr := v.verifyBuild(ctx, candidate, build, expectedDigest)
		if verifyErr == nil {
			return verified, nil
		}
	}
	return nil, fmt.Errorf(
		"%w: no completed build contains receipt %s",
		ErrPublicationEvidence, expectedDigest,
	)
}

func (v DurablePublicationVerifier) verifyBuild(
	ctx context.Context,
	candidate *scannerrelease.Candidate,
	build *scannerrelease.BuildRun,
	expectedDigest string,
) (*VerifiedPublication, error) {
	steps, err := v.Store.ListBuildSteps(ctx, build.ID)
	if err != nil {
		return nil, err
	}
	expected, err := expectedPublicationPlan(build)
	if err != nil {
		return nil, err
	}
	latest := make(map[string]scannerrelease.BuildStep)
	for _, step := range steps {
		current, exists := latest[step.StepKey]
		if !exists || step.Attempt > current.Attempt {
			latest[step.StepKey] = step
		}
	}
	if len(latest) != len(expected.Steps) {
		return nil, fmt.Errorf(
			"durable build plan has %d logical steps, expected %d",
			len(latest), len(expected.Steps),
		)
	}
	for _, planStep := range expected.Steps {
		step, exists := latest[planStep.Key]
		if !exists {
			return nil, fmt.Errorf("required build step %q is absent", planStep.Key)
		}
		if step.State != scannerrelease.BuildCompleted {
			return nil, fmt.Errorf("required build step %q is %s", planStep.Key, step.State)
		}
		if !digestPattern.MatchString(step.OutputDigest) {
			return nil, fmt.Errorf("required build step %q has no valid evidence digest", planStep.Key)
		}
		if err := verifyPersistedStepEvidence(step, candidate); err != nil {
			return nil, fmt.Errorf("required build step %q: %w", planStep.Key, err)
		}
	}
	final, exists := latest["candidate-evidence-summary"]
	if !exists || final.OutputDigest != expectedDigest {
		return nil, errors.New("final evidence step does not match requested receipt")
	}
	var payload struct {
		Evidence struct {
			Summary struct {
				PublicationReceipt scannerrelease.PublicationReceipt `json:"publication_receipt"`
			} `json:"summary"`
		} `json:"evidence"`
	}
	if err := json.Unmarshal([]byte(final.SummaryJSON), &payload); err != nil {
		return nil, fmt.Errorf("decode final publication receipt: %w", err)
	}
	receipt := payload.Evidence.Summary.PublicationReceipt
	actualDigest, err := scannerrelease.PublicationReceiptDigest(receipt)
	if err != nil || actualDigest != expectedDigest {
		return nil, errors.New("final publication receipt digest is invalid")
	}
	switch {
	case receipt.SchemaVersion != scannerrelease.PublicationReceiptSchema:
		return nil, errors.New("unsupported publication receipt schema")
	case receipt.CandidateID != candidate.ID || receipt.BuildRunID != build.ID:
		return nil, errors.New("publication receipt is not bound to candidate build")
	case receipt.DefinitionCommit != scannerrelease.EffectiveDefinitionCommit(candidate):
		return nil, errors.New("publication receipt definition commit is stale")
	case receipt.LockDigest != candidate.LockDigest:
		return nil, errors.New("publication receipt lock digest is stale")
	case receipt.PolicyID != candidate.PolicyID || receipt.PolicyRevision != candidate.PolicyRevision:
		return nil, errors.New("publication receipt policy snapshot is stale")
	case receipt.PolicyDecisionDigest != candidate.PolicyDecision:
		return nil, errors.New("publication receipt policy decision is stale")
	}
	if err := validatePublication(candidate, "", receipt); err != nil {
		return nil, fmt.Errorf("publication receipt inventory is invalid: %w", err)
	}
	if err := verifyReceiptAgainstDurableEvidence(receipt, latest); err != nil {
		return nil, err
	}
	return &VerifiedPublication{Receipt: receipt, Digest: actualDigest}, nil
}

func expectedPublicationPlan(build *scannerrelease.BuildRun) (scannerpipeline.Plan, error) {
	if build == nil || strings.TrimSpace(build.PlatformsJSON) == "" {
		return scannerpipeline.Plan{}, errors.New("completed build has no immutable image/platform snapshot")
	}
	var images []scannerpipeline.Image
	decoder := json.NewDecoder(strings.NewReader(build.PlatformsJSON))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&images); err != nil {
		return scannerpipeline.Plan{}, fmt.Errorf("decode build image/platform snapshot: %w", err)
	}
	if err := ensureControlJSONEOF(decoder); err != nil {
		return scannerpipeline.Plan{}, err
	}
	if err := validateCompleteImageSet(images, defaultImages()); err != nil {
		return scannerpipeline.Plan{}, fmt.Errorf("build image/platform snapshot is incomplete: %w", err)
	}
	plan, err := scannerpipeline.Default(scannerpipeline.Inputs{
		Images: images, RequireCompose: true, RequireKubernetes: true, RequireMirror: true,
	})
	if err != nil {
		return scannerpipeline.Plan{}, fmt.Errorf("reconstruct required publication plan: %w", err)
	}
	return plan, nil
}

func ensureControlJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("build image/platform snapshot has trailing JSON")
		}
		return fmt.Errorf("decode trailing build image/platform snapshot: %w", err)
	}
	return nil
}

func verifyPersistedStepEvidence(
	step scannerrelease.BuildStep,
	candidate *scannerrelease.Candidate,
) error {
	var summary struct {
		Evidence struct {
			Summary      map[string]any                   `json:"summary"`
			Verification scannerreleaseworkerVerification `json:"verification"`
		} `json:"evidence"`
	}
	if err := json.Unmarshal([]byte(step.SummaryJSON), &summary); err != nil {
		return fmt.Errorf("decode durable evidence: %w", err)
	}
	if summary.Evidence.Summary == nil {
		return errors.New("durable evidence summary is absent")
	}
	verification := summary.Evidence.Verification
	if verification.DefinitionCommit != scannerrelease.EffectiveDefinitionCommit(candidate) ||
		verification.LockDigest != candidate.LockDigest ||
		verification.PolicyID != candidate.PolicyID ||
		verification.PolicyRevision != candidate.PolicyRevision {
		return errors.New("durable evidence immutable binding is absent or stale")
	}
	return nil
}

// scannerreleaseworkerVerification deliberately mirrors the persistence-safe
// JSON shape without importing the worker package into the control plane.
type scannerreleaseworkerVerification struct {
	DefinitionCommit     string `json:"definition_commit"`
	LockDigest           string `json:"lock_digest"`
	PolicyID             string `json:"policy_id"`
	PolicyRevision       int64  `json:"policy_revision"`
	PolicyDecisionDigest string `json:"policy_decision_digest"`
}

func verifyReceiptAgainstDurableEvidence(
	receipt scannerrelease.PublicationReceipt,
	steps map[string]scannerrelease.BuildStep,
) error {
	manifest := steps["release-manifest"]
	if receipt.ManifestDigest != manifest.OutputDigest ||
		receipt.ManifestURI != manifest.OutputURI {
		return errors.New("publication receipt manifest is not the durable release-manifest result")
	}
	mirrorSignatures, err := durableMirrorSignatures(steps["mirror-copy-verify"])
	if err != nil {
		return err
	}
	for _, image := range receipt.Images {
		if mirror, ok := mirrorSignatures[image.ImageKey]; ok &&
			mirror.RegistryTargetID == image.RegistryTargetID {
			if !mirror.matches(image) {
				return fmt.Errorf("publication receipt image %q mirror signature is not bound to durable evidence", image.ImageKey)
			}
			continue
		}
		primary, err := durablePrimarySignature(steps["signature/"+image.ImageKey])
		if err != nil || !primary.matches(image) {
			return fmt.Errorf("publication receipt image %q primary signature is not bound to durable evidence", image.ImageKey)
		}
	}

	images := make(map[string]scannerrelease.ReleaseImage)
	for _, image := range receipt.Images {
		if existing, ok := images[image.ImageKey]; ok && existing.Digest != image.Digest {
			return fmt.Errorf("publication receipt image %q differs across registries", image.ImageKey)
		}
		images[image.ImageKey] = image
	}
	for key, image := range images {
		checks := []struct {
			step   string
			digest string
		}{
			{"image-manifest/" + key, image.Digest},
			{"candidate-publish/" + key, image.Digest},
			{"published-verify/" + key, image.Digest},
			{"sbom/" + key, image.SBOMDigest},
			{"provenance/" + key, image.ProvenanceDigest},
		}
		for _, check := range checks {
			step, exists := steps[check.step]
			if !exists || step.OutputDigest != check.digest {
				return fmt.Errorf(
					"publication receipt image %q is not bound to durable step %q",
					key, check.step,
				)
			}
		}
		var platforms map[string]string
		if err := json.Unmarshal([]byte(image.PlatformDigests), &platforms); err != nil {
			return fmt.Errorf("decode publication image %q platforms: %w", key, err)
		}
		for platform, digest := range platforms {
			buildKey := "build/" + key + "/" + strings.ReplaceAll(platform, "/", "-")
			if step, exists := steps[buildKey]; !exists || step.OutputDigest != digest {
				return fmt.Errorf(
					"publication receipt image %q platform %q is not bound to durable build evidence",
					key, platform,
				)
			}
		}
	}

	artifacts := make(map[string]scannerrelease.ReleaseArtifact, len(receipt.Artifacts))
	for _, artifact := range receipt.Artifacts {
		artifacts[artifact.ArtifactType] = artifact
	}
	for _, key := range requiredPublicationArtifactSteps() {
		step := steps[key]
		artifact, exists := artifacts[key]
		if !exists || artifact.Digest != step.OutputDigest || artifact.URI != step.OutputURI {
			return fmt.Errorf("publication receipt is missing durable artifact %q", key)
		}
		parsed, err := url.Parse(artifact.URI)
		if err != nil || parsed.Scheme == "" {
			return fmt.Errorf("publication receipt artifact %q URI is invalid", key)
		}
	}
	return nil
}

type durableSignatureIdentity struct {
	RegistryTargetID                                  string
	SubjectDigest                                     string
	PayloadDigest                                     string
	ArtifactURI                                       string
	ArtifactDigest                                    string
	MediaType                                         string
	SizeBytes                                         int64
	CertificateDigest                                 string
	Identity, Issuer, Subject, TrustRoot, OperationID string
}

func (e durableSignatureIdentity) matches(image scannerrelease.ReleaseImage) bool {
	return e.SubjectDigest == image.Digest &&
		e.PayloadDigest == image.SignatureDigest &&
		e.ArtifactURI == image.SignatureArtifactURI &&
		e.ArtifactDigest == image.SignatureArtifactDigest &&
		e.MediaType == image.SignatureMediaType &&
		e.SizeBytes == image.SignatureArtifactSizeBytes &&
		e.CertificateDigest == image.SignatureCertificateDigest &&
		e.Identity == image.SignatureIdentity && e.Issuer == image.SignatureIssuer &&
		e.Subject == image.SignatureSubject && e.TrustRoot == image.SignatureTrustRoot &&
		e.OperationID == image.SignatureOperationID
}

func durableStepSummary(step scannerrelease.BuildStep) (map[string]json.RawMessage, error) {
	if step.StepKey == "" {
		return nil, nil
	}
	var envelope struct {
		Evidence struct {
			Summary map[string]json.RawMessage `json:"summary"`
		} `json:"evidence"`
	}
	if err := json.Unmarshal([]byte(step.SummaryJSON), &envelope); err != nil || envelope.Evidence.Summary == nil {
		return nil, fmt.Errorf("decode durable step %q signature summary", step.StepKey)
	}
	return envelope.Evidence.Summary, nil
}

func durablePrimarySignature(step scannerrelease.BuildStep) (durableSignatureIdentity, error) {
	summary, err := durableStepSummary(step)
	if err != nil {
		return durableSignatureIdentity{}, err
	}
	raw := summary["signing_evidence"]
	if len(raw) == 0 {
		return durableSignatureIdentity{}, errors.New("durable primary signing evidence is absent")
	}
	var evidence struct {
		OperationID             string `json:"operation_id"`
		ArtifactDigest          string `json:"artifact_digest"`
		ArtifactSubjectDigest   string `json:"artifact_subject_digest"`
		SignatureDigest         string `json:"signature_digest"`
		SignatureURI            string `json:"signature_uri"`
		SignatureArtifactDigest string `json:"signature_artifact_digest"`
		SignatureMediaType      string `json:"signature_media_type"`
		SignatureArtifactSize   int64  `json:"signature_artifact_size_bytes"`
		CertificateDigest       string `json:"certificate_digest"`
		ObservedIdentity        string `json:"observed_identity"`
		ObservedIssuer          string `json:"observed_issuer"`
		ObservedSubject         string `json:"observed_subject"`
		ObservedTrustRoot       string `json:"observed_trust_root"`
		Verified                bool   `json:"verified"`
	}
	if err := json.Unmarshal(raw, &evidence); err != nil || !evidence.Verified ||
		evidence.ArtifactDigest != evidence.ArtifactSubjectDigest {
		return durableSignatureIdentity{}, errors.New("durable primary signing evidence is invalid")
	}
	return durableSignatureIdentity{
		SubjectDigest: evidence.ArtifactDigest, PayloadDigest: evidence.SignatureDigest,
		ArtifactURI: evidence.SignatureURI, ArtifactDigest: evidence.SignatureArtifactDigest,
		MediaType: evidence.SignatureMediaType, SizeBytes: evidence.SignatureArtifactSize,
		CertificateDigest: evidence.CertificateDigest, Identity: evidence.ObservedIdentity,
		Issuer: evidence.ObservedIssuer, Subject: evidence.ObservedSubject,
		TrustRoot: evidence.ObservedTrustRoot, OperationID: evidence.OperationID,
	}, nil
}

func durableMirrorSignatures(step scannerrelease.BuildStep) (map[string]durableSignatureIdentity, error) {
	if step.StepKey == "" {
		return nil, nil
	}
	summary, err := durableStepSummary(step)
	if err != nil {
		return nil, err
	}
	raw := summary["images"]
	if len(raw) == 0 {
		return nil, errors.New("durable mirror signature inventory is absent")
	}
	var images map[string]struct {
		Digest                string `json:"digest"`
		RegistryTargetID      string `json:"registry_target_id"`
		SignatureDigest       string `json:"signature_digest"`
		SignatureURI          string `json:"signature_uri"`
		SignatureArtifact     string `json:"signature_artifact_digest"`
		SignatureMediaType    string `json:"signature_media_type"`
		SignatureArtifactSize int64  `json:"signature_artifact_size_bytes"`
		CertificateDigest     string `json:"certificate_digest"`
		SigningOperationID    string `json:"signing_operation_id"`
		SignerIdentity        string `json:"signer_identity"`
		SignerIssuer          string `json:"signer_issuer"`
		SignerSubject         string `json:"signer_subject"`
		SignerTrustRoot       string `json:"signer_trust_root"`
	}
	if err := json.Unmarshal(raw, &images); err != nil {
		return nil, errors.New("durable mirror signature inventory is invalid")
	}
	result := make(map[string]durableSignatureIdentity, len(images))
	for key, image := range images {
		result[key] = durableSignatureIdentity{
			RegistryTargetID: image.RegistryTargetID, SubjectDigest: image.Digest,
			PayloadDigest: image.SignatureDigest, ArtifactURI: image.SignatureURI,
			ArtifactDigest: image.SignatureArtifact, MediaType: image.SignatureMediaType,
			SizeBytes: image.SignatureArtifactSize, CertificateDigest: image.CertificateDigest,
			Identity: image.SignerIdentity, Issuer: image.SignerIssuer, Subject: image.SignerSubject,
			TrustRoot: image.SignerTrustRoot, OperationID: image.SigningOperationID,
		}
	}
	return result, nil
}

func requiredPublicationArtifactSteps() []string {
	return []string{
		"aggregate-sbom", "finding-regression", "mirror-copy-verify",
		"compose-integration", "compose-scanner-integration",
		"kubernetes-integration", "kind-scanner-integration",
		"release-manifest", "release-manifest-signature",
		"mirror-release-closure-verify", "policy-decision-artifact",
	}
}
