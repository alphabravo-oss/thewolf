package routes

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/alphabravocompany/thewolf/internal/artifacts"
	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/scannerbundle"
	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
	"github.com/alphabravocompany/thewolf/internal/scannersigning"
)

func TestScannerReleaseBundleSignedRoundTripAndReplay(t *testing.T) {
	source := newScannerBundleTestStore(t)
	seedScannerBundleRelease(t, source)

	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	trustPath := filepath.Join(t.TempDir(), "trust.json")
	writeTestJSON(t, trustPath, bundleTrustPolicy{
		SchemaVersion: bundleTrustSchema,
		Keys: []bundleTrustKey{{
			KeyID: "release-test-key", Algorithm: "ed25519",
			PublicKey: base64.RawStdEncoding.EncodeToString(privateKey.Public().(ed25519.PublicKey)),
		}},
	})
	t.Setenv(bundleTrustPolicyEnv, trustPath)
	previousSignerFactory := portableBundleSignerFactory
	portableBundleSignerFactory = func(
		scannersigning.Binding,
		string,
	) (scannerbundle.ManifestSigner, string, error) {
		return scannerbundle.Ed25519Signer{
			KeyID: "release-test-key", PrivateKey: privateKey,
		}, "signed-ed25519", nil
	}
	t.Cleanup(func() { portableBundleSignerFactory = previousSignerFactory })

	previousHandler := DefaultHandler
	previousArtifacts := artifacts.Global
	t.Cleanup(func() {
		DefaultHandler = previousHandler
		artifacts.Global = previousArtifacts
	})
	SetHandler(source, nil)
	if err := artifacts.Init(t.TempDir()); err != nil {
		t.Fatal(err)
	}

	exportRequest := httptest.NewRequest(http.MethodGet, "/scanner-supply-chain/releases/release-portable-1/export", nil)
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("id", "release-portable-1")
	exportRequest = exportRequest.WithContext(context.WithValue(exportRequest.Context(), chi.RouteCtxKey, routeContext))
	exportResponse := httptest.NewRecorder()
	ScannerSupplyChainExportReleaseBundle(exportResponse, exportRequest)
	if exportResponse.Code != http.StatusOK {
		t.Fatalf("export = %d: %s", exportResponse.Code, exportResponse.Body)
	}
	if got := exportResponse.Header().Get("Content-Type"); got != ScannerReleaseBundleMediaType {
		t.Fatalf("export content type = %q", got)
	}
	if got := exportResponse.Header().Get("X-Wolf-Bundle-Signature-Status"); got != "signed-ed25519" {
		t.Fatalf("export signature status = %q", got)
	}
	if got := exportResponse.Header().Get("X-Wolf-Bundle-Digest"); got != digestBytes(exportResponse.Body.Bytes()) {
		t.Fatalf("export bundle digest = %q", got)
	}

	extractDir := filepath.Join(t.TempDir(), "verify")
	verified, err := scannerbundle.Read(
		context.Background(), bytes.NewReader(exportResponse.Body.Bytes()), extractDir,
		scannerbundle.ReadOptions{Verifier: mustLoadTestTrustStore(t)},
	)
	if err != nil {
		t.Fatalf("verify exported bundle: %v", err)
	}
	if _, ok := verified.Files[portableInventoryPath]; !ok {
		t.Fatalf("export files = %#v", verified.Files)
	}

	target := newScannerBundleTestStore(t)
	SetHandler(target, nil)
	targetArtifactRoot := t.TempDir()
	if err := artifacts.Init(targetArtifactRoot); err != nil {
		t.Fatal(err)
	}
	tampered := append([]byte(nil), exportResponse.Body.Bytes()...)
	tampered[len(tampered)/2] ^= 0xff
	tamperedResponse := importScannerBundleRequest(t, tampered, false, "tampered-import")
	if tamperedResponse.Code != http.StatusUnprocessableEntity {
		t.Fatalf("tampered import = %d: %s", tamperedResponse.Code, tamperedResponse.Body)
	}
	importResponse := importScannerBundleRequest(t, exportResponse.Body.Bytes(), false, "signed-import-1")
	if importResponse.Code != http.StatusCreated {
		t.Fatalf("import = %d: %s", importResponse.Code, importResponse.Body)
	}
	var importedEnvelope struct {
		Data releaseBundleImportResult `json:"data"`
	}
	if err := json.Unmarshal(importResponse.Body.Bytes(), &importedEnvelope); err != nil {
		t.Fatal(err)
	}
	if !importedEnvelope.Data.Created ||
		!importedEnvelope.Data.IntegrityVerified ||
		importedEnvelope.Data.SignatureStatus != "verified-ed25519" ||
		importedEnvelope.Data.ExternalSignaturesVerified ||
		importedEnvelope.Data.NetworkMode != "no-network" ||
		importedEnvelope.Data.DestinationReadBack {
		t.Fatalf("import result = %+v", importedEnvelope.Data)
	}
	persisted, err := target.ScannerReleases().GetRelease(context.Background(), "release-portable-1")
	if err != nil {
		t.Fatal(err)
	}
	if !persisted.Imported || !persisted.RollbackEligible ||
		persisted.ManifestDigest != importedEnvelope.Data.ManifestDigest {
		t.Fatalf("persisted release = %+v", persisted)
	}
	bundlePath := filepath.Join(
		targetArtifactRoot, "scanner-release-bundles",
		filepath.Base(importedEnvelope.Data.BundleURI),
	)
	if info, err := os.Stat(bundlePath); err != nil || info.Size() != int64(exportResponse.Body.Len()) {
		t.Fatalf("durable bundle info=%v err=%v", info, err)
	}

	replay := importScannerBundleRequest(t, exportResponse.Body.Bytes(), false, "signed-import-1")
	if replay.Code != http.StatusOK {
		t.Fatalf("replay = %d: %s", replay.Code, replay.Body)
	}
	if err := json.Unmarshal(replay.Body.Bytes(), &importedEnvelope); err != nil {
		t.Fatal(err)
	}
	if importedEnvelope.Data.Created {
		t.Fatalf("replay result = %+v", importedEnvelope.Data)
	}
}

