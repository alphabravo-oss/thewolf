package routes

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/thewolf/internal/api/response"
	"github.com/alphabravocompany/thewolf/internal/artifacts"
	"github.com/alphabravocompany/thewolf/internal/scannerbundle"
	"github.com/alphabravocompany/thewolf/internal/scannerregistry"
	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
	"github.com/alphabravocompany/thewolf/internal/scannersigning"
)

const (
	ScannerReleaseBundleMediaType      = "application/vnd.wolf.scanner-release-bundle.v1+tar+zstd"
	ScannerReleaseBundleMediaTypeV2    = "application/vnd.wolf.scanner-release-bundle.v2+tar+zstd"
	ScannerReleaseBundleMaxUploadBytes = int64(8 << 30)
	ScannerReleaseBundleMaxFileBytes   = int64(4 << 30)
	ScannerReleaseBundleMaxTotalBytes  = int64(16 << 30)

	portableInventorySchema   = "wolf.scanner-release-inventory/v1"
	portableInventoryPath     = "evidence/release-inventory.json"
	portableInventoryType     = "wolf-scanner-release-inventory"
	bundleTrustSchema         = "wolf.scanner-bundle-trust/v1"
	bundleImageReceiptSchema  = "wolf.scanner-offline-image-verification-receipt/v1"
	maxPortableInventoryBytes = int64(16 << 20)
	maxBundleKeyConfigBytes   = int64(1 << 20)

	bundleTrustPolicyEnv = "WOLF_SCANNER_BUNDLE_TRUST_POLICY_FILE"
)

type portablePolicySnapshot struct {
	ID       string          `json:"id"`
	Scope    string          `json:"scope"`
	Revision int64           `json:"revision"`
	Schedule json.RawMessage `json:"schedule"`
	Rules    json.RawMessage `json:"rules"`
}

type portableReleaseInventory struct {
	SchemaVersion string                           `json:"schema_version"`
	Release       scannerrelease.Release           `json:"release"`
	Policy        portablePolicySnapshot           `json:"policy"`
	Tools         []scannerrelease.ReleaseTool     `json:"tools"`
	Images        []scannerrelease.ReleaseImage    `json:"images"`
	Artifacts     []scannerrelease.ReleaseArtifact `json:"artifacts"`
}

type ociArtifactTransfer struct {
	StorageDigest    string
	StorageReference string
	StorageMediaType string
	StorageSize      int64
	PayloadDigest    string
	PayloadMediaType string
	PayloadSize      int64
	PayloadPath      string
	SubjectDigest    string
	Closure          []string
}

type bundleTrustKey struct {
	KeyID           string `json:"key_id"`
	Algorithm       string `json:"algorithm"`
	PublicKey       string `json:"public_key,omitempty"`
	PublicKeyPEM    string `json:"public_key_pem,omitempty"`
	ProfileDigest   string `json:"profile_digest,omitempty"`
	Identity        string `json:"identity,omitempty"`
	Issuer          string `json:"issuer,omitempty"`
	Subject         string `json:"subject,omitempty"`
	TrustRootDigest string `json:"trust_root_digest,omitempty"`
	Revoked         bool   `json:"revoked,omitempty"`
}

type bundleTrustPolicy struct {
	SchemaVersion string           `json:"schema_version"`
	Keys          []bundleTrustKey `json:"keys"`
}

type portableBundleSignerFactoryFunc func(
	scannersigning.Binding,
	string,
) (scannerbundle.ManifestSigner, string, error)

var portableBundleSignerFactory portableBundleSignerFactoryFunc = loadBundleSigner

type bundleTrustVerifier struct {
	legacy scannerbundle.Ed25519TrustStore
	policy scannersigning.BundleTrustStore
}

func (v bundleTrustVerifier) VerifyManifest(
	ctx context.Context,
	manifest []byte,
	signature scannerbundle.Signature,
) error {
	if signature.ProfileDigest != "" {
		return v.policy.VerifyManifest(ctx, manifest, signature)
	}
	return v.legacy.VerifyManifest(ctx, manifest, signature)
}

type releaseBundleImportResult struct {
	ReleaseID                  string                         `json:"release_id"`
	ManifestDigest             string                         `json:"manifest_digest"`
	BundleDigest               string                         `json:"bundle_digest"`
	BundleSizeBytes            int64                          `json:"bundle_size_bytes"`
	BundleURI                  string                         `json:"bundle_uri"`
	Created                    bool                           `json:"created"`
	IntegrityVerified          bool                           `json:"integrity_verified"`
	SignatureStatus            string                         `json:"signature_status"`
	SignatureKeyID             string                         `json:"signature_key_id,omitempty"`
	ExternalSignaturesVerified bool                           `json:"external_signatures_verified"`
	ExternalVerificationDigest string                         `json:"external_signature_verification_digest,omitempty"`
	BundleSchema               string                         `json:"bundle_schema"`
	OCIClosureVerified         bool                           `json:"oci_closure_verified"`
	NetworkMode                string                         `json:"network_mode"`
	DestinationReadBack        bool                           `json:"destination_read_back_verified"`
	RegistryMappings           []releaseBundleRegistryMapping `json:"registry_mappings,omitempty"`
}

type releaseBundleRegistryMapping struct {
	ImageKey             string `json:"image_key"`
	SourceReference      string `json:"source_reference"`
	DestinationReference string `json:"destination_reference"`
	Digest               string `json:"digest"`
	ReadBackVerified     bool   `json:"read_back_verified"`
}

type bundleImageVerificationReceipt struct {
	SchemaVersion      string                          `json:"schema_version"`
	ReleaseID          string                          `json:"release_id"`
	ManifestDigest     string                          `json:"manifest_digest"`
	BundleDigest       string                          `json:"bundle_digest"`
	VerificationDigest string                          `json:"verification_digest"`
	TrustPolicyDigest  string                          `json:"trust_policy_digest"`
	Results            []bundleImageVerificationResult `json:"results"`
}

// ScannerSupplyChainExportReleaseBundle emits a deterministic, content-addressed
// release bundle. The response is copied from a temporary file so a large
// bundle is never buffered in memory and validation failures occur before
// response headers are committed.
func ScannerSupplyChainExportReleaseBundle(w http.ResponseWriter, r *http.Request) {
	store, err := scannerReleaseStore()
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	inventory, err := store.GetReleaseInventory(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	policy, err := store.GetPolicy(r.Context(), inventory.Release.PolicyID)
	if err != nil {
		scannerWriteError(w, fmt.Errorf("load release policy snapshot: %w", err))
		return
	}
	version := strings.TrimSpace(r.URL.Query().Get("bundle_version"))
	if version == "" {
		version = "1"
	}
	if version != "1" && version != "2" {
		response.WriteError(w, http.StatusBadRequest, "invalid_bundle_version", "bundle_version must be 1 or 2")
		return
	}
	selectedPlatforms, err := selectedBundlePlatforms(r.URL.Query()["platform"])
	if err != nil {
		response.WriteError(w, http.StatusBadRequest, "invalid_bundle_platform", err.Error())
		return
	}
	if version == "1" && len(selectedPlatforms) != 0 {
		response.WriteError(w, http.StatusBadRequest, "invalid_bundle_platform", "platform selection requires bundle_version=2")
		return
	}
	staging, err := os.MkdirTemp("", "wolf-scanner-release-oci-*")
	if err != nil {
		scannerWriteError(w, err)
		return
	}
	defer os.RemoveAll(staging)
	manifest, sources, signer, signatureStatus, err := buildPortableReleaseBundle(
		r.Context(), inventory, policy, version, selectedPlatforms, staging,
	)
	if err != nil {
		response.WriteError(w, http.StatusUnprocessableEntity, "release_bundle_invalid", err.Error())
		return
	}

	temp, err := os.CreateTemp("", "wolf-scanner-release-export-*.tar.zst")
	if err != nil {
		scannerWriteError(w, fmt.Errorf("create release bundle temporary file: %w", err))
		return
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	bundleHash := sha256.New()
	bundleSchema := scannerbundle.BundleSchema
	contentType := ScannerReleaseBundleMediaType
	if version == "2" {
		bundleSchema = scannerbundle.BundleSchemaV2
		contentType = ScannerReleaseBundleMediaTypeV2
	}
	if err := scannerbundle.Write(r.Context(), io.MultiWriter(temp, bundleHash), scannerbundle.WriteOptions{
		Manifest: manifest, Sources: sources, Signer: signer, SourceDateEpoch: manifest.GeneratedAt,
		SchemaVersion: bundleSchema,
	}); err != nil {
		_ = temp.Close()
		response.WriteError(w, http.StatusUnprocessableEntity, "release_bundle_invalid", err.Error())
		return
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		scannerWriteError(w, fmt.Errorf("sync release bundle: %w", err))
		return
	}
	info, err := temp.Stat()
	if err != nil {
		_ = temp.Close()
		scannerWriteError(w, fmt.Errorf("inspect release bundle: %w", err))
		return
	}
	if info.Size() > ScannerReleaseBundleMaxUploadBytes {
		_ = temp.Close()
		response.WriteError(
			w, http.StatusUnprocessableEntity, "release_bundle_too_large",
			"release bundle exceeds the 8 GiB compressed import limit",
		)
		return
	}
	if _, err := temp.Seek(0, io.SeekStart); err != nil {
		_ = temp.Close()
		scannerWriteError(w, fmt.Errorf("rewind release bundle: %w", err))
		return
	}
	defer temp.Close()

	manifestDigest, _ := manifest.Digest()
	bundleDigest := "sha256:" + hex.EncodeToString(bundleHash.Sum(nil))
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.scanner-release.tar.zst"`, manifest.ReleaseID))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", info.Size()))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Wolf-Release-ID", manifest.ReleaseID)
	w.Header().Set("X-Wolf-Manifest-Digest", manifestDigest)
	w.Header().Set("X-Wolf-Bundle-Digest", bundleDigest)
	w.Header().Set("X-Wolf-Bundle-Signature-Status", signatureStatus)
	w.Header().Set("X-Wolf-Bundle-Schema", bundleSchema)
	w.Header().Set("X-Wolf-Bundle-Platforms", strings.Join(selectedPlatforms, ","))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, temp)
}

