package scannerreleaseadapter

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/alphabravocompany/thewolf/internal/fix/qualification"
	"github.com/alphabravocompany/thewolf/internal/scannerpipeline"
	"github.com/alphabravocompany/thewolf/internal/scannerpolicy"
	"github.com/alphabravocompany/thewolf/internal/scannerquality"
	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
	"github.com/alphabravocompany/thewolf/internal/scannerreleasebackend"
	"github.com/alphabravocompany/thewolf/internal/scannerreleaseworker"
	"github.com/alphabravocompany/thewolf/internal/scannerreleaseworkspace"
	scannerlock "github.com/alphabravocompany/thewolf/internal/scannertools/lock"
)

const (
	gitPath          = "/usr/bin/git"
	scannerToolsPath = "/usr/local/bin/scannertools"
	syncContextPath  = "/usr/local/bin/synccontext"
	dockerPath       = "/usr/bin/docker"
	trivyPath        = "/usr/local/bin/trivy"
	orasPath         = "/usr/local/bin/oras"
	qualificationDir = "/usr/local/libexec/wolf/release-qualification"
	commandOutputMax = 4 << 20
)

type productionActions struct{}

func (productionActions) Execute(
	ctx context.Context,
	lane scannerreleasebackend.AdapterLane,
	invocation scannerreleasebackend.Invocation,
	commandID string,
) (ActionResult, error) {
	lock, err := scannerlock.LoadFile(filepath.Join(invocation.Request.Workspace, scannerlock.DefaultLockPath))
	if err != nil {
		return ActionResult{}, fmt.Errorf("load immutable scanner lock: %w", err)
	}
	if lock.LockDigest != invocation.Binding.LockDigest {
		return ActionResult{}, errors.New("scanner release adapter lock binding mismatch")
	}
	switch lane {
	case scannerreleasebackend.AdapterLaneFixed:
		return executeFixed(ctx, invocation, commandID, lock)
	case scannerreleasebackend.AdapterLaneQuality:
		return executeQuality(ctx, invocation, commandID, lock)
	case scannerreleasebackend.AdapterLaneIntegration:
		return executeIntegration(ctx, invocation, commandID)
	default:
		return ActionResult{}, fmt.Errorf("unsupported adapter lane %q", lane)
	}
}

func executeFixed(
	ctx context.Context,
	invocation scannerreleasebackend.Invocation,
	commandID string,
	lock *scannerlock.Lock,
) (ActionResult, error) {
	switch invocation.Action.Name {
	case "checkout":
		result, err := runBounded(ctx, invocation.Request.Workspace, gitPath, "rev-parse", "--verify", "HEAD")
		if err != nil {
			return ActionResult{}, err
		}
		if strings.TrimSpace(string(result)) != invocation.Binding.DefinitionCommit {
			return ActionResult{}, errors.New("checked-out commit does not match immutable definition commit")
		}
		return commandEvidence(commandID, result), nil
	case "manifest-validate":
		return runFixedCommand(ctx, invocation, commandID, scannerToolsPath, "validate")
	case "generated-parity":
		output, err := runBoundedWithEnvironment(
			ctx, invocation.Request.Workspace, syncContextPath,
			map[string]string{"GOTOOLCHAIN": "local", "GOPROXY": "off", "GOSUMDB": "off"},
			"--check", "--root", invocation.Request.Workspace,
		)
		if err != nil {
			return ActionResult{}, err
		}
		return commandEvidence(commandID, output), nil
	case "update-source-recheck":
		return runFixedCommand(
			ctx, invocation, commandID, scannerToolsPath,
			"upstream-images", "--platforms", "linux/amd64,linux/arm64",
		)
	case "lock-reproducibility":
		return runFixedCommand(
			ctx, invocation, commandID, scannerToolsPath,
			"lock", "--check", "--require-resolved",
		)
	case "license-metadata":
		return runFixedCommand(ctx, invocation, commandID, scannerToolsPath, "validate")
	case "release-manifest":
		return assembleReleaseManifest(invocation)
	case "policy-evaluation":
		return assemblePolicyInput(invocation)
	case "policy-decision-artifact":
		return assemblePolicyDecisionArtifact(invocation)
	case "candidate-evidence-summary":
		return assemblePublicationReceipt(invocation, lock)
	default:
		return ActionResult{}, fmt.Errorf("fixed adapter action %q is not implemented", invocation.Action.Name)
	}
}

func assemblePolicyDecisionArtifact(
	invocation scannerreleasebackend.Invocation,
) (ActionResult, error) {
	results, err := workspaceResults(invocation)
	if err != nil {
		return ActionResult{}, err
	}
	result, ok := results["policy-evaluation"]
	if !ok || result.PolicyDecision == nil || !digest(result.OutputDigest) ||
		result.PolicyDecision.PolicyDecisionDigest != result.OutputDigest {
		return ActionResult{}, errors.New("trusted policy decision evidence is absent or mismatched")
	}
	payload, err := json.Marshal(result.PolicyDecision)
	if err != nil {
		return ActionResult{}, errors.New("encode trusted policy decision")
	}
	return ActionResult{
		Payload:   payload,
		MediaType: "application/vnd.wolf.scanner-policy-decision.v1+json",
		Summary: map[string]any{
			"policy_decision_digest": result.OutputDigest,
			"outcome":                result.PolicyDecision.Outcome,
		},
	}, nil
}

func runFixedCommand(
	ctx context.Context,
	invocation scannerreleasebackend.Invocation,
	commandID, path string,
	args ...string,
) (ActionResult, error) {
	result, err := runBounded(ctx, invocation.Request.Workspace, path, args...)
	if err != nil {
		return ActionResult{}, err
	}
	return commandEvidence(commandID, result), nil
}

func executeQuality(
	ctx context.Context,
	invocation scannerreleasebackend.Invocation,
	commandID string,
	lock *scannerlock.Lock,
) (ActionResult, error) {
	action := invocation.Action.Name
	if action == "finding-regression" {
		return assembleFindingRegression(invocation)
	}
	if action == "aggregate-sbom" {
		return aggregateSBOM(ctx, invocation)
	}
	if action == "fixer-integration" {
		return runFixerIntegration(ctx, invocation, commandID, lock)
	}
	imageKey := actionImage(action)
	if imageKey == "" {
		return ActionResult{}, fmt.Errorf("quality adapter action %q has no image binding", action)
	}
	imageURI, imageDigest, err := imageDependency(invocation, imageKey)
	if err != nil {
		return ActionResult{}, err
	}
	reference := strings.TrimPrefix(imageURI, "oci://")
	var output []byte
	switch {
	case strings.HasPrefix(action, "strict-version-smoke/"):
		selection, err := releaseVariant(lock, imageKey)
		if err != nil {
			return ActionResult{}, err
		}
		combined := make([]byte, 0)
		for _, platform := range selection.Platforms {
			args := []string{
				"run", "--rm", "--pull=always", "--platform", platform,
				"--network", "none", "--read-only", "--cap-drop", "ALL",
				"--security-opt", "no-new-privileges=true",
			}
			if strings.HasPrefix(imageKey, "fixer-") {
				if len(selection.SmokeCommand) == 0 {
					return ActionResult{}, fmt.Errorf("fixer image %q has no compiled smoke command", imageKey)
				}
				args = append(args, reference)
				args = append(args, selection.SmokeCommand...)
			} else {
				args = append(args,
					"--env", "WOLF_SMOKE_STRICT=1",
					"--entrypoint", "/usr/local/bin/smoke-test.sh", reference,
				)
			}
			platformOutput, runErr := runBounded(
				ctx, invocation.Request.Workspace, dockerPath, args...,
			)
			if runErr != nil {
				return ActionResult{}, fmt.Errorf("strict smoke %s on %s: %w", imageKey, platform, runErr)
			}
			combined = append(combined, []byte(platform+"\x00"+sha256Digest(platformOutput)+"\n")...)
		}
		output = combined
	case strings.HasPrefix(action, "invocation-smoke/"), strings.HasPrefix(action, "fixer-auth-contract/"):
		output, err = runBounded(
			ctx, invocation.Request.Workspace, dockerPath,
			"image", "inspect", "--format", "{{json .Config}}", reference,
		)
		if err != nil {
			return ActionResult{}, err
		}
		if err := validateImageRuntimeContract(action, imageKey, output, lock); err != nil {
			return ActionResult{}, err
		}
	case strings.HasPrefix(action, "candidate-stable-comparison/"):
		return executeMeasuredQualityComparison(
			ctx, invocation, imageKey, reference, imageDigest, lock,
		)
	case strings.HasPrefix(action, "recorded-resource-gate/"):
		return measuredResourceGate(invocation, imageKey, imageDigest)
	case strings.HasPrefix(action, "parser-fixtures/"),
		strings.HasPrefix(action, "normalized-golden/"):
		output, err = runBounded(ctx, invocation.Request.Workspace, scannerToolsPath, "quality")
		if err != nil {
			return ActionResult{}, err
		}
	case strings.HasPrefix(action, "vulnerability-scan/"):
		cache, database, err := materializeLockedTrivyDatabases(ctx, invocation)
		if err != nil {
			return ActionResult{}, err
		}
		output, err = runBoundedReport(ctx, invocation.Request.Workspace, trivyPath,
			"image", "--scanners", "vuln", "--severity", "HIGH,CRITICAL",
			"--ignore-unfixed", "--exit-code", "0", "--format", "json",
			"--cache-dir", cache, "--skip-db-update", "--skip-java-db-update", reference,
		)
		if err != nil {
			return ActionResult{}, err
		}
		summary, err := trivyVulnerabilitySummary(output, database)
		if err != nil {
			return ActionResult{}, err
		}
		summary["image"] = imageKey
		summary["image_digest"] = imageDigest
		return ActionResult{
			Payload: output, MediaType: "application/vnd.aquasecurity.trivy.report+json",
			Summary: summary,
		}, nil
	case strings.HasPrefix(action, "vulnerability-db-identity/"):
		_, database, err := materializeLockedTrivyDatabases(ctx, invocation)
		if err != nil {
			return ActionResult{}, err
		}
		output, err = json.Marshal(database)
		if err != nil {
			return ActionResult{}, err
		}
		return ActionResult{
			Payload: output, MediaType: "application/vnd.wolf.scanner-trivy-db-evidence.v1+json",
			Summary: map[string]any{
				"image": imageKey, "image_digest": imageDigest,
				"database_identity":      database.Identity,
				"java_database_identity": database.JavaIdentity,
				"read_back_verified":     true,
			},
		}, nil
	case strings.HasPrefix(action, "secret-scan/"):
		output, err = runBoundedReport(ctx, invocation.Request.Workspace, trivyPath,
			"image", "--scanners", "secret", "--exit-code", "0", "--format", "json", reference,
		)
		if err != nil {
			return ActionResult{}, err
		}
		count, err := trivySecretCount(output)
		if err != nil {
			return ActionResult{}, err
		}
		return ActionResult{
			Payload: output, MediaType: "application/vnd.aquasecurity.trivy.report+json",
			Summary: map[string]any{
				"image": imageKey, "image_digest": imageDigest, "secret_count": count,
			},
		}, nil
	case strings.HasPrefix(action, "license-scan/"):
		output, err = runBoundedReport(ctx, invocation.Request.Workspace, trivyPath,
			"image", "--scanners", "license", "--exit-code", "0", "--format", "json", reference,
		)
		if err != nil {
			return ActionResult{}, err
		}
		licenses, unknown, err := trivyLicenseSummary(output)
		if err != nil {
			return ActionResult{}, err
		}
		return ActionResult{
			Payload: output, MediaType: "application/vnd.aquasecurity.trivy.report+json",
			Summary: map[string]any{
				"image": imageKey, "image_digest": imageDigest,
				"detected_licenses": licenses, "unknown_licenses": unknown,
			},
		}, nil
	case strings.HasPrefix(action, "sbom/"):
		output, err = runBoundedReport(ctx, invocation.Request.Workspace, trivyPath,
			"image", "--format", "spdx-json", reference,
		)
		if err != nil {
			return ActionResult{}, err
		}
		namespace, err := validateSPDXDocument(output)
		if err != nil {
			return ActionResult{}, fmt.Errorf("validate generated SPDX SBOM: %w", err)
		}
		return ActionResult{
			Payload: output, MediaType: "application/spdx+json",
			SubjectURI: imageURI, SubjectDigest: imageDigest,
			Summary: map[string]any{
				"image": imageKey, "subject_digest": imageDigest,
				"document_namespace": namespace,
			},
		}, nil
	case strings.HasPrefix(action, "oci-annotations/"):
		output, err = runBounded(ctx, invocation.Request.Workspace, orasPath,
			"manifest", "fetch", reference,
		)
		if err != nil {
			return ActionResult{}, err
		}
		var manifestIdentity struct {
			MediaType string `json:"mediaType"`
		}
		if json.Unmarshal(output, &manifestIdentity) != nil ||
			strings.TrimSpace(manifestIdentity.MediaType) == "" ||
			sha256Digest(output) != imageDigest {
			return ActionResult{}, errors.New("OCI annotation fetch did not return the exact image manifest bytes")
		}
		payload, err := json.Marshal(map[string]any{
			"schema_version": "wolf.scanner-oci-annotation-verification/v1",
			"image":          imageKey, "subject_digest": imageDigest,
			"manifest_media_type": manifestIdentity.MediaType,
		})
		if err != nil {
			return ActionResult{}, err
		}
		return ActionResult{
			Payload: payload, MediaType: "application/vnd.wolf.scanner-oci-annotation-verification.v1+json",
			SubjectURI: imageURI, SubjectDigest: imageDigest,
			ImageManifestPayload: output, ImageManifestMediaType: manifestIdentity.MediaType,
			Summary: map[string]any{
				"image": imageKey, "image_digest": imageDigest,
				"manifest_media_type": manifestIdentity.MediaType,
			},
		}, nil
	case strings.HasPrefix(action, "provenance/"):
		return provenanceVerification(invocation, imageKey, imageURI, imageDigest)
	default:
		return ActionResult{}, fmt.Errorf("quality adapter action %q is not implemented", action)
	}
	result := commandEvidence(commandID, output)
	result.Summary["image"] = imageKey
	result.Summary["image_digest"] = imageDigest
	return result, nil
}

