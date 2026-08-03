package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/alphabravocompany/thewolf/internal/scannerbundle"
	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
	"github.com/alphabravocompany/thewolf/internal/scannersigning"
)

const (
	bundleImageVerifierEnv     = "WOLF_SCANNER_BUNDLE_IMAGE_VERIFIER"
	bundleImageTrustPolicyEnv  = "WOLF_SCANNER_BUNDLE_IMAGE_TRUST_POLICY_FILE"
	bundleImageRequestSchema   = "wolf.scanner-offline-image-verification-request/v1"
	bundleImageResultSchema    = "wolf.scanner-offline-image-verification-result/v1"
	bundleImageVerifierMaxIO   = 1 << 20
	bundleImageTrustMaxBytes   = 1 << 20
	bundleImageVerifierTimeout = 2 * time.Minute
)

type bundleImageClosureFile struct {
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
	MediaType string `json:"media_type"`
	Kind      string `json:"kind"`
	Path      string `json:"path"`
}

type bundleImageVerificationRequest struct {
	SchemaVersion           string                   `json:"schema_version"`
	OperationID             string                   `json:"operation_id"`
	TrustPolicyPath         string                   `json:"trust_policy_path"`
	TrustPolicyDigest       string                   `json:"trust_policy_digest"`
	ImageKey                string                   `json:"image_key"`
	RegistryTargetID        string                   `json:"registry_target_id"`
	ImageDigest             string                   `json:"image_digest"`
	SignatureDigest         string                   `json:"signature_digest"`
	SignatureArtifactDigest string                   `json:"signature_artifact_digest"`
	SignatureMediaType      string                   `json:"signature_media_type"`
	CertificateDigest       string                   `json:"certificate_digest,omitempty"`
	Identity                string                   `json:"identity"`
	Issuer                  string                   `json:"issuer"`
	Subject                 string                   `json:"subject"`
	TrustRoot               string                   `json:"trust_root"`
	SigningOperationID      string                   `json:"signing_operation_id"`
	Closure                 []bundleImageClosureFile `json:"closure"`
}

type bundleImageVerificationResult struct {
	SchemaVersion           string `json:"schema_version"`
	OperationID             string `json:"operation_id"`
	TrustPolicyDigest       string `json:"trust_policy_digest"`
	ImageKey                string `json:"image_key"`
	RegistryTargetID        string `json:"registry_target_id"`
	ImageDigest             string `json:"image_digest"`
	SignatureDigest         string `json:"signature_digest"`
	SignatureArtifactDigest string `json:"signature_artifact_digest"`
	Identity                string `json:"identity"`
	Issuer                  string `json:"issuer"`
	Subject                 string `json:"subject"`
	TrustRoot               string `json:"trust_root"`
	VerifierID              string `json:"verifier_id"`
	VerifierVersion         string `json:"verifier_version"`
	EvidenceDigest          string `json:"evidence_digest"`
	Verified                bool   `json:"verified"`
}

type bundleImageVerifier interface {
	Verify(context.Context, *scannerbundle.ImportedBundle, *portableReleaseInventory) ([]bundleImageVerificationResult, error)
}

type bundleImageVerifierFactoryFunc func() (bundleImageVerifier, bool, error)

var bundleImageVerifierFactory bundleImageVerifierFactoryFunc = loadBundleImageVerifier

type commandBundleImageVerifier struct {
	path              string
	trustPolicyPath   string
	trustPolicyDigest string
	timeout           time.Duration
}

func loadBundleImageVerifier() (bundleImageVerifier, bool, error) {
	path := strings.TrimSpace(os.Getenv(bundleImageVerifierEnv))
	trustPath := strings.TrimSpace(os.Getenv(bundleImageTrustPolicyEnv))
	if path == "" && trustPath == "" {
		return nil, false, nil
	}
	if !filepath.IsAbs(path) || !filepath.IsAbs(trustPath) {
		return nil, false, errors.New("absolute offline image verifier and image trust policy paths are both required")
	}
	for label, value := range map[string]string{"verifier": path, "trust policy": trustPath} {
		info, err := os.Stat(value)
		if err != nil || !info.Mode().IsRegular() {
			return nil, false, fmt.Errorf("offline image %s must be a regular file", label)
		}
		if label == "verifier" && info.Mode().Perm()&0o111 == 0 {
			return nil, false, errors.New("offline image verifier must be executable")
		}
		if label == "trust policy" && (info.Size() <= 0 || info.Size() > bundleImageTrustMaxBytes) {
			return nil, false, errors.New("offline image trust policy must be between 1 byte and 1 MiB")
		}
	}
	value, err := os.ReadFile(trustPath)
	if err != nil {
		return nil, false, err
	}
	return commandBundleImageVerifier{
		path: path, trustPolicyPath: trustPath, trustPolicyDigest: digestBytes(value),
		timeout: bundleImageVerifierTimeout,
	}, true, nil
}