// ScannerSupplyChainImportReleaseBundle verifies, durably stores, and
// idempotently persists an offline release inventory. An explicit reason is
// required because this operation adds trusted executable inventory.
func ScannerSupplyChainImportReleaseBundle(w http.ResponseWriter, r *http.Request) {
	key, ok := scannerIdempotencyKey(w, r)
	if !ok {
		return
	}
	reason := strings.TrimSpace(r.Header.Get("X-Wolf-Import-Reason"))
	if reason == "" || len(reason) > 500 {
		response.WriteError(w, http.StatusBadRequest, "import_reason_required", "X-Wolf-Import-Reason is required and must be at most 500 characters")
		return
	}
	allowUnverified, err := parseAllowUnverified(r)
	if err != nil {
		response.WriteError(w, http.StatusBadRequest, "invalid_import_option", err.Error())
		return
	}
	if err := validateBundleContentType(r.Header.Get("Content-Type")); err != nil {
		response.WriteError(w, http.StatusUnsupportedMediaType, "unsupported_bundle_media_type", err.Error())
		return
	}
	if r.ContentLength > ScannerReleaseBundleMaxUploadBytes {
		response.WriteError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "release bundle exceeds the 8 GiB compressed upload limit")
		return
	}
	if artifacts.Global == nil || strings.TrimSpace(artifacts.Global.Root()) == "" {
		response.WriteError(w, http.StatusServiceUnavailable, "artifact_store_unavailable", "artifact storage is required for release bundle imports")
		return
	}
	store, err := scannerReleaseStore()
	if err != nil {
		scannerWriteError(w, err)
		return
	}

	bundleDir := filepath.Join(artifacts.Global.Root(), "scanner-release-bundles")
	if err := os.MkdirAll(bundleDir, 0o750); err != nil {
		scannerWriteError(w, fmt.Errorf("create release bundle storage: %w", err))
		return
	}
	upload, err := os.CreateTemp(bundleDir, ".upload-*.tar.zst")
	if err != nil {
		scannerWriteError(w, fmt.Errorf("create release bundle upload: %w", err))
		return
	}
	uploadPath := upload.Name()
	defer os.Remove(uploadPath)

	hash := sha256.New()
	size, copyErr := io.CopyBuffer(io.MultiWriter(upload, hash), r.Body, make([]byte, 128*1024))
	if copyErr != nil {
		_ = upload.Close()
		var tooLarge *http.MaxBytesError
		if errors.As(copyErr, &tooLarge) {
			response.WriteError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "release bundle exceeds the 8 GiB compressed upload limit")
			return
		}
		response.WriteError(w, http.StatusBadRequest, "bundle_upload_failed", copyErr.Error())
		return
	}
	if size == 0 {
		_ = upload.Close()
		response.WriteError(w, http.StatusBadRequest, "empty_release_bundle", "release bundle body is empty")
		return
	}
	if err := upload.Sync(); err != nil {
		_ = upload.Close()
		scannerWriteError(w, fmt.Errorf("sync uploaded release bundle: %w", err))
		return
	}
	if _, err := upload.Seek(0, io.SeekStart); err != nil {
		_ = upload.Close()
		scannerWriteError(w, fmt.Errorf("rewind uploaded release bundle: %w", err))
		return
	}

	extractDir, err := os.MkdirTemp(bundleDir, ".extract-*")
	if err != nil {
		_ = upload.Close()
		scannerWriteError(w, fmt.Errorf("create release bundle extraction directory: %w", err))
		return
	}
	defer os.RemoveAll(extractDir)
	imported, err := scannerbundle.Read(r.Context(), upload, extractDir, scannerbundle.ReadOptions{
		MaxFiles: 10_000, MaxFileBytes: ScannerReleaseBundleMaxFileBytes,
		MaxTotalBytes: ScannerReleaseBundleMaxTotalBytes, AllowUnsigned: true,
	})
	if closeErr := upload.Close(); err == nil && closeErr != nil {
		err = closeErr
	}
	if err != nil {
		response.WriteError(w, http.StatusUnprocessableEntity, "release_bundle_verification_failed", err.Error())
		return
	}

	verifier, trustConfigured, err := loadBundleTrustStore()
	if err != nil {
		scannerWriteError(w, fmt.Errorf("load scanner bundle trust policy: %w", err))
		return
	}
	signatureStatus, signatureKeyID, err := verifyBundleSignature(r.Context(), imported, verifier, trustConfigured, allowUnverified)
	if err != nil {
		response.WriteError(w, http.StatusUnprocessableEntity, "release_bundle_signature_rejected", err.Error())
		return
	}
	inventory, err := readPortableInventory(imported)
	if err != nil {
		response.WriteError(w, http.StatusUnprocessableEntity, "release_inventory_invalid", err.Error())
		return
	}
	externalSignaturesVerified := false
	externalVerificationDigest := ""
	var externalVerificationResults []bundleImageVerificationResult
	if imported.SchemaVersion == scannerbundle.BundleSchemaV2 {
		ociByDigest := make(map[string]scannerbundle.OCIRecord, len(imported.Manifest.OCIRecords))
		for _, record := range imported.Manifest.OCIRecords {
			ociByDigest[record.Digest] = record
		}
		if err := validateV2EvidenceCoverage(
			inventory.Images, imported.Manifest.Images,
			imported.Manifest.Artifacts, ociByDigest,
		); err != nil {
			response.WriteError(w, http.StatusUnprocessableEntity, "release_evidence_invalid", err.Error())
			return
		}
		imageVerifier, configured, loadErr := bundleImageVerifierFactory()
		if loadErr != nil {
			scannerWriteError(w, fmt.Errorf("load offline image signature verifier: %w", loadErr))
			return
		}
		if !configured {
			if !allowUnverified {
				response.WriteError(w, http.StatusUnprocessableEntity, "image_signature_verifier_required", "v2 import requires an offline image-signature verifier and trust policy, or explicit break-glass allow_unverified=true")
				return
			}
		} else {
			verificationResults, verifyErr := imageVerifier.Verify(r.Context(), imported, inventory)
			if verifyErr != nil {
				if !allowUnverified {
					response.WriteError(w, http.StatusUnprocessableEntity, "image_signature_verification_rejected", verifyErr.Error())
					return
				}
			} else if setErr := validateBundleImageVerificationSet(inventory, verificationResults); setErr != nil {
				if !allowUnverified {
					response.WriteError(w, http.StatusUnprocessableEntity, "image_signature_verification_rejected", setErr.Error())
					return
				}
			} else {
				verificationResults = canonicalBundleImageVerificationResults(verificationResults)
				externalSignaturesVerified = true
				externalVerificationDigest = bundleImageVerificationDigest(verificationResults)
				externalVerificationResults = verificationResults
			}
		}
	}
	noNetwork, err := parseOptionalBool(r.URL.Query().Get("no_network"))
	if err != nil {
		response.WriteError(w, http.StatusBadRequest, "invalid_import_option", "no_network must be true or false")
		return
	}
	registryTargetID := strings.TrimSpace(r.URL.Query().Get("registry_target_id"))
	if noNetwork && registryTargetID != "" {
		response.WriteError(w, http.StatusBadRequest, "invalid_import_option", "registry_target_id is incompatible with no_network=true")
		return
	}
	var mappings []releaseBundleRegistryMapping
	registryOverrides := make(map[string]string)
	destinationReadBack := false
	if registryTargetID != "" {
		if imported.SchemaVersion != scannerbundle.BundleSchemaV2 {
			response.WriteError(w, http.StatusUnprocessableEntity, "bundle_registry_upload_unsupported", "private-registry upload requires a v2 bundle")
			return
		}
		mappings, registryOverrides, err = uploadImportedOCI(
			r.Context(), store, imported, inventory, registryTargetID,
		)
		if err != nil {
			response.WriteError(w, http.StatusUnprocessableEntity, "bundle_registry_upload_failed", err.Error())
			return
		}
		destinationReadBack = true
	}

	bundleDigest := "sha256:" + hex.EncodeToString(hash.Sum(nil))
	finalName := strings.TrimPrefix(bundleDigest, "sha256:") + ".scanner-release.tar.zst"
	finalPath := filepath.Join(bundleDir, finalName)
	bundleURI := "artifact://scanner-release-bundles/" + finalName
	created, err := persistPortableRelease(
		r.Context(), store, imported, inventory, finalPath, uploadPath,
		bundleURI, bundleDigest, size, signatureStatus, signatureKeyID,
		scannerActor(r), reason, key, registryOverrides, externalVerificationResults,
	)
	if err != nil {
		if errors.Is(err, errReleaseBundleConflict) {
			response.WriteError(w, http.StatusConflict, "release_bundle_conflict", err.Error())
			return
		}
		scannerWriteError(w, err)
		return
	}

	status := http.StatusCreated
	if !created {
		status = http.StatusOK
	}
	response.WriteJSON(w, status, response.SuccessResponse{Data: releaseBundleImportResult{
		ReleaseID:                  imported.Manifest.ReleaseID,
		ManifestDigest:             imported.ManifestDigest,
		BundleDigest:               bundleDigest,
		BundleSizeBytes:            size,
		BundleURI:                  bundleURI,
		Created:                    created,
		IntegrityVerified:          true,
		SignatureStatus:            signatureStatus,
		SignatureKeyID:             signatureKeyID,
		ExternalSignaturesVerified: externalSignaturesVerified,
		ExternalVerificationDigest: externalVerificationDigest,
		BundleSchema:               imported.SchemaVersion,
		OCIClosureVerified:         imported.SchemaVersion == scannerbundle.BundleSchemaV2,
		NetworkMode:                map[bool]string{true: "registry-enabled", false: "no-network"}[registryTargetID != ""],
		DestinationReadBack:        destinationReadBack,
		RegistryMappings:           mappings,
	}})
}