func provenanceVerification(
	invocation scannerreleasebackend.Invocation,
	image, subjectURI, subjectDigest string,
) (ActionResult, error) {
	results, err := workspaceResults(invocation)
	if err != nil {
		return ActionResult{}, err
	}
	manifest, ok := results["image-manifest/"+image]
	if !ok || manifest.OutputURI != subjectURI || manifest.OutputDigest != subjectDigest {
		return ActionResult{}, errors.New("provenance image-manifest evidence is absent or mismatched")
	}
	var summary struct {
		PlatformDigests   map[string]string `json:"platform_digests"`
		BuildAttestations map[string]map[string]struct {
			ManifestDigest string `json:"manifest_digest"`
			PayloadDigest  string `json:"payload_digest"`
			PredicateType  string `json:"predicate_type"`
			SubjectDigest  string `json:"subject_digest"`
			BuilderID      string `json:"builder_id"`
		} `json:"build_attestations"`
		ReadBackVerified bool `json:"read_back_verified"`
	}
	value, err := json.Marshal(manifest.Summary)
	if err != nil || json.Unmarshal(value, &summary) != nil || !summary.ReadBackVerified ||
		len(summary.PlatformDigests) == 0 || len(summary.BuildAttestations) != len(summary.PlatformDigests) {
		return ActionResult{}, errors.New("provenance Buildx attestation inventory is invalid")
	}
	platforms := make([]string, 0, len(summary.PlatformDigests))
	for platform, runnableDigest := range summary.PlatformDigests {
		attestation := summary.BuildAttestations[platform]["provenance"]
		if !digest(runnableDigest) || !digest(attestation.ManifestDigest) ||
			!digest(attestation.PayloadDigest) || attestation.SubjectDigest != runnableDigest ||
			(attestation.PredicateType != "https://slsa.dev/provenance/v0.2" &&
				attestation.PredicateType != "https://slsa.dev/provenance/v1") ||
			strings.TrimSpace(attestation.BuilderID) == "" {
			return ActionResult{}, fmt.Errorf("provenance for platform %q is incomplete", platform)
		}
		platforms = append(platforms, platform)
	}
	sort.Strings(platforms)
	type platformProvenance struct {
		Platform                  string `json:"platform"`
		RunnableDigest            string `json:"runnable_digest"`
		AttestationManifestDigest string `json:"attestation_manifest_digest"`
		AttestationPayloadDigest  string `json:"attestation_payload_digest"`
		PredicateType             string `json:"predicate_type"`
		BuilderID                 string `json:"builder_id"`
	}
	entries := make([]platformProvenance, 0, len(platforms))
	for _, platform := range platforms {
		attestation := summary.BuildAttestations[platform]["provenance"]
		entries = append(entries, platformProvenance{
			Platform: platform, RunnableDigest: summary.PlatformDigests[platform],
			AttestationManifestDigest: attestation.ManifestDigest,
			AttestationPayloadDigest:  attestation.PayloadDigest,
			PredicateType:             attestation.PredicateType, BuilderID: attestation.BuilderID,
		})
	}
	payload, err := json.Marshal(struct {
		SchemaVersion string               `json:"schema_version"`
		Image         string               `json:"image"`
		SubjectURI    string               `json:"subject_uri"`
		SubjectDigest string               `json:"subject_digest"`
		Platforms     []platformProvenance `json:"platforms"`
	}{
		SchemaVersion: "wolf.scanner-provenance-verification/v1",
		Image:         image, SubjectURI: subjectURI, SubjectDigest: subjectDigest,
		Platforms: entries,
	})
	if err != nil {
		return ActionResult{}, err
	}
	return ActionResult{
		Payload:    payload,
		MediaType:  "application/vnd.wolf.scanner-provenance-verification.v1+json",
		SubjectURI: subjectURI, SubjectDigest: subjectDigest,
		Summary: map[string]any{
			"image": image, "subject_digest": subjectDigest,
			"platform_count": len(entries),
		},
	}, nil
}

func validateImageRuntimeContract(
	action, image string,
	value []byte,
	lock *scannerlock.Lock,
) error {
	var config struct {
		User       string            `json:"User"`
		Entrypoint []string          `json:"Entrypoint"`
		Labels     map[string]string `json:"Labels"`
	}
	if err := json.Unmarshal(value, &config); err != nil {
		return fmt.Errorf("decode image runtime contract: %w", err)
	}
	if strings.TrimSpace(config.User) == "" || config.User == "0" || config.User == "root" {
		return fmt.Errorf("image %q does not declare a non-root user", image)
	}
	if strings.HasPrefix(image, "fixer-") {
		variant, err := releaseVariant(lock, image)
		if err != nil {
			return err
		}
		if strings.HasPrefix(action, "fixer-auth-contract/") &&
			config.Labels["dev.wolf.fixer.auth-mode"] != variant.AuthMode {
			return fmt.Errorf("fixer image %q authentication boundary label mismatch", image)
		}
		return nil
	}
	if len(config.Entrypoint) != 1 || config.Entrypoint[0] != "/usr/local/bin/wolf-tool-entry" {
		return fmt.Errorf("scanner image %q entrypoint contract mismatch", image)
	}
	return nil
}

func executeIntegration(
	ctx context.Context,
	invocation scannerreleasebackend.Invocation,
	commandID string,
) (ActionResult, error) {
	name := map[string]string{
		"compose-integration":         "scanner-rollout-compose.sh",
		"compose-scanner-integration": "scanner-quality-compose.sh",
		"kubernetes-integration":      "scanner-rollout-kind.sh",
		"kind-scanner-integration":    "scanner-quality-kind.sh",
	}[invocation.Action.Name]
	if name == "" {
		return ActionResult{}, fmt.Errorf("integration adapter action %q is not implemented", invocation.Action.Name)
	}
	runtime := map[string]string{
		"compose-integration": "compose", "compose-scanner-integration": "compose",
		"kubernetes-integration": "kubernetes", "kind-scanner-integration": "kind",
	}[invocation.Action.Name]
	imageReferences := make(map[string]string)
	for key, evidence := range invocation.Request.Dependencies {
		if !strings.HasPrefix(key, "published-verify/") {
			continue
		}
		image := strings.TrimPrefix(key, "published-verify/")
		if !digest(evidence.OutputDigest) || !strings.HasPrefix(evidence.OutputURI, "oci://") ||
			!strings.Contains(evidence.OutputURI, "@"+evidence.OutputDigest) {
			return ActionResult{}, fmt.Errorf("integration image %q is not an immutable OCI reference", image)
		}
		imageReferences[image] = strings.TrimPrefix(evidence.OutputURI, "oci://")
	}
	if len(imageReferences) == 0 {
		return ActionResult{}, errors.New("integration action has no immutable image inventory")
	}
	defaultImage := imageReferences["default"]
	if defaultImage == "" {
		return ActionResult{}, errors.New("integration action has no immutable default scanner image")
	}
	environment := map[string]string{}
	switch invocation.Action.Name {
	case "compose-integration":
		environment["WOLF_RUN_ROLLOUT_COMPOSE_E2E"] = "1"
		environment["WOLF_ROLLOUT_COMPOSE_E2E_NEW_TAG"] = defaultImage
	case "compose-scanner-integration":
		environment["WOLF_RUN_SCANNER_COMPOSE_E2E"] = "1"
		environment["WOLF_SCANNER_E2E_IMAGE"] = defaultImage
	case "kubernetes-integration":
		environment["WOLF_RUN_ROLLOUT_KIND_E2E"] = "1"
		environment["WOLF_ROLLOUT_KIND_E2E_IMAGE"] = defaultImage
	case "kind-scanner-integration":
		environment["WOLF_RUN_SCANNER_KIND_E2E"] = "1"
		environment["WOLF_SCANNER_E2E_IMAGE"] = defaultImage
	}
	output, err := runBoundedWithEnvironment(
		ctx, invocation.Request.Workspace, filepath.Join(qualificationDir, name), environment,
	)
	if err != nil {
		return ActionResult{}, err
	}
	if strings.Contains(string(output), "SKIP:") {
		return ActionResult{}, errors.New("integration qualification returned a skip result")
	}
	result := commandEvidence(commandID, output)
	result.Runtime = runtime
	// These fixed scenarios exercise only the default scanner image. Never
	// represent unexecuted release images as integration coverage.
	result.ImageDigests = map[string]string{"default": invocation.Request.Dependencies["published-verify/default"].OutputDigest}
	return result, nil
}

