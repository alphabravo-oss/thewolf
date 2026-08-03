package routes

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/scannerbundle"
	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

func TestLoadBundleImageVerifierRequiresCompleteExecutableConfiguration(t *testing.T) {
	t.Setenv(bundleImageVerifierEnv, "")
	t.Setenv(bundleImageTrustPolicyEnv, "")
	if _, configured, err := loadBundleImageVerifier(); err != nil || configured {
		t.Fatalf("empty configuration configured=%t err=%v", configured, err)
	}

	directory := t.TempDir()
	verifierPath := filepath.Join(directory, "verify")
	trustPath := filepath.Join(directory, "trust.json")
	if err := os.WriteFile(verifierPath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(trustPath, []byte(`{"trusted":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(bundleImageVerifierEnv, verifierPath)
	if _, _, err := loadBundleImageVerifier(); err == nil {
		t.Fatal("partial verifier configuration was accepted")
	}
	t.Setenv(bundleImageTrustPolicyEnv, "relative/trust.json")
	if _, _, err := loadBundleImageVerifier(); err == nil {
		t.Fatal("relative trust policy path was accepted")
	}
	t.Setenv(bundleImageTrustPolicyEnv, trustPath)
	loaded, configured, err := loadBundleImageVerifier()
	if err != nil || !configured || loaded == nil {
		t.Fatalf("valid verifier configuration configured=%t err=%v", configured, err)
	}
	if err := os.Chmod(verifierPath, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadBundleImageVerifier(); err == nil {
		t.Fatal("non-executable verifier was accepted")
	}
	if err := os.Chmod(verifierPath, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(trustPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadBundleImageVerifier(); err == nil {
		t.Fatal("empty trust policy was accepted")
	}
}

func TestBundleImageVerificationRequestsBindVerifiedClosureAndTarget(t *testing.T) {
	imported, inventory, trustPath, trustDigest := bundleVerifierFixture(t)
	requests, err := buildBundleImageVerificationRequests(imported, inventory, trustPath, trustDigest)
	if err != nil {
		t.Fatal(err)
	}
	if len(requests) != 1 {
		t.Fatalf("requests=%d", len(requests))
	}
	request := requests[0]
	image := inventory.Images[0]
	if request.OperationID != bundleImageVerificationOperation(request) ||
		request.ImageKey != image.ImageKey || request.RegistryTargetID != image.RegistryTargetID ||
		request.ImageDigest != image.Digest || request.SignatureDigest != image.SignatureDigest ||
		request.SignatureArtifactDigest != image.SignatureArtifactDigest || len(request.Closure) != 2 {
		t.Fatalf("request is not exactly bound: %+v", request)
	}
	for _, item := range request.Closure {
		if !filepath.IsAbs(item.Path) || !strings.HasPrefix(item.Path, imported.Root+string(filepath.Separator)) {
			t.Fatalf("closure path escaped verified root: %+v", item)
		}
		if item.Path != filepath.Join(imported.Root, filepath.FromSlash(scannerbundle.OCIPath(item.Digest))) {
			t.Fatalf("closure path=%q digest=%q", item.Path, item.Digest)
		}
	}
	if _, err := buildBundleImageVerificationRequests(imported, inventory, trustPath, "sha256:short"); err == nil {
		t.Fatal("invalid trust policy digest was accepted")
	}
}

func TestBundleImageVerifierRejectsMismatchedAndTrailingResults(t *testing.T) {
	imported, inventory, trustPath, trustDigest := bundleVerifierFixture(t)
	requests, err := buildBundleImageVerificationRequests(imported, inventory, trustPath, trustDigest)
	if err != nil {
		t.Fatal(err)
	}
	result := verifierResultForImage(requests[0], inventory.Images[0])
	if err := validateBundleImageVerificationResult(requests[0], result); err != nil {
		t.Fatalf("valid result rejected: %v", err)
	}
	mismatch := result
	mismatch.ImageDigest = verifierTestDigest("f")
	if err := validateBundleImageVerificationResult(requests[0], mismatch); err == nil {
		t.Fatal("mismatched result was accepted")
	}

	resultJSON, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	adapterPath := filepath.Join(t.TempDir(), "verifier")
	writeVerifierScript(t, adapterPath, string(resultJSON))
	verifier := commandBundleImageVerifier{
		path: adapterPath, trustPolicyPath: trustPath, trustPolicyDigest: trustDigest,
	}
	results, err := verifier.Verify(context.Background(), imported, inventory)
	if err != nil || len(results) != 1 {
		t.Fatalf("command verifier results=%d err=%v", len(results), err)
	}
	writeVerifierScript(t, adapterPath, string(resultJSON)+"\n{}")
	if _, err := verifier.Verify(context.Background(), imported, inventory); err == nil {
		t.Fatal("trailing verifier JSON was accepted")
	}
	if err := os.WriteFile(adapterPath, []byte("#!/bin/sh\nwhile :; do :; done\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	verifier.timeout = 25 * time.Millisecond
	if _, err := verifier.Verify(context.Background(), imported, inventory); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("verifier timeout error=%v", err)
	}
}

func TestValidateBundleImageVerificationSetRequiresEveryMirrorExactlyOnce(t *testing.T) {
	_, inventory, _, _ := bundleVerifierFixture(t)
	mirror := inventory.Images[0]
	mirror.RegistryTargetID = "mirror"
	mirror.Repository = "mirror.example/wolf/scanners"
	mirror.SignatureDigest = verifierTestDigest("6")
	mirror.SignatureArtifactDigest = verifierTestDigest("7")
	mirror.SignatureOperationID = verifierTestDigest("8")
	inventory.Images = append(inventory.Images, mirror)

	results := []bundleImageVerificationResult{
		verifierResultForImage(bundleImageVerificationRequest{OperationID: verifierTestDigest("9"), TrustPolicyDigest: verifierTestDigest("a")}, inventory.Images[0]),
		verifierResultForImage(bundleImageVerificationRequest{OperationID: verifierTestDigest("b"), TrustPolicyDigest: verifierTestDigest("a")}, inventory.Images[1]),
	}
	if err := validateBundleImageVerificationSet(inventory, results); err != nil {
		t.Fatalf("valid mirror set rejected: %v", err)
	}
	reversed := []bundleImageVerificationResult{results[1], results[0]}
	if bundleImageVerificationDigest(results) != bundleImageVerificationDigest(reversed) {
		t.Fatal("verification digest depends on verifier result order")
	}
	if err := validateBundleImageVerificationSet(inventory, results[:1]); err == nil {
		t.Fatal("partial mirror verification set was accepted")
	}
	duplicate := []bundleImageVerificationResult{results[0], results[0]}
	if err := validateBundleImageVerificationSet(inventory, duplicate); err == nil {
		t.Fatal("duplicate mirror verification result was accepted")
	}
	mismatch := append([]bundleImageVerificationResult(nil), results...)
	mismatch[1].Identity = "unexpected"
	if err := validateBundleImageVerificationSet(inventory, mismatch); err == nil {
		t.Fatal("signature identity mismatch was accepted")
	}
}

func bundleVerifierFixture(t *testing.T) (
	*scannerbundle.ImportedBundle,
	*portableReleaseInventory,
	string,
	string,
) {
	t.Helper()
	root := t.TempDir()
	trustPath := filepath.Join(t.TempDir(), "trust.json")
	if err := os.WriteFile(trustPath, []byte(`{"trusted":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	payloadDigest := verifierTestDigest("1")
	storageDigest := verifierTestDigest("2")
	image := scannerrelease.ReleaseImage{
		ImageKey: "default", RegistryTargetID: "primary",
		Repository: "registry.example/wolf/scanners", Digest: verifierTestDigest("3"),
		SignatureStatus: "verified", SignatureDigest: payloadDigest,
		SignatureArtifactURI:       "oci://registry.example/wolf/signatures@" + storageDigest,
		SignatureArtifactDigest:    storageDigest,
		SignatureMediaType:         "application/vnd.oci.image.manifest.v1+json",
		SignatureArtifactSizeBytes: 512,
		SignatureCertificateDigest: verifierTestDigest("4"),
		SignatureIdentity:          "scanner-release@example.test", SignatureIssuer: "https://issuer.example.test",
		SignatureSubject:   "registry.example/wolf/scanners@" + verifierTestDigest("3"),
		SignatureTrustRoot: verifierTestDigest("5"), SignatureOperationID: verifierTestDigest("6"),
	}
	artifact := scannerbundle.ReleaseArtifact{
		Key: imageSignatureBundleKey(image), Type: "image-signature",
		Digest: payloadDigest, MediaType: "application/vnd.dev.cosign.simplesigning.v1+json", Size: 64,
		StorageDigest: storageDigest, StorageReference: image.SignatureArtifactURI,
		StorageMediaType: image.SignatureMediaType, StorageSize: image.SignatureArtifactSizeBytes,
		OCIClosure: []string{storageDigest, payloadDigest},
	}
	records := []scannerbundle.OCIRecord{
		{Digest: storageDigest, Size: 512, MediaType: image.SignatureMediaType, Kind: "oci-trust-manifest", BundlePath: scannerbundle.OCIPath(storageDigest)},
		{Digest: payloadDigest, Size: 64, MediaType: artifact.MediaType, Kind: "oci-blob", BundlePath: scannerbundle.OCIPath(payloadDigest)},
	}
	return &scannerbundle.ImportedBundle{
		Root: root, ManifestDigest: verifierTestDigest("d"), SchemaVersion: scannerbundle.BundleSchemaV2,
		Manifest: scannerbundle.ReleaseManifest{ReleaseID: "release-verifier", Artifacts: []scannerbundle.ReleaseArtifact{artifact}, OCIRecords: records},
	}, &portableReleaseInventory{Images: []scannerrelease.ReleaseImage{image}}, trustPath, digestBytes([]byte(`{"trusted":true}`))
}

func verifierResultForImage(
	request bundleImageVerificationRequest,
	image scannerrelease.ReleaseImage,
) bundleImageVerificationResult {
	return bundleImageVerificationResult{
		SchemaVersion: bundleImageResultSchema, OperationID: request.OperationID,
		TrustPolicyDigest: request.TrustPolicyDigest,
		ImageKey:          image.ImageKey, RegistryTargetID: image.RegistryTargetID, ImageDigest: image.Digest,
		SignatureDigest: image.SignatureDigest, SignatureArtifactDigest: image.SignatureArtifactDigest,
		Identity: image.SignatureIdentity, Issuer: image.SignatureIssuer,
		Subject: image.SignatureSubject, TrustRoot: image.SignatureTrustRoot,
		VerifierID: "cosign-offline", VerifierVersion: "2.6.0",
		EvidenceDigest: verifierTestDigest("e"), Verified: true,
	}
}

func writeVerifierScript(t *testing.T, path, output string) {
	t.Helper()
	quoted := strings.ReplaceAll(output, "'", "'\\''")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '%s\\n' '"+quoted+"'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
}

func verifierTestDigest(character string) string {
	return "sha256:" + strings.Repeat(character, 64)
}