func buildPortableReleaseBundle(
	ctx context.Context,
	inventory *scannerrelease.ReleaseInventory,
	policy *scannerrelease.Policy,
	version string,
	selectedPlatforms []string,
	staging string,
) (scannerbundle.ReleaseManifest, []scannerbundle.Source, scannerbundle.ManifestSigner, string, error) {
	if inventory == nil || policy == nil {
		return scannerbundle.ReleaseManifest{}, nil, nil, "", errors.New("release inventory and policy are required")
	}
	policySnapshot, err := snapshotPolicy(policy)
	if err != nil {
		return scannerbundle.ReleaseManifest{}, nil, nil, "", err
	}
	policyDigest, err := digestJSON(policySnapshot)
	if err != nil {
		return scannerbundle.ReleaseManifest{}, nil, nil, "", err
	}
	portable := portableReleaseInventory{
		SchemaVersion: portableInventorySchema,
		Release:       inventory.Release,
		Policy:        policySnapshot,
		Tools:         append([]scannerrelease.ReleaseTool(nil), inventory.Tools...),
		Images:        append([]scannerrelease.ReleaseImage(nil), inventory.Images...),
		Artifacts:     portableReleaseArtifacts(inventory.Artifacts),
	}
	images, err := bundleImages(portable.Images, portable.Tools)
	if err != nil {
		return scannerbundle.ReleaseManifest{}, nil, nil, "", err
	}
	var ociRecords []scannerbundle.OCIRecord
	var ociSources []scannerbundle.Source
	artifactTransfers := make(map[string]ociArtifactTransfer)
	if version == "2" {
		images, ociRecords, ociSources, artifactTransfers, err = attachOCITransfer(
			ctx, inventory, &portable, images, selectedPlatforms, staging,
		)
		if err != nil {
			return scannerbundle.ReleaseManifest{}, nil, nil, "", err
		}
	}
	sortPortableInventory(&portable)
	inventoryBytes, err := json.Marshal(portable)
	if err != nil {
		return scannerbundle.ReleaseManifest{}, nil, nil, "", fmt.Errorf("encode release inventory: %w", err)
	}
	if int64(len(inventoryBytes)) > maxPortableInventoryBytes {
		return scannerbundle.ReleaseManifest{}, nil, nil, "", fmt.Errorf("portable release inventory exceeds the %d-byte limit", maxPortableInventoryBytes)
	}
	inventoryDigest := digestBytes(inventoryBytes)

	bundleArtifacts := make([]scannerbundle.ReleaseArtifact, 0, len(portable.Artifacts)+1)
	sources := append([]scannerbundle.Source{
		bytesSource(portableInventoryPath, inventoryBytes),
	}, ociSources...)
	ociByDigest := make(map[string]scannerbundle.OCIRecord, len(ociRecords))
	for _, record := range ociRecords {
		ociByDigest[record.Digest] = record
	}
	for i := range portable.Artifacts {
		artifact := portable.Artifacts[i]
		key := artifactBundleKey(artifact, i)
		item := scannerbundle.ReleaseArtifact{
			Key: key, Type: artifact.ArtifactType,
			MediaType: artifact.MediaType, Digest: artifact.Digest, Size: artifact.SizeBytes,
		}
		if transfer, exists := artifactTransfers["release/"+key]; exists {
			item.BundlePath = transfer.PayloadPath
			item.StorageDigest = transfer.StorageDigest
			item.StorageReference = transfer.StorageReference
			item.StorageMediaType = transfer.StorageMediaType
			item.StorageSize = transfer.StorageSize
			item.OCIClosure = append([]string(nil), transfer.Closure...)
		} else if record, exists := ociByDigest[item.Digest]; exists {
			item.BundlePath = record.BundlePath
		} else if source, ok, sourceErr := localArtifactSource(artifact, item.Key); sourceErr != nil {
			return scannerbundle.ReleaseManifest{}, nil, nil, "", sourceErr
		} else if ok {
			item.BundlePath = source.Path
			sources = append(sources, source)
		}
		bundleArtifacts = append(bundleArtifacts, item)
	}
	if version == "2" {
		for _, image := range portable.Images {
			key := imageSignatureBundleKey(image)
			transfer, exists := artifactTransfers["signature/"+key]
			if !exists {
				return scannerbundle.ReleaseManifest{}, nil, nil, "", fmt.Errorf(
					"image %q target %q signature closure is missing", image.ImageKey, image.RegistryTargetID,
				)
			}
			bundleArtifacts = append(bundleArtifacts, scannerbundle.ReleaseArtifact{
				Key: key, Type: "image-signature", MediaType: transfer.PayloadMediaType,
				Digest: transfer.PayloadDigest, Size: transfer.PayloadSize,
				BundlePath:    transfer.PayloadPath,
				StorageDigest: transfer.StorageDigest, StorageMediaType: transfer.StorageMediaType,
				StorageReference: transfer.StorageReference,
				StorageSize:      transfer.StorageSize, OCIClosure: append([]string(nil), transfer.Closure...),
			})
		}
	}
	if version == "2" {
		for _, artifact := range bundleArtifacts {
			kind := strings.ToLower(artifact.Type)
			if (strings.Contains(kind, "signature") ||
				strings.Contains(kind, "provenance") ||
				strings.Contains(kind, "sbom") ||
				strings.Contains(kind, "trust")) &&
				artifact.BundlePath == "" {
				return scannerbundle.ReleaseManifest{}, nil, nil, "", fmt.Errorf(
					"required trust artifact %q is not available for offline transfer",
					artifact.Key,
				)
			}
		}
		if err := validateV2EvidenceCoverage(
			portable.Images, images, bundleArtifacts, ociByDigest,
		); err != nil {
			return scannerbundle.ReleaseManifest{}, nil, nil, "", err
		}
	}
	bundleArtifacts = append(bundleArtifacts, scannerbundle.ReleaseArtifact{
		Key: portableInventoryType, Type: portableInventoryType,
		MediaType: "application/vnd.wolf.scanner-release.inventory.v1+json",
		Digest:    inventoryDigest, Size: int64(len(inventoryBytes)), BundlePath: portableInventoryPath,
	})
	if err := validatePortableBundleSourceLimits(sources); err != nil {
		return scannerbundle.ReleaseManifest{}, nil, nil, "", err
	}

	signer, signatureStatus, err := portableBundleSignerFactory(
		scannersigning.Binding{
			DefinitionCommit: inventory.Release.DefinitionCommit,
			LockDigest:       inventory.Release.LockDigest,
			PolicyID:         policy.ID, PolicyRevision: policy.Revision,
		},
		inventory.Release.ID,
	)
	if err != nil {
		return scannerbundle.ReleaseManifest{}, nil, nil, "", err
	}
	generatedAt := inventory.Release.PublishedAt
	if generatedAt.IsZero() {
		generatedAt = inventory.Release.CreatedAt
	}
	manifest := scannerbundle.ReleaseManifest{
		SchemaVersion: scannerbundle.ManifestSchema, ReleaseID: inventory.Release.ID,
		LockDigest: inventory.Release.LockDigest, DefinitionCommit: inventory.Release.DefinitionCommit,
		BuildPolicyDigest: policyDigest, GeneratedAt: generatedAt.UTC().Truncate(time.Second),
		Images: images, Artifacts: bundleArtifacts, OCIRecords: ociRecords,
		Metadata: map[string]string{
			"original_manifest_digest":  inventory.Release.ManifestDigest,
			"original_manifest_uri":     inventory.Release.ManifestURI,
			"original_signer_identity":  inventory.Release.SignerIdentity,
			"portable_signature_status": signatureStatus,
		},
	}
	if err := manifest.Validate(); err != nil {
		return scannerbundle.ReleaseManifest{}, nil, nil, "", fmt.Errorf("release cannot be represented as a portable bundle: %w", err)
	}
	return manifest, sources, signer, signatureStatus, nil
}

func validateV2EvidenceCoverage(
	portableImages []scannerrelease.ReleaseImage,
	images []scannerbundle.ReleaseImage,
	artifacts []scannerbundle.ReleaseArtifact,
	ociByDigest map[string]scannerbundle.OCIRecord,
) error {
	embedded := make(map[string]struct{}, len(ociByDigest)+len(artifacts))
	artifactsByKey := make(map[string]scannerbundle.ReleaseArtifact, len(artifacts))
	for digest := range ociByDigest {
		embedded[digest] = struct{}{}
	}
	for _, artifact := range artifacts {
		artifactsByKey[artifact.Key] = artifact
		if artifact.BundlePath != "" {
			embedded[artifact.Digest] = struct{}{}
		}
	}
	transferByKey := make(map[string]scannerbundle.ReleaseImage, len(images))
	for _, image := range images {
		transferByKey[image.Key] = image
	}
	for _, image := range portableImages {
		if err := validatePortableImageSignature(image); err != nil {
			return err
		}
		for label, digest := range map[string]string{
			"provenance": image.ProvenanceDigest,
			"SBOM":       image.SBOMDigest,
		} {
			if digest == "" {
				return fmt.Errorf(
					"image %q has no published %s digest for complete offline transfer",
					image.ImageKey, label,
				)
			}
			if _, exists := embedded[digest]; !exists {
				return fmt.Errorf(
					"image %q %s %s is not embedded in the v2 bundle",
					image.ImageKey, label, digest,
				)
			}
		}
		signature, exists := artifactsByKey[imageSignatureBundleKey(image)]
		if !exists || signature.Type != "image-signature" ||
			signature.Digest != image.SignatureDigest ||
			signature.StorageDigest != image.SignatureArtifactDigest ||
			signature.StorageReference != strings.TrimPrefix(image.SignatureArtifactURI, "oci://") ||
			signature.StorageMediaType != image.SignatureMediaType ||
			signature.StorageSize != image.SignatureArtifactSizeBytes ||
			signature.BundlePath != scannerbundle.OCIPath(image.SignatureDigest) {
			return fmt.Errorf("image %q target %q exact signature closure is absent", image.ImageKey, image.RegistryTargetID)
		}
		if image.SignatureCertificateDigest != "" {
			if _, exists := ociByDigest[image.SignatureCertificateDigest]; !exists ||
				!containsString(signature.OCIClosure, image.SignatureCertificateDigest) {
				return fmt.Errorf(
					"image %q target %q signature certificate %s is absent from its exact closure",
					image.ImageKey, image.RegistryTargetID, image.SignatureCertificateDigest,
				)
			}
		}
		closure := transferByKey[image.ImageKey].BlobDigests
		for _, digest := range signature.OCIClosure {
			if !containsString(closure, digest) {
				return fmt.Errorf("image %q target %q signature record %s is outside its image closure", image.ImageKey, image.RegistryTargetID, digest)
			}
		}
	}
	return nil
}

func selectedBundlePlatforms(values []string) ([]string, error) {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		parts := strings.Split(value, "/")
		if len(parts) < 2 || len(parts) > 3 {
			return nil, fmt.Errorf("invalid platform %q", value)
		}
		for _, part := range parts {
			if part == "" || strings.ContainsAny(part, " \t\r\n,") {
				return nil, fmt.Errorf("invalid platform %q", value)
			}
		}
		if _, duplicate := seen[value]; duplicate {
			return nil, fmt.Errorf("duplicate platform %q", value)
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result, nil
}

func attachOCITransfer(
	ctx context.Context,
	inventory *scannerrelease.ReleaseInventory,
	portable *portableReleaseInventory,
	images []scannerbundle.ReleaseImage,
	selectedPlatforms []string,
	staging string,
) ([]scannerbundle.ReleaseImage, []scannerbundle.OCIRecord, []scannerbundle.Source, map[string]ociArtifactTransfer, error) {
	if !filepath.IsAbs(staging) {
		return nil, nil, nil, nil, errors.New("OCI transfer staging path must be absolute")
	}
	store, err := scannerReleaseStore()
	if err != nil {
		return nil, nil, nil, nil, err
	}
	logicalImages, err := logicalReleaseImages(inventory.Images)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	inventoryByKey := make(map[string]scannerrelease.ReleaseImage, len(logicalImages))
	for _, image := range logicalImages {
		inventoryByKey[image.ImageKey] = image
	}
	portableIndex := make(map[string][]int)
	for index := range portable.Images {
		portableIndex[portable.Images[index].ImageKey] = append(portableIndex[portable.Images[index].ImageKey], index)
	}
	recordByDigest := make(map[string]scannerbundle.OCIRecord)
	sourceByDigest := make(map[string]scannerbundle.Source)
	for index := range images {
		sourceImage, exists := inventoryByKey[images[index].Key]
		if !exists {
			return nil, nil, nil, nil, fmt.Errorf("release image %q has no source registry assignment", images[index].Key)
		}
		target, err := store.GetRegistryTarget(ctx, sourceImage.RegistryTargetID)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		client, host, err := scannerRegistryClient(ctx, target)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		client.MaxBlobBytes = ScannerReleaseBundleMaxFileBytes
		reference, err := scannerregistry.ParseReference(images[index].Reference)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		if reference.Registry != host {
			return nil, nil, nil, nil, fmt.Errorf(
				"release image %q registry %q does not match target %q",
				images[index].Key, reference.Registry, host,
			)
		}
		directory := filepath.Join(staging, safeBundleName(images[index].Key))
		closure, err := client.FetchTransferClosure(
			ctx, reference, selectedPlatforms, directory,
		)
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("fetch OCI closure for %s: %w", images[index].Key, err)
		}
		for platform, digest := range closure.Platforms {
			expected, exists := images[index].Platforms[platform]
			if !exists {
				return nil, nil, nil, nil, fmt.Errorf(
					"source registry returned undeclared platform %s for image %q",
					platform, images[index].Key,
				)
			}
			if expected != digest {
				return nil, nil, nil, nil, fmt.Errorf(
					"source registry platform %s for image %q changed from %s to %s",
					platform, images[index].Key, expected, digest,
				)
			}
		}
		if len(closure.Platforms) == 0 {
			closure.Platforms = make(map[string]string, len(images[index].Platforms))
			for platform, digest := range images[index].Platforms {
				if len(selectedPlatforms) == 0 || containsString(selectedPlatforms, platform) {
					if digest != closure.RootDigest {
						return nil, nil, nil, nil, fmt.Errorf(
							"single-manifest image %q platform %s digest mismatch",
							images[index].Key, platform,
						)
					}
					closure.Platforms[platform] = digest
				}
			}
		}
		images[index].SourceReference = closure.SourceReference
		images[index].SourceDigest = closure.SourceDigest
		images[index].Digest = closure.RootDigest
		images[index].Reference = reference.Registry + "/" + reference.Repository + "@" + closure.RootDigest
		images[index].Platforms = closure.Platforms
		images[index].BlobDigests = make([]string, 0, len(closure.Blobs))
		for _, blob := range closure.Blobs {
			images[index].BlobDigests = append(images[index].BlobDigests, blob.Digest)
			record := scannerbundle.OCIRecord{
				Digest: blob.Digest, Size: blob.Size, MediaType: blob.MediaType,
				Kind: blob.Kind, BundlePath: scannerbundle.OCIPath(blob.Digest),
			}
			if existing, duplicate := recordByDigest[blob.Digest]; duplicate {
				if existing.Size != record.Size || existing.MediaType != record.MediaType {
					return nil, nil, nil, nil, fmt.Errorf("conflicting OCI record %s", blob.Digest)
				}
				continue
			}
			recordByDigest[blob.Digest] = record
			path := blob.Path
			sourceByDigest[blob.Digest] = scannerbundle.Source{
				Path: record.BundlePath, Size: record.Size, Digest: record.Digest,
				Open: func() (io.ReadCloser, error) { return os.Open(path) },
			}
		}
		platformsJSON, _ := json.Marshal(closure.Platforms)
		for _, portablePosition := range portableIndex[images[index].Key] {
			portable.Images[portablePosition].Digest = closure.RootDigest
			portable.Images[portablePosition].PlatformDigests = string(platformsJSON)
		}
	}
	artifactTransfers := make(map[string]ociArtifactTransfer)
	for index := range portable.Artifacts {
		artifact := portable.Artifacts[index]
		if !strings.HasPrefix(artifact.URI, "oci://") {
			continue
		}
		key := artifactBundleKey(artifact, index)
		transfer, blobs, err := fetchBundleArtifactClosure(
			ctx, store, inventory.Images, artifact.URI, artifact.Digest,
			artifact.MediaType, artifact.SizeBytes,
			filepath.Join(staging, "release-"+safeBundleName(key)), "oci-release-artifact-manifest",
		)
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("fetch release artifact %q: %w", artifact.ID, err)
		}
		if err := mergeBundleTransfer(blobs, recordByDigest, sourceByDigest); err != nil {
			return nil, nil, nil, nil, err
		}
		artifactTransfers["release/"+key] = transfer
	}
	for _, image := range portable.Images {
		if err := validatePortableImageSignature(image); err != nil {
			return nil, nil, nil, nil, err
		}
		key := imageSignatureBundleKey(image)
		transfer, blobs, err := fetchBundleArtifactClosure(
			ctx, store, []scannerrelease.ReleaseImage{image}, image.SignatureArtifactURI,
			image.SignatureDigest, "", 0,
			filepath.Join(staging, "signature-"+safeBundleName(key)), "oci-trust-manifest",
		)
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("fetch image %q target %q signature: %w", image.ImageKey, image.RegistryTargetID, err)
		}
		if transfer.StorageDigest != image.SignatureArtifactDigest ||
			transfer.StorageMediaType != image.SignatureMediaType ||
			transfer.StorageSize != image.SignatureArtifactSizeBytes {
			return nil, nil, nil, nil, fmt.Errorf("image %q target %q signature storage identity changed", image.ImageKey, image.RegistryTargetID)
		}
		if transfer.SubjectDigest != image.Digest {
			return nil, nil, nil, nil, fmt.Errorf("image %q target %q signature is attached to %q", image.ImageKey, image.RegistryTargetID, transfer.SubjectDigest)
		}
		if image.SignatureCertificateDigest != "" && !containsString(transfer.Closure, image.SignatureCertificateDigest) {
			return nil, nil, nil, nil, fmt.Errorf("image %q target %q signature certificate is absent", image.ImageKey, image.RegistryTargetID)
		}
		if err := mergeBundleTransfer(blobs, recordByDigest, sourceByDigest); err != nil {
			return nil, nil, nil, nil, err
		}
		artifactTransfers["signature/"+key] = transfer
		for candidate := range images {
			if images[candidate].Key == image.ImageKey {
				images[candidate].BlobDigests = appendUniqueStrings(images[candidate].BlobDigests, transfer.Closure...)
				break
			}
		}
	}
	records := make([]scannerbundle.OCIRecord, 0, len(recordByDigest))
	sources := make([]scannerbundle.Source, 0, len(sourceByDigest))
	for digest, record := range recordByDigest {
		records = append(records, record)
		sources = append(sources, sourceByDigest[digest])
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Digest < records[j].Digest })
	sort.Slice(sources, func(i, j int) bool { return sources[i].Path < sources[j].Path })
	return images, records, sources, artifactTransfers, nil
}