func runFixerIntegration(
	ctx context.Context,
	invocation scannerreleasebackend.Invocation,
	commandID string,
	lock *scannerlock.Lock,
) (ActionResult, error) {
	variantNames := make([]string, 0, len(lock.ReleaseInputs.FixerVariants))
	for name := range lock.ReleaseInputs.FixerVariants {
		variantNames = append(variantNames, name)
	}
	sort.Strings(variantNames)
	if len(variantNames) == 0 {
		return ActionResult{}, errors.New("fixer integration lock has no fixer variants")
	}
	type platformEvidence struct {
		Image       string               `json:"image"`
		Platform    string               `json:"platform"`
		ImageURI    string               `json:"image_uri"`
		ImageDigest string               `json:"image_digest"`
		Report      qualification.Report `json:"report"`
	}
	reports := make([]platformEvidence, 0)
	for _, variantName := range variantNames {
		key := "published-verify/fixer-" + variantName
		evidence, exists := invocation.Request.Dependencies[key]
		if !exists {
			return ActionResult{}, fmt.Errorf("fixer integration is missing dependency %q", key)
		}
		image := "fixer-" + variantName
		if !digest(evidence.OutputDigest) || !strings.HasPrefix(evidence.OutputURI, "oci://") ||
			!strings.Contains(evidence.OutputURI, evidence.OutputDigest) {
			return ActionResult{}, fmt.Errorf("fixer integration image %q is not immutable", image)
		}
		variant, err := releaseVariant(lock, image)
		if err != nil {
			return ActionResult{}, err
		}
		if len(variant.Platforms) == 0 || strings.TrimSpace(variant.AuthMode) == "" {
			return ActionResult{}, fmt.Errorf("fixer integration image %q has incomplete runtime policy", image)
		}
		for _, platform := range variant.Platforms {
			args, err := fixerQualificationArgs(
				platform, strings.TrimPrefix(evidence.OutputURI, "oci://"), variantName, variant.AuthMode,
			)
			if err != nil {
				return ActionResult{}, err
			}
			output, err := runBounded(ctx, invocation.Request.Workspace, dockerPath, args...)
			if err != nil {
				return ActionResult{}, fmt.Errorf("fixer integration %s on %s: %w", image, platform, err)
			}
			report, err := decodeFixerQualificationReport(output)
			if err != nil {
				return ActionResult{}, fmt.Errorf("decode fixer integration %s on %s: %w", image, platform, err)
			}
			if err := qualification.ValidateReport(report, variantName, variant.AuthMode); err != nil {
				return ActionResult{}, fmt.Errorf("validate fixer integration %s on %s: %w", image, platform, err)
			}
			reports = append(reports, platformEvidence{
				Image: image, Platform: platform, ImageURI: evidence.OutputURI,
				ImageDigest: evidence.OutputDigest, Report: report,
			})
		}
	}
	payload, err := json.Marshal(struct {
		SchemaVersion string             `json:"schema_version"`
		Results       []platformEvidence `json:"results"`
	}{SchemaVersion: "wolf.fixer-integration-evidence/v1", Results: reports})
	if err != nil {
		return ActionResult{}, err
	}
	return ActionResult{
		Payload: payload, MediaType: "application/vnd.wolf.fixer-integration-evidence.v1+json",
		Summary: map[string]any{
			"command_id": commandID, "output_digest": sha256Digest(payload),
			"output_bytes": len(payload), "fixer_count": len(variantNames),
			"platform_count": len(reports),
		},
	}, nil
}

func fixerQualificationArgs(platform, reference, variant, authMode string) ([]string, error) {
	if (platform != "linux/amd64" && platform != "linux/arm64") ||
		!strings.Contains(reference, "@sha256:") || strings.ContainsAny(reference, " \t\r\n") ||
		strings.TrimSpace(variant) == "" || strings.TrimSpace(authMode) == "" {
		return nil, errors.New("fixer qualification platform, image, variant, or auth mode is invalid")
	}
	return []string{
		"run", "--rm", "--pull=always", "--platform", platform,
		"--network", "none", "--read-only", "--cap-drop", "ALL",
		"--security-opt", "no-new-privileges=true", "--pids-limit", "128",
		"--memory", "512m", "--cpus", "1",
		"--tmpfs", "/home/wolf:rw,noexec,nosuid,nodev,size=16777216",
		"--tmpfs", "/run/wolf-qualification:rw,nosuid,nodev,uid=1000,gid=1000,mode=0700,size=16777216",
		reference, "qualification", "--expected-variant", variant,
		"--expected-auth-mode", authMode, "--scratch", "/run/wolf-qualification",
	}, nil
}

func decodeFixerQualificationReport(value []byte) (qualification.Report, error) {
	var report qualification.Report
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&report); err != nil {
		return qualification.Report{}, err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return qualification.Report{}, errors.New("fixer qualification report has trailing JSON")
	}
	return report, nil
}

func commandEvidence(commandID string, output []byte) ActionResult {
	return ActionResult{Summary: map[string]any{
		"command_id": commandID, "output_digest": sha256Digest(output),
		"output_bytes": len(output),
	}}
}

func runBounded(ctx context.Context, directory, path string, args ...string) ([]byte, error) {
	return runBoundedWithEnvironment(ctx, directory, path, nil, args...)
}

// runBoundedReport keeps verbose evidence out of command stdout. Trusted
// report-producing CLIs write to a worker-owned scratch file which is then
// read through a traversal-safe root with the media-class evidence limit.
func runBoundedReport(
	ctx context.Context, directory, path string, args ...string,
) ([]byte, error) {
	if len(args) == 0 {
		return nil, errors.New("report command has no immutable subject argument")
	}
	scratch := strings.TrimSpace(os.Getenv("WOLF_SCANNER_RELEASE_SCRATCH_DIR"))
	if !filepath.IsAbs(scratch) {
		return nil, errors.New("report command requires an absolute scratch root")
	}
	file, err := os.CreateTemp(scratch, ".wolf-report-*.json")
	if err != nil {
		return nil, err
	}
	reportPath := file.Name()
	if err := file.Close(); err != nil {
		return nil, err
	}
	defer os.Remove(reportPath)
	last := args[len(args)-1]
	commandArgs := append([]string(nil), args[:len(args)-1]...)
	commandArgs = append(commandArgs, "--output", reportPath, last)
	if _, err := runBoundedWithEnvironment(ctx, directory, path, nil, commandArgs...); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(filepath.Dir(reportPath))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	report, err := root.Open(filepath.Base(reportPath))
	if err != nil {
		return nil, err
	}
	defer report.Close()
	info, err := report.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > evidencePayloadLimit {
		return nil, errors.New("report command output is empty, non-regular, or exceeds its media limit")
	}
	value, err := io.ReadAll(io.LimitReader(report, evidencePayloadLimit+1))
	if err != nil || int64(len(value)) != info.Size() {
		return nil, errors.New("report command output read is incomplete")
	}
	return value, nil
}

func runBoundedWithEnvironment(
	ctx context.Context,
	directory, path string,
	overrides map[string]string,
	args ...string,
) ([]byte, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("adapter executable path must be absolute")
	}
	command := exec.CommandContext(ctx, path, args...) // #nosec G204 -- path and argv come only from the compiled catalog above.
	command.Dir = directory
	environment, err := safeEnvironment(path)
	if err != nil {
		return nil, err
	}
	for name, value := range overrides {
		if name == "" || strings.ContainsAny(name, "=\x00") || strings.ContainsRune(value, '\x00') {
			return nil, errors.New("adapter command environment override is invalid")
		}
		environment = append(environment, name+"="+value)
	}
	sort.Strings(environment)
	command.Env = environment
	var stdout, stderr boundedBuffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err = command.Run()
	if stdout.exceeded || stderr.exceeded {
		return nil, errors.New("adapter command output exceeded its independent size bound")
	}
	if err != nil {
		return nil, fmt.Errorf("adapter command %q failed: %w", filepath.Base(path), err)
	}
	return stdout.value.Bytes(), nil
}

type boundedBuffer struct {
	value    bytes.Buffer
	exceeded bool
}

func (b *boundedBuffer) Write(value []byte) (int, error) {
	original := len(value)
	remaining := commandOutputMax - b.value.Len()
	if remaining <= 0 {
		b.exceeded = b.exceeded || len(value) > 0
		return original, nil
	}
	if len(value) > remaining {
		b.exceeded = true
		value = value[:remaining]
	}
	_, err := b.value.Write(value)
	return original, err
}

func safeEnvironment(executable string) ([]string, error) {
	allowed := map[string]bool{
		"PATH": true, "SSL_CERT_FILE": true, "SSL_CERT_DIR": true,
		"KUBECONFIG": true, "HOME": true,
	}
	result := make([]string, 0, len(allowed))
	for _, item := range os.Environ() {
		name, _, ok := strings.Cut(item, "=")
		if ok && allowed[name] {
			result = append(result, item)
		}
	}
	engineCredentialDir := strings.TrimSpace(os.Getenv("WOLF_SCANNER_RELEASE_ENGINE_CREDENTIAL_DIR"))
	if executable == dockerPath || strings.HasPrefix(executable, qualificationDir+string(filepath.Separator)) {
		registryCredentialDir := strings.TrimSpace(os.Getenv("WOLF_SCANNER_RELEASE_REGISTRY_CREDENTIAL_DIR"))
		if _, err := dockerCredentialFile(registryCredentialDir); err != nil {
			return nil, err
		}
		result = append(result, "DOCKER_CONFIG="+registryCredentialDir)
		engine, err := readEngineConfig(engineCredentialDir)
		if err != nil {
			return nil, err
		}
		result = append(result,
			"DOCKER_HOST="+engine.Host,
			"DOCKER_TLS_VERIFY=1",
			"DOCKER_CERT_PATH="+engineCredentialDir,
		)
		if strings.HasPrefix(executable, qualificationDir+string(filepath.Separator)) {
			if engine.KindAPIAddress == "" || engine.KindQualityAPIPort == 0 ||
				engine.KindRolloutAPIPort == 0 {
				return nil, errors.New("integration engine config requires reachable Kind API address and distinct ports")
			}
			result = append(result,
				"WOLF_KIND_API_ADDRESS="+engine.KindAPIAddress,
				"WOLF_KIND_QUALITY_API_PORT="+strconv.Itoa(engine.KindQualityAPIPort),
				"WOLF_KIND_ROLLOUT_API_PORT="+strconv.Itoa(engine.KindRolloutAPIPort),
			)
		}
	}
	sort.Strings(result)
	return result, nil
}

type engineConfig struct {
	SchemaVersion              string            `json:"schema_version"`
	Host                       string            `json:"host"`
	QualityNetwork             string            `json:"quality_network,omitempty"`
	QualityNetworkPolicyDigest string            `json:"quality_network_policy_digest,omitempty"`
	QualityTargets             map[string]string `json:"quality_targets,omitempty"`
	KindAPIAddress             string            `json:"kind_api_address,omitempty"`
	KindQualityAPIPort         int               `json:"kind_quality_api_port,omitempty"`
	KindRolloutAPIPort         int               `json:"kind_rollout_api_port,omitempty"`
}

func readEngineConfig(credentialDir string) (engineConfig, error) {
	if !filepath.IsAbs(credentialDir) {
		return engineConfig{}, errors.New("remote engine requires an absolute adapter credential directory")
	}
	value, err := readCredentialFile(credentialDir, "engine.json", 64<<10)
	if err != nil {
		return engineConfig{}, errors.New("remote engine requires a bounded regular engine.json")
	}
	var config engineConfig
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return engineConfig{}, fmt.Errorf("decode remote engine config: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return engineConfig{}, errors.New("remote engine config has trailing JSON")
	}
	parsed, err := url.Parse(config.Host)
	if config.SchemaVersion != "wolf.scanner-release-engine/v1" || err != nil ||
		parsed.Scheme != "tcp" || parsed.Hostname() == "" || parsed.Port() == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" {
		return engineConfig{}, errors.New("remote engine must be an explicit credential-free tcp host and port")
	}
	if config.KindAPIAddress != "" || config.KindQualityAPIPort != 0 || config.KindRolloutAPIPort != 0 {
		if net.ParseIP(config.KindAPIAddress) == nil ||
			config.KindQualityAPIPort < 1024 || config.KindQualityAPIPort > 65535 ||
			config.KindRolloutAPIPort < 1024 || config.KindRolloutAPIPort > 65535 ||
			config.KindQualityAPIPort == config.KindRolloutAPIPort {
			return engineConfig{}, errors.New("remote engine Kind API address or ports are invalid")
		}
	}
	if config.QualityNetwork != "" &&
		(!strings.HasPrefix(config.QualityNetwork, "wolf-quality-") ||
			len(config.QualityNetwork) > 63 || strings.ContainsAny(config.QualityNetwork, " \t\r\n/")) {
		return engineConfig{}, errors.New("remote engine quality network must be a dedicated wolf-quality-* network")
	}
	if (config.QualityNetwork == "") != (config.QualityNetworkPolicyDigest == "") ||
		(config.QualityNetworkPolicyDigest != "" && !digest(config.QualityNetworkPolicyDigest)) {
		return engineConfig{}, errors.New("remote engine quality network and exact policy digest must be configured together")
	}
	if len(config.QualityTargets) > 8 || (len(config.QualityTargets) > 0 && config.QualityNetwork == "") {
		return engineConfig{}, errors.New("remote engine quality targets require a controlled quality network")
	}
	for tool, target := range config.QualityTargets {
		parsedTarget, targetErr := url.Parse(target)
		port, portErr := strconv.Atoi(parsedTarget.Port())
		if tool != "nuclei" || targetErr != nil || parsedTarget.Scheme != "http" ||
			!strings.HasPrefix(parsedTarget.Hostname(), "wolf-quality-") ||
			portErr != nil || port < 1024 || port > 65535 || parsedTarget.User != nil ||
			parsedTarget.RawQuery != "" || parsedTarget.Fragment != "" ||
			(parsedTarget.Path != "" && parsedTarget.Path != "/") {
			return engineConfig{}, fmt.Errorf("remote engine quality target %q is not an approved internal fixture endpoint", tool)
		}
	}
	for _, name := range []string{"ca.pem", "cert.pem", "key.pem"} {
		if _, readErr := readCredentialFile(credentialDir, name, 1<<20); readErr != nil {
			return engineConfig{}, fmt.Errorf("remote engine credential %s is not a bounded regular file", name)
		}
	}
	return config, nil
}