func TestScannerReleaseBundleImportFailsClosedAndLabelsBreakGlass(t *testing.T) {
	source := newScannerBundleTestStore(t)
	seedScannerBundleRelease(t, source)
	previousHandler := DefaultHandler
	previousArtifacts := artifacts.Global
	t.Cleanup(func() {
		DefaultHandler = previousHandler
		artifacts.Global = previousArtifacts
	})
	SetHandler(source, nil)
	if err := artifacts.Init(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WOLF_SCANNER_SIGNER_PROFILE_FILE", "")
	t.Setenv("WOLF_SCANNER_SIGNER_ADAPTER", "")
	t.Setenv(bundleTrustPolicyEnv, "")

	exportRequest := httptest.NewRequest(http.MethodGet, "/export", nil)
	routeContext := chi.NewRouteContext()
	routeContext.URLParams.Add("id", "release-portable-1")
	exportRequest = exportRequest.WithContext(context.WithValue(exportRequest.Context(), chi.RouteCtxKey, routeContext))
	exportResponse := httptest.NewRecorder()
	ScannerSupplyChainExportReleaseBundle(exportResponse, exportRequest)
	if exportResponse.Code != http.StatusOK {
		t.Fatalf("unsigned export = %d: %s", exportResponse.Code, exportResponse.Body)
	}

	target := newScannerBundleTestStore(t)
	SetHandler(target, nil)
	if err := artifacts.Init(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	rejected := importScannerBundleRequest(t, exportResponse.Body.Bytes(), false, "unsigned-rejected")
	if rejected.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unsigned default import = %d: %s", rejected.Code, rejected.Body)
	}
	if _, err := target.ScannerReleases().GetRelease(context.Background(), "release-portable-1"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("release after rejected import err = %v", err)
	}

	allowed := importScannerBundleRequest(t, exportResponse.Body.Bytes(), true, "unsigned-approved")
	if allowed.Code != http.StatusCreated {
		t.Fatalf("break-glass import = %d: %s", allowed.Code, allowed.Body)
	}
	var envelope struct {
		Data releaseBundleImportResult `json:"data"`
	}
	if err := json.Unmarshal(allowed.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.SignatureStatus != "unsigned" || !envelope.Data.IntegrityVerified {
		t.Fatalf("break-glass result = %+v", envelope.Data)
	}
	persisted, err := target.ScannerReleases().GetRelease(context.Background(), "release-portable-1")
	if err != nil {
		t.Fatal(err)
	}
	if persisted.RollbackEligible || persisted.SignerIdentity != "unsigned-offline-bundle" {
		t.Fatalf("break-glass release = %+v", persisted)
	}
}

func TestLogicalReleaseImagesCollapseMirrorsAndRejectIdentityDrift(t *testing.T) {
	base := scannerrelease.ReleaseImage{
		ImageKey: "default", ImageKind: scannerrelease.ReleaseImageScanner,
		RegistryTargetID: "primary", Repository: "registry.example/wolf/scanners",
		Digest:          verifierTestDigest("1"),
		PlatformDigests: `{"linux/amd64":"` + verifierTestDigest("2") + `","linux/arm64":"` + verifierTestDigest("3") + `"}`,
		SizeBytes:       2048, ProvenanceDigest: verifierTestDigest("4"), SBOMDigest: verifierTestDigest("5"),
	}
	mirror := base
	mirror.RegistryTargetID = "mirror"
	mirror.Repository = "mirror.example/wolf/scanners"
	logical, err := logicalReleaseImages([]scannerrelease.ReleaseImage{base, mirror})
	if err != nil || len(logical) != 1 || logical[0].ImageKey != "default" {
		t.Fatalf("logical images=%+v err=%v", logical, err)
	}
	bundled, err := bundleImages([]scannerrelease.ReleaseImage{mirror, base}, []scannerrelease.ReleaseTool{{
		ToolKey: "semgrep", MetadataJSON: `{"image_key":"default","kind":"wolf"}`,
	}})
	if err != nil || len(bundled) != 1 || len(bundled[0].Tools) != 1 {
		t.Fatalf("bundle images=%+v err=%v", bundled, err)
	}

	duplicate := mirror
	duplicate.Repository = "other.example/wolf/scanners"
	if _, err := logicalReleaseImages([]scannerrelease.ReleaseImage{mirror, duplicate}); err == nil {
		t.Fatal("duplicate logical image target was accepted")
	}
	mutations := []func(*scannerrelease.ReleaseImage){
		func(image *scannerrelease.ReleaseImage) { image.Digest = verifierTestDigest("6") },
		func(image *scannerrelease.ReleaseImage) {
			image.PlatformDigests = `{"linux/amd64":"` + verifierTestDigest("7") + `"}`
		},
		func(image *scannerrelease.ReleaseImage) { image.SizeBytes++ },
		func(image *scannerrelease.ReleaseImage) { image.ImageKind = scannerrelease.ReleaseImageFixer },
		func(image *scannerrelease.ReleaseImage) { image.ProvenanceDigest = verifierTestDigest("8") },
		func(image *scannerrelease.ReleaseImage) { image.SBOMDigest = verifierTestDigest("9") },
	}
	for index, mutate := range mutations {
		drifted := mirror
		mutate(&drifted)
		if _, err := logicalReleaseImages([]scannerrelease.ReleaseImage{base, drifted}); err == nil {
			t.Errorf("identity drift mutation %d was accepted", index)
		}
	}
}

func TestImageVerificationReceiptIsContentAddressedAndReplaySafe(t *testing.T) {
	imported, inventory, _, trustDigest := bundleVerifierFixture(t)
	request := bundleImageVerificationRequest{
		OperationID: verifierTestDigest("a"), TrustPolicyDigest: trustDigest,
	}
	results := []bundleImageVerificationResult{verifierResultForImage(request, inventory.Images[0])}
	directory := t.TempDir()
	payload, digest, uri, path, err := prepareBundleImageVerificationReceipt(
		imported, inventory, filepath.Join(directory, "bundle.tar.zst"), verifierTestDigest("b"), results,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(payload) == 0 || digest != digestBytes(payload) || !strings.Contains(uri, strings.TrimPrefix(digest, "sha256:")) || filepath.Dir(path) != directory {
		t.Fatalf("receipt payload=%d digest=%q uri=%q path=%q", len(payload), digest, uri, path)
	}
	owned, err := writeContentAddressedReceipt(path, payload)
	if err != nil || !owned {
		t.Fatalf("first receipt write owned=%t err=%v", owned, err)
	}
	owned, err = writeContentAddressedReceipt(path, payload)
	if err != nil || owned {
		t.Fatalf("receipt replay owned=%t err=%v", owned, err)
	}
	if err := os.WriteFile(path, []byte("tampered"), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := writeContentAddressedReceipt(path, payload); err == nil {
		t.Fatal("tampered content-addressed receipt was accepted")
	}
}

func importScannerBundleRequest(
	t *testing.T,
	bundle []byte,
	allowUnverified bool,
	idempotencyKey string,
) *httptest.ResponseRecorder {
	t.Helper()
	path := "/scanner-supply-chain/release-imports?no_network=true"
	if allowUnverified {
		path += "&allow_unverified=true"
	}
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(bundle))
	request.Header.Set("Content-Type", ScannerReleaseBundleMediaType)
	request.Header.Set("Idempotency-Key", idempotencyKey)
	request.Header.Set("X-Wolf-Import-Reason", "test offline transfer")
	response := httptest.NewRecorder()
	ScannerSupplyChainImportReleaseBundle(response, request)
	return response
}

func newScannerBundleTestStore(t *testing.T) *db.SQLiteStore {
	t.Helper()
	store, err := db.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func seedScannerBundleRelease(t *testing.T, store *db.SQLiteStore) {
	t.Helper()
	ctx := context.Background()
	persistence := store.ScannerReleases()
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	policy := &scannerrelease.Policy{
		ID: "policy-portable-1", Scope: "global", Revision: 1, Enabled: true,
		ScheduleJSON: `{}`, RulesJSON: `{}`, CreatedBy: "test", CreatedAt: now, UpdatedAt: now,
	}
	if err := persistence.CreatePolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	registry := &scannerrelease.RegistryTarget{
		ID: "registry-portable-1", Name: "test", Type: scannerrelease.RegistryManaged,
		Host: "registry.example", Namespace: "wolf", PlatformPolicyJSON: "{}",
		Enabled: true, CreatedBy: "test", CreatedAt: now, UpdatedAt: now,
	}
	if err := persistence.CreateRegistryTarget(ctx, registry); err != nil {
		t.Fatal(err)
	}
	lockDigest := "sha256:" + string(bytes.Repeat([]byte{'1'}, 64))
	candidate := &scannerrelease.Candidate{
		ID: "candidate-portable-1", DefinitionCommit: "abcdef1", LockDigest: lockDigest,
		LockURI: "oci://registry.example/wolf/lock@sha256:1", State: scannerrelease.CandidatePublished,
		RiskSummaryJSON: "{}", RequiredGatesJSON: "[]", PolicyDecision: "allow",
		PolicyID: policy.ID, PolicyRevision: policy.Revision, Actor: "test",
		IdempotencyKey: "candidate-portable-1", CreatedAt: now, UpdatedAt: now,
	}
	command := scannerrelease.TransitionCommand{
		Actor: "test", Reason: "test", IdempotencyKey: "seed-portable-release",
		PolicyRevision: 1, PayloadJSON: "{}",
	}
	if err := persistence.CreateCandidate(ctx, candidate, command); err != nil {
		t.Fatal(err)
	}
	imageDigest := "sha256:" + string(bytes.Repeat([]byte{'2'}, 64))
	platformDigest := "sha256:" + string(bytes.Repeat([]byte{'3'}, 64))
	release := scannerrelease.Release{
		ID: "release-portable-1", Name: "portable-test-release", CandidateID: candidate.ID,
		LockDigest: lockDigest, ManifestDigest: "sha256:" + string(bytes.Repeat([]byte{'4'}, 64)),
		ManifestURI: "oci://registry.example/wolf/release@sha256:4",
		State:       scannerrelease.ReleasePublished, SignerIdentity: "cosign:test",
		PolicyID: policy.ID, PolicyRevision: policy.Revision, DefinitionCommit: "abcdef1",
		Protected: true, RollbackEligible: true, RetentionClass: "published",
		PublishedAt: now, CreatedAt: now, UpdatedAt: now,
	}
	inventory := &scannerrelease.ReleaseInventory{
		Release: release,
		Tools: []scannerrelease.ReleaseTool{{
			ID: "tool-portable-1", ToolKey: "semgrep", Version: "1.2.3",
			SourceReference: "https://example.test/semgrep", ParserCompatibility: "v1",
			MetadataJSON: `{"image_key":"default","kind":"wolf"}`, CreatedAt: now,
		}},
		Images: []scannerrelease.ReleaseImage{{
			ID: "image-portable-1", ImageKey: "default", RegistryTargetID: registry.ID,
			Repository: "registry.example/wolf/scanners", Digest: imageDigest,
			PlatformDigests: `{"linux/amd64":"` + platformDigest + `"}`,
			SizeBytes:       1024, SignatureStatus: "verified", ProvenanceDigest: platformDigest,
			SBOMDigest: platformDigest, CreatedAt: now,
		}},
	}
	if err := persistence.CreateRelease(ctx, inventory, command); err != nil {
		t.Fatal(err)
	}
}

func writeTestJSON(t *testing.T, path string, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustLoadTestTrustStore(t *testing.T) scannerbundle.ManifestVerifier {
	t.Helper()
	verifier, configured, err := loadBundleTrustStore()
	if err != nil {
		t.Fatal(err)
	}
	if !configured {
		t.Fatal("test trust store is not configured")
	}
	return verifier
}