func validatePortableImageSignature(image scannerrelease.ReleaseImage) error {
	if image.SignatureStatus != "verified" || !scannersigning.ValidDigest(image.SignatureDigest) ||
		!scannersigning.ValidDigest(image.SignatureArtifactDigest) ||
		!strings.Contains(image.SignatureArtifactURI, image.SignatureArtifactDigest) ||
		strings.TrimSpace(image.SignatureMediaType) == "" || image.SignatureArtifactSizeBytes <= 0 ||
		strings.TrimSpace(image.SignatureIdentity) == "" || strings.TrimSpace(image.SignatureIssuer) == "" ||
		strings.TrimSpace(image.SignatureSubject) == "" || strings.TrimSpace(image.SignatureTrustRoot) == "" ||
		!scannersigning.ValidDigest(image.SignatureOperationID) {
		return fmt.Errorf("image %q target %q has incomplete exact signature identity", image.ImageKey, image.RegistryTargetID)
	}
	if image.SignatureCertificateDigest != "" && !scannersigning.ValidDigest(image.SignatureCertificateDigest) {
		return fmt.Errorf("image %q target %q has an invalid signature certificate digest", image.ImageKey, image.RegistryTargetID)
	}
	return nil
}

func imageSignatureBundleKey(image scannerrelease.ReleaseImage) string {
	value := image.ImageKey + "\x00" + image.RegistryTargetID + "\x00" + image.SignatureArtifactDigest
	sum := sha256.Sum256([]byte(value))
	return "image-signature-" + hex.EncodeToString(sum[:12])
}

func fetchBundleArtifactClosure(
	ctx context.Context,
	store scannerrelease.Persistence,
	targetImages []scannerrelease.ReleaseImage,
	uri, payloadDigest, payloadMediaType string,
	payloadSize int64,
	directory, rootKind string,
) (ociArtifactTransfer, []scannerregistry.TransferBlob, error) {
	if !strings.HasPrefix(uri, "oci://") || !filepath.IsAbs(directory) ||
		!scannersigning.ValidDigest(payloadDigest) {
		return ociArtifactTransfer{}, nil, errors.New("OCI artifact reference, payload digest, or staging directory is invalid")
	}
	reference, err := scannerregistry.ParseReference(strings.TrimPrefix(uri, "oci://"))
	if err != nil || !scannersigning.ValidDigest(reference.Digest) {
		return ociArtifactTransfer{}, nil, errors.New("OCI artifact URI is not an immutable digest reference")
	}
	ordered := append([]scannerrelease.ReleaseImage(nil), targetImages...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].RegistryTargetID < ordered[j].RegistryTargetID })
	var client scannerregistry.Client
	found := false
	for _, image := range ordered {
		target, loadErr := store.GetRegistryTarget(ctx, image.RegistryTargetID)
		if loadErr != nil {
			return ociArtifactTransfer{}, nil, loadErr
		}
		candidate, host, clientErr := scannerRegistryClient(ctx, target)
		if clientErr != nil {
			return ociArtifactTransfer{}, nil, clientErr
		}
		if host == reference.Registry {
			client = candidate
			found = true
			break
		}
	}
	if !found {
		return ociArtifactTransfer{}, nil, fmt.Errorf("OCI artifact registry %q has no exact inventory target", reference.Registry)
	}
	client.MaxBlobBytes = ScannerReleaseBundleMaxFileBytes
	closure, err := client.FetchTransferClosure(ctx, reference, nil, directory)
	if err != nil {
		return ociArtifactTransfer{}, nil, err
	}
	if closure.RootDigest != reference.Digest || closure.SourceDigest != reference.Digest {
		return ociArtifactTransfer{}, nil, errors.New("OCI artifact root identity changed during transfer")
	}
	var rootBlob, payloadBlob scannerregistry.TransferBlob
	for index := range closure.Blobs {
		if closure.Blobs[index].Digest == closure.RootDigest {
			closure.Blobs[index].Kind = rootKind
			rootBlob = closure.Blobs[index]
		}
		if closure.Blobs[index].Digest == payloadDigest {
			payloadBlob = closure.Blobs[index]
		}
	}
	if rootBlob.Digest == "" || payloadBlob.Digest == "" {
		return ociArtifactTransfer{}, nil, errors.New("OCI artifact storage root does not reach its declared payload")
	}
	var rootDocument struct {
		Subject *struct {
			Digest string `json:"digest"`
		} `json:"subject,omitempty"`
	}
	if strings.Contains(rootKind, "manifest") {
		value, readErr := os.ReadFile(rootBlob.Path)
		if readErr != nil || json.Unmarshal(value, &rootDocument) != nil {
			return ociArtifactTransfer{}, nil, errors.New("decode OCI artifact storage manifest")
		}
	}
	if payloadMediaType != "" && payloadBlob.MediaType != payloadMediaType {
		return ociArtifactTransfer{}, nil, errors.New("OCI artifact payload media type changed")
	}
	if payloadSize > 0 && payloadBlob.Size != payloadSize {
		return ociArtifactTransfer{}, nil, errors.New("OCI artifact payload size changed")
	}
	digests := make([]string, 0, len(closure.Blobs))
	for _, blob := range closure.Blobs {
		digests = append(digests, blob.Digest)
	}
	sort.Strings(digests)
	return ociArtifactTransfer{
		StorageDigest: rootBlob.Digest, StorageMediaType: rootBlob.MediaType, StorageSize: rootBlob.Size,
		StorageReference: reference.String(),
		PayloadDigest:    payloadBlob.Digest, PayloadMediaType: payloadBlob.MediaType,
		PayloadSize: payloadBlob.Size, PayloadPath: scannerbundle.OCIPath(payloadBlob.Digest),
		SubjectDigest: func() string {
			if rootDocument.Subject == nil {
				return ""
			}
			return rootDocument.Subject.Digest
		}(),
		Closure: digests,
	}, closure.Blobs, nil
}

func mergeBundleTransfer(
	blobs []scannerregistry.TransferBlob,
	records map[string]scannerbundle.OCIRecord,
	sources map[string]scannerbundle.Source,
) error {
	for _, blob := range blobs {
		record := scannerbundle.OCIRecord{
			Digest: blob.Digest, Size: blob.Size, MediaType: blob.MediaType,
			Kind: blob.Kind, BundlePath: scannerbundle.OCIPath(blob.Digest),
		}
		if existing, duplicate := records[blob.Digest]; duplicate {
			if existing.Size != record.Size || existing.MediaType != record.MediaType {
				return fmt.Errorf("conflicting OCI record %s", blob.Digest)
			}
			if existing.Kind != "oci-trust-manifest" && record.Kind == "oci-trust-manifest" {
				records[blob.Digest] = record
			}
			continue
		}
		records[blob.Digest] = record
		path := blob.Path
		sources[blob.Digest] = scannerbundle.Source{
			Path: record.BundlePath, Size: record.Size, Digest: record.Digest,
			Open: func() (io.ReadCloser, error) { return os.Open(path) },
		}
	}
	return nil
}

func appendUniqueStrings(values []string, additions ...string) []string {
	seen := make(map[string]struct{}, len(values)+len(additions))
	for _, value := range values {
		seen[value] = struct{}{}
	}
	for _, value := range additions {
		if _, exists := seen[value]; !exists {
			values = append(values, value)
			seen[value] = struct{}{}
		}
	}
	sort.Strings(values)
	return values
}