// readCredentialFile uses os.Root's traversal-resistant resolver. Kubernetes
// projected Secret keys are symlinks into a versioned ..data directory, so a
// raw Lstat symlink ban would reject every real pod. Root.Open permits those
// in-volume links while refusing absolute links and relative escapes.
func readCredentialFile(directory, name string, maximum int64) ([]byte, error) {
	if !filepath.IsAbs(directory) || filepath.Base(name) != name || name == "." || maximum <= 0 {
		return nil, errors.New("credential file request is invalid")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maximum {
		return nil, errors.New("credential is not a bounded regular file")
	}
	value, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil || int64(len(value)) != info.Size() || int64(len(value)) > maximum {
		return nil, errors.New("credential read is incomplete or exceeds its bound")
	}
	return value, nil
}

type trivyDatabaseEvidence struct {
	SchemaVersion string    `json:"schema_version"`
	Identity      string    `json:"identity"`
	JavaIdentity  string    `json:"java_identity"`
	RecordedAt    time.Time `json:"recorded_at"`
	ExpiresAt     time.Time `json:"expires_at"`
	VerifiedAt    time.Time `json:"verified_at"`
}

func materializeLockedTrivyDatabases(
	ctx context.Context,
	invocation scannerreleasebackend.Invocation,
) (string, trivyDatabaseEvidence, error) {
	type databaseSpec struct {
		lockPath, cacheDirectory, databaseName string
	}
	specs := []databaseSpec{
		{scannerquality.DBLockPath, "db", "trivy.db"},
		{"scanners/quality/trivy-java-db.lock.json", "java-db", "trivy-java.db"},
	}
	credentialDir := strings.TrimSpace(os.Getenv("WOLF_SCANNER_RELEASE_REGISTRY_CREDENTIAL_DIR"))
	credentialFile, err := dockerCredentialFile(credentialDir)
	if err != nil {
		return "", trivyDatabaseEvidence{}, err
	}
	scratch := strings.TrimSpace(os.Getenv("WOLF_SCANNER_RELEASE_SCRATCH_DIR"))
	if !filepath.IsAbs(scratch) {
		return "", trivyDatabaseEvidence{}, errors.New("Trivy database scratch root must be absolute")
	}
	cache := filepath.Join(scratch, "trivy-cache")
	if err := os.RemoveAll(cache); err != nil {
		return "", trivyDatabaseEvidence{}, err
	}
	if err := os.MkdirAll(cache, 0o700); err != nil {
		return "", trivyDatabaseEvidence{}, err
	}
	now := time.Now().UTC()
	locks := make([]scannerquality.DBLock, 0, len(specs))
	for _, spec := range specs {
		lock, err := readTrivyDatabaseLock(filepath.Join(invocation.Request.Workspace, spec.lockPath), now)
		if err != nil {
			return "", trivyDatabaseEvidence{}, err
		}
		locks = append(locks, lock)
		reference := lock.Repository + "@" + lock.Digest
		descriptor, err := runBoundedWithEnvironment(
			ctx, invocation.Request.Workspace, orasPath, nil,
			"manifest", "fetch", "--registry-config", credentialFile, "--descriptor", reference,
		)
		if err != nil {
			return "", trivyDatabaseEvidence{}, fmt.Errorf("verify locked Trivy database %s: %w", lock.Repository, err)
		}
		var observed struct {
			Digest string `json:"digest"`
			Size   int64  `json:"size"`
		}
		if err := json.Unmarshal(descriptor, &observed); err != nil ||
			observed.Digest != lock.Digest || observed.Size <= 0 {
			return "", trivyDatabaseEvidence{}, fmt.Errorf("locked Trivy database %s readback mismatch", lock.Repository)
		}
		stage := filepath.Join(scratch, "trivy-oci-"+spec.cacheDirectory)
		if err := os.RemoveAll(stage); err != nil {
			return "", trivyDatabaseEvidence{}, err
		}
		if err := os.MkdirAll(stage, 0o700); err != nil {
			return "", trivyDatabaseEvidence{}, err
		}
		if _, err := runBoundedWithEnvironment(
			ctx, invocation.Request.Workspace, orasPath, nil,
			"pull", "--registry-config", credentialFile, "--output", stage, reference,
		); err != nil {
			return "", trivyDatabaseEvidence{}, fmt.Errorf("pull locked Trivy database %s: %w", lock.Repository, err)
		}
		archive, err := findSingleGzipArchive(stage)
		if err != nil {
			return "", trivyDatabaseEvidence{}, fmt.Errorf("locate locked Trivy database %s: %w", lock.Repository, err)
		}
		if err := extractTrivyDatabaseArchive(
			archive, filepath.Join(cache, spec.cacheDirectory), spec.databaseName,
		); err != nil {
			return "", trivyDatabaseEvidence{}, fmt.Errorf("extract locked Trivy database %s: %w", lock.Repository, err)
		}
	}
	evidence := trivyDatabaseEvidence{
		SchemaVersion: "wolf.scanner-trivy-db-evidence/v1",
		Identity:      locks[0].Repository + "@" + locks[0].Digest,
		JavaIdentity:  locks[1].Repository + "@" + locks[1].Digest,
		RecordedAt:    locks[0].RecordedAt, ExpiresAt: locks[0].ExpiresAt,
		VerifiedAt: now,
	}
	return cache, evidence, nil
}

func readTrivyDatabaseLock(path string, now time.Time) (scannerquality.DBLock, error) {
	value, err := os.ReadFile(path)
	if err != nil || len(value) == 0 || len(value) > 64<<10 {
		return scannerquality.DBLock{}, errors.New("Trivy database lock is missing or exceeds its bound")
	}
	var lock scannerquality.DBLock
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&lock); err != nil {
		return scannerquality.DBLock{}, fmt.Errorf("decode Trivy database lock: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return scannerquality.DBLock{}, errors.New("Trivy database lock has trailing JSON")
	}
	if lock.SchemaVersion != "wolf.scanners/vulnerability-db-lock/v1" ||
		(lock.Provider != "trivy") ||
		(lock.Repository != "ghcr.io/aquasecurity/trivy-db" &&
			lock.Repository != "ghcr.io/aquasecurity/trivy-java-db") ||
		!digest(lock.Digest) || lock.RecordedAt.IsZero() || lock.ExpiresAt.IsZero() ||
		lock.RecordedAt.After(now.Add(5*time.Minute)) || !now.Before(lock.ExpiresAt) {
		return scannerquality.DBLock{}, errors.New("Trivy database lock identity is invalid or stale")
	}
	return lock, nil
}

func findSingleGzipArchive(root string) (string, error) {
	var archives []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("Trivy OCI database contains a symlink")
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 1<<30 {
			return errors.New("Trivy OCI database contains an invalid file")
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		header := make([]byte, 2)
		_, readErr := io.ReadFull(file, header)
		closeErr := file.Close()
		if readErr == nil && header[0] == 0x1f && header[1] == 0x8b {
			archives = append(archives, path)
		}
		if closeErr != nil {
			return closeErr
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(archives) != 1 {
		return "", fmt.Errorf("expected one gzip layer, found %d", len(archives))
	}
	return archives[0], nil
}

func extractTrivyDatabaseArchive(archivePath, target, databaseName string) error {
	archive, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer archive.Close()
	compressed, err := gzip.NewReader(io.LimitReader(archive, 1<<30))
	if err != nil {
		return err
	}
	defer compressed.Close()
	if err := os.MkdirAll(target, 0o700); err != nil {
		return err
	}
	wanted := map[string]string{databaseName: databaseName, "metadata.json": "metadata.json"}
	found := make(map[string]bool, len(wanted))
	reader := tar.NewReader(compressed)
	var total int64
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			return errors.New("Trivy database archive contains a non-regular entry")
		}
		name := filepath.Base(filepath.Clean(header.Name))
		destinationName, ok := wanted[name]
		if !ok || filepath.Clean(header.Name) != name || found[name] ||
			header.Size <= 0 || header.Size > 1<<30 {
			return errors.New("Trivy database archive entry is unexpected or unsafe")
		}
		total += header.Size
		if total > 1<<30 {
			return errors.New("Trivy database archive exceeds its extraction bound")
		}
		destination, err := os.OpenFile(
			filepath.Join(target, destinationName), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600,
		)
		if err != nil {
			return err
		}
		written, copyErr := io.CopyN(destination, reader, header.Size)
		closeErr := destination.Close()
		if copyErr != nil || written != header.Size || closeErr != nil {
			return errors.New("Trivy database archive extraction was incomplete")
		}
		found[name] = true
	}
	for name := range wanted {
		if !found[name] {
			return fmt.Errorf("Trivy database archive is missing %s", name)
		}
	}
	return nil
}

type trivyReport struct {
	SchemaVersion int `json:"SchemaVersion"`
	Results       []struct {
		Vulnerabilities []struct {
			Severity string `json:"Severity"`
		} `json:"Vulnerabilities"`
		Secrets  []json.RawMessage `json:"Secrets"`
		Licenses []struct {
			Name string `json:"Name"`
		} `json:"Licenses"`
	} `json:"Results"`
}

func decodeTrivyReport(value []byte) (trivyReport, error) {
	var report trivyReport
	if len(value) == 0 || len(value) > commandOutputMax {
		return report, errors.New("Trivy report is empty or exceeds its bound")
	}
	if err := json.Unmarshal(value, &report); err != nil || report.SchemaVersion < 2 || report.Results == nil {
		return report, errors.New("Trivy report schema or result inventory is invalid")
	}
	return report, nil
}

func trivyVulnerabilitySummary(value []byte, database trivyDatabaseEvidence) (map[string]any, error) {
	report, err := decodeTrivyReport(value)
	if err != nil {
		return nil, err
	}
	critical, high := 0, 0
	for _, result := range report.Results {
		for _, vulnerability := range result.Vulnerabilities {
			switch strings.ToUpper(strings.TrimSpace(vulnerability.Severity)) {
			case "CRITICAL":
				critical++
			case "HIGH":
				high++
			}
		}
	}
	return map[string]any{
		"critical": critical, "high": high,
		"database_identity":      database.Identity,
		"java_database_identity": database.JavaIdentity,
	}, nil
}

func trivySecretCount(value []byte) (int, error) {
	report, err := decodeTrivyReport(value)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, result := range report.Results {
		count += len(result.Secrets)
	}
	return count, nil
}

func trivyLicenseSummary(value []byte) ([]string, int, error) {
	report, err := decodeTrivyReport(value)
	if err != nil {
		return nil, 0, err
	}
	seen := make(map[string]bool)
	unknown := 0
	for _, result := range report.Results {
		for _, license := range result.Licenses {
			name := strings.TrimSpace(license.Name)
			if name == "" || strings.EqualFold(name, "unknown") {
				unknown++
				continue
			}
			seen[name] = true
		}
	}
	licenses := make([]string, 0, len(seen))
	for name := range seen {
		licenses = append(licenses, name)
	}
	sort.Strings(licenses)
	return licenses, unknown, nil
}

func actionImage(action string) string {
	prefixes := []string{
		"strict-version-smoke/", "invocation-smoke/", "fixer-auth-contract/",
		"parser-fixtures/", "normalized-golden/", "candidate-stable-comparison/",
		"recorded-resource-gate/", "vulnerability-scan/", "vulnerability-db-identity/",
		"secret-scan/", "license-scan/", "sbom/", "oci-annotations/", "provenance/",
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(action, prefix) {
			return strings.TrimPrefix(action, prefix)
		}
	}
	return ""
}

func imageDependency(invocation scannerreleasebackend.Invocation, image string) (string, string, error) {
	dependency, exists := invocation.Request.Dependencies["image-manifest/"+image]
	if !exists || !digest(dependency.OutputDigest) ||
		!strings.Contains(dependency.OutputURI, dependency.OutputDigest) {
		return "", "", errors.New("quality action has no immutable image-manifest dependency")
	}
	return dependency.OutputURI, dependency.OutputDigest, nil
}

func releaseVariant(lock *scannerlock.Lock, image string) (scannerlock.BuildVariant, error) {
	if variant, ok := lock.ReleaseInputs.Variants[image]; ok {
		return variant, nil
	}
	if strings.HasPrefix(image, "fixer-") {
		if variant, ok := lock.ReleaseInputs.FixerVariants[strings.TrimPrefix(image, "fixer-")]; ok {
			return variant, nil
		}
	}
	return scannerlock.BuildVariant{}, fmt.Errorf("scanner lock has no image %q", image)
}

func aggregateSBOM(
	ctx context.Context,
	invocation scannerreleasebackend.Invocation,
) (ActionResult, error) {
	results, err := workspaceResults(invocation)
	if err != nil {
		return ActionResult{}, err
	}
	var images []scannerpipeline.Image
	decoder := json.NewDecoder(strings.NewReader(invocation.Request.PlatformsJSON))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&images); err != nil || len(images) == 0 {
		return ActionResult{}, errors.New("aggregate SBOM image inventory is invalid")
	}
	sort.Slice(images, func(i, j int) bool { return images[i].Key < images[j].Key })
	type externalDocumentReference struct {
		ExternalDocumentID string `json:"externalDocumentId"`
		SPDXDocument       string `json:"spdxDocument"`
		Checksum           struct {
			Algorithm     string `json:"algorithm"`
			ChecksumValue string `json:"checksumValue"`
		} `json:"checksum"`
	}
	type relationship struct {
		SPDXElementID      string `json:"spdxElementId"`
		RelationshipType   string `json:"relationshipType"`
		RelatedSPDXElement string `json:"relatedSpdxElement"`
	}
	references := make([]externalDocumentReference, 0, len(images))
	relationships := make([]relationship, 0, len(images))
	for _, image := range images {
		result, ok := results["sbom/"+image.Key]
		if !ok || !digest(result.OutputDigest) || result.OutputURI == "" {
			return ActionResult{}, fmt.Errorf("aggregate SBOM has no immutable SBOM for image %q", image.Key)
		}
		namespace, ok := result.Summary["document_namespace"].(string)
		if !ok || !absoluteDocumentURI(namespace) {
			return ActionResult{}, fmt.Errorf("aggregate SBOM image %q has no valid document namespace", image.Key)
		}
		externalID, err := spdxExternalDocumentID(image.Key)
		if err != nil {
			return ActionResult{}, err
		}
		reference := externalDocumentReference{
			ExternalDocumentID: externalID,
			SPDXDocument:       namespace,
		}
		reference.Checksum.Algorithm = "SHA256"
		reference.Checksum.ChecksumValue = strings.TrimPrefix(result.OutputDigest, "sha256:")
		references = append(references, reference)
		relationships = append(relationships, relationship{
			SPDXElementID: "SPDXRef-DOCUMENT", RelationshipType: "DESCRIBES",
			RelatedSPDXElement: externalID + ":SPDXRef-DOCUMENT",
		})
	}
	created, err := definitionCommitTime(ctx, invocation)
	if err != nil {
		return ActionResult{}, err
	}
	namespaceInput, err := json.Marshal(references)
	if err != nil {
		return ActionResult{}, err
	}
	namespaceDigest := strings.TrimPrefix(sha256Digest(namespaceInput), "sha256:")
	document := struct {
		SPDXVersion          string                      `json:"spdxVersion"`
		DataLicense          string                      `json:"dataLicense"`
		SPDXID               string                      `json:"SPDXID"`
		Name                 string                      `json:"name"`
		DocumentNamespace    string                      `json:"documentNamespace"`
		CreationInfo         map[string]any              `json:"creationInfo"`
		ExternalDocumentRefs []externalDocumentReference `json:"externalDocumentRefs"`
		Relationships        []relationship              `json:"relationships"`
	}{
		SPDXVersion: "SPDX-2.3", DataLicense: "CC0-1.0", SPDXID: "SPDXRef-DOCUMENT",
		Name: "wolf-scanners-release-" + invocation.Request.CandidateID,
		DocumentNamespace: "https://github.com/alphabravocompany/thewolf/spdx/scanner-releases/" +
			url.PathEscape(invocation.Request.CandidateID) + "/" + namespaceDigest,
		CreationInfo: map[string]any{
			"created":  created,
			"creators": []string{"Tool: wolf-scanner-release-quality-adapter"},
		},
		ExternalDocumentRefs: references, Relationships: relationships,
	}
	value, err := json.Marshal(document)
	if err != nil {
		return ActionResult{}, err
	}
	return ActionResult{Payload: value, MediaType: "application/spdx+json", Summary: map[string]any{
		"image_count": len(images), "document_namespace": document.DocumentNamespace,
	}}, nil
}

