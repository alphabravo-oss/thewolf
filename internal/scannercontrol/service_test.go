package scannercontrol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/scannerpipeline"
	"github.com/alphabravocompany/thewolf/internal/scannerpolicy"
	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

func TestServiceInitializesDefaultPolicyAndCreatesDurableDiscovery(t *testing.T) {
	t.Parallel()
	store := newFakePersistence()
	service := Service{Store: store}
	run, err := service.CreateDiscovery(context.Background(), DiscoveryCommand{
		Trigger:          scannerrelease.DiscoveryOnDemand,
		DefinitionCommit: "0123456789abcdef",
		Actor:            "operator-1",
		IdempotencyKey:   "discovery-1",
		Scope:            map[string]any{"type": "all"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(store.policies) != 1 || !store.policies[0].Enabled {
		t.Fatalf("policies = %#v", store.policies)
	}
	var rules scannerpolicy.Policy
	if err := json.Unmarshal([]byte(store.policies[0].RulesJSON), &rules); err != nil {
		t.Fatal(err)
	}
	if rules.ApprovalMode != scannerpolicy.ApprovalManual {
		t.Fatalf("default approval mode = %s", rules.ApprovalMode)
	}
	if run.State != scannerrelease.DiscoveryQueued ||
		run.PolicyRevision != store.policies[0].Revision ||
		run.ScopeJSON != `{"type":"all"}` ||
		store.discoveryCommand.IdempotencyKey != "discovery-1" {
		t.Fatalf("discovery = %#v, command = %#v", run, store.discoveryCommand)
	}
}

func TestServicePersistsCandidateSelectionOutsideEventPayload(t *testing.T) {
	t.Parallel()
	store := newFakePersistence()
	service := Service{Store: store}
	policy, err := service.EnsureDefaultPolicy(context.Background(), "operator-1")
	if err != nil {
		t.Fatal(err)
	}
	discovery := &scannerrelease.DiscoveryRun{
		ID: "discovery-selection-1", DefinitionCommit: "0123456789abcdef",
		PolicyID: policy.ID, PolicyRevision: policy.Revision,
		ScopeJSON: `{"mode":"complete"}`, State: scannerrelease.DiscoveryCompleted,
	}
	store.discoveries[discovery.ID] = discovery
	candidate, err := service.CreateCandidate(context.Background(), CandidateCommand{
		DiscoveryRunID:   discovery.ID,
		DefinitionCommit: "0123456789abcdef",
		Actor:            "operator-1",
		Reason:           "test selected scanner updates",
		IdempotencyKey:   "candidate-selection-1",
		SelectedItems:    []string{"tool:semgrep", "base_image:default"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var selection struct {
		Mode  string   `json:"mode"`
		Items []string `json:"items"`
	}
	if err := json.Unmarshal([]byte(candidate.SelectionJSON), &selection); err != nil {
		t.Fatal(err)
	}
	if selection.Mode != "explicit" || len(selection.Items) != 2 {
		t.Fatalf("candidate selection = %#v", selection)
	}
}

func TestServiceRecordsOnlyScopedApprovedExpiringCandidateExceptions(t *testing.T) {
	t.Parallel()
	store := newFakePersistence()
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	service := Service{Store: store, Now: func() time.Time { return now }}
	candidate, err := service.CreateCandidate(context.Background(), CandidateCommand{
		DefinitionCommit: "0123456789abcdef", LockDigest: validControlDigest("a"),
		LockURI: "git:scanners/scanner-lock.yaml", Actor: "candidate-creator",
		IdempotencyKey: "candidate-exception", Reason: "review exception workflow",
		RiskSummary: map[string]any{"highest": "high"},
	})
	if err != nil {
		t.Fatal(err)
	}
	exception, err := service.AddCandidateException(context.Background(), ExceptionCommand{
		CandidateID: candidate.ID, Gate: "vulnerability", OwnerID: "security-owner",
		Reason: "temporary upstream advisory", CompensatingControl: "isolate candidate registry",
		EvidenceDigest: validControlDigest("b"), ExpiresAt: now.Add(7 * 24 * time.Hour),
		Actor: "release-approver", IdempotencyKey: "exception-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if exception.Action != "exception" || exception.ExceptionScope != "vulnerability" ||
		exception.ExceptionOwner != "security-owner" || exception.ExpiresAt == nil {
		t.Fatalf("exception = %#v", exception)
	}
	if _, err := service.AddCandidateException(context.Background(), ExceptionCommand{
		CandidateID: candidate.ID, Gate: "signature", OwnerID: "security-owner",
		Reason: "must remain hard", CompensatingControl: "none",
		EvidenceDigest: validControlDigest("c"), ExpiresAt: now.Add(time.Hour),
		Actor: "release-approver", IdempotencyKey: "exception-hard",
	}); err == nil || !strings.Contains(err.Error(), "cannot be bypassed") {
		t.Fatalf("hard-gate exception error = %v", err)
	}
	if _, err := service.AddCandidateException(context.Background(), ExceptionCommand{
		CandidateID: candidate.ID, Gate: "vulnerability", OwnerID: "release-approver",
		Reason: "self approved", CompensatingControl: "none",
		EvidenceDigest: validControlDigest("d"), ExpiresAt: now.Add(time.Hour),
		Actor: "release-approver", IdempotencyKey: "exception-self",
	}); err == nil || !strings.Contains(err.Error(), "must be distinct") {
		t.Fatalf("self-approved exception error = %v", err)
	}
}

func TestServiceCreatesCompleteDurableBuildPlan(t *testing.T) {
	t.Parallel()
	store := newFakePersistence()
	service := Service{Store: store}
	candidate, err := service.CreateCandidate(context.Background(), CandidateCommand{
		DefinitionCommit: "0123456789abcdef",
		LockDigest:       "sha256:lock",
		LockURI:          "git:scanners/scanner-lock.yaml",
		Actor:            "operator-1",
		Reason:           "test immutable scanner candidate",
		IdempotencyKey:   "candidate-1",
		RiskSummary:      map[string]any{"highest": "low"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if candidate.State != scannerrelease.CandidateQueued {
		t.Fatalf("candidate state = %s", candidate.State)
	}
	if len(store.builds) != 1 || store.builds[0].State != scannerrelease.BuildQueued {
		t.Fatalf("builds = %#v", store.builds)
	}
	if len(store.steps) < 60 {
		t.Fatalf("durable build steps = %d, want complete multi-image pipeline", len(store.steps))
	}
	stepKeys := make(map[string]bool)
	for _, step := range store.steps {
		stepKeys[step.StepKey] = true
	}
	for _, key := range []string{
		"build/default/linux-amd64",
		"build/default/linux-arm64",
		"build/codeql/linux-amd64",
		"build/fixer-base/linux-arm64",
		"build/fixer-codex/linux-amd64",
		"fixer-auth-contract/fixer-codex",
		"fixer-integration",
		"signature/default",
		"compose-integration",
		"kubernetes-integration",
		"release-manifest-signature",
		"policy-evaluation",
	} {
		if !stepKeys[key] {
			t.Errorf("build plan missing %q", key)
		}
	}
	if stepKeys["build/codeql/linux-arm64"] {
		t.Fatal("build plan contains undeclared CodeQL arm64 build")
	}
	if stepKeys["parser-fixtures/fixer-codex"] {
		t.Fatal("build plan applies scanner parser fixtures to a fixer image")
	}

	incomplete := defaultImages()[:4]
	if err := EnqueueCandidateBuildPlan(context.Background(), store, candidate, incomplete); err == nil ||
		!strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("incomplete candidate image set error = %v", err)
	}
}

func TestServiceRejectsMissingReasonAndPartialDiscoveryCandidate(t *testing.T) {
	t.Parallel()
	store := newFakePersistence()
	service := Service{Store: store}
	if _, err := service.CreateCandidate(context.Background(), CandidateCommand{
		DefinitionCommit: "0123456789abcdef", LockDigest: "sha256:lock",
		Actor: "operator-1", IdempotencyKey: "candidate-missing-reason",
	}); !errors.Is(err, ErrValidation) {
		t.Fatalf("missing reason error = %v", err)
	}
	policy, err := service.EnsureDefaultPolicy(context.Background(), "operator-1")
	if err != nil {
		t.Fatal(err)
	}
	partial := &scannerrelease.DiscoveryRun{
		ID: "partial-discovery", DefinitionCommit: "0123456789abcdef",
		PolicyID: policy.ID, PolicyRevision: policy.Revision,
		ScopeJSON: `{"mode":"complete"}`, State: scannerrelease.DiscoveryCompleted,
		Coverage: 0.5, TotalCount: 2, CoveredCount: 1, UnreachableCount: 1,
		ErrorClass: "partial_coverage",
	}
	store.discoveries[partial.ID] = partial
	if _, err := service.CreateCandidate(context.Background(), CandidateCommand{
		DiscoveryRunID: partial.ID, DefinitionCommit: partial.DefinitionCommit,
		Actor: "operator-1", Reason: "attempt candidate from partial discovery",
		IdempotencyKey: "candidate-partial-discovery",
	}); !errors.Is(err, ErrCandidateNotReady) {
		t.Fatalf("partial discovery error = %v", err)
	}
}

func TestServiceEnforcesApprovalBindingAndSeparationOfDuties(t *testing.T) {
	t.Parallel()
	store := newFakePersistence()
	receiptDigest := validControlDigest("e")
	service := Service{Store: store, PublicationVerifier: staticPublicationVerifier{digest: receiptDigest}}
	policy, err := service.EnsureDefaultPolicy(context.Background(), "system")
	if err != nil {
		t.Fatal(err)
	}
	candidate := &scannerrelease.Candidate{
		ID: "candidate-1", State: scannerrelease.CandidateAwaitingApproval,
		LockDigest: "sha256:lock", PolicyDecision: "sha256:decision",
		PolicyID: policy.ID, PolicyRevision: policy.Revision,
		Actor: "creator-1", Version: 4,
	}
	store.candidates[candidate.ID] = candidate

	updated, err := service.ApproveCandidate(context.Background(), ApprovalCommand{
		CandidateID: candidate.ID, LockDigest: candidate.LockDigest,
		PolicyDecisionDigest: candidate.PolicyDecision,
		EvidenceDigest:       receiptDigest, Actor: "creator-1", Reason: "self review", IdempotencyKey: "approval-self",
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.State != scannerrelease.CandidateAwaitingApproval {
		t.Fatalf("self approval changed state to %s", updated.State)
	}

	updated, err = service.ApproveCandidate(context.Background(), ApprovalCommand{
		CandidateID: candidate.ID, LockDigest: candidate.LockDigest,
		PolicyDecisionDigest: candidate.PolicyDecision,
		EvidenceDigest:       receiptDigest, Actor: "approver-1", Reason: "all evidence passed", IdempotencyKey: "approval-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.State != scannerrelease.CandidateApproved {
		t.Fatalf("approved state = %s", updated.State)
	}

	store.candidates[candidate.ID].State = scannerrelease.CandidateAwaitingApproval
	if _, err := service.ApproveCandidate(context.Background(), ApprovalCommand{
		CandidateID: candidate.ID, LockDigest: "sha256:other",
		PolicyDecisionDigest: candidate.PolicyDecision,
		EvidenceDigest:       receiptDigest, Actor: "approver-2", Reason: "stale", IdempotencyKey: "approval-stale",
	}); err != ErrApprovalStale {
		t.Fatalf("stale approval error = %v", err)
	}
}

func TestServiceCreatesCanaryAndStableRollout(t *testing.T) {
	t.Parallel()
	store := newFakePersistence()
	service := Service{Store: store}
	store.releases["new"] = &scannerrelease.Release{
		ID: "new", Name: "scanner-set-2026.31.1", State: scannerrelease.ReleasePublished,
		PublishedAt: time.Date(2026, 7, 30, 14, 0, 0, 0, time.UTC),
	}
	store.releases["old"] = &scannerrelease.Release{
		ID: "old", Name: "scanner-set-2026.30.1", State: scannerrelease.ReleaseStable,
		PublishedAt: time.Date(2026, 7, 23, 14, 0, 0, 0, time.UTC),
	}
	rollout, err := service.PromoteRelease(context.Background(), RolloutCommand{
		ReleaseID: "new", Target: "production", Actor: "operator-1",
		Reason: "approved maintenance window", IdempotencyKey: "rollout-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if rollout.State != scannerrelease.RolloutPending || rollout.FromReleaseID != "old" {
		t.Fatalf("rollout = %#v", rollout)
	}
	if len(store.cohorts) != 2 ||
		store.cohorts[0].Name != "canary" ||
		store.cohorts[1].Name != "stable" {
		t.Fatalf("cohorts = %#v", store.cohorts)
	}
}

func TestServicePublishesOnlyExactApprovedCandidateInventory(t *testing.T) {
	t.Parallel()
	store := newFakePersistence()
	service := Service{Store: store}
	policy, err := service.EnsureDefaultPolicy(context.Background(), "system")
	if err != nil {
		t.Fatal(err)
	}
	candidate := &scannerrelease.Candidate{
		ID: "candidate-publish", State: scannerrelease.CandidateApproved,
		LockDigest: validControlDigest("a"), DefinitionCommit: "commit-1",
		ProposedCommit: "commit-2",
		PolicyDecision: validControlDigest("b"),
		PolicyID:       policy.ID, PolicyRevision: policy.Revision, Actor: "creator", Version: 7,
	}
	store.candidates[candidate.ID] = candidate
	manifestDigest := validControlDigest("c")
	receipt := validPublicationReceipt(candidate, "build-publish", manifestDigest)
	receiptDigest, err := scannerrelease.PublicationReceiptDigest(receipt)
	if err != nil {
		t.Fatal(err)
	}
	service.PublicationVerifier = staticPublicationVerifier{receipt: receipt, digest: receiptDigest}
	store.approvals = append(store.approvals, scannerrelease.Approval{
		CandidateID: candidate.ID, Action: "approve", PolicyDecision: candidate.PolicyDecision,
		EvidenceDigest: receiptDigest,
	})
	release, err := service.PublishCandidate(context.Background(), PublicationCommand{
		CandidateID: candidate.ID, Name: "scanner-set-2026.31.1",
		ReceiptDigest: receiptDigest, Actor: "publisher",
		Reason: "verified registry re-read", IdempotencyKey: "publish-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if release.ManifestDigest != manifestDigest ||
		release.LockDigest != candidate.LockDigest ||
		release.DefinitionCommit != candidate.ProposedCommit ||
		release.State != scannerrelease.ReleasePublished ||
		!release.Protected || !release.RollbackEligible {
		t.Fatalf("release = %#v", release)
	}
	if store.candidates[candidate.ID].State != scannerrelease.CandidatePublished {
		t.Fatalf("candidate state = %s", store.candidates[candidate.ID].State)
	}
	if len(store.releaseInventories) != 1 ||
		len(store.releaseInventories[0].Tools) != 49 ||
		len(store.releaseInventories[0].Images) != 8 {
		t.Fatalf("inventories = %#v", store.releaseInventories)
	}

	store.candidates[candidate.ID].State = scannerrelease.CandidateAwaitingApproval
	if _, err := service.PublishCandidate(context.Background(), PublicationCommand{
		CandidateID: candidate.ID, Name: "other", ReceiptDigest: receiptDigest, Actor: "publisher",
		Reason: "attempted bypass", IdempotencyKey: "publish-2",
	}); err == nil {
		t.Fatal("unapproved candidate publication succeeded")
	}
}

func validControlDigest(character string) string {
	return "sha256:" + strings.Repeat(character, 64)
}

func TestPublicationValidationRejectsMissingSupplyChainEvidence(t *testing.T) {
	t.Parallel()

	candidate := &scannerrelease.Candidate{
		LockDigest: validControlDigest("a"), PolicyDecision: validControlDigest("b"),
	}
	receipt := scannerrelease.PublicationReceipt{
		ManifestDigest: validControlDigest("c"),
		ManifestURI:    "oci://registry/release@" + validControlDigest("c"), SignerIdentity: "signer",
		Tools: requiredControlReleaseTools(),
		Images: requiredControlReleaseImages(scannerrelease.ReleaseImage{
			ImageKey: "default", RegistryTargetID: "primary", Repository: "scanner",
			Digest: validControlDigest("d"), PlatformDigests: `{"linux/amd64":"` + validControlDigest("e") + `"}`,
			SignatureStatus: "verified", ProvenanceDigest: validControlDigest("f"),
			SBOMDigest: validControlDigest("0"),
		}),
		Artifacts: requiredControlReleaseArtifacts(),
	}
	if err := validatePublication(candidate, "scanner-set-2026.31.1", receipt); err != nil {
		t.Fatal(err)
	}
	if err := validatePublication(candidate, "scanner-set-2026.31.1", receipt); err != nil {
		t.Fatalf("complete release with fixer image: %v", err)
	}

	badKind := receipt
	badKind.Images = append([]scannerrelease.ReleaseImage(nil), receipt.Images...)
	badKind.Images[4].ImageKind = "worker"
	if err := validatePublication(candidate, "scanner-set-2026.31.1", badKind); err == nil ||
		!strings.Contains(err.Error(), "kind") {
		t.Fatalf("invalid image kind validation error = %v", err)
	}

	missingSignature := receipt
	missingSignature.Images = append([]scannerrelease.ReleaseImage(nil), receipt.Images...)
	missingSignature.Images[0].SignatureStatus = "unsigned"
	if err := validatePublication(candidate, "scanner-set-2026.31.1", missingSignature); err == nil ||
		!strings.Contains(err.Error(), "signature") {
		t.Fatalf("signature validation error = %v", err)
	}

	missingPlatform := receipt
	missingPlatform.Images = append([]scannerrelease.ReleaseImage(nil), receipt.Images...)
	missingPlatform.Images[0].PlatformDigests = "{}"
	if err := validatePublication(candidate, "scanner-set-2026.31.1", missingPlatform); err == nil ||
		!strings.Contains(err.Error(), "platform") {
		t.Fatalf("platform validation error = %v", err)
	}

	missingOwned := receipt
	missingOwned.Images = append([]scannerrelease.ReleaseImage(nil), receipt.Images[:7]...)
	if err := validatePublication(candidate, "scanner-set-2026.31.1", missingOwned); err == nil ||
		!strings.Contains(err.Error(), "missing required owned image") {
		t.Fatalf("missing owned image validation error = %v", err)
	}

	toolOnFixer := receipt
	toolOnFixer.Tools = append([]scannerrelease.ReleaseTool(nil), receipt.Tools...)
	toolOnFixer.Tools[0].MetadataJSON = `{"image_key":"fixer-codex","kind":"wolf"}`
	if err := validatePublication(candidate, "scanner-set-2026.31.1", toolOnFixer); err == nil ||
		!strings.Contains(err.Error(), "scanner runtime image") {
		t.Fatalf("tool-to-fixer validation error = %v", err)
	}
}

func TestDurablePublicationVerifierBindsCompletedDAGReceipt(t *testing.T) {
	t.Parallel()
	store := newFakePersistence()
	candidate := &scannerrelease.Candidate{
		ID: "candidate-receipt", DefinitionCommit: "commit-receipt",
		LockDigest: validControlDigest("a"), PolicyDecision: validControlDigest("b"),
		PolicyID: "policy-receipt", PolicyRevision: 7,
	}
	receipt, digest, build, steps := durablePublicationEvidence(t, candidate, "build-receipt")
	store.builds = append(store.builds, build)
	store.steps = append(store.steps, steps...)
	verified, err := (DurablePublicationVerifier{Store: store}).VerifyPublication(
		context.Background(), candidate, digest,
	)
	if err != nil || verified.Digest != digest || verified.Receipt.BuildRunID != receipt.BuildRunID {
		t.Fatalf("verified=%#v err=%v", verified, err)
	}
	latest := make(map[string]scannerrelease.BuildStep, len(steps))
	for _, step := range steps {
		latest[step.StepKey] = *step
	}
	tamperedSignature := receipt
	tamperedSignature.Images = append([]scannerrelease.ReleaseImage(nil), receipt.Images...)
	tamperedSignature.Images[0].SignatureOperationID = validControlDigest("8")
	if err := verifyReceiptAgainstDurableEvidence(tamperedSignature, latest); err == nil ||
		!strings.Contains(err.Error(), "signature") {
		t.Fatalf("tampered exact signature binding error = %v", err)
	}

	store.steps[0].State = scannerrelease.BuildFailed
	if _, err := (DurablePublicationVerifier{Store: store}).VerifyPublication(
		context.Background(), candidate, digest,
	); !errors.Is(err, ErrPublicationEvidence) {
		t.Fatalf("incomplete DAG publication error = %v", err)
	}
}

func durablePublicationEvidence(
	t *testing.T,
	candidate *scannerrelease.Candidate,
	buildID string,
) (scannerrelease.PublicationReceipt, string, *scannerrelease.BuildRun, []*scannerrelease.BuildStep) {
	t.Helper()
	images := defaultImages()
	platforms, err := json.Marshal(images)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := scannerpipeline.Default(scannerpipeline.Inputs{
		Images: images, RequireCompose: true, RequireKubernetes: true, RequireMirror: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	receipt := validPublicationReceipt(candidate, buildID, validControlDigest("c"))
	byImage := make(map[string]scannerrelease.ReleaseImage, len(receipt.Images))
	for _, image := range receipt.Images {
		byImage[image.ImageKey] = image
	}
	type output struct{ uri, digest string }
	outputs := make(map[string]output, len(plan.Steps))
	for _, step := range plan.Steps {
		current := output{
			uri:    "artifact://scanner-release/" + step.Key,
			digest: validControlDigest("9"),
		}
		switch {
		case step.Key == "release-manifest":
			current = output{uri: receipt.ManifestURI, digest: receipt.ManifestDigest}
		case step.Key == "policy-evaluation":
			current.digest = candidate.PolicyDecision
		case strings.HasPrefix(step.Key, "image-manifest/"):
			current.digest = byImage[strings.TrimPrefix(step.Key, "image-manifest/")].Digest
		case strings.HasPrefix(step.Key, "candidate-publish/"):
			current.digest = byImage[strings.TrimPrefix(step.Key, "candidate-publish/")].Digest
		case strings.HasPrefix(step.Key, "published-verify/"):
			current.digest = byImage[strings.TrimPrefix(step.Key, "published-verify/")].Digest
		case strings.HasPrefix(step.Key, "sbom/"):
			current.digest = byImage[strings.TrimPrefix(step.Key, "sbom/")].SBOMDigest
		case strings.HasPrefix(step.Key, "provenance/"):
			current.digest = byImage[strings.TrimPrefix(step.Key, "provenance/")].ProvenanceDigest
		case strings.HasPrefix(step.Key, "build/"):
			parts := strings.Split(step.Key, "/")
			if len(parts) == 3 {
				var platformDigests map[string]string
				if err := json.Unmarshal([]byte(byImage[parts[1]].PlatformDigests), &platformDigests); err != nil {
					t.Fatal(err)
				}
				current.digest = platformDigests[strings.ReplaceAll(parts[2], "-", "/")]
			}
		}
		outputs[step.Key] = current
	}
	receipt.Artifacts = nil
	for _, key := range requiredPublicationArtifactSteps() {
		value := outputs[key]
		receipt.Artifacts = append(receipt.Artifacts, scannerrelease.ReleaseArtifact{
			ArtifactType: key, MediaType: "application/json", URI: value.uri,
			Digest: value.digest, SizeBytes: 1, RetentionClass: "published", Protected: true,
		})
	}
	digest, err := scannerrelease.PublicationReceiptDigest(receipt)
	if err != nil {
		t.Fatal(err)
	}
	outputs["candidate-evidence-summary"] = output{
		uri: "artifact://scanner-release/candidate-evidence-summary", digest: digest,
	}
	steps := make([]*scannerrelease.BuildStep, 0, len(plan.Steps))
	for _, planStep := range plan.Steps {
		evidenceSummary := map[string]any{"status": "passed"}
		if strings.HasPrefix(planStep.Key, "signature/") {
			image := byImage[strings.TrimPrefix(planStep.Key, "signature/")]
			evidenceSummary["signing_evidence"] = map[string]any{
				"operation_id":    image.SignatureOperationID,
				"artifact_digest": image.Digest, "artifact_subject_digest": image.Digest,
				"signature_digest":              image.SignatureDigest,
				"signature_uri":                 image.SignatureArtifactURI,
				"signature_artifact_digest":     image.SignatureArtifactDigest,
				"signature_media_type":          image.SignatureMediaType,
				"signature_artifact_size_bytes": image.SignatureArtifactSizeBytes,
				"certificate_digest":            image.SignatureCertificateDigest,
				"observed_identity":             image.SignatureIdentity,
				"observed_issuer":               image.SignatureIssuer,
				"observed_subject":              image.SignatureSubject,
				"observed_trust_root":           image.SignatureTrustRoot,
				"verified":                      true,
			}
		}
		if planStep.Key == "mirror-copy-verify" {
			evidenceSummary["images"] = map[string]any{}
		}
		if planStep.Key == "candidate-evidence-summary" {
			evidenceSummary["publication_receipt"] = receipt
		}
		summary, err := json.Marshal(map[string]any{
			"kind": planStep.Kind,
			"evidence": map[string]any{
				"summary": evidenceSummary,
				"verification": map[string]any{
					"definition_commit": scannerrelease.EffectiveDefinitionCommit(candidate),
					"lock_digest":       candidate.LockDigest, "policy_id": candidate.PolicyID,
					"policy_revision": candidate.PolicyRevision,
				},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		value := outputs[planStep.Key]
		steps = append(steps, &scannerrelease.BuildStep{
			BuildRunID: buildID, StepKey: planStep.Key, Attempt: 1,
			State: scannerrelease.BuildCompleted, OutputURI: value.uri,
			OutputDigest: value.digest, SummaryJSON: string(summary),
		})
	}
	build := &scannerrelease.BuildRun{
		ID: buildID, CandidateID: candidate.ID, Attempt: 1,
		State: scannerrelease.BuildCompleted, PlatformsJSON: string(platforms),
	}
	return receipt, digest, build, steps
}

func requiredControlReleaseImages(defaultImage scannerrelease.ReleaseImage) []scannerrelease.ReleaseImage {
	platformDigest := validControlDigest("1")
	image := func(key, kind string, platforms []string) scannerrelease.ReleaseImage {
		platformDigests := make(map[string]string, len(platforms))
		for _, platform := range platforms {
			platformDigests[platform] = platformDigest
		}
		raw, _ := json.Marshal(platformDigests)
		return scannerrelease.ReleaseImage{
			ImageKey: key, ImageKind: kind, RegistryTargetID: defaultImage.RegistryTargetID,
			Repository: "security/" + key, Digest: validControlDigest("2"),
			PlatformDigests: string(raw), SignatureStatus: "verified",
			SignatureDigest:            validControlDigest("5"),
			SignatureArtifactURI:       "oci://registry.example/signatures@" + validControlDigest("6"),
			SignatureArtifactDigest:    validControlDigest("6"),
			SignatureMediaType:         "application/vnd.dev.cosign.simplesigning.v1+json",
			SignatureArtifactSizeBytes: 1,
			SignatureIdentity:          "test-signer", SignatureIssuer: "https://issuer.test",
			SignatureSubject: "test-subject", SignatureTrustRoot: "secret://***",
			SignatureOperationID: validControlDigest("7"),
			ProvenanceDigest:     validControlDigest("3"), SBOMDigest: validControlDigest("4"),
		}
	}
	defaultImage.ImageKind = scannerrelease.ReleaseImageScanner
	defaultImage.SignatureDigest = validControlDigest("5")
	defaultImage.SignatureArtifactURI = "oci://registry.example/signatures@" + validControlDigest("6")
	defaultImage.SignatureArtifactDigest = validControlDigest("6")
	defaultImage.SignatureMediaType = "application/vnd.dev.cosign.simplesigning.v1+json"
	defaultImage.SignatureArtifactSizeBytes = 1
	defaultImage.SignatureIdentity = "test-signer"
	defaultImage.SignatureIssuer = "https://issuer.test"
	defaultImage.SignatureSubject = "test-subject"
	defaultImage.SignatureTrustRoot = "secret://***"
	defaultImage.SignatureOperationID = validControlDigest("7")
	defaultImage.PlatformDigests = `{"linux/amd64":"` + platformDigest + `","linux/arm64":"` + platformDigest + `"}`
	images := []scannerrelease.ReleaseImage{defaultImage}
	for _, key := range []string{"jvm", "rust"} {
		images = append(images, image(key, scannerrelease.ReleaseImageScanner, []string{"linux/amd64", "linux/arm64"}))
	}
	images = append(images, image("codeql", scannerrelease.ReleaseImageScanner, []string{"linux/amd64"}))
	for _, key := range []string{"fixer-base", "fixer-api", "fixer-claude", "fixer-codex"} {
		images = append(images, image(key, scannerrelease.ReleaseImageFixer, []string{"linux/amd64", "linux/arm64"}))
	}
	return images
}

func requiredControlReleaseTools() []scannerrelease.ReleaseTool {
	tools := make([]scannerrelease.ReleaseTool, 0, len(requiredReleaseToolKeys))
	for _, key := range requiredReleaseToolKeys {
		tools = append(tools, scannerrelease.ReleaseTool{
			ToolKey: key, Version: "1.2.3", SourceReference: "locked:" + key + "/1.2.3",
			ParserCompatibility: "quality_policy:json",
			MetadataJSON:        `{"image_key":"default","kind":"wolf","integration_tier":"default","platforms":["linux/amd64","linux/arm64"],"parser_format":"json"}`,
		})
	}
	return tools
}

func requiredControlReleaseArtifacts() []scannerrelease.ReleaseArtifact {
	artifacts := make([]scannerrelease.ReleaseArtifact, 0, len(scannerrelease.RequiredPublicationArtifactTypes))
	for index, kind := range scannerrelease.RequiredPublicationArtifactTypes {
		digest := validControlDigest(fmt.Sprintf("%x", (index%15)+1))
		artifacts = append(artifacts, scannerrelease.ReleaseArtifact{
			ArtifactType: kind, MediaType: "application/vnd.wolf.test+json",
			URI:    "oci://registry.example/evidence/" + kind + "@" + digest,
			Digest: digest, SizeBytes: 1, RetentionClass: "release", Protected: true,
		})
	}
	return artifacts
}

func validPublicationReceipt(
	candidate *scannerrelease.Candidate,
	buildID, manifestDigest string,
) scannerrelease.PublicationReceipt {
	return scannerrelease.PublicationReceipt{
		SchemaVersion: scannerrelease.PublicationReceiptSchema,
		CandidateID:   candidate.ID, BuildRunID: buildID,
		DefinitionCommit: scannerrelease.EffectiveDefinitionCommit(candidate), LockDigest: candidate.LockDigest,
		PolicyID: candidate.PolicyID, PolicyRevision: candidate.PolicyRevision,
		PolicyDecisionDigest: candidate.PolicyDecision,
		ManifestDigest:       manifestDigest,
		ManifestURI:          "oci://registry.example/release@" + manifestDigest,
		SignerIdentity:       "https://github.com/acme/scanners/.github/workflows/release.yml",
		Tools:                requiredControlReleaseTools(),
		Images: requiredControlReleaseImages(scannerrelease.ReleaseImage{
			ImageKey: "default", RegistryTargetID: "registry-primary",
			Repository: "security/scanner-default", Digest: validControlDigest("d"),
			PlatformDigests: `{"linux/amd64":"` + validControlDigest("e") + `"}`,
			SignatureStatus: "verified", ProvenanceDigest: validControlDigest("f"),
			SBOMDigest: validControlDigest("0"),
		}),
		Artifacts: requiredControlReleaseArtifacts(),
	}
}

type staticPublicationVerifier struct {
	receipt scannerrelease.PublicationReceipt
	digest  string
	err     error
}

func (v staticPublicationVerifier) VerifyPublication(
	_ context.Context,
	_ *scannerrelease.Candidate,
	expected string,
) (*VerifiedPublication, error) {
	if v.err != nil {
		return nil, v.err
	}
	if expected != v.digest {
		return nil, ErrPublicationEvidence
	}
	return &VerifiedPublication{Receipt: v.receipt, Digest: v.digest}, nil
}

type fakePersistence struct {
	scannerrelease.Persistence
	policies           []scannerrelease.Policy
	discoveries        map[string]*scannerrelease.DiscoveryRun
	discoveryCommand   scannerrelease.TransitionCommand
	candidates         map[string]*scannerrelease.Candidate
	builds             []*scannerrelease.BuildRun
	steps              []*scannerrelease.BuildStep
	approvals          []scannerrelease.Approval
	releases           map[string]*scannerrelease.Release
	releaseInventories []*scannerrelease.ReleaseInventory
	rollouts           []*scannerrelease.Rollout
	cohorts            []scannerrelease.RolloutCohort
}

func newFakePersistence() *fakePersistence {
	return &fakePersistence{
		discoveries: make(map[string]*scannerrelease.DiscoveryRun),
		candidates:  make(map[string]*scannerrelease.Candidate),
		releases:    make(map[string]*scannerrelease.Release),
	}
}

func (s *fakePersistence) CreatePolicy(_ context.Context, policy *scannerrelease.Policy) error {
	s.policies = append(s.policies, *policy)
	return nil
}

func (s *fakePersistence) GetPolicy(_ context.Context, id string) (*scannerrelease.Policy, error) {
	for index := range s.policies {
		if s.policies[index].ID == id {
			copy := s.policies[index]
			return &copy, nil
		}
	}
	return nil, ErrPolicyNotFound
}

func (s *fakePersistence) ListPolicies(_ context.Context, scope string, enabled bool) ([]scannerrelease.Policy, error) {
	var result []scannerrelease.Policy
	for _, policy := range s.policies {
		if policy.Scope == scope && (!enabled || policy.Enabled) {
			result = append(result, policy)
		}
	}
	return result, nil
}

func (s *fakePersistence) CreateDiscoveryRun(
	_ context.Context,
	run *scannerrelease.DiscoveryRun,
	command scannerrelease.TransitionCommand,
) error {
	s.discoveries[run.ID] = run
	s.discoveryCommand = command
	return nil
}

func (s *fakePersistence) GetDiscoveryRun(_ context.Context, id string) (*scannerrelease.DiscoveryRun, error) {
	return s.discoveries[id], nil
}

func (s *fakePersistence) GetLatestCompletedDiscovery(
	_ context.Context,
	definitionCommit, policyID string,
	policyRevision int64,
	scopeJSON string,
) (*scannerrelease.DiscoveryRun, error) {
	for _, discovery := range s.discoveries {
		if discovery.DefinitionCommit == definitionCommit && discovery.PolicyID == policyID &&
			discovery.PolicyRevision == policyRevision && discovery.ScopeJSON == scopeJSON &&
			scannerrelease.DiscoveryEligibleForCandidate(discovery) {
			return discovery, nil
		}
	}
	return nil, nil
}

func (s *fakePersistence) CreateCandidate(
	_ context.Context,
	candidate *scannerrelease.Candidate,
	_ scannerrelease.TransitionCommand,
) error {
	s.candidates[candidate.ID] = candidate
	return nil
}

func (s *fakePersistence) GetCandidate(_ context.Context, id string) (*scannerrelease.Candidate, error) {
	return s.candidates[id], nil
}

func (s *fakePersistence) TransitionCandidate(
	_ context.Context,
	id string,
	expectedVersion int64,
	state scannerrelease.CandidateState,
	_ scannerrelease.TransitionCommand,
) (*scannerrelease.Candidate, error) {
	candidate := s.candidates[id]
	if candidate.Version != expectedVersion {
		return nil, scannerrelease.ErrVersionConflict
	}
	candidate.State = state
	candidate.Version++
	return candidate, nil
}

func (s *fakePersistence) CreateBuildRun(
	_ context.Context,
	build *scannerrelease.BuildRun,
	_ scannerrelease.TransitionCommand,
) error {
	s.builds = append(s.builds, build)
	return nil
}

func (s *fakePersistence) CreateBuildPlan(
	ctx context.Context,
	build *scannerrelease.BuildRun,
	steps []scannerrelease.BuildStep,
	command scannerrelease.TransitionCommand,
) error {
	if err := s.CreateBuildRun(ctx, build, command); err != nil {
		return err
	}
	for index := range steps {
		if err := s.CreateBuildStep(ctx, &steps[index], command); err != nil {
			return err
		}
	}
	return nil
}

func (s *fakePersistence) CreateBuildStep(
	_ context.Context,
	step *scannerrelease.BuildStep,
	_ scannerrelease.TransitionCommand,
) error {
	s.steps = append(s.steps, step)
	return nil
}

func (s *fakePersistence) ListBuildRuns(
	_ context.Context,
	candidateID string,
) ([]scannerrelease.BuildRun, error) {
	var result []scannerrelease.BuildRun
	for _, build := range s.builds {
		if build.CandidateID == candidateID {
			result = append(result, *build)
		}
	}
	return result, nil
}

func (s *fakePersistence) ListBuildSteps(
	_ context.Context,
	buildID string,
) ([]scannerrelease.BuildStep, error) {
	var result []scannerrelease.BuildStep
	for _, step := range s.steps {
		if step.BuildRunID == buildID {
			result = append(result, *step)
		}
	}
	return result, nil
}

func (s *fakePersistence) AddApproval(_ context.Context, approval *scannerrelease.Approval) error {
	s.approvals = append(s.approvals, *approval)
	return nil
}

func (s *fakePersistence) ListApprovals(
	_ context.Context,
	aggregateType, aggregateID string,
) ([]scannerrelease.Approval, error) {
	var result []scannerrelease.Approval
	for _, approval := range s.approvals {
		if aggregateType == "candidate" && approval.CandidateID == aggregateID {
			result = append(result, approval)
		}
	}
	return result, nil
}

func (s *fakePersistence) GetRelease(_ context.Context, id string) (*scannerrelease.Release, error) {
	return s.releases[id], nil
}

func (s *fakePersistence) CreateRelease(
	_ context.Context,
	inventory *scannerrelease.ReleaseInventory,
	_ scannerrelease.TransitionCommand,
) error {
	s.releaseInventories = append(s.releaseInventories, inventory)
	release := inventory.Release
	s.releases[release.ID] = &release
	return nil
}

func (s *fakePersistence) CommitCandidatePublication(
	ctx context.Context,
	candidateID string,
	expectedVersion int64,
	inventory *scannerrelease.ReleaseInventory,
	command scannerrelease.TransitionCommand,
) (*scannerrelease.Release, error) {
	candidate := s.candidates[candidateID]
	if candidate == nil {
		return nil, scannerrelease.ErrIdempotencyConflict
	}
	for _, release := range s.releases {
		if release.CandidateID == candidateID {
			if release.ManifestDigest != inventory.Release.ManifestDigest {
				return nil, scannerrelease.ErrIdempotencyConflict
			}
			return release, nil
		}
	}
	if candidate.Version != expectedVersion {
		return nil, scannerrelease.ErrVersionConflict
	}
	if err := s.CreateRelease(ctx, inventory, command); err != nil {
		return nil, err
	}
	candidate.State = scannerrelease.CandidatePublished
	candidate.Version += 2
	return s.releases[inventory.Release.ID], nil
}

func (s *fakePersistence) ListReleases(
	_ context.Context,
	filter scannerrelease.ReleaseFilter,
	_ scannerrelease.PageRequest,
) (scannerrelease.ReleasePage, error) {
	var items []scannerrelease.Release
	for _, release := range s.releases {
		if filter.State == "" || release.State == filter.State {
			items = append(items, *release)
		}
	}
	sortReleases(items)
	return scannerrelease.ReleasePage{Items: items}, nil
}

func sortReleases(releases []scannerrelease.Release) {
	for i := 0; i < len(releases); i++ {
		for j := i + 1; j < len(releases); j++ {
			if releases[j].PublishedAt.After(releases[i].PublishedAt) {
				releases[i], releases[j] = releases[j], releases[i]
			}
		}
	}
}

func (s *fakePersistence) CreateRollout(
	_ context.Context,
	rollout *scannerrelease.Rollout,
	cohorts []scannerrelease.RolloutCohort,
	_ scannerrelease.TransitionCommand,
) error {
	s.rollouts = append(s.rollouts, rollout)
	s.cohorts = append([]scannerrelease.RolloutCohort(nil), cohorts...)
	return nil
}