func validatePortableBundleSourceLimits(sources []scannerbundle.Source) error {
	total := int64(0)
	for _, source := range sources {
		if source.Size < 0 || source.Size > ScannerReleaseBundleMaxFileBytes {
			return fmt.Errorf(
				"bundle source %q exceeds the %d-byte per-file import limit",
				source.Path, ScannerReleaseBundleMaxFileBytes,
			)
		}
		if source.Size > ScannerReleaseBundleMaxTotalBytes-total {
			return fmt.Errorf(
				"bundle sources exceed the %d-byte total import limit",
				ScannerReleaseBundleMaxTotalBytes,
			)
		}
		total += source.Size
	}
	return nil
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func snapshotPolicy(policy *scannerrelease.Policy) (portablePolicySnapshot, error) {
	snapshot := portablePolicySnapshot{
		ID: policy.ID, Scope: policy.Scope, Revision: policy.Revision,
		Schedule: json.RawMessage(policy.ScheduleJSON), Rules: json.RawMessage(policy.RulesJSON),
	}
	if !json.Valid(snapshot.Schedule) || !json.Valid(snapshot.Rules) {
		return portablePolicySnapshot{}, errors.New("release policy contains invalid JSON")
	}
	return snapshot, nil
}

func bundleImages(images []scannerrelease.ReleaseImage, tools []scannerrelease.ReleaseTool) ([]scannerbundle.ReleaseImage, error) {
	type assignment struct {
		ImageKey   string `json:"image_key"`
		Kind       string `json:"kind"`
		Entrypoint string `json:"entrypoint"`
	}
	assignments := make(map[string][]string)
	kinds := make(map[string]string)
	entrypoints := make(map[string]string)
	for _, tool := range tools {
		meta := assignment{ImageKey: "default", Kind: "wolf"}
		if strings.TrimSpace(tool.MetadataJSON) != "" {
			if err := json.Unmarshal([]byte(tool.MetadataJSON), &meta); err != nil {
				return nil, fmt.Errorf("tool %q has invalid image metadata: %w", tool.ToolKey, err)
			}
		}
		if meta.ImageKey == "" {
			meta.ImageKey = "default"
		}
		if meta.Kind == "" {
			meta.Kind = "wolf"
		}
		assignments[meta.ImageKey] = append(assignments[meta.ImageKey], tool.ToolKey)
		if previous := kinds[meta.ImageKey]; previous != "" && previous != meta.Kind {
			return nil, fmt.Errorf("image %q has conflicting tool kinds", meta.ImageKey)
		}
		kinds[meta.ImageKey] = meta.Kind
		if meta.Entrypoint != "" {
			if previous := entrypoints[meta.ImageKey]; previous != "" && previous != meta.Entrypoint {
				return nil, fmt.Errorf("image %q has conflicting entrypoints", meta.ImageKey)
			}
			entrypoints[meta.ImageKey] = meta.Entrypoint
		}
	}
	logicalImages, err := logicalReleaseImages(images)
	if err != nil {
		return nil, err
	}
	result := make([]scannerbundle.ReleaseImage, 0, len(logicalImages))
	imageKeys := make(map[string]struct{}, len(logicalImages))
	for _, image := range logicalImages {
		imageKeys[image.ImageKey] = struct{}{}
		var platforms map[string]string
		if err := json.Unmarshal([]byte(image.PlatformDigests), &platforms); err != nil {
			return nil, fmt.Errorf("image %q has invalid platform digests: %w", image.ImageKey, err)
		}
		kind := kinds[image.ImageKey]
		if scannerrelease.NormalizedImageKind(image) == scannerrelease.ReleaseImageFixer {
			if len(assignments[image.ImageKey]) != 0 {
				return nil, fmt.Errorf("fixer image %q cannot own scanner tools", image.ImageKey)
			}
			kind = "fixer"
		} else if kind == "" {
			kind = "wolf"
		}
		imageTools := append([]string(nil), assignments[image.ImageKey]...)
		sort.Strings(imageTools)
		repository := image.Repository
		if at := strings.LastIndexByte(repository, '@'); at > 0 {
			repository = repository[:at]
		}
		result = append(result, scannerbundle.ReleaseImage{
			Key: image.ImageKey, Kind: kind, Reference: repository + "@" + image.Digest,
			Digest: image.Digest, Platforms: platforms, Tools: imageTools,
			Entrypoint: entrypoints[image.ImageKey], Size: image.SizeBytes, Required: true,
		})
	}
	for imageKey := range assignments {
		if _, exists := imageKeys[imageKey]; !exists {
			return nil, fmt.Errorf("tools reference missing image %q", imageKey)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Key < result[j].Key })
	return result, nil
}

// logicalReleaseImages collapses primary/mirror inventory rows into one
// executable image identity for an offline OCI transfer. Every property that
// describes executable content must agree; target-specific signature and
// registry identities remain intact in the portable inventory.
func logicalReleaseImages(images []scannerrelease.ReleaseImage) ([]scannerrelease.ReleaseImage, error) {
	ordered := append([]scannerrelease.ReleaseImage(nil), images...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].ImageKey != ordered[j].ImageKey {
			return ordered[i].ImageKey < ordered[j].ImageKey
		}
		if ordered[i].RegistryTargetID != ordered[j].RegistryTargetID {
			return ordered[i].RegistryTargetID < ordered[j].RegistryTargetID
		}
		return ordered[i].Repository < ordered[j].Repository
	})
	result := make([]scannerrelease.ReleaseImage, 0, len(ordered))
	seenTargets := make(map[string]struct{}, len(ordered))
	for _, image := range ordered {
		if strings.TrimSpace(image.ImageKey) == "" || strings.TrimSpace(image.RegistryTargetID) == "" {
			return nil, errors.New("release image key and registry target are required")
		}
		targetKey := image.ImageKey + "\x00" + image.RegistryTargetID
		if _, duplicate := seenTargets[targetKey]; duplicate {
			return nil, fmt.Errorf("image %q repeats registry target %q", image.ImageKey, image.RegistryTargetID)
		}
		seenTargets[targetKey] = struct{}{}
		if len(result) == 0 || result[len(result)-1].ImageKey != image.ImageKey {
			result = append(result, image)
			continue
		}
		canonical := result[len(result)-1]
		var canonicalPlatforms, imagePlatforms map[string]string
		if json.Unmarshal([]byte(canonical.PlatformDigests), &canonicalPlatforms) != nil ||
			json.Unmarshal([]byte(image.PlatformDigests), &imagePlatforms) != nil {
			return nil, fmt.Errorf("image %q has invalid platform inventory", image.ImageKey)
		}
		if canonical.Digest != image.Digest || !reflect.DeepEqual(canonicalPlatforms, imagePlatforms) ||
			canonical.SizeBytes != image.SizeBytes ||
			scannerrelease.NormalizedImageKind(canonical) != scannerrelease.NormalizedImageKind(image) ||
			canonical.ProvenanceDigest != image.ProvenanceDigest || canonical.SBOMDigest != image.SBOMDigest {
			return nil, fmt.Errorf("image %q registry targets disagree on executable or evidence identity", image.ImageKey)
		}
	}
	return result, nil
}

func bytesSource(path string, value []byte) scannerbundle.Source {
	value = append([]byte(nil), value...)
	return scannerbundle.Source{
		Path: path, Size: int64(len(value)), Digest: digestBytes(value),
		Open: func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(value)), nil
		},
	}
}

func localArtifactSource(artifact scannerrelease.ReleaseArtifact, key string) (scannerbundle.Source, bool, error) {
	if artifacts.Global == nil || strings.TrimSpace(artifact.URI) == "" {
		return scannerbundle.Source{}, false, nil
	}
	root, err := filepath.Abs(artifacts.Global.Root())
	if err != nil {
		return scannerbundle.Source{}, false, nil
	}
	candidate := artifact.URI
	if parsed, parseErr := url.Parse(artifact.URI); parseErr == nil && parsed.Scheme == "file" {
		candidate = parsed.Path
	} else if parseErr == nil && parsed.Scheme != "" {
		return scannerbundle.Source{}, false, nil
	}
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, filepath.FromSlash(candidate))
	}
	candidate, err = filepath.Abs(candidate)
	if err != nil {
		return scannerbundle.Source{}, false, nil
	}
	relative, err := filepath.Rel(root, candidate)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return scannerbundle.Source{}, false, nil
	}
	info, err := os.Lstat(candidate)
	if errors.Is(err, os.ErrNotExist) {
		return scannerbundle.Source{}, false, fmt.Errorf("local release artifact %q is missing", artifact.ID)
	}
	if err != nil {
		return scannerbundle.Source{}, false, err
	}
	if !info.Mode().IsRegular() {
		return scannerbundle.Source{}, false, fmt.Errorf("release artifact %q is not a regular file", artifact.ID)
	}
	if info.Size() != artifact.SizeBytes {
		return scannerbundle.Source{}, false, fmt.Errorf("release artifact %q size changed after publication", artifact.ID)
	}
	bundlePath := "evidence/artifacts/" + safeBundleName(key)
	return scannerbundle.Source{
		Path: bundlePath, Size: artifact.SizeBytes, Digest: artifact.Digest,
		Open: func() (io.ReadCloser, error) {
			file, openErr := os.Open(candidate)
			if openErr != nil {
				return nil, openErr
			}
			openedInfo, statErr := file.Stat()
			if statErr != nil || !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
				_ = file.Close()
				return nil, fmt.Errorf("release artifact %q changed while opening", artifact.ID)
			}
			return file, nil
		},
	}, true, nil
}

func safeBundleName(value string) string {
	var result strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			result.WriteRune(r)
		} else {
			result.WriteByte('-')
		}
	}
	if result.Len() == 0 {
		return "artifact"
	}
	return result.String()
}

func artifactBundleKey(artifact scannerrelease.ReleaseArtifact, index int) string {
	identity := artifact.ID
	if identity == "" {
		identity = fmt.Sprintf("%s\x00%s\x00%s\x00%d", artifact.ArtifactType, artifact.Digest, artifact.URI, index)
	}
	sum := sha256.Sum256([]byte(identity))
	return "release-artifact-" + hex.EncodeToString(sum[:12])
}

func portableReleaseArtifacts(artifacts []scannerrelease.ReleaseArtifact) []scannerrelease.ReleaseArtifact {
	portable := make([]scannerrelease.ReleaseArtifact, 0, len(artifacts))
	for _, artifact := range artifacts {
		if reservedPortableArtifactType(artifact.ArtifactType) {
			continue
		}
		portable = append(portable, artifact)
	}
	return portable
}

func reservedPortableArtifactType(artifactType string) bool {
	switch artifactType {
	case "offline-release-bundle", portableInventoryType, "offline-image-signature-verification":
		return true
	default:
		return false
	}
}

func sortPortableInventory(inventory *portableReleaseInventory) {
	sort.Slice(inventory.Tools, func(i, j int) bool { return inventory.Tools[i].ToolKey < inventory.Tools[j].ToolKey })
	sort.Slice(inventory.Images, func(i, j int) bool {
		if inventory.Images[i].ImageKey == inventory.Images[j].ImageKey {
			return inventory.Images[i].RegistryTargetID < inventory.Images[j].RegistryTargetID
		}
		return inventory.Images[i].ImageKey < inventory.Images[j].ImageKey
	})
	sort.Slice(inventory.Artifacts, func(i, j int) bool {
		if inventory.Artifacts[i].ArtifactType == inventory.Artifacts[j].ArtifactType {
			return inventory.Artifacts[i].ID < inventory.Artifacts[j].ID
		}
		return inventory.Artifacts[i].ArtifactType < inventory.Artifacts[j].ArtifactType
	})
}