func validateSPDXDocument(value []byte) (string, error) {
	var document struct {
		SPDXVersion       string `json:"spdxVersion"`
		DataLicense       string `json:"dataLicense"`
		SPDXID            string `json:"SPDXID"`
		Name              string `json:"name"`
		DocumentNamespace string `json:"documentNamespace"`
		CreationInfo      struct {
			Created  string   `json:"created"`
			Creators []string `json:"creators"`
		} `json:"creationInfo"`
	}
	if err := json.Unmarshal(value, &document); err != nil {
		return "", err
	}
	if document.SPDXVersion != "SPDX-2.3" || document.DataLicense == "" ||
		document.SPDXID != "SPDXRef-DOCUMENT" || strings.TrimSpace(document.Name) == "" ||
		!absoluteDocumentURI(document.DocumentNamespace) || document.CreationInfo.Created == "" ||
		len(document.CreationInfo.Creators) == 0 {
		return "", errors.New("SPDX document identity or creation information is incomplete")
	}
	if _, err := time.Parse(time.RFC3339, document.CreationInfo.Created); err != nil {
		return "", errors.New("SPDX creation timestamp is not RFC3339")
	}
	return document.DocumentNamespace, nil
}

func absoluteDocumentURI(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.IsAbs() && parsed.Scheme != "file" && parsed.User == nil &&
		parsed.RawQuery == "" && parsed.Fragment == ""
}

func spdxExternalDocumentID(image string) (string, error) {
	if image == "" || len(image) > 80 {
		return "", errors.New("SPDX image identifier is invalid")
	}
	for _, character := range image {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') &&
			character != '.' && character != '-' {
			return "", errors.New("SPDX image identifier contains unsupported characters")
		}
	}
	return "DocumentRef-" + image, nil
}

func definitionCommitTime(
	ctx context.Context,
	invocation scannerreleasebackend.Invocation,
) (string, error) {
	value, err := runBounded(
		ctx, invocation.Request.Workspace, gitPath,
		"show", "-s", "--format=%cI", "--no-show-signature", invocation.Binding.DefinitionCommit,
	)
	if err != nil {
		return "", fmt.Errorf("read immutable definition commit timestamp: %w", err)
	}
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(string(value)))
	if err != nil {
		return "", errors.New("definition commit timestamp is not RFC3339")
	}
	return parsed.UTC().Format(time.RFC3339), nil
}

func workspaceResults(invocation scannerreleasebackend.Invocation) (map[string]scannerreleaseworker.StepResult, error) {
	binding := scannerreleaseworkspace.NewBinding(
		invocation.Request.BuildRunID, invocation.Request.CandidateID,
		invocation.Request.BuildAttempt, invocation.Binding.DefinitionCommit,
		invocation.Binding.LockDigest, invocation.Binding.PolicyID,
		invocation.Binding.PolicyRevision,
	)
	evidence, err := scannerreleaseworkspace.ReadAllEvidence(invocation.Request.Workspace, binding)
	if err != nil {
		return nil, err
	}
	results := make(map[string]scannerreleaseworker.StepResult, len(evidence))
	for key, item := range evidence {
		var result scannerreleaseworker.StepResult
		if err := item.DecodeResult(&result); err != nil {
			return nil, fmt.Errorf("decode workspace evidence %q: %w", key, err)
		}
		results[key] = result
	}
	return results, nil
}

func assembleReleaseManifest(invocation scannerreleasebackend.Invocation) (ActionResult, error) {
	results, err := workspaceResults(invocation)
	if err != nil {
		return ActionResult{}, err
	}
	type entry struct {
		Step   string `json:"step"`
		URI    string `json:"uri"`
		Digest string `json:"digest"`
	}
	entries := make([]entry, 0, len(results))
	for key, result := range results {
		entries = append(entries, entry{key, result.OutputURI, result.OutputDigest})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Step < entries[j].Step })
	payload, err := json.Marshal(struct {
		SchemaVersion string                        `json:"schema_version"`
		CandidateID   string                        `json:"candidate_id"`
		BuildRunID    string                        `json:"build_run_id"`
		Binding       scannerreleasebackend.Binding `json:"binding"`
		Evidence      []entry                       `json:"evidence"`
	}{
		"wolf.scanner-release-manifest/v1", invocation.Request.CandidateID,
		invocation.Request.BuildRunID, invocation.Binding, entries,
	})
	if err != nil {
		return ActionResult{}, err
	}
	return ActionResult{Payload: payload, MediaType: "application/vnd.wolf.scanner-release-manifest.v1+json",
		StorageIdentity: true, Summary: map[string]any{"evidence_count": len(entries)}}, nil
}