func (v commandBundleImageVerifier) Verify(
	ctx context.Context,
	imported *scannerbundle.ImportedBundle,
	inventory *portableReleaseInventory,
) ([]bundleImageVerificationResult, error) {
	requests, err := buildBundleImageVerificationRequests(imported, inventory, v.trustPolicyPath, v.trustPolicyDigest)
	if err != nil {
		return nil, err
	}
	results := make([]bundleImageVerificationResult, 0, len(requests))
	for _, request := range requests {
		payload, err := json.Marshal(request)
		if err != nil || len(payload) > bundleImageVerifierMaxIO {
			return nil, errors.New("offline image verification request exceeds its bound")
		}
		timeout := v.timeout
		if timeout <= 0 {
			timeout = bundleImageVerifierTimeout
		}
		commandContext, cancel := context.WithTimeout(ctx, timeout)
		command := exec.CommandContext(commandContext, v.path) // #nosec G204 -- absolute deployment-owned verifier path.
		command.Env = selectedOfflineVerifierEnvironment()
		command.Stdin = bytes.NewReader(payload)
		var stdout, stderr boundedBundleVerifierBuffer
		command.Stdout, command.Stderr = &stdout, &stderr
		runErr := command.Run()
		cancel()
		if errors.Is(commandContext.Err(), context.DeadlineExceeded) {
			return nil, errors.New("offline image verifier timed out")
		}
		if runErr != nil {
			return nil, fmt.Errorf("offline image verifier failed: %w", runErr)
		}
		if stdout.exceeded || stderr.exceeded {
			return nil, errors.New("offline image verifier output exceeded its bound")
		}
		var result bundleImageVerificationResult
		decoder := json.NewDecoder(bytes.NewReader(stdout.value.Bytes()))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&result); err != nil {
			return nil, fmt.Errorf("decode offline image verifier result: %w", err)
		}
		var trailing any
		if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
			return nil, errors.New("offline image verifier returned trailing JSON")
		}
		if err := validateBundleImageVerificationResult(request, result); err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, nil
}

func buildBundleImageVerificationRequests(
	imported *scannerbundle.ImportedBundle,
	inventory *portableReleaseInventory,
	trustPath, trustDigest string,
) ([]bundleImageVerificationRequest, error) {
	if imported == nil || inventory == nil || !filepath.IsAbs(imported.Root) ||
		!filepath.IsAbs(trustPath) || !scannersigning.ValidDigest(trustDigest) {
		return nil, errors.New("offline image verifier inputs are incomplete")
	}
	artifacts := make(map[string]scannerbundle.ReleaseArtifact, len(imported.Manifest.Artifacts))
	for _, artifact := range imported.Manifest.Artifacts {
		artifacts[artifact.Key] = artifact
	}
	records := make(map[string]scannerbundle.OCIRecord, len(imported.Manifest.OCIRecords))
	for _, record := range imported.Manifest.OCIRecords {
		records[record.Digest] = record
	}
	images := append([]scannerrelease.ReleaseImage(nil), inventory.Images...)
	sort.Slice(images, func(i, j int) bool {
		if images[i].ImageKey != images[j].ImageKey {
			return images[i].ImageKey < images[j].ImageKey
		}
		return images[i].RegistryTargetID < images[j].RegistryTargetID
	})
	requests := make([]bundleImageVerificationRequest, 0, len(images))
	for _, image := range images {
		artifact, exists := artifacts[imageSignatureBundleKey(image)]
		if !exists {
			return nil, fmt.Errorf("image %q target %q has no offline signature artifact", image.ImageKey, image.RegistryTargetID)
		}
		closure := make([]bundleImageClosureFile, 0, len(artifact.OCIClosure))
		for _, digest := range artifact.OCIClosure {
			record, exists := records[digest]
			if !exists {
				return nil, fmt.Errorf("offline signature closure record %s is absent", digest)
			}
			path := filepath.Join(imported.Root, filepath.FromSlash(record.BundlePath))
			relative, err := filepath.Rel(imported.Root, path)
			if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				return nil, errors.New("offline signature closure path escapes the verified bundle root")
			}
			closure = append(closure, bundleImageClosureFile{
				Digest: digest, Size: record.Size, MediaType: record.MediaType, Kind: record.Kind, Path: path,
			})
		}
		request := bundleImageVerificationRequest{
			SchemaVersion:   bundleImageRequestSchema,
			TrustPolicyPath: trustPath, TrustPolicyDigest: trustDigest,
			ImageKey: image.ImageKey, RegistryTargetID: image.RegistryTargetID,
			ImageDigest: image.Digest, SignatureDigest: image.SignatureDigest,
			SignatureArtifactDigest: image.SignatureArtifactDigest,
			SignatureMediaType:      image.SignatureMediaType,
			CertificateDigest:       image.SignatureCertificateDigest,
			Identity:                image.SignatureIdentity, Issuer: image.SignatureIssuer,
			Subject: image.SignatureSubject, TrustRoot: image.SignatureTrustRoot,
			SigningOperationID: image.SignatureOperationID, Closure: closure,
		}
		request.OperationID = bundleImageVerificationOperation(request)
		requests = append(requests, request)
	}
	return requests, nil
}