func readPortableInventory(imported *scannerbundle.ImportedBundle) (*portableReleaseInventory, error) {
	record, exists := imported.Files[portableInventoryPath]
	if !exists {
		return nil, errors.New("portable release inventory is missing")
	}
	if record.Size > maxPortableInventoryBytes {
		return nil, fmt.Errorf("portable release inventory exceeds the %d-byte limit", maxPortableInventoryBytes)
	}
	raw, err := os.ReadFile(filepath.Join(imported.Root, filepath.FromSlash(portableInventoryPath)))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) != record.Size || digestBytes(raw) != record.Digest {
		return nil, errors.New("portable release inventory does not match the verified bundle index")
	}
	var inventory portableReleaseInventory
	if err := decodeStrictJSON(raw, &inventory); err != nil {
		return nil, fmt.Errorf("decode portable release inventory: %w", err)
	}
	if inventory.SchemaVersion != portableInventorySchema {
		return nil, fmt.Errorf("unsupported portable inventory schema %q", inventory.SchemaVersion)
	}
	if err := validatePortableInventory(imported.Manifest, &inventory); err != nil {
		return nil, err
	}
	return &inventory, nil
}

func validatePortableInventory(manifest scannerbundle.ReleaseManifest, inventory *portableReleaseInventory) error {
	if inventory.Release.ID != manifest.ReleaseID ||
		inventory.Release.LockDigest != manifest.LockDigest ||
		inventory.Release.DefinitionCommit != manifest.DefinitionCommit {
		return errors.New("portable inventory release identity does not match the release manifest")
	}
	policyDigest, err := digestJSON(inventory.Policy)
	if err != nil {
		return err
	}
	if policyDigest != manifest.BuildPolicyDigest {
		return errors.New("portable inventory policy does not match the release manifest")
	}
	expectedImages, err := bundleImages(inventory.Images, inventory.Tools)
	if err != nil {
		return err
	}
	actualImages := append([]scannerbundle.ReleaseImage(nil), manifest.Images...)
	sort.Slice(actualImages, func(i, j int) bool { return actualImages[i].Key < actualImages[j].Key })
	for index := range expectedImages {
		for _, actual := range actualImages {
			if actual.Key == expectedImages[index].Key {
				expectedImages[index].SourceReference = actual.SourceReference
				expectedImages[index].SourceDigest = actual.SourceDigest
				expectedImages[index].BlobDigests = append([]string(nil), actual.BlobDigests...)
				break
			}
		}
	}
	if !reflect.DeepEqual(expectedImages, actualImages) {
		return errors.New("portable inventory images do not match the release manifest")
	}
	manifestArtifacts := make(map[string]scannerbundle.ReleaseArtifact, len(manifest.Artifacts))
	for _, artifact := range manifest.Artifacts {
		manifestArtifacts[artifact.Key] = artifact
	}
	for i, artifact := range inventory.Artifacts {
		if reservedPortableArtifactType(artifact.ArtifactType) {
			return fmt.Errorf("portable inventory uses reserved artifact type %q", artifact.ArtifactType)
		}
		key := artifactBundleKey(artifact, i)
		expected, ok := manifestArtifacts[key]
		if !ok || expected.Type != artifact.ArtifactType || expected.MediaType != artifact.MediaType ||
			expected.Digest != artifact.Digest || expected.Size != artifact.SizeBytes {
			return fmt.Errorf("portable inventory artifact %q does not match the release manifest", artifact.ID)
		}
		delete(manifestArtifacts, key)
	}
	for _, image := range inventory.Images {
		key := imageSignatureBundleKey(image)
		if signature, exists := manifestArtifacts[key]; exists {
			if signature.Type != "image-signature" || signature.Digest != image.SignatureDigest ||
				signature.StorageDigest != image.SignatureArtifactDigest ||
				signature.StorageReference != strings.TrimPrefix(image.SignatureArtifactURI, "oci://") ||
				signature.StorageMediaType != image.SignatureMediaType ||
				signature.StorageSize != image.SignatureArtifactSizeBytes {
				return fmt.Errorf("portable inventory image %q target %q signature does not match the release manifest", image.ImageKey, image.RegistryTargetID)
			}
			delete(manifestArtifacts, key)
		}
	}
	inventoryArtifact, ok := manifestArtifacts[portableInventoryType]
	if !ok || inventoryArtifact.Type != portableInventoryType ||
		inventoryArtifact.BundlePath != portableInventoryPath {
		return errors.New("release manifest does not declare the portable inventory artifact")
	}
	delete(manifestArtifacts, portableInventoryType)
	if len(manifestArtifacts) != 0 {
		return errors.New("release manifest contains artifacts absent from the portable inventory")
	}
	return nil
}

var errReleaseBundleConflict = errors.New("release bundle conflicts with existing state")

func persistPortableRelease(
	ctx context.Context,
	store scannerrelease.Persistence,
	imported *scannerbundle.ImportedBundle,
	portable *portableReleaseInventory,
	finalPath, uploadPath, bundleURI, bundleDigest string,
	bundleSize int64,
	signatureStatus, signatureKeyID, actor, reason, requestKey string,
	registryOverrides map[string]string,
	externalVerificationResults []bundleImageVerificationResult,
) (bool, error) {
	receiptPayload, receiptDigest, receiptURI, receiptPath, err := prepareBundleImageVerificationReceipt(
		imported, portable, finalPath, bundleDigest, externalVerificationResults,
	)
	if err != nil {
		return false, err
	}
	existing, err := store.GetRelease(ctx, imported.Manifest.ReleaseID)
	switch {
	case err == nil:
		if existing.Imported && existing.ManifestDigest == imported.ManifestDigest {
			releaseArtifacts, listErr := store.ListArtifacts(ctx, existing.ID, "")
			if listErr != nil {
				return false, listErr
			}
			bundleFound := false
			receiptFound := false
			for _, artifact := range releaseArtifacts {
				if artifact.ArtifactType != "offline-release-bundle" {
					if artifact.ArtifactType == "offline-image-signature-verification" {
						receiptFound = true
						if len(receiptPayload) == 0 || artifact.Digest != receiptDigest || artifact.URI != receiptURI {
							return false, fmt.Errorf("%w: the release is already associated with different image-verification evidence", errReleaseBundleConflict)
						}
					}
					continue
				}
				bundleFound = true
				if artifact.Digest != bundleDigest || artifact.URI != bundleURI {
					return false, fmt.Errorf("%w: the release manifest is already associated with a different bundle payload", errReleaseBundleConflict)
				}
				if info, statErr := os.Stat(finalPath); errors.Is(statErr, os.ErrNotExist) {
					if linkErr := os.Link(uploadPath, finalPath); linkErr != nil && !errors.Is(linkErr, os.ErrExist) {
						return false, fmt.Errorf("restore durable release bundle: %w", linkErr)
					}
				} else if statErr != nil || !info.Mode().IsRegular() || info.Size() != bundleSize {
					return false, errors.New("persisted release bundle is unavailable or does not match its inventory")
				}
			}
			if !bundleFound {
				return false, fmt.Errorf("%w: imported release has no durable bundle artifact", errReleaseBundleConflict)
			}
			if receiptFound != (len(receiptPayload) != 0) {
				return false, fmt.Errorf("%w: image-verification evidence differs from the original import decision", errReleaseBundleConflict)
			}
			if receiptFound {
				persistedReceipt, readErr := os.ReadFile(receiptPath)
				if readErr != nil || !bytes.Equal(persistedReceipt, receiptPayload) {
					return false, errors.New("persisted image-verification receipt is unavailable or does not match its inventory")
				}
			}
			return false, nil
		}
		return false, fmt.Errorf("%w: release ID %q already exists with a different origin or manifest", errReleaseBundleConflict, imported.Manifest.ReleaseID)
	case !errors.Is(err, sql.ErrNoRows):
		return false, err
	}

	ownsBundle := false
	if err := os.Link(uploadPath, finalPath); err == nil {
		ownsBundle = true
	} else if !errors.Is(err, os.ErrExist) {
		return false, fmt.Errorf("persist release bundle: %w", err)
	}
	cleanup := true
	ownsReceipt := false
	if len(receiptPayload) != 0 {
		ownsReceipt, err = writeContentAddressedReceipt(receiptPath, receiptPayload)
		if err != nil {
			if ownsBundle {
				_ = os.Remove(finalPath)
			}
			return false, err
		}
	}
	defer func() {
		if cleanup && ownsBundle {
			_ = os.Remove(finalPath)
		}
		if cleanup && ownsReceipt {
			_ = os.Remove(receiptPath)
		}
	}()

	policy, err := ensureImportedPolicy(ctx, store, portable.Policy, imported.Manifest.BuildPolicyDigest, actor)
	if err != nil {
		return false, err
	}
	registryIDs := make(map[string]string)
	for _, image := range portable.Images {
		host := registryHost(image.Repository)
		if id := registryOverrides[host]; id != "" {
			registryIDs[host] = id
			continue
		}
		if _, exists := registryIDs[host]; exists {
			continue
		}
		registryIDs[host], err = ensureImportedRegistry(ctx, store, host, actor)
		if err != nil {
			return false, err
		}
	}

	candidateID := deterministicImportID("offline-candidate-", imported.ManifestDigest, 32)
	requiredGates := `["bundle_integrity","bundle_signature_policy"]`
	if imported.SchemaVersion == scannerbundle.BundleSchemaV2 {
		requiredGates = `["bundle_integrity","bundle_signature_policy","external_image_signatures"]`
	}
	candidate := scannerrelease.Candidate{
		ID: candidateID, DefinitionCommit: imported.Manifest.DefinitionCommit,
		LockDigest: imported.Manifest.LockDigest, LockURI: bundleURI + "#" + scannerbundle.ManifestPath,
		RiskSummaryJSON: `{"source":"offline_release_bundle"}`, State: scannerrelease.CandidatePublished,
		RequiredGatesJSON: requiredGates,
		PolicyDecision:    signatureStatus, PolicyID: policy.ID, PolicyRevision: policy.Revision,
		Actor: actor, IdempotencyKey: "offline-import-request:" + requestKey,
	}
	externalVerificationDigest := ""
	if len(externalVerificationResults) != 0 {
		externalVerificationDigest = bundleImageVerificationDigest(externalVerificationResults)
	}
	eventPayload, err := json.Marshal(struct {
		ManifestDigest             string `json:"manifest_digest"`
		SignatureStatus            string `json:"signature_status"`
		ExternalSignaturesVerified bool   `json:"external_signatures_verified"`
		ExternalVerificationDigest string `json:"external_signature_verification_digest,omitempty"`
		ExternalReceiptDigest      string `json:"external_signature_receipt_digest,omitempty"`
	}{
		ManifestDigest: imported.ManifestDigest, SignatureStatus: signatureStatus,
		ExternalSignaturesVerified: len(externalVerificationResults) != 0,
		ExternalVerificationDigest: externalVerificationDigest,
		ExternalReceiptDigest:      receiptDigest,
	})
	if err != nil {
		return false, err
	}
	command := scannerrelease.TransitionCommand{
		Actor: actor, Reason: reason, PolicyRevision: policy.Revision,
		IdempotencyKey: requestKey + "/offline-import", PayloadJSON: string(eventPayload),
	}
	existingCandidate, candidateErr := store.GetCandidate(ctx, candidate.ID)
	if errors.Is(candidateErr, sql.ErrNoRows) {
		if err := store.CreateCandidate(ctx, &candidate, command); err != nil {
			if !errors.Is(err, scannerrelease.ErrIdempotencyConflict) {
				return false, err
			}
			return false, fmt.Errorf("%w: Idempotency-Key is already bound to a different release bundle", errReleaseBundleConflict)
		}
		if candidate.ID != candidateID || candidate.LockDigest != imported.Manifest.LockDigest {
			return false, fmt.Errorf("%w: Idempotency-Key is already bound to a different release bundle", errReleaseBundleConflict)
		}
	} else if candidateErr != nil {
		return false, candidateErr
	} else if existingCandidate.DefinitionCommit != candidate.DefinitionCommit ||
		existingCandidate.LockDigest != candidate.LockDigest ||
		existingCandidate.PolicyID != candidate.PolicyID ||
		existingCandidate.PolicyRevision != candidate.PolicyRevision {
		return false, fmt.Errorf("%w: imported candidate identity is already bound to different content", errReleaseBundleConflict)
	} else {
		candidate = *existingCandidate
	}

	release := portable.Release
	release.ID = imported.Manifest.ReleaseID
	release.CandidateID = candidate.ID
	release.LockDigest = imported.Manifest.LockDigest
	release.ManifestDigest = imported.ManifestDigest
	release.ManifestURI = bundleURI + "#" + scannerbundle.ManifestPath
	release.State = importedReleaseState(portable.Release.State)
	release.SignerIdentity = importedSignerIdentity(signatureStatus, signatureKeyID)
	release.PolicyID = policy.ID
	release.PolicyRevision = policy.Revision
	release.DefinitionCommit = imported.Manifest.DefinitionCommit
	release.Imported = true
	release.Protected = true
	release.RollbackEligible = portable.Release.RollbackEligible &&
		strings.HasPrefix(signatureStatus, "verified-") &&
		(imported.SchemaVersion != scannerbundle.BundleSchemaV2 || len(externalVerificationResults) != 0)
	release.Version = 1
	release.CreatedAt = time.Time{}
	release.UpdatedAt = time.Time{}
	if release.PublishedAt.IsZero() {
		release.PublishedAt = imported.Manifest.GeneratedAt
	}

	tools := append([]scannerrelease.ReleaseTool(nil), portable.Tools...)
	for i := range tools {
		tools[i].ID = uuid.NewString()
		tools[i].ReleaseID = release.ID
		tools[i].CreatedAt = time.Time{}
	}
	images := append([]scannerrelease.ReleaseImage(nil), portable.Images...)
	for i := range images {
		images[i].ID = uuid.NewString()
		images[i].ReleaseID = release.ID
		images[i].RegistryTargetID = registryIDs[registryHost(images[i].Repository)]
		images[i].CreatedAt = time.Time{}
	}
	releaseArtifacts := append([]scannerrelease.ReleaseArtifact(nil), portable.Artifacts...)
	bundleArtifactPaths := make(map[string]string)
	for _, artifact := range imported.Manifest.Artifacts {
		if artifact.BundlePath != "" {
			bundleArtifactPaths[artifact.Key] = artifact.BundlePath
		}
	}
	for i := range releaseArtifacts {
		releaseArtifacts[i].ID = uuid.NewString()
		releaseArtifacts[i].ReleaseID = release.ID
		releaseArtifacts[i].CandidateID = ""
		releaseArtifacts[i].CreatedAt = time.Time{}
		if bundlePath := bundleArtifactPaths[artifactBundleKey(portable.Artifacts[i], i)]; bundlePath != "" {
			releaseArtifacts[i].URI = bundleURI + "#" + bundlePath
		}
	}
	bundleMediaType := ScannerReleaseBundleMediaType
	if imported.SchemaVersion == scannerbundle.BundleSchemaV2 {
		bundleMediaType = ScannerReleaseBundleMediaTypeV2
	}
	releaseArtifacts = append(releaseArtifacts,
		scannerrelease.ReleaseArtifact{
			ID: uuid.NewString(), ReleaseID: release.ID, ArtifactType: "offline-release-bundle",
			MediaType: bundleMediaType, URI: bundleURI, Digest: bundleDigest,
			SizeBytes: bundleSize, RetentionClass: "release", Protected: true,
		},
		scannerrelease.ReleaseArtifact{
			ID: uuid.NewString(), ReleaseID: release.ID, ArtifactType: portableInventoryType,
			MediaType:      "application/vnd.wolf.scanner-release.inventory.v1+json",
			URI:            bundleURI + "#" + portableInventoryPath,
			Digest:         imported.Files[portableInventoryPath].Digest,
			SizeBytes:      imported.Files[portableInventoryPath].Size,
			RetentionClass: "release", Protected: true,
		},
	)
	if len(receiptPayload) != 0 {
		releaseArtifacts = append(releaseArtifacts, scannerrelease.ReleaseArtifact{
			ID: uuid.NewString(), ReleaseID: release.ID,
			ArtifactType: "offline-image-signature-verification",
			MediaType:    "application/vnd.wolf.scanner-offline-image-verification-receipt.v1+json",
			URI:          receiptURI, Digest: receiptDigest, SizeBytes: int64(len(receiptPayload)),
			RetentionClass: "release", Protected: true,
		})
	}
	if err := store.CreateRelease(ctx, &scannerrelease.ReleaseInventory{
		Release: release, Tools: tools, Images: images, Artifacts: releaseArtifacts,
	}, command); err != nil {
		if errors.Is(err, scannerrelease.ErrIdempotencyConflict) || isDatabaseUniquenessError(err) {
			return false, fmt.Errorf("%w: release name, manifest digest, or imported identity is already in use", errReleaseBundleConflict)
		}
		return false, err
	}
	persisted, err := store.GetRelease(ctx, release.ID)
	if err != nil {
		return false, err
	}
	if persisted.ManifestDigest != imported.ManifestDigest || !persisted.Imported {
		return false, fmt.Errorf("%w: persistence returned a different release identity", errReleaseBundleConflict)
	}
	cleanup = false
	return true, nil
}