func assemblePolicyInput(invocation scannerreleasebackend.Invocation) (ActionResult, error) {
	results, err := workspaceResults(invocation)
	if err != nil {
		return ActionResult{}, err
	}
	var images []scannerpipeline.Image
	decoder := json.NewDecoder(strings.NewReader(invocation.Request.PlatformsJSON))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&images); err != nil || len(images) == 0 {
		return ActionResult{}, errors.New("policy input image inventory is invalid")
	}
	scannerCount, fixerCount := 0, 0
	for _, image := range images {
		if image.Kind == scannerpipeline.ImageKindFixer {
			fixerCount++
		} else {
			scannerCount++
		}
	}
	type gateRule struct {
		name     string
		minimum  int
		optional bool
		match    func(string) bool
	}
	prefix := func(prefixes ...string) func(string) bool {
		return func(key string) bool {
			for _, candidate := range prefixes {
				if key == candidate || strings.HasPrefix(key, candidate+"/") {
					return true
				}
			}
			return false
		}
	}
	rules := []gateRule{
		{"lock", 3, false, prefix("manifest-validate", "generated-parity", "lock-reproducibility")},
		{"artifacts", 3*len(images) + 1, false, prefix("image-manifest", "candidate-publish", "published-verify", "release-manifest")},
		{"platforms", len(images), false, prefix("image-manifest")},
		{"smoke", 2*len(images) + fixerCount, false, prefix("strict-version-smoke", "invocation-smoke", "fixer-auth-contract", "fixer-integration")},
		{"parser", 4*scannerCount + 1, false, prefix("parser-fixtures", "normalized-golden", "candidate-stable-comparison", "recorded-resource-gate", "finding-regression")},
		{"vulnerability", 2 * len(images), false, prefix("vulnerability-scan", "vulnerability-db-identity")},
		{"license", len(images) + 1, false, prefix("license-metadata", "license-scan")},
		{"sbom", len(images) + 1, false, prefix("sbom", "aggregate-sbom")},
		{"signature", len(images) + 1, false, prefix("signature", "release-manifest-signature")},
		{"provenance", len(images), false, prefix("provenance")},
		{"source", 2, false, prefix("checkout", "update-source-recheck")},
		{"secret_scan", len(images), false, prefix("secret-scan")},
		{"compose", 2, false, prefix("compose-integration", "compose-scanner-integration")},
		{"kubernetes", 2, false, prefix("kubernetes-integration", "kind-scanner-integration")},
	}
	gates := make([]scannerpolicy.Gate, 0, len(rules))
	for _, rule := range rules {
		matched := make(map[string]scannerreleaseworker.StepResult)
		for key, result := range results {
			if rule.match(key) {
				matched[key] = result
			}
		}
		if len(matched) == 0 && rule.optional {
			continue
		}
		if len(matched) < rule.minimum {
			return ActionResult{}, fmt.Errorf(
				"policy gate %q has %d evidence records, expected at least %d",
				rule.name, len(matched), rule.minimum,
			)
		}
		evidenceDigest, err := combinedEvidenceDigest(matched)
		if err != nil {
			return ActionResult{}, fmt.Errorf("policy gate %q: %w", rule.name, err)
		}
		gates = append(gates, scannerpolicy.Gate{
			Name: rule.name, Status: scannerpolicy.GatePassed,
			EvidenceDigest: evidenceDigest,
			Summary:        fmt.Sprintf("%d immutable evidence records passed", len(matched)),
		})
	}
	quality, err := measuredQualityEvidence(results, len(images))
	if err != nil {
		return ActionResult{}, err
	}
	if quality.SecretCount > 0 {
		for index := range gates {
			if gates[index].Name == "secret_scan" {
				gates[index].Status = scannerpolicy.GateFailed
				gates[index].Summary = fmt.Sprintf(
					"%d secrets detected in final images", quality.SecretCount,
				)
			}
		}
	}
	input := &scannerreleaseworker.PolicyInput{
		Gates: gates,
		Evidence: &scannerpolicy.Evidence{
			Vulnerabilities: scannerpolicy.VulnerabilityEvidence{
				Critical: quality.Critical, High: quality.High,
				DatabaseIdentity: quality.DatabaseIdentity,
			},
			Licenses: scannerpolicy.LicenseEvidence{
				Detected: quality.Licenses, Unknown: quality.UnknownLicenses,
			},
			ParserFailures: quality.ParserFailures,
			ExpectedLosses: quality.ExpectedFindingLosses,
			DurationDelta:  quality.DurationRegression,
			ResourceDelta:  quality.ResourceRegression,
		},
	}
	payload, err := json.Marshal(struct {
		SchemaVersion string                            `json:"schema_version"`
		Input         *scannerreleaseworker.PolicyInput `json:"policy_input"`
	}{"wolf.scanner-policy-input/v1", input})
	if err != nil {
		return ActionResult{}, err
	}
	return ActionResult{Payload: payload, MediaType: "application/vnd.wolf.scanner-policy-input.v1+json",
		PolicyInput: input, Summary: map[string]any{
			"gate_count": len(gates), "critical": quality.Critical, "high": quality.High,
			"secret_count": quality.SecretCount, "database_identity": quality.DatabaseIdentity,
			"detected_licenses": quality.Licenses, "unknown_licenses": quality.UnknownLicenses,
			"parser_failures":         quality.ParserFailures,
			"expected_finding_losses": quality.ExpectedFindingLosses,
			"duration_regression":     quality.DurationRegression,
			"resource_regression":     quality.ResourceRegression,
		}}, nil
}

type measuredQualitySummary struct {
	Critical, High, SecretCount            int
	UnknownLicenses, ParserFailures        int
	ExpectedFindingLosses                  int
	DurationRegression, ResourceRegression float64
	DatabaseIdentity                       string
	Licenses                               []string
}

func measuredQualityEvidence(
	results map[string]scannerreleaseworker.StepResult,
	expectedImages int,
) (measuredQualitySummary, error) {
	var measured measuredQualitySummary
	identities := make(map[string]bool)
	licenses := make(map[string]bool)
	vulnerabilityReports, databaseReports, licenseReports, secretReports := 0, 0, 0, 0
	for key, result := range results {
		switch {
		case strings.HasPrefix(key, "vulnerability-scan/"):
			critical, err := requiredSummaryInt(result.Summary, "critical")
			if err != nil {
				return measured, fmt.Errorf("%s: %w", key, err)
			}
			high, err := requiredSummaryInt(result.Summary, "high")
			if err != nil {
				return measured, fmt.Errorf("%s: %w", key, err)
			}
			identity, err := requiredSummaryString(result.Summary, "database_identity")
			if err != nil {
				return measured, fmt.Errorf("%s: %w", key, err)
			}
			measured.Critical += critical
			measured.High += high
			identities[identity] = true
			vulnerabilityReports++
		case strings.HasPrefix(key, "vulnerability-db-identity/"):
			identity, err := requiredSummaryString(result.Summary, "database_identity")
			if err != nil {
				return measured, fmt.Errorf("%s: %w", key, err)
			}
			verified, ok := result.Summary["read_back_verified"].(bool)
			if !ok || !verified {
				return measured, fmt.Errorf("%s: database identity was not read-back verified", key)
			}
			identities[identity] = true
			databaseReports++
		case strings.HasPrefix(key, "license-scan/"):
			values, err := requiredSummaryStrings(result.Summary, "detected_licenses")
			if err != nil {
				return measured, fmt.Errorf("%s: %w", key, err)
			}
			unknown, err := requiredSummaryInt(result.Summary, "unknown_licenses")
			if err != nil {
				return measured, fmt.Errorf("%s: %w", key, err)
			}
			for _, value := range values {
				licenses[value] = true
			}
			measured.UnknownLicenses += unknown
			licenseReports++
		case strings.HasPrefix(key, "secret-scan/"):
			count, err := requiredSummaryInt(result.Summary, "secret_count")
			if err != nil {
				return measured, fmt.Errorf("%s: %w", key, err)
			}
			measured.SecretCount += count
			secretReports++
		}
	}
	if vulnerabilityReports != expectedImages || databaseReports != expectedImages ||
		licenseReports != expectedImages || secretReports != expectedImages {
		return measured, fmt.Errorf(
			"measured final-image evidence coverage is vuln=%d db=%d license=%d secret=%d, expected %d each",
			vulnerabilityReports, databaseReports, licenseReports, secretReports, expectedImages,
		)
	}
	if len(identities) != 1 {
		return measured, errors.New("final-image scans do not share one exact vulnerability database identity")
	}
	for identity := range identities {
		if !strings.Contains(identity, "@sha256:") {
			return measured, errors.New("vulnerability database identity is not immutable")
		}
		measured.DatabaseIdentity = identity
	}
	for value := range licenses {
		measured.Licenses = append(measured.Licenses, value)
	}
	sort.Strings(measured.Licenses)
	regression, ok := results["finding-regression"]
	if !ok {
		return measured, errors.New("measured candidate-versus-stable evidence is absent")
	}
	var err error
	if measured.ParserFailures, err = requiredSummaryInt(regression.Summary, "parser_failures"); err != nil {
		return measured, err
	}
	if measured.ExpectedFindingLosses, err = requiredSummaryInt(regression.Summary, "expected_finding_losses"); err != nil {
		return measured, err
	}
	if measured.DurationRegression, err = requiredSummaryFloat(regression.Summary, "duration_regression"); err != nil {
		return measured, err
	}
	if measured.ResourceRegression, err = requiredSummaryFloat(regression.Summary, "resource_regression"); err != nil {
		return measured, err
	}
	return measured, nil
}

// assembleFindingRegression accepts only measured, per-image comparison
// summaries. A repository-policy check is intentionally insufficient here:
// the release policy needs the actual stable/candidate observations.
func assembleFindingRegression(
	invocation scannerreleasebackend.Invocation,
) (ActionResult, error) {
	results, err := workspaceResults(invocation)
	if err != nil {
		return ActionResult{}, err
	}
	keys := make([]string, 0)
	for key := range results {
		if strings.HasPrefix(key, "candidate-stable-comparison/") {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return ActionResult{}, errors.New("candidate-versus-stable comparison evidence is absent")
	}
	parserFailures, findingLosses := 0, 0
	durationRegression, resourceRegression := float64(0), float64(0)
	type comparison struct {
		Image                 string  `json:"image"`
		EvidenceDigest        string  `json:"evidence_digest"`
		ParserFailures        int     `json:"parser_failures"`
		ExpectedFindingLosses int     `json:"expected_finding_losses"`
		DurationRegression    float64 `json:"duration_regression"`
		ResourceRegression    float64 `json:"resource_regression"`
	}
	comparisons := make([]comparison, 0, len(keys))
	for _, key := range keys {
		result := results[key]
		if !immutableStepEvidence(result) {
			return ActionResult{}, fmt.Errorf("%s is not immutable comparison evidence", key)
		}
		measured, ok := result.Summary["measured"].(bool)
		if !ok || !measured {
			return ActionResult{}, fmt.Errorf("%s is not measured stable/candidate evidence", key)
		}
		failures, err := requiredSummaryInt(result.Summary, "parser_failures")
		if err != nil {
			return ActionResult{}, fmt.Errorf("%s: %w", key, err)
		}
		losses, err := requiredSummaryInt(result.Summary, "expected_finding_losses")
		if err != nil {
			return ActionResult{}, fmt.Errorf("%s: %w", key, err)
		}
		duration, err := requiredSummaryFloat(result.Summary, "duration_regression")
		if err != nil {
			return ActionResult{}, fmt.Errorf("%s: %w", key, err)
		}
		resource, err := requiredSummaryFloat(result.Summary, "resource_regression")
		if err != nil {
			return ActionResult{}, fmt.Errorf("%s: %w", key, err)
		}
		parserFailures += failures
		findingLosses += losses
		durationRegression = max(durationRegression, duration)
		resourceRegression = max(resourceRegression, resource)
		comparisons = append(comparisons, comparison{
			Image:          strings.TrimPrefix(key, "candidate-stable-comparison/"),
			EvidenceDigest: result.OutputDigest, ParserFailures: failures,
			ExpectedFindingLosses: losses, DurationRegression: duration,
			ResourceRegression: resource,
		})
	}
	payload, err := json.Marshal(struct {
		SchemaVersion string       `json:"schema_version"`
		Comparisons   []comparison `json:"comparisons"`
	}{"wolf.scanner-finding-regression/v1", comparisons})
	if err != nil {
		return ActionResult{}, err
	}
	return ActionResult{
		Payload:   payload,
		MediaType: "application/vnd.wolf.scanner-finding-regression.v1+json",
		Summary: map[string]any{
			"measured": true, "comparison_count": len(comparisons),
			"parser_failures":         parserFailures,
			"expected_finding_losses": findingLosses,
			"duration_regression":     durationRegression,
			"resource_regression":     resourceRegression,
		},
	}, nil
}

func requiredSummaryInt(summary map[string]any, key string) (int, error) {
	value, ok := summary[key]
	if !ok {
		return 0, fmt.Errorf("measured summary field %q is missing", key)
	}
	switch typed := value.(type) {
	case int:
		if typed >= 0 {
			return typed, nil
		}
	case float64:
		if typed >= 0 && typed == float64(int(typed)) {
			return int(typed), nil
		}
	}
	return 0, fmt.Errorf("measured summary field %q is not a non-negative integer", key)
}

func requiredSummaryFloat(summary map[string]any, key string) (float64, error) {
	value, ok := summary[key]
	if !ok {
		return 0, fmt.Errorf("measured summary field %q is missing", key)
	}
	switch typed := value.(type) {
	case float64:
		if typed >= 0 {
			return typed, nil
		}
	case int:
		if typed >= 0 {
			return float64(typed), nil
		}
	}
	return 0, fmt.Errorf("measured summary field %q is not a non-negative number", key)
}

func requiredSummaryString(summary map[string]any, key string) (string, error) {
	value, ok := summary[key].(string)
	if !ok || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("measured summary field %q is missing or invalid", key)
	}
	return value, nil
}