func bundleImageVerificationOperation(request bundleImageVerificationRequest) string {
	request.OperationID = ""
	value, _ := json.Marshal(request)
	return digestBytes(value)
}

func validateBundleImageVerificationResult(
	request bundleImageVerificationRequest,
	result bundleImageVerificationResult,
) error {
	if result.SchemaVersion != bundleImageResultSchema || !result.Verified ||
		result.OperationID != request.OperationID || result.TrustPolicyDigest != request.TrustPolicyDigest ||
		result.ImageKey != request.ImageKey || result.RegistryTargetID != request.RegistryTargetID ||
		result.ImageDigest != request.ImageDigest || result.SignatureDigest != request.SignatureDigest ||
		result.SignatureArtifactDigest != request.SignatureArtifactDigest ||
		result.Identity != request.Identity || result.Issuer != request.Issuer ||
		result.Subject != request.Subject || result.TrustRoot != request.TrustRoot ||
		strings.TrimSpace(result.VerifierID) == "" || strings.TrimSpace(result.VerifierVersion) == "" ||
		!scannersigning.ValidDigest(result.EvidenceDigest) {
		return errors.New("offline image verifier result has an invalid immutable binding")
	}
	return nil
}

func bundleImageVerificationDigest(results []bundleImageVerificationResult) string {
	value, _ := json.Marshal(canonicalBundleImageVerificationResults(results))
	return digestBytes(value)
}

// validateBundleImageVerificationSet prevents a configured verifier (or a
// test/different implementation of the interface) from proving only a subset
// of the imported inventory. The command implementation validates each
// individual response; this second boundary proves exact set membership.
func validateBundleImageVerificationSet(
	inventory *portableReleaseInventory,
	results []bundleImageVerificationResult,
) error {
	if inventory == nil || len(inventory.Images) == 0 || len(results) != len(inventory.Images) {
		return errors.New("offline image verifier did not return exactly one result for every image target")
	}
	expected := make(map[string]scannerrelease.ReleaseImage, len(inventory.Images))
	for _, image := range inventory.Images {
		key := image.ImageKey + "\x00" + image.RegistryTargetID
		if _, duplicate := expected[key]; duplicate {
			return errors.New("portable inventory contains a duplicate image target")
		}
		expected[key] = image
	}
	seen := make(map[string]struct{}, len(results))
	trustDigest := ""
	for _, result := range results {
		key := result.ImageKey + "\x00" + result.RegistryTargetID
		image, exists := expected[key]
		if !exists {
			return errors.New("offline image verifier returned an unexpected image target")
		}
		if _, duplicate := seen[key]; duplicate {
			return errors.New("offline image verifier returned a duplicate image target")
		}
		seen[key] = struct{}{}
		if result.SchemaVersion != bundleImageResultSchema || !result.Verified ||
			!scannersigning.ValidDigest(result.OperationID) ||
			!scannersigning.ValidDigest(result.TrustPolicyDigest) ||
			!scannersigning.ValidDigest(result.EvidenceDigest) ||
			result.ImageDigest != image.Digest ||
			result.SignatureDigest != image.SignatureDigest ||
			result.SignatureArtifactDigest != image.SignatureArtifactDigest ||
			result.Identity != image.SignatureIdentity || result.Issuer != image.SignatureIssuer ||
			result.Subject != image.SignatureSubject || result.TrustRoot != image.SignatureTrustRoot ||
			strings.TrimSpace(result.VerifierID) == "" || strings.TrimSpace(result.VerifierVersion) == "" {
			return errors.New("offline image verifier result does not match its imported image target")
		}
		if trustDigest == "" {
			trustDigest = result.TrustPolicyDigest
		} else if result.TrustPolicyDigest != trustDigest {
			return errors.New("offline image verifier used more than one trust policy")
		}
	}
	return nil
}

func canonicalBundleImageVerificationResults(
	results []bundleImageVerificationResult,
) []bundleImageVerificationResult {
	canonical := append([]bundleImageVerificationResult(nil), results...)
	sort.Slice(canonical, func(i, j int) bool {
		if canonical[i].ImageKey != canonical[j].ImageKey {
			return canonical[i].ImageKey < canonical[j].ImageKey
		}
		return canonical[i].RegistryTargetID < canonical[j].RegistryTargetID
	})
	return canonical
}

type boundedBundleVerifierBuffer struct {
	value    bytes.Buffer
	exceeded bool
}

func (b *boundedBundleVerifierBuffer) Write(value []byte) (int, error) {
	original := len(value)
	remaining := bundleImageVerifierMaxIO - b.value.Len()
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

func (b *boundedBundleVerifierBuffer) String() string { return b.value.String() }

func selectedOfflineVerifierEnvironment() []string {
	allowed := map[string]struct{}{"PATH": {}, "SSL_CERT_FILE": {}, "SSL_CERT_DIR": {}}
	result := make([]string, 0, len(allowed))
	for _, value := range os.Environ() {
		name, _, ok := strings.Cut(value, "=")
		if ok {
			if _, exists := allowed[name]; exists {
				result = append(result, value)
			}
		}
	}
	sort.Strings(result)
	return result
}