func prepareBundleImageVerificationReceipt(
	imported *scannerbundle.ImportedBundle,
	portable *portableReleaseInventory,
	finalPath, bundleDigest string,
	results []bundleImageVerificationResult,
) ([]byte, string, string, string, error) {
	if len(results) == 0 {
		return nil, "", "", "", nil
	}
	if imported == nil || portable == nil || !scannersigning.ValidDigest(bundleDigest) {
		return nil, "", "", "", errors.New("image-verification receipt inputs are incomplete")
	}
	results = canonicalBundleImageVerificationResults(results)
	if err := validateBundleImageVerificationSet(portable, results); err != nil {
		return nil, "", "", "", err
	}
	verificationDigest := bundleImageVerificationDigest(results)
	receipt := bundleImageVerificationReceipt{
		SchemaVersion: bundleImageReceiptSchema, ReleaseID: imported.Manifest.ReleaseID,
		ManifestDigest: imported.ManifestDigest, BundleDigest: bundleDigest,
		VerificationDigest: verificationDigest, TrustPolicyDigest: results[0].TrustPolicyDigest,
		Results: results,
	}
	payload, err := json.Marshal(receipt)
	if err != nil {
		return nil, "", "", "", err
	}
	digest := digestBytes(payload)
	name := strings.TrimPrefix(digest, "sha256:") + ".offline-image-verification.json"
	return payload, digest,
		"artifact://scanner-release-bundles/" + name,
		filepath.Join(filepath.Dir(finalPath), name), nil
}