func requiredSummaryStrings(summary map[string]any, key string) ([]string, error) {
	raw, ok := summary[key]
	if !ok {
		return nil, fmt.Errorf("measured summary field %q is missing", key)
	}
	value, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var values []string
	if err := json.Unmarshal(value, &values); err != nil || values == nil {
		return nil, fmt.Errorf("measured summary field %q is invalid", key)
	}
	seen := make(map[string]bool, len(values))
	for _, item := range values {
		if strings.TrimSpace(item) == "" || seen[item] {
			return nil, fmt.Errorf("measured summary field %q has an empty or duplicate value", key)
		}
		seen[item] = true
	}
	sort.Strings(values)
	return values, nil
}

func combinedEvidenceDigest(results map[string]scannerreleaseworker.StepResult) (string, error) {
	keys := make([]string, 0, len(results))
	for key := range results {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	type entry struct {
		Step   string `json:"step"`
		URI    string `json:"uri"`
		Digest string `json:"digest"`
	}
	entries := make([]entry, 0, len(keys))
	for _, key := range keys {
		result := results[key]
		if !immutableStepEvidence(result) {
			return "", fmt.Errorf("step %q is not immutable evidence", key)
		}
		entries = append(entries, entry{key, result.OutputURI, result.OutputDigest})
	}
	value, err := json.Marshal(entries)
	if err != nil {
		return "", err
	}
	return sha256Digest(value), nil
}

func immutableStepEvidence(result scannerreleaseworker.StepResult) bool {
	if !digest(result.OutputDigest) || result.OutputURI == "" {
		return false
	}
	if strings.Contains(result.OutputURI, result.OutputDigest) {
		return true
	}
	raw, ok := result.Summary["adapter_evidence"]
	if !ok {
		return false
	}
	value, err := json.Marshal(raw)
	if err != nil {
		return false
	}
	var evidence struct {
		OutputIdentity string `json:"output_identity"`
		Artifact       struct {
			URI              string `json:"uri"`
			Digest           string `json:"digest"`
			PayloadDigest    string `json:"payload_digest"`
			StorageMediaType string `json:"storage_media_type"`
			StorageSizeBytes int64  `json:"storage_size_bytes"`
		} `json:"artifact"`
	}
	if err := json.Unmarshal(value, &evidence); err != nil {
		return false
	}
	if evidence.Artifact.URI != result.OutputURI || !digest(evidence.Artifact.Digest) ||
		!digest(evidence.Artifact.PayloadDigest) ||
		strings.TrimSpace(evidence.Artifact.StorageMediaType) == "" ||
		evidence.Artifact.StorageSizeBytes <= 0 ||
		!strings.Contains(evidence.Artifact.URI, evidence.Artifact.Digest) {
		return false
	}
	switch evidence.OutputIdentity {
	case "payload":
		return evidence.Artifact.PayloadDigest == result.OutputDigest
	case "storage":
		return evidence.Artifact.Digest == result.OutputDigest
	default:
		return false
	}
}

func gateDigest(gates []scannerpolicy.Gate, name string) string {
	for _, gate := range gates {
		if gate.Name == name {
			return gate.EvidenceDigest
		}
	}
	return ""
}

func assemblePublicationReceipt(
	invocation scannerreleasebackend.Invocation,
	lock *scannerlock.Lock,
) (ActionResult, error) {
	results, err := workspaceResults(invocation)
	if err != nil {
		return ActionResult{}, err
	}
	manifest, ok := results["release-manifest"]
	if !ok {
		return ActionResult{}, errors.New("publication receipt has no release manifest")
	}
	policy, ok := results["policy-evaluation"]
	if !ok || !digest(policy.OutputDigest) {
		return ActionResult{}, errors.New("publication receipt has no trusted policy decision")
	}
	signerIdentity, err := releaseSignerIdentity(results["release-manifest-signature"])
	if err != nil {
		return ActionResult{}, err
	}
	tools, err := releaseTools(lock)
	if err != nil {
		return ActionResult{}, err
	}
	images, err := releaseImages(invocation, results)
	if err != nil {
		return ActionResult{}, err
	}
	artifacts, err := releaseArtifacts(results)
	if err != nil {
		return ActionResult{}, err
	}
	receipt := scannerrelease.PublicationReceipt{
		SchemaVersion: scannerrelease.PublicationReceiptSchema,
		CandidateID:   invocation.Request.CandidateID, BuildRunID: invocation.Request.BuildRunID,
		DefinitionCommit: invocation.Binding.DefinitionCommit, LockDigest: invocation.Binding.LockDigest,
		PolicyID: invocation.Binding.PolicyID, PolicyRevision: invocation.Binding.PolicyRevision,
		PolicyDecisionDigest: policy.OutputDigest,
		ManifestDigest:       manifest.OutputDigest, ManifestURI: manifest.OutputURI,
		SignerIdentity: signerIdentity, Tools: tools, Images: images, Artifacts: artifacts,
	}
	if err := scannerrelease.ValidatePublicationReceiptInventory(receipt); err != nil {
		return ActionResult{}, err
	}
	canonical := scannerrelease.CanonicalPublicationReceipt(receipt)
	payload, err := json.Marshal(canonical)
	if err != nil {
		return ActionResult{}, err
	}
	receiptDigest, err := scannerrelease.PublicationReceiptDigest(canonical)
	if err != nil || receiptDigest != sha256Digest(payload) {
		return ActionResult{}, errors.New("publication receipt canonical digest mismatch")
	}
	return ActionResult{Payload: payload, MediaType: "application/vnd.wolf.scanner-publication-receipt.v1+json",
		Summary: map[string]any{"publication_receipt": canonical}}, nil
}

func releaseSignerIdentity(result scannerreleaseworker.StepResult) (string, error) {
	raw, ok := result.Summary["signing_evidence"]
	if !ok {
		return "", errors.New("release manifest signature has no signing evidence")
	}
	value, err := json.Marshal(raw)
	if err != nil {
		return "", err
	}
	var evidence struct {
		ObservedIdentity string `json:"observed_identity"`
		ExpectedIdentity string `json:"expected_identity"`
		Verified         bool   `json:"verified"`
	}
	if err := json.Unmarshal(value, &evidence); err != nil || !evidence.Verified {
		return "", errors.New("release manifest signature identity is not verified")
	}
	identity := strings.TrimSpace(evidence.ObservedIdentity)
	if identity == "" {
		identity = strings.TrimSpace(evidence.ExpectedIdentity)
	}
	if identity == "" {
		return "", errors.New("release manifest signer identity is empty")
	}
	return identity, nil
}

func releaseTools(lock *scannerlock.Lock) ([]scannerrelease.ReleaseTool, error) {
	if lock == nil {
		return nil, errors.New("release tool inventory has no scanner lock")
	}
	keys := make([]string, 0, len(lock.Tools))
	for key := range lock.Tools {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]scannerrelease.ReleaseTool, 0, len(keys))
	for _, key := range keys {
		tool := lock.Tools[key]
		version := strings.TrimSpace(tool.PinnedVersion)
		parser := strings.TrimSpace(tool.ParserContract.Status)
		if version == "" || releasePlaceholder(version) ||
			parser != "quality_policy" || strings.TrimSpace(tool.ParserContract.Format) == "" {
			return nil, fmt.Errorf("release tool %q has no exact version or parser contract", key)
		}
		imageKey := tool.Bucket
		if imageKey == "" {
			imageKey = "default"
		}
		source := firstString(tool.UpdateSource.Repository, tool.UpdateSource.Package,
			tool.UpdateSource.Module, strings.Trim(tool.UpdateSource.Owner+"/"+tool.UpdateSource.Repo, "/"),
			tool.UpdateSource.Channel)
		kind := "wolf"
		sourceDigest := strings.TrimSpace(tool.SourceIntegrity.SHA256)
		metadata := map[string]any{
			"image_key": imageKey, "kind": kind,
			"integration_tier":        tool.IntegrationTier,
			"platforms":               tool.Platforms,
			"parser_format":           tool.ParserContract.Format,
			"parser_fixtures":         tool.ParserContract.Fixtures,
			"source_integrity_status": tool.SourceIntegrity.Status,
		}
		if tool.IntegrationTier == "upstream" {
			image, ok := lock.UpstreamImages[key]
			if !ok || !digest(image.Digest) ||
				strings.TrimSpace(image.ResolvedReference) == "" ||
				!strings.Contains(image.ResolvedReference, "@"+image.Digest) ||
				(image.ResolutionStatus != "digest_pinned" && image.ResolutionStatus != "registry_resolved") ||
				len(image.Platforms) == 0 {
				return nil, fmt.Errorf("release tool %q upstream image identity is unresolved", key)
			}
			kind = "upstream"
			imageKey = "default"
			source = image.ResolvedReference
			sourceDigest = image.Digest
			metadata["image_key"] = imageKey
			metadata["kind"] = kind
			metadata["declared_reference"] = image.DeclaredReference
			metadata["resolved_reference"] = image.ResolvedReference
			metadata["resolved_digest"] = image.Digest
			metadata["resolution_status"] = image.ResolutionStatus
			metadata["platforms"] = image.Platforms
		} else if tool.IntegrationTier != "default" && tool.IntegrationTier != "bucket" {
			return nil, fmt.Errorf("release tool %q has unsupported integration tier %q", key, tool.IntegrationTier)
		}
		if strings.TrimSpace(source) == "" || releasePlaceholder(source) {
			return nil, fmt.Errorf("release tool %q source identity is incomplete", key)
		}
		encodedMetadata, err := json.Marshal(metadata)
		if err != nil {
			return nil, fmt.Errorf("encode release tool %q metadata: %w", key, err)
		}
		result = append(result, scannerrelease.ReleaseTool{
			ToolKey: key, Version: version,
			SourceReference: source, SourceDigest: sourceDigest,
			Checksum:            strings.TrimSpace(tool.SourceIntegrity.SHA256),
			ParserCompatibility: parser + ":" + tool.ParserContract.Format,
			MetadataJSON:        string(encodedMetadata),
		})
	}
	return result, nil
}

func releasePlaceholder(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "locked", "declared", "unknown", "unresolved", "latest":
		return true
	default:
		return false
	}
}

