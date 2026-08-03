package routes

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/alphabravocompany/thewolf/internal/artifacts"
	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/scannerbundle"
	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
	"github.com/alphabravocompany/thewolf/internal/scannersigning"
)

func TestScannerReleaseBundleV2DualRegistryOfflineImportVerifiesEveryTarget(t *testing.T) {
	primaryRegistry, primaryIdentity := newBundleV2Registry(t, "primary")
	mirrorRegistry, mirrorIdentity := newBundleV2Registry(t, "mirror")
	if primaryIdentity.imageDigest != mirrorIdentity.imageDigest ||
		primaryIdentity.platformDigest != mirrorIdentity.platformDigest ||
		primaryIdentity.provenanceDigest != mirrorIdentity.provenanceDigest ||
		primaryIdentity.sbomDigest != mirrorIdentity.sbomDigest {
		t.Fatal("dual registries do not expose the same executable/evidence identity")
	}

	source := newScannerBundleTestStore(t)
	seedScannerBundleV2Release(t, source, primaryRegistry.URL, mirrorRegistry.URL, primaryIdentity, mirrorIdentity)
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	trustPath := filepath.Join(t.TempDir(), "portable-trust.json")
	writeTestJSON(t, trustPath, bundleTrustPolicy{
		SchemaVersion: bundleTrustSchema,
		Keys: []bundleTrustKey{{
			KeyID: "v2-portable-key", Algorithm: "ed25519",
			PublicKey: base64.RawStdEncoding.EncodeToString(privateKey.Public().(ed25519.PublicKey)),
		}},
	})
	t.Setenv(bundleTrustPolicyEnv, trustPath)
	previousSignerFactory := portableBundleSignerFactory
	previousVerifierFactory := bundleImageVerifierFactory
	previousHandler := DefaultHandler
	previousArtifacts := artifacts.Global
	t.Cleanup(func() {
		portableBundleSignerFactory = previousSignerFactory
		bundleImageVerifierFactory = previousVerifierFactory
		DefaultHandler = previousHandler
		artifacts.Global = previousArtifacts
	})
	portableBundleSignerFactory = func(
		scannersigning.Binding,
		string,
	) (scannerbundle.ManifestSigner, string, error) {
		return scannerbundle.Ed25519Signer{KeyID: "v2-portable-key", PrivateKey: privateKey}, "signed-ed25519", nil
	}
	SetHandler(source, nil)
	if err := artifacts.Init(t.TempDir()); err != nil {
		t.Fatal(err)
	}

	exportRequest := httptest.NewRequest(http.MethodGet, "/export?bundle_version=2", nil)
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("id", "release-portable-v2")
	exportRequest = exportRequest.WithContext(context.WithValue(exportRequest.Context(), chi.RouteCtxKey, routeContext))
	exportResponse := httptest.NewRecorder()
	ScannerSupplyChainExportReleaseBundle(exportResponse, exportRequest)
	if exportResponse.Code != http.StatusOK {
		t.Fatalf("v2 export=%d: %s", exportResponse.Code, exportResponse.Body)
	}
	if exportResponse.Header().Get("Content-Type") != ScannerReleaseBundleMediaTypeV2 ||
		exportResponse.Header().Get("X-Wolf-Bundle-Schema") != scannerbundle.BundleSchemaV2 {
		t.Fatalf("v2 export headers=%v", exportResponse.Header())
	}
	verifiedBundle, err := scannerbundle.Read(
		context.Background(), bytes.NewReader(exportResponse.Body.Bytes()), t.TempDir(),
		scannerbundle.ReadOptions{Verifier: mustLoadTestTrustStore(t)},
	)
	if err != nil {
		t.Fatalf("read v2 bundle: %v", err)
	}
	portable, err := readPortableInventory(verifiedBundle)
	if err != nil {
		t.Fatalf("read v2 inventory: %v", err)
	}
	if len(verifiedBundle.Manifest.Images) != 1 || len(portable.Images) != 2 {
		t.Fatalf("manifest images=%d portable targets=%d", len(verifiedBundle.Manifest.Images), len(portable.Images))
	}
	for _, image := range portable.Images {
		found := false
		for _, artifact := range verifiedBundle.Manifest.Artifacts {
			if artifact.Key == imageSignatureBundleKey(image) && artifact.StorageDigest == image.SignatureArtifactDigest {
				found = true
			}
		}
		if !found {
			t.Fatalf("target %s has no exact signature artifact", image.RegistryTargetID)
		}
	}
	ociByDigest := make(map[string]scannerbundle.OCIRecord, len(verifiedBundle.Manifest.OCIRecords))
	for _, record := range verifiedBundle.Manifest.OCIRecords {
		ociByDigest[record.Digest] = record
	}
	missingCertificate := append([]scannerrelease.ReleaseImage(nil), portable.Images...)
	missingCertificate[0].SignatureCertificateDigest = "sha256:" + strings.Repeat("f", 64)
	if err := validateV2EvidenceCoverage(
		missingCertificate, verifiedBundle.Manifest.Images,
		verifiedBundle.Manifest.Artifacts, ociByDigest,
	); err == nil || !strings.Contains(err.Error(), "signature certificate") {
		t.Fatalf("missing signature certificate closure error = %v", err)
	}

	// Prove the subsequent no-network imports do not depend on either source
	// registry. Any accidental registry access now fails immediately.
	primaryRegistry.Close()
	mirrorRegistry.Close()

	bundleImageVerifierFactory = func() (bundleImageVerifier, bool, error) { return nil, false, nil }
	breakGlassStore := newScannerBundleTestStore(t)
	SetHandler(breakGlassStore, nil)
	if err := artifacts.Init(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	rejected := importScannerBundleRequest(t, exportResponse.Body.Bytes(), false, "v2-verifier-required")
	if rejected.Code != http.StatusUnprocessableEntity || !strings.Contains(rejected.Body.String(), "image_signature_verifier_required") {
		t.Fatalf("missing verifier import=%d: %s", rejected.Code, rejected.Body)
	}
	breakGlass := importScannerBundleRequest(t, exportResponse.Body.Bytes(), true, "v2-break-glass")
	if breakGlass.Code != http.StatusCreated {
		t.Fatalf("v2 break-glass import=%d: %s", breakGlass.Code, breakGlass.Body)
	}
	breakGlassRelease, err := breakGlassStore.ScannerReleases().GetRelease(context.Background(), "release-portable-v2")
	if err != nil || breakGlassRelease.RollbackEligible {
		t.Fatalf("break-glass release=%+v err=%v", breakGlassRelease, err)
	}

	verifier := &completeBundleImageVerifier{}
	bundleImageVerifierFactory = func() (bundleImageVerifier, bool, error) { return verifier, true, nil }
	target := newScannerBundleTestStore(t)
	SetHandler(target, nil)
	targetRoot := t.TempDir()
	if err := artifacts.Init(targetRoot); err != nil {
		t.Fatal(err)
	}
	importResponse := importScannerBundleRequest(t, exportResponse.Body.Bytes(), false, "v2-offline-verified")
	if importResponse.Code != http.StatusCreated {
		t.Fatalf("v2 verified import=%d: %s", importResponse.Code, importResponse.Body)
	}
	var envelope struct {
		Data releaseBundleImportResult `json:"data"`
	}
	if err := json.Unmarshal(importResponse.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if !envelope.Data.ExternalSignaturesVerified || envelope.Data.ExternalVerificationDigest == "" ||
		!envelope.Data.OCIClosureVerified || envelope.Data.NetworkMode != "no-network" ||
		envelope.Data.DestinationReadBack {
		t.Fatalf("v2 import result=%+v", envelope.Data)
	}
	if verifier.calls != 1 || verifier.lastTargetCount != 2 {
		t.Fatalf("verifier calls=%d target_count=%d", verifier.calls, verifier.lastTargetCount)
	}
	persisted, err := target.ScannerReleases().GetRelease(context.Background(), "release-portable-v2")
	if err != nil || !persisted.RollbackEligible || !persisted.Imported {
		t.Fatalf("persisted v2 release=%+v err=%v", persisted, err)
	}
	releaseArtifacts, err := target.ScannerReleases().ListArtifacts(context.Background(), persisted.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	receiptFound := false
	for _, artifact := range releaseArtifacts {
		if artifact.ArtifactType != "offline-image-signature-verification" {
			continue
		}
		receiptFound = artifact.Protected && artifact.Digest != "" && artifact.SizeBytes > 0
		path := filepath.Join(targetRoot, "scanner-release-bundles", filepath.Base(artifact.URI))
		value, readErr := os.ReadFile(path)
		if readErr != nil || digestBytes(value) != artifact.Digest {
			t.Fatalf("verification receipt path=%q err=%v", path, readErr)
		}
	}
	if !receiptFound {
		t.Fatal("protected image-verification receipt was not persisted")
	}
	events, err := target.ScannerReleases().ListEvents(context.Background(), "release", persisted.ID, 0, 10)
	if err != nil || len(events) != 1 ||
		!strings.Contains(events[0].PayloadJSON, envelope.Data.ExternalVerificationDigest) ||
		!strings.Contains(events[0].PayloadJSON, "external_signature_receipt_digest") {
		t.Fatalf("release events=%+v err=%v", events, err)
	}
	replay := importScannerBundleRequest(t, exportResponse.Body.Bytes(), false, "v2-offline-verified")
	if replay.Code != http.StatusOK {
		t.Fatalf("v2 replay=%d: %s", replay.Code, replay.Body)
	}
}

type completeBundleImageVerifier struct {
	calls           int
	lastTargetCount int
}

func (v *completeBundleImageVerifier) Verify(
	_ context.Context,
	_ *scannerbundle.ImportedBundle,
	inventory *portableReleaseInventory,
) ([]bundleImageVerificationResult, error) {
	v.calls++
	v.lastTargetCount = len(inventory.Images)
	results := make([]bundleImageVerificationResult, 0, len(inventory.Images))
	trustDigest := digestBytes([]byte("offline-image-trust-policy"))
	for _, image := range inventory.Images {
		results = append(results, verifierResultForImage(bundleImageVerificationRequest{
			OperationID:       digestBytes([]byte("verify:" + image.ImageKey + ":" + image.RegistryTargetID)),
			TrustPolicyDigest: trustDigest,
		}, image))
		results[len(results)-1].EvidenceDigest = digestBytes([]byte("evidence:" + image.RegistryTargetID))
	}
	return results, nil
}

type bundleV2RegistryIdentity struct {
	imageDigest             string
	platformDigest          string
	imageSize               int64
	provenanceDigest        string
	sbomDigest              string
	signatureDigest         string
	signatureArtifactDigest string
	signatureArtifactSize   int64
	certificateDigest       string
}

type bundleV2Repository struct {
	manifests  map[string][]byte
	mediaTypes map[string]string
	blobs      map[string][]byte
	referrers  map[string][]bundleV2Descriptor
}

type bundleV2Descriptor struct {
	MediaType    string            `json:"mediaType"`
	Digest       string            `json:"digest"`
	Size         int64             `json:"size"`
	ArtifactType string            `json:"artifactType,omitempty"`
	Platform     map[string]string `json:"platform,omitempty"`
}

type bundleV2Registry struct {
	mu           sync.Mutex
	repositories map[string]*bundleV2Repository
	calls        int
}

func newBundleV2Registry(t *testing.T, label string) (*httptest.Server, bundleV2RegistryIdentity) {
	t.Helper()
	imageConfig := []byte(`{"architecture":"amd64","os":"linux"}`)
	imageLayer := []byte("scanner-runtime-layer")
	imageConfigDigest := digestBytes(imageConfig)
	imageLayerDigest := digestBytes(imageLayer)
	platformManifest := bundleV2JSON(t, map[string]any{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.image.manifest.v1+json",
		"config": bundleV2Descriptor{
			MediaType: "application/vnd.oci.image.config.v1+json", Digest: imageConfigDigest, Size: int64(len(imageConfig)),
		},
		"layers": []bundleV2Descriptor{{
			MediaType: "application/vnd.oci.image.layer.v1.tar+gzip", Digest: imageLayerDigest, Size: int64(len(imageLayer)),
		}},
	})
	platformDigest := digestBytes(platformManifest)
	imageIndex := bundleV2JSON(t, map[string]any{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.image.index.v1+json",
		"manifests": []bundleV2Descriptor{{
			MediaType: "application/vnd.oci.image.manifest.v1+json", Digest: platformDigest,
			Size: int64(len(platformManifest)), Platform: map[string]string{"os": "linux", "architecture": "amd64"},
		}},
	})
	imageDigest := digestBytes(imageIndex)
	emptyConfig := []byte(`{}`)
	emptyConfigDigest := digestBytes(emptyConfig)
	provenance := []byte(`{"_type":"https://in-toto.io/Statement/v1","predicateType":"https://slsa.dev/provenance/v1"}`)
	sbom := []byte(`{"spdxVersion":"SPDX-2.3","name":"wolf-scanners"}`)
	provenanceDigest := digestBytes(provenance)
	sbomDigest := digestBytes(sbom)
	attestation := bundleV2JSON(t, map[string]any{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.image.manifest.v1+json",
		"artifactType":  "application/vnd.in-toto+json",
		"subject":       bundleV2Descriptor{MediaType: "application/vnd.oci.image.index.v1+json", Digest: imageDigest, Size: int64(len(imageIndex))},
		"config":        bundleV2Descriptor{MediaType: "application/vnd.oci.empty.v1+json", Digest: emptyConfigDigest, Size: int64(len(emptyConfig))},
		"layers": []bundleV2Descriptor{
			{MediaType: "application/vnd.in-toto+json", Digest: provenanceDigest, Size: int64(len(provenance))},
			{MediaType: "application/spdx+json", Digest: sbomDigest, Size: int64(len(sbom))},
		},
	})
	attestationDigest := digestBytes(attestation)
	signaturePayload := []byte("signature-payload-" + label)
	certificate := []byte("certificate-" + label)
	signatureDigest := digestBytes(signaturePayload)
	certificateDigest := digestBytes(certificate)
	signatureManifest := bundleV2JSON(t, map[string]any{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.image.manifest.v1+json",
		"artifactType":  "application/vnd.dev.cosign.simplesigning.v1+json",
		"subject":       bundleV2Descriptor{MediaType: "application/vnd.oci.image.index.v1+json", Digest: imageDigest, Size: int64(len(imageIndex))},
		"config":        bundleV2Descriptor{MediaType: "application/vnd.oci.empty.v1+json", Digest: emptyConfigDigest, Size: int64(len(emptyConfig))},
		"layers": []bundleV2Descriptor{
			{MediaType: "application/vnd.dev.cosign.simplesigning.v1+json", Digest: signatureDigest, Size: int64(len(signaturePayload))},
			{MediaType: "application/x-pem-file", Digest: certificateDigest, Size: int64(len(certificate))},
		},
	})
	signatureArtifactDigest := digestBytes(signatureManifest)
	registry := &bundleV2Registry{repositories: map[string]*bundleV2Repository{
		"source/scanners": {
			manifests: map[string][]byte{
				imageDigest: imageIndex, platformDigest: platformManifest, attestationDigest: attestation,
			},
			mediaTypes: map[string]string{
				imageDigest: "application/vnd.oci.image.index.v1+json", platformDigest: "application/vnd.oci.image.manifest.v1+json",
				attestationDigest: "application/vnd.oci.image.manifest.v1+json",
			},
			blobs: map[string][]byte{
				imageConfigDigest: imageConfig, imageLayerDigest: imageLayer, emptyConfigDigest: emptyConfig,
				provenanceDigest: provenance, sbomDigest: sbom,
			},
			referrers: map[string][]bundleV2Descriptor{
				imageDigest: {{MediaType: "application/vnd.oci.image.manifest.v1+json", Digest: attestationDigest, Size: int64(len(attestation)), ArtifactType: "application/vnd.in-toto+json"}},
			},
		},
		"source/signatures": {
			manifests:  map[string][]byte{signatureArtifactDigest: signatureManifest},
			mediaTypes: map[string]string{signatureArtifactDigest: "application/vnd.oci.image.manifest.v1+json"},
			blobs: map[string][]byte{
				emptyConfigDigest: emptyConfig, signatureDigest: signaturePayload, certificateDigest: certificate,
			},
			referrers: map[string][]bundleV2Descriptor{},
		},
	}}
	server := httptest.NewServer(registry)
	t.Cleanup(server.Close)
	return server, bundleV2RegistryIdentity{
		imageDigest: imageDigest, platformDigest: platformDigest, imageSize: int64(len(imageIndex)),
		provenanceDigest: provenanceDigest, sbomDigest: sbomDigest,
		signatureDigest: signatureDigest, signatureArtifactDigest: signatureArtifactDigest,
		signatureArtifactSize: int64(len(signatureManifest)), certificateDigest: certificateDigest,
	}
}

func (r *bundleV2Registry) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	path := strings.TrimPrefix(request.URL.Path, "/v2/")
	for marker, kind := range map[string]string{"/manifests/": "manifest", "/blobs/": "blob", "/referrers/": "referrer"} {
		position := strings.LastIndex(path, marker)
		if position < 0 {
			continue
		}
		repository, digest := path[:position], path[position+len(marker):]
		data := r.repositories[repository]
		if data == nil || request.Method != http.MethodGet {
			http.NotFound(w, request)
			return
		}
		switch kind {
		case "manifest":
			value, exists := data.manifests[digest]
			if !exists {
				http.NotFound(w, request)
				return
			}
			w.Header().Set("Content-Type", data.mediaTypes[digest])
			w.Header().Set("Docker-Content-Digest", digest)
			w.Header().Set("Content-Length", strconv.Itoa(len(value)))
			_, _ = w.Write(value)
		case "blob":
			value, exists := data.blobs[digest]
			if !exists {
				http.NotFound(w, request)
				return
			}
			w.Header().Set("Content-Length", strconv.Itoa(len(value)))
			_, _ = w.Write(value)
		case "referrer":
			value := bundleV2JSON(nil, map[string]any{
				"schemaVersion": 2, "mediaType": "application/vnd.oci.image.index.v1+json",
				"manifests": data.referrers[digest],
			})
			w.Header().Set("Content-Type", "application/vnd.oci.image.index.v1+json")
			_, _ = w.Write(value)
		}
		return
	}
	http.NotFound(w, request)
}

func seedScannerBundleV2Release(
	t *testing.T,
	store *db.SQLiteStore,
	primaryURL, mirrorURL string,
	primary, mirror bundleV2RegistryIdentity,
) {
	t.Helper()
	ctx := context.Background()
	persistence := store.ScannerReleases()
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	policy := &scannerrelease.Policy{
		ID: "policy-portable-v2", Scope: "global", Revision: 1, Enabled: true,
		ScheduleJSON: `{}`, RulesJSON: `{}`, CreatedBy: "test", CreatedAt: now, UpdatedAt: now,
	}
	if err := persistence.CreatePolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	registries := []scannerrelease.RegistryTarget{
		{ID: "a-mirror", Name: "mirror", Type: scannerrelease.RegistryManaged, Host: mirrorURL, Namespace: "source", PlatformPolicyJSON: `{}`, Enabled: true, CreatedBy: "test"},
		{ID: "b-primary", Name: "primary", Type: scannerrelease.RegistryManaged, Host: primaryURL, Namespace: "source", PlatformPolicyJSON: `{}`, Enabled: true, CreatedBy: "test"},
	}
	for index := range registries {
		if err := persistence.CreateRegistryTarget(ctx, &registries[index]); err != nil {
			t.Fatal(err)
		}
	}
	lockDigest := digestBytes([]byte("v2-lock"))
	candidate := &scannerrelease.Candidate{
		ID: "candidate-portable-v2", DefinitionCommit: "abcdef1234567", LockDigest: lockDigest,
		LockURI: "oci://release.test/lock@" + lockDigest, State: scannerrelease.CandidatePublished,
		RiskSummaryJSON: `{}`, RequiredGatesJSON: `[]`, PolicyDecision: "allow",
		PolicyID: policy.ID, PolicyRevision: policy.Revision, Actor: "test", IdempotencyKey: "candidate-portable-v2",
	}
	command := scannerrelease.TransitionCommand{
		Actor: "test", Reason: "test", IdempotencyKey: "seed-portable-v2", PolicyRevision: 1, PayloadJSON: `{}`,
	}
	if err := persistence.CreateCandidate(ctx, candidate, command); err != nil {
		t.Fatal(err)
	}
	release := scannerrelease.Release{
		ID: "release-portable-v2", Name: "portable-v2", CandidateID: candidate.ID,
		LockDigest: lockDigest, ManifestDigest: digestBytes([]byte("v2-manifest")),
		ManifestURI: "oci://release.test/manifest@" + digestBytes([]byte("v2-manifest")),
		State:       scannerrelease.ReleasePublished, SignerIdentity: "cosign:test",
		PolicyID: policy.ID, PolicyRevision: policy.Revision, DefinitionCommit: candidate.DefinitionCommit,
		Protected: true, RollbackEligible: true, RetentionClass: "published", PublishedAt: now,
	}
	host := func(raw string) string { return strings.TrimPrefix(raw, "http://") }
	imageFor := func(id, repository string, identity bundleV2RegistryIdentity) scannerrelease.ReleaseImage {
		return scannerrelease.ReleaseImage{
			ID: "image-" + id, ImageKey: "default", ImageKind: scannerrelease.ReleaseImageScanner,
			RegistryTargetID: id, Repository: host(repository) + "/source/scanners",
			Digest:          identity.imageDigest,
			PlatformDigests: `{"linux/amd64":"` + identity.platformDigest + `"}`,
			SizeBytes:       identity.imageSize, SignatureStatus: "verified",
			SignatureDigest:            identity.signatureDigest,
			SignatureArtifactURI:       "oci://" + host(repository) + "/source/signatures@" + identity.signatureArtifactDigest,
			SignatureArtifactDigest:    identity.signatureArtifactDigest,
			SignatureMediaType:         "application/vnd.oci.image.manifest.v1+json",
			SignatureArtifactSizeBytes: identity.signatureArtifactSize,
			SignatureCertificateDigest: identity.certificateDigest,
			SignatureIdentity:          "scanner-release@example.test", SignatureIssuer: "https://issuer.example.test",
			SignatureSubject:     host(repository) + "/source/scanners@" + identity.imageDigest,
			SignatureTrustRoot:   digestBytes([]byte("image-trust-root")),
			SignatureOperationID: digestBytes([]byte("sign:" + id)),
			ProvenanceDigest:     identity.provenanceDigest, SBOMDigest: identity.sbomDigest,
		}
	}
	inventory := &scannerrelease.ReleaseInventory{
		Release: release,
		Tools: []scannerrelease.ReleaseTool{{
			ID: "tool-portable-v2", ToolKey: "semgrep", Version: "1.2.3",
			SourceReference: "https://example.test/semgrep", ParserCompatibility: "v1",
			MetadataJSON: `{"image_key":"default","kind":"wolf"}`,
		}},
		Images: []scannerrelease.ReleaseImage{
			imageFor("b-primary", primaryURL, primary), imageFor("a-mirror", mirrorURL, mirror),
		},
	}
	if err := persistence.CreateRelease(ctx, inventory, command); err != nil {
		t.Fatal(err)
	}
}

func bundleV2JSON(t *testing.T, value any) []byte {
	if t != nil {
		t.Helper()
	}
	result, err := json.Marshal(value)
	if err != nil {
		if t != nil {
			t.Fatal(err)
		}
		panic(err)
	}
	return result
}

func TestPortableReleaseArtifactsExcludeImportBookkeepingAndReserveNamespace(t *testing.T) {
	artifacts := []scannerrelease.ReleaseArtifact{
		{ID: "sbom", ArtifactType: "sbom"},
		{ID: "bundle", ArtifactType: "offline-release-bundle"},
		{ID: "inventory", ArtifactType: portableInventoryType},
		{ID: "verification", ArtifactType: "offline-image-signature-verification"},
	}
	portable := portableReleaseArtifacts(artifacts)
	if len(portable) != 1 || portable[0].ID != "sbom" {
		t.Fatalf("portable artifacts = %#v", portable)
	}
	for _, artifact := range artifacts[1:] {
		if !reservedPortableArtifactType(artifact.ArtifactType) {
			t.Fatalf("internal artifact type %q is not reserved", artifact.ArtifactType)
		}
	}
}