func writeContentAddressedReceipt(path string, payload []byte) (bool, error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if errors.Is(err, os.ErrExist) {
		existing, readErr := os.ReadFile(path)
		if readErr != nil || !bytes.Equal(existing, payload) {
			return false, errors.New("existing image-verification receipt does not match its content address")
		}
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("create image-verification receipt: %w", err)
	}
	remove := true
	defer func() {
		_ = file.Close()
		if remove {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(payload); err != nil {
		return false, fmt.Errorf("write image-verification receipt: %w", err)
	}
	if err := file.Sync(); err != nil {
		return false, fmt.Errorf("sync image-verification receipt: %w", err)
	}
	if err := file.Close(); err != nil {
		return false, fmt.Errorf("close image-verification receipt: %w", err)
	}
	remove = false
	return true, nil
}

func ensureImportedPolicy(
	ctx context.Context,
	store scannerrelease.Persistence,
	snapshot portablePolicySnapshot,
	digest, actor string,
) (*scannerrelease.Policy, error) {
	id := deterministicImportID("offline-policy-", digest, 32)
	existing, err := store.GetPolicy(ctx, id)
	if err == nil {
		if existing.ScheduleJSON != string(snapshot.Schedule) || existing.RulesJSON != string(snapshot.Rules) {
			return nil, fmt.Errorf("%w: imported policy digest is already bound to different policy data", errReleaseBundleConflict)
		}
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	revision := snapshot.Revision
	if revision < 1 {
		revision = 1
	}
	policy := &scannerrelease.Policy{
		ID: id, Scope: "offline:" + strings.TrimPrefix(digest, "sha256:"),
		Revision: revision, Enabled: true, ScheduleJSON: string(snapshot.Schedule),
		RulesJSON: string(snapshot.Rules), CreatedBy: actor,
	}
	if err := store.CreatePolicy(ctx, policy); err != nil {
		return nil, err
	}
	return policy, nil
}

func ensureImportedRegistry(
	ctx context.Context,
	store scannerrelease.Persistence,
	host, actor string,
) (string, error) {
	id := deterministicImportID("offline-registry-", digestBytes([]byte(host)), 24)
	existing, err := store.GetRegistryTarget(ctx, id)
	if err == nil {
		if existing.Host != host || existing.Namespace != "wolf-offline-imports" {
			return "", fmt.Errorf("%w: imported registry identity is already bound to different metadata", errReleaseBundleConflict)
		}
		return existing.ID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	target := &scannerrelease.RegistryTarget{
		ID: id, Name: "Offline import " + host, Type: scannerrelease.RegistryAirGap,
		Host: host, Namespace: "wolf-offline-imports", PlatformPolicyJSON: "{}",
		Enabled: true, CreatedBy: actor, TrustPolicyRef: bundleTrustPolicyEnv,
	}
	if err := store.CreateRegistryTarget(ctx, target); err == nil {
		return target.ID, nil
	}
	targets, listErr := store.ListRegistryTargets(ctx, false)
	if listErr != nil {
		return "", listErr
	}
	for _, candidate := range targets {
		if candidate.Host == target.Host && candidate.Namespace == target.Namespace {
			return candidate.ID, nil
		}
	}
	return "", fmt.Errorf("create imported registry metadata for %q", host)
}

func registryHost(repository string) string {
	repository = strings.TrimSpace(repository)
	if at := strings.LastIndexByte(repository, '@'); at > 0 {
		repository = repository[:at]
	}
	first := strings.SplitN(repository, "/", 2)[0]
	if strings.Contains(first, ".") || strings.Contains(first, ":") || first == "localhost" {
		return first
	}
	return "docker.io"
}

func importedReleaseState(source scannerrelease.ReleaseState) scannerrelease.ReleaseState {
	switch source {
	case scannerrelease.ReleaseDeprecated, scannerrelease.ReleaseRevoked:
		return source
	default:
		return scannerrelease.ReleasePublished
	}
}

func importedSignerIdentity(status, keyID string) string {
	if strings.HasPrefix(status, "verified-") {
		return "offline-" + strings.TrimPrefix(status, "verified-") + ":" + keyID
	}
	switch status {
	case "present-not-verified":
		return "unverified-offline-signature:" + keyID
	default:
		return "unsigned-offline-bundle"
	}
}

func verifyBundleSignature(
	ctx context.Context,
	imported *scannerbundle.ImportedBundle,
	verifier scannerbundle.ManifestVerifier,
	trustConfigured, allowUnverified bool,
) (string, string, error) {
	if imported.Signature == nil {
		if !allowUnverified {
			return "", "", errors.New("signed release bundle is required; configure WOLF_SCANNER_BUNDLE_TRUST_POLICY_FILE or explicitly allow an unverified import")
		}
		return "unsigned", "", nil
	}
	keyID := imported.Signature.KeyID
	if trustConfigured && verifier != nil {
		canonical, err := imported.Manifest.CanonicalJSON()
		if err != nil {
			return "", keyID, err
		}
		if err := verifier.VerifyManifest(ctx, canonical, *imported.Signature); err == nil {
			return "verified-" + imported.Signature.Algorithm, keyID, nil
		} else if !allowUnverified {
			return "", keyID, err
		}
	} else if !allowUnverified {
		return "", keyID, errors.New("no scanner bundle trust policy is configured")
	}
	return "present-not-verified", keyID, nil
}

func parseAllowUnverified(r *http.Request) (bool, error) {
	value := strings.TrimSpace(r.URL.Query().Get("allow_unverified"))
	if value == "" {
		return false, nil
	}
	parsed, err := strconvParseBool(value)
	if err != nil {
		return false, errors.New("allow_unverified must be true or false")
	}
	return parsed, nil
}

func strconvParseBool(value string) (bool, error) {
	switch strings.ToLower(value) {
	case "true", "1":
		return true, nil
	case "false", "0":
		return false, nil
	default:
		return false, errors.New("invalid boolean")
	}
}

func validateBundleContentType(value string) error {
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil {
		return errors.New("Content-Type must identify a scanner release bundle")
	}
	if mediaType != ScannerReleaseBundleMediaType &&
		mediaType != ScannerReleaseBundleMediaTypeV2 &&
		mediaType != "application/octet-stream" {
		return fmt.Errorf("Content-Type must be %s, %s, or application/octet-stream", ScannerReleaseBundleMediaType, ScannerReleaseBundleMediaTypeV2)
	}
	return nil
}

func parseOptionalBool(value string) (bool, error) {
	if strings.TrimSpace(value) == "" {
		return false, nil
	}
	return strconvParseBool(value)
}

func uploadImportedOCI(
	ctx context.Context,
	store scannerrelease.Persistence,
	imported *scannerbundle.ImportedBundle,
	inventory *portableReleaseInventory,
	targetID string,
) ([]releaseBundleRegistryMapping, map[string]string, error) {
	target, err := store.GetRegistryTarget(ctx, targetID)
	if err != nil {
		return nil, nil, fmt.Errorf("load destination registry: %w", err)
	}
	if !target.Enabled {
		return nil, nil, errors.New("destination registry is disabled")
	}
	client, host, err := scannerRegistryClient(ctx, target)
	if err != nil {
		return nil, nil, err
	}
	records := make(map[string]scannerbundle.OCIRecord, len(imported.Manifest.OCIRecords))
	for _, record := range imported.Manifest.OCIRecords {
		records[record.Digest] = record
	}
	portableByKey := make(map[string]int)
	for index := range inventory.Images {
		portableByKey[inventory.Images[index].ImageKey] = index
	}
	var mappings []releaseBundleRegistryMapping
	overrides := map[string]string{host: target.ID}
	for _, image := range imported.Manifest.Images {
		source, err := scannerregistry.ParseReference(image.Reference)
		if err != nil {
			return nil, nil, err
		}
		repository := source.Repository
		if target.Namespace != "" {
			name := repository
			if slash := strings.LastIndexByte(name, '/'); slash >= 0 {
				name = name[slash+1:]
			}
			repository = strings.Trim(target.Namespace, "/") + "/" + name
		}
		result, err := client.PushBundleImage(
			ctx, host, repository, image, records, imported.Root,
		)
		if err != nil {
			return nil, nil, fmt.Errorf("upload image %q: %w", image.Key, err)
		}
		position, exists := portableByKey[image.Key]
		if !exists {
			return nil, nil, fmt.Errorf("portable inventory image %q is missing", image.Key)
		}
		oldHost := registryHost(inventory.Images[position].Repository)
		overrides[oldHost] = target.ID
		inventory.Images[position].Repository = host + "/" + repository
		inventory.Images[position].RegistryTargetID = target.ID
		mappings = append(mappings, releaseBundleRegistryMapping{
			ImageKey: image.Key, SourceReference: image.SourceReference,
			DestinationReference: result.Reference, Digest: result.Digest,
			ReadBackVerified: result.ReadBack,
		})
	}
	uploadedArtifacts := make(map[string]struct{})
	for _, artifact := range imported.Manifest.Artifacts {
		if artifact.StorageDigest == "" {
			continue
		}
		source, err := scannerregistry.ParseReference(artifact.StorageReference)
		if err != nil || source.Digest != artifact.StorageDigest {
			return nil, nil, fmt.Errorf("artifact %q has an invalid storage reference", artifact.Key)
		}
		repository := source.Repository
		if target.Namespace != "" {
			name := repository
			if slash := strings.LastIndexByte(name, '/'); slash >= 0 {
				name = name[slash+1:]
			}
			repository = strings.Trim(target.Namespace, "/") + "/" + name
		}
		identity := repository + "\x00" + artifact.StorageDigest
		if _, exists := uploadedArtifacts[identity]; exists {
			continue
		}
		result, err := client.PushBundleArtifact(
			ctx, host, repository, artifact, records, imported.Root,
		)
		if err != nil || !result.ReadBack || result.Digest != artifact.StorageDigest {
			if err == nil {
				err = errors.New("artifact readback identity mismatch")
			}
			return nil, nil, fmt.Errorf("upload artifact %q: %w", artifact.Key, err)
		}
		uploadedArtifacts[identity] = struct{}{}
	}
	sort.Slice(mappings, func(i, j int) bool {
		return mappings[i].ImageKey < mappings[j].ImageKey
	})
	return mappings, overrides, nil
}

func loadBundleSigner(
	binding scannersigning.Binding,
	releaseID string,
) (scannerbundle.ManifestSigner, string, error) {
	profilePath := strings.TrimSpace(os.Getenv("WOLF_SCANNER_SIGNER_PROFILE_FILE"))
	adapterPath := strings.TrimSpace(os.Getenv("WOLF_SCANNER_SIGNER_ADAPTER"))
	if profilePath == "" && adapterPath == "" {
		return nil, "unsigned", nil
	}
	if profilePath == "" || adapterPath == "" ||
		!filepath.IsAbs(profilePath) || !filepath.IsAbs(adapterPath) {
		return nil, "", errors.New(
			"absolute WOLF_SCANNER_SIGNER_PROFILE_FILE and WOLF_SCANNER_SIGNER_ADAPTER are both required",
		)
	}
	profile, err := scannersigning.ReadProfileFile(profilePath)
	if err != nil {
		return nil, "", err
	}
	journal := strings.TrimSpace(os.Getenv("WOLF_SCANNER_SIGNER_JOURNAL"))
	if journal == "" && artifacts.Global != nil {
		journal = filepath.Join(artifacts.Global.Root(), "scanner-signing-journal")
	}
	if !filepath.IsAbs(journal) {
		return nil, "", errors.New("absolute WOLF_SCANNER_SIGNER_JOURNAL or artifact root is required")
	}
	environment := selectedBundleSignerEnvironment()
	return scannersigning.ManifestSigner{
		Service: scannersigning.Service{
			Adapter: scannersigning.CommandAdapter{
				Path: adapterPath, Environment: environment,
			},
			JournalRoot: journal,
		},
		Profile: profile, Binding: binding,
		ArtifactURI: "wolf-bundle://release/" + releaseID + "/manifest",
	}, "signed-" + string(profile.Provider), nil
}

func loadBundleTrustStore() (scannerbundle.ManifestVerifier, bool, error) {
	path := strings.TrimSpace(os.Getenv(bundleTrustPolicyEnv))
	if path == "" {
		return nil, false, nil
	}
	var config bundleTrustPolicy
	if err := readStrictJSONFile(path, &config); err != nil {
		return nil, false, err
	}
	if config.SchemaVersion != bundleTrustSchema {
		return nil, false, fmt.Errorf("unsupported scanner bundle trust schema %q", config.SchemaVersion)
	}
	if len(config.Keys) == 0 {
		return nil, false, errors.New("scanner bundle trust policy has no trusted keys")
	}
	store := scannerbundle.Ed25519TrustStore{}
	policyStore := scannersigning.BundleTrustStore{}
	for _, key := range config.Keys {
		if strings.TrimSpace(key.KeyID) == "" {
			return nil, false, errors.New("scanner bundle trust key_id is required")
		}
		if _, exists := store[key.KeyID]; exists {
			return nil, false, fmt.Errorf("duplicate scanner bundle trust key %q", key.KeyID)
		}
		if _, exists := policyStore[key.KeyID]; exists {
			return nil, false, fmt.Errorf("duplicate scanner bundle trust key %q", key.KeyID)
		}
		if key.ProfileDigest != "" {
			if !scannersigning.ValidDigest(key.ProfileDigest) ||
				!scannersigning.ValidDigest(key.TrustRootDigest) ||
				key.Algorithm == "" || key.Identity == "" || key.Issuer == "" ||
				key.Subject == "" || key.PublicKeyPEM == "" {
				return nil, false, fmt.Errorf("trusted signer profile %q is incomplete", key.KeyID)
			}
			policyStore[key.KeyID] = scannersigning.BundleTrustProfile{
				Algorithm: key.Algorithm, ProfileDigest: key.ProfileDigest,
				Identity: key.Identity, Issuer: key.Issuer, Subject: key.Subject,
				TrustRootDigest: key.TrustRootDigest,
				PublicKeyPEM:    key.PublicKeyPEM, Revoked: key.Revoked,
			}
			continue
		}
		if key.Algorithm != "" && key.Algorithm != "ed25519" {
			return nil, false, fmt.Errorf("unsupported legacy scanner bundle trust algorithm %q", key.Algorithm)
		}
		raw, err := decodeBase64Key(key.PublicKey)
		if err != nil || len(raw) != ed25519.PublicKeySize {
			return nil, false, fmt.Errorf("trusted Ed25519 key %q is invalid", key.KeyID)
		}
		store[key.KeyID] = ed25519.PublicKey(raw)
	}
	return bundleTrustVerifier{legacy: store, policy: policyStore}, true, nil
}

func selectedBundleSignerEnvironment() []string {
	names := []string{
		"PATH", "SSL_CERT_FILE", "SSL_CERT_DIR",
		"AWS_REGION", "AWS_DEFAULT_REGION", "AWS_ROLE_ARN",
		"AWS_WEB_IDENTITY_TOKEN_FILE", "AWS_CONTAINER_CREDENTIALS_FULL_URI",
		"GOOGLE_APPLICATION_CREDENTIALS", "GOOGLE_CLOUD_PROJECT",
		"AZURE_CLIENT_ID", "AZURE_TENANT_ID", "AZURE_FEDERATED_TOKEN_FILE",
		"PKCS11_MODULE_PATH", "PKCS11_CONFIG",
		"SIGSTORE_ID_TOKEN_FILE", "SIGSTORE_FULCIO_URL", "SIGSTORE_REKOR_URL",
	}
	environment := make([]string, 0, len(names))
	for _, name := range names {
		if value, ok := os.LookupEnv(name); ok {
			environment = append(environment, name+"="+value)
		}
	}
	return environment
}

func readStrictJSONFile(path string, target any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > maxBundleKeyConfigBytes {
		return fmt.Errorf("scanner bundle key configuration must be a regular file no larger than %d bytes", maxBundleKeyConfigBytes)
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxBundleKeyConfigBytes+1))
	if err != nil {
		return err
	}
	return decodeStrictJSON(raw, target)
}

func decodeStrictJSON(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("unexpected trailing JSON")
		}
		return err
	}
	return nil
}

func decodeBase64Key(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	if raw, err := base64.RawStdEncoding.DecodeString(value); err == nil {
		return raw, nil
	}
	return base64.StdEncoding.DecodeString(value)
}

func digestJSON(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return digestBytes(raw), nil
}

func digestBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func deterministicImportID(prefix, digest string, length int) string {
	value := strings.TrimPrefix(digest, "sha256:")
	if len(value) > length {
		value = value[:length]
	}
	return prefix + value
}

func isDatabaseUniquenessError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique constraint") ||
		strings.Contains(message, "duplicate key") ||
		strings.Contains(message, "already exists")
}