func releaseImages(
	invocation scannerreleasebackend.Invocation,
	results map[string]scannerreleaseworker.StepResult,
) ([]scannerrelease.ReleaseImage, error) {
	var images []scannerpipeline.Image
	if err := json.Unmarshal([]byte(invocation.Request.PlatformsJSON), &images); err != nil {
		return nil, err
	}
	context, err := scannerreleaseworkspace.ReadContext(invocation.Request.Workspace)
	if err != nil {
		return nil, err
	}
	mirrorImages, err := mirrorImageInventory(results["mirror-copy-verify"])
	if err != nil {
		return nil, err
	}
	result := make([]scannerrelease.ReleaseImage, 0, len(images)*2)
	for _, image := range images {
		manifest := results["image-manifest/"+image.Key]
		published := results["candidate-publish/"+image.Key]
		signature := results["signature/"+image.Key]
		sbom := results["sbom/"+image.Key]
		provenance := results["provenance/"+image.Key]
		if !digest(manifest.OutputDigest) || manifest.OutputDigest != published.OutputDigest ||
			!digest(signature.OutputDigest) || !digest(sbom.OutputDigest) || !digest(provenance.OutputDigest) {
			return nil, fmt.Errorf("publication image %q evidence is incomplete", image.Key)
		}
		platforms, err := summaryJSON(manifest.Summary, "platform_digests")
		if err != nil {
			return nil, fmt.Errorf("publication image %q platforms: %w", image.Key, err)
		}
		kind := scannerrelease.ReleaseImageScanner
		if image.Kind == scannerpipeline.ImageKindFixer {
			kind = scannerrelease.ReleaseImageFixer
		}
		primaryRepository, err := immutableRepository(published.OutputURI, manifest.OutputDigest)
		if err != nil {
			return nil, fmt.Errorf("publication image %q primary repository: %w", image.Key, err)
		}
		mirrorImage, ok := mirrorImages[image.Key]
		if !ok || mirrorImage.Digest != manifest.OutputDigest ||
			mirrorImage.RegistryTargetID != context.Mirror.ID {
			return nil, fmt.Errorf("publication image %q mirror inventory is incomplete", image.Key)
		}
		mirrorRepository, err := immutableRepository(mirrorImage.Reference, manifest.OutputDigest)
		if err != nil {
			return nil, fmt.Errorf("publication image %q mirror repository: %w", image.Key, err)
		}
		primarySignature, err := primaryReleaseSignature(signature, manifest.OutputDigest)
		if err != nil {
			return nil, fmt.Errorf("publication image %q primary signature: %w", image.Key, err)
		}
		for _, target := range []struct {
			id, repository string
			signature      releaseSignatureEvidence
		}{
			{context.Primary.ID, primaryRepository, primarySignature},
			{context.Mirror.ID, mirrorRepository, mirrorImage.signatureEvidence()},
		} {
			if err := target.signature.validate(manifest.OutputDigest); err != nil {
				return nil, fmt.Errorf("publication image %q signature for target %q: %w", image.Key, target.id, err)
			}
			result = append(result, scannerrelease.ReleaseImage{
				ImageKey: image.Key, ImageKind: kind, RegistryTargetID: target.id,
				Repository: target.repository,
				Digest:     manifest.OutputDigest, PlatformDigests: platforms,
				SignatureStatus:            "verified",
				SignatureDigest:            target.signature.Digest,
				SignatureArtifactURI:       target.signature.ArtifactURI,
				SignatureArtifactDigest:    target.signature.ArtifactDigest,
				SignatureMediaType:         target.signature.MediaType,
				SignatureArtifactSizeBytes: target.signature.SizeBytes,
				SignatureCertificateDigest: target.signature.CertificateDigest,
				SignatureIdentity:          target.signature.Identity,
				SignatureIssuer:            target.signature.Issuer,
				SignatureSubject:           target.signature.Subject,
				SignatureTrustRoot:         target.signature.TrustRoot,
				SignatureOperationID:       target.signature.OperationID,
				ProvenanceDigest:           provenance.OutputDigest,
				SBOMDigest:                 sbom.OutputDigest,
			})
		}
	}
	return result, nil
}

type mirrorImageEvidence struct {
	Reference             string `json:"reference"`
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

type releaseSignatureEvidence struct {
	Digest, ArtifactURI, ArtifactDigest, MediaType, CertificateDigest string
	Identity, Issuer, Subject, TrustRoot, OperationID                 string
	SizeBytes                                                         int64
}

func (e mirrorImageEvidence) signatureEvidence() releaseSignatureEvidence {
	return releaseSignatureEvidence{
		Digest: e.SignatureDigest, ArtifactURI: e.SignatureURI,
		ArtifactDigest: e.SignatureArtifact, MediaType: e.SignatureMediaType,
		SizeBytes: e.SignatureArtifactSize, CertificateDigest: e.CertificateDigest,
		Identity: e.SignerIdentity, Issuer: e.SignerIssuer, Subject: e.SignerSubject,
		TrustRoot: e.SignerTrustRoot, OperationID: e.SigningOperationID,
	}
}

func primaryReleaseSignature(
	result scannerreleaseworker.StepResult,
	subjectDigest string,
) (releaseSignatureEvidence, error) {
	raw, ok := result.Summary["signing_evidence"]
	if !ok {
		return releaseSignatureEvidence{}, errors.New("signing evidence is absent")
	}
	value, err := json.Marshal(raw)
	if err != nil {
		return releaseSignatureEvidence{}, err
	}
	var evidence struct {
		SchemaVersion           string `json:"schema_version"`
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
	if err := json.Unmarshal(value, &evidence); err != nil ||
		evidence.SchemaVersion != "wolf.scanner-signing-evidence/v1" || !evidence.Verified ||
		evidence.SignatureArtifactDigest != result.OutputDigest ||
		evidence.ArtifactDigest != subjectDigest || evidence.ArtifactSubjectDigest != subjectDigest {
		return releaseSignatureEvidence{}, errors.New("signing evidence subject or payload binding is invalid")
	}
	out := releaseSignatureEvidence{
		Digest: evidence.SignatureDigest, ArtifactURI: evidence.SignatureURI,
		ArtifactDigest: evidence.SignatureArtifactDigest, MediaType: evidence.SignatureMediaType,
		SizeBytes: evidence.SignatureArtifactSize, CertificateDigest: evidence.CertificateDigest,
		Identity: evidence.ObservedIdentity, Issuer: evidence.ObservedIssuer,
		Subject: evidence.ObservedSubject, TrustRoot: evidence.ObservedTrustRoot,
		OperationID: evidence.OperationID,
	}
	return out, out.validate(subjectDigest)
}

func (e releaseSignatureEvidence) validate(subjectDigest string) error {
	if !digest(e.Digest) || !digest(e.ArtifactDigest) || !digest(subjectDigest) ||
		!strings.Contains(e.ArtifactURI, e.ArtifactDigest) || strings.TrimSpace(e.MediaType) == "" ||
		e.SizeBytes <= 0 || strings.TrimSpace(e.Identity) == "" || strings.TrimSpace(e.Issuer) == "" ||
		strings.TrimSpace(e.Subject) == "" || strings.TrimSpace(e.TrustRoot) == "" || !digest(e.OperationID) {
		return errors.New("exact signature artifact, trust, or operation identity is incomplete")
	}
	if e.CertificateDigest != "" && !digest(e.CertificateDigest) {
		return errors.New("signature certificate digest is invalid")
	}
	return nil
}

func mirrorImageInventory(
	result scannerreleaseworker.StepResult,
) (map[string]mirrorImageEvidence, error) {
	raw, ok := result.Summary["images"]
	if !ok {
		return nil, errors.New("publication receipt has no mirror image inventory")
	}
	value, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var images map[string]mirrorImageEvidence
	if err := json.Unmarshal(value, &images); err != nil || len(images) == 0 {
		return nil, errors.New("publication receipt mirror image inventory is invalid")
	}
	return images, nil
}

func immutableRepository(uri, expectedDigest string) (string, error) {
	reference := strings.TrimPrefix(uri, "oci://")
	repository, observedDigest, found := strings.Cut(reference, "@")
	if !found || observedDigest != expectedDigest || strings.TrimSpace(repository) == "" ||
		strings.ContainsAny(repository, " \t\r\n") {
		return "", errors.New("OCI reference does not bind the expected digest")
	}
	return repository, nil
}

func releaseArtifacts(results map[string]scannerreleaseworker.StepResult) ([]scannerrelease.ReleaseArtifact, error) {
	result := make([]scannerrelease.ReleaseArtifact, 0, len(scannerrelease.RequiredPublicationArtifactTypes))
	for _, kind := range scannerrelease.RequiredPublicationArtifactTypes {
		step, ok := results[kind]
		if !ok || step.OutputURI == "" || !digest(step.OutputDigest) {
			return nil, fmt.Errorf("publication receipt is missing artifact %q", kind)
		}
		mediaType, size, err := publicationArtifactMetadata(kind, step)
		if err != nil {
			return nil, err
		}
		result = append(result, scannerrelease.ReleaseArtifact{
			ArtifactType: kind, MediaType: mediaType,
			URI: step.OutputURI, Digest: step.OutputDigest,
			SizeBytes:      size,
			RetentionClass: firstString(step.RetentionClass, "release"), Protected: true,
		})
	}
	return result, nil
}

func publicationArtifactMetadata(
	kind string,
	result scannerreleaseworker.StepResult,
) (string, int64, error) {
	if raw, ok := result.Summary["adapter_evidence"]; ok {
		value, err := json.Marshal(raw)
		if err != nil {
			return "", 0, err
		}
		var evidence struct {
			OutputIdentity string `json:"output_identity"`
			Artifact       struct {
				URI              string `json:"uri"`
				Digest           string `json:"digest"`
				PayloadDigest    string `json:"payload_digest"`
				MediaType        string `json:"media_type"`
				SizeBytes        int64  `json:"size_bytes"`
				StorageMediaType string `json:"storage_media_type"`
				StorageSizeBytes int64  `json:"storage_size_bytes"`
			} `json:"artifact"`
		}
		if err := json.Unmarshal(value, &evidence); err != nil ||
			evidence.Artifact.URI != result.OutputURI {
			return "", 0, fmt.Errorf("publication artifact %q adapter metadata is invalid", kind)
		}
		switch evidence.OutputIdentity {
		case "payload":
			if evidence.Artifact.PayloadDigest != result.OutputDigest ||
				strings.TrimSpace(evidence.Artifact.MediaType) == "" || evidence.Artifact.SizeBytes < 0 {
				return "", 0, fmt.Errorf("publication artifact %q payload metadata is invalid", kind)
			}
			return evidence.Artifact.MediaType, evidence.Artifact.SizeBytes, nil
		case "storage":
			if evidence.Artifact.Digest != result.OutputDigest ||
				strings.TrimSpace(evidence.Artifact.StorageMediaType) == "" ||
				evidence.Artifact.StorageSizeBytes <= 0 {
				return "", 0, fmt.Errorf("publication artifact %q storage metadata is invalid", kind)
			}
			return evidence.Artifact.StorageMediaType, evidence.Artifact.StorageSizeBytes, nil
		default:
			return "", 0, fmt.Errorf("publication artifact %q output identity is invalid", kind)
		}
	}
	if value, ok := result.Summary["media_type"].(string); ok && strings.TrimSpace(value) != "" {
		return value, 0, nil
	}
	if kind == "release-manifest-signature" {
		raw, ok := result.Summary["signing_evidence"]
		if !ok {
			return "", 0, errors.New("release manifest signature metadata is absent")
		}
		value, err := json.Marshal(raw)
		if err != nil {
			return "", 0, err
		}
		var evidence struct {
			SignatureMediaType string `json:"signature_media_type"`
			Verified           bool   `json:"verified"`
		}
		if err := json.Unmarshal(value, &evidence); err != nil || !evidence.Verified ||
			strings.TrimSpace(evidence.SignatureMediaType) == "" {
			return "", 0, errors.New("release manifest signature metadata is invalid")
		}
		return evidence.SignatureMediaType, 0, nil
	}
	mediaTypes := map[string]string{
		"mirror-copy-verify":            "application/vnd.wolf.scanner-mirror-receipt.v1+json",
		"mirror-release-closure-verify": "application/vnd.wolf.scanner-mirror-release-closure.v1+json",
	}
	if value := mediaTypes[kind]; value != "" {
		return value, 0, nil
	}
	return "", 0, fmt.Errorf("publication artifact %q has no trustworthy media type", kind)
}

func summaryJSON(summary map[string]any, key string) (string, error) {
	value, ok := summary[key]
	if !ok {
		return "", fmt.Errorf("summary field %q is missing", key)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func firstString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" && strings.Trim(value, "/") != "" {
			return strings.TrimSpace(value)
		}
	}
	return "unknown"
}

var _ io.Writer = (*boundedBuffer)(nil)
