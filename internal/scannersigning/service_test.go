package scannersigning

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

const (
	testArtifactDigest = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	testLockDigest     = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testOperationID    = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
)

type fakeKMSAdapter struct {
	private ed25519.PrivateKey
	public  ed25519.PublicKey
	calls   atomic.Int64
	mutate  func(*Result)
	fail    error
	request Request
}

func (a *fakeKMSAdapter) Sign(_ context.Context, request Request) (Result, error) {
	a.calls.Add(1)
	a.request = request
	if a.fail != nil {
		return Result{}, a.fail
	}
	payload, err := base64.RawStdEncoding.DecodeString(request.Payload)
	if err != nil {
		return Result{}, err
	}
	publicDER, err := x509.MarshalPKIXPublicKey(a.public)
	if err != nil {
		return Result{}, err
	}
	signature := ed25519.Sign(a.private, payload)
	signatureDigest := DigestValue(signature)
	artifactDigest := DigestValue(append([]byte("stored-signature:"), signature...))
	result := Result{
		SchemaVersion: ResultSchema, OperationID: request.OperationID,
		RequestDigest: request.RequestDigest, ProfileDigest: request.ProfileDigest,
		Algorithm:    request.Profile.Algorithm,
		Signature:    base64.RawStdEncoding.EncodeToString(signature),
		SignatureURI: "oci://registry.example/wolf-signatures@" + artifactDigest,
		PublicKeyPEM: string(pem.EncodeToMemory(&pem.Block{
			Type: "PUBLIC KEY", Bytes: publicDER,
		})),
		KeyVersion: "version-7", Identity: request.Profile.Identity,
		Issuer: request.Profile.Issuer, Subject: request.Profile.Subject,
		TrustRootReference:        request.Profile.TrustRootReference,
		TrustVerified:             true,
		ExternalOperationID:       request.OperationID,
		SignatureArtifactDigest:   artifactDigest,
		SignatureMediaType:        "application/vnd.dev.cosign.simplesigning.v1+json",
		SignatureArtifactSize:     512,
		SignatureReadBackVerified: true,
		ArtifactSubjectDigest:     request.Artifact.Digest,
		StoredSignatureDigest:     signatureDigest,
	}
	if a.mutate != nil {
		a.mutate(&result)
	}
	return result, nil
}

func TestManagedSigningRequiresDurableSubjectBoundArtifactReadback(t *testing.T) {
	t.Parallel()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	adapter := &fakeKMSAdapter{
		private: private, public: public,
		mutate: func(result *Result) { result.SignatureReadBackVerified = false },
	}
	service := Service{
		Adapter: adapter, JournalRoot: t.TempDir(), RequireDurableArtifact: true,
	}
	if _, _, err := service.Sign(
		context.Background(), testProfile(),
		Artifact{
			URI:    "oci://registry.example/wolf@" + testArtifactDigest,
			Digest: testArtifactDigest, MediaType: "application/vnd.oci.image.manifest.v1+json",
		},
		testBinding(), testOperationID,
	); err == nil || !strings.Contains(err.Error(), "read-back-verified") {
		t.Fatalf("non-durable signing artifact error = %v", err)
	}
}

func TestServiceVerifiesIdentityAndCachesExternalOperation(t *testing.T) {
	t.Parallel()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	adapter := &fakeKMSAdapter{private: private, public: public}
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	service := Service{
		Adapter: adapter, JournalRoot: t.TempDir(),
		Now: func() time.Time { return now },
	}
	profile := testProfile()
	evidence, first, err := service.Sign(
		context.Background(), profile,
		Artifact{
			URI:    "oci://registry.example/wolf@" + testArtifactDigest,
			Digest: testArtifactDigest, MediaType: "application/vnd.oci.image.manifest.v1+json",
		},
		testBinding(), testOperationID,
	)
	if err != nil {
		t.Fatal(err)
	}
	replayed, second, err := service.Sign(
		context.Background(), profile,
		Artifact{
			URI:    "oci://registry.example/wolf@" + testArtifactDigest,
			Digest: testArtifactDigest, MediaType: "application/vnd.oci.image.manifest.v1+json",
		},
		testBinding(), testOperationID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if adapter.calls.Load() != 1 || !evidence.Verified || !replayed.Verified ||
		first.Signature != second.Signature ||
		evidence.ExpectedSubject != evidence.ObservedSubject ||
		evidence.VerifiedAt != now {
		t.Fatalf(
			"calls=%d evidence=%#v replay=%#v",
			adapter.calls.Load(), evidence, replayed,
		)
	}
	if strings.Contains(evidence.KeyReference, "arn:aws:kms") ||
		evidence.RequestDigest != adapter.request.RequestDigest {
		t.Fatalf("evidence leaked reference or lost request binding: %#v", evidence)
	}
}

func TestServiceRejectsIdentityMismatchRevocationAndAmbiguousRetry(t *testing.T) {
	t.Parallel()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	adapter := &fakeKMSAdapter{
		private: private, public: public,
		mutate: func(result *Result) { result.Subject = "other-subject" },
	}
	service := Service{Adapter: adapter, JournalRoot: t.TempDir()}
	if _, _, err := service.Sign(
		context.Background(), testProfile(),
		Artifact{Digest: testArtifactDigest, MediaType: "application/test"},
		testBinding(), testOperationID,
	); err == nil || !strings.Contains(err.Error(), "identity policy") {
		t.Fatalf("identity mismatch error = %v", err)
	}

	revoked := testProfile()
	at := time.Now().UTC()
	revoked.State = scannerrelease.SignerRevoked
	revoked.RevokedAt = &at
	revoked.RevocationReason = "compromised key"
	if _, err := PrepareRequest(
		revoked, Artifact{Digest: testArtifactDigest, MediaType: "application/test"},
		testBinding(), testOperationID,
	); err == nil || !strings.Contains(err.Error(), "not active") {
		t.Fatalf("revoked signer error = %v", err)
	}

	failing := &fakeKMSAdapter{
		private: private, public: public, fail: errors.New("provider timeout"),
	}
	ambiguous := Service{Adapter: failing, JournalRoot: t.TempDir()}
	if _, _, err := ambiguous.Sign(
		context.Background(), testProfile(),
		Artifact{Digest: testArtifactDigest, MediaType: "application/test"},
		testBinding(), testOperationID,
	); err == nil {
		t.Fatal("first ambiguous call unexpectedly succeeded")
	}
	if _, _, err := ambiguous.Sign(
		context.Background(), testProfile(),
		Artifact{Digest: testArtifactDigest, MediaType: "application/test"},
		testBinding(), testOperationID,
	); !errors.Is(err, ErrAmbiguousSigningResult) {
		t.Fatalf("ambiguous retry error = %v", err)
	}
	if failing.calls.Load() != 1 {
		t.Fatalf("ambiguous external operation called %d times", failing.calls.Load())
	}
}

func TestValidateProfilesForEverySupportedProvider(t *testing.T) {
	t.Parallel()
	cases := map[scannerrelease.SignerProvider]string{
		scannerrelease.SignerAWSKMS:         "aws-kms://arn:aws:kms:us-east-1:123456789012:key/key-id",
		scannerrelease.SignerGCPKMS:         "gcp-kms://projects/p/locations/global/keyRings/r/cryptoKeys/k",
		scannerrelease.SignerAzureKeyVault:  "azure-keyvault://vault/keys/release/version",
		scannerrelease.SignerPKCS11:         "pkcs11:object=release;type=private",
		scannerrelease.SignerKeyless:        "workload://github/actions/release",
		scannerrelease.SignerOffline:        "offline://release-root-2026",
		scannerrelease.SignerManagedKeyless: "managed-keyless://wolf-release",
	}
	for provider, reference := range cases {
		profile := testProfile()
		profile.Provider = provider
		profile.KeyReference = reference
		if provider == scannerrelease.SignerKeyless ||
			provider == scannerrelease.SignerManagedKeyless {
			profile.Algorithm = "cosign-keyless"
		}
		if err := ValidateProfile(profile); err != nil {
			t.Fatalf("%s profile: %v", provider, err)
		}
	}
	profile := testProfile()
	profile.KeyReference = "aws-kms://-----BEGIN PRIVATE KEY-----"
	if err := ValidateProfile(profile); err == nil {
		t.Fatal("profile accepted embedded private material")
	}
}

func testProfile() scannerrelease.SignerProfile {
	return scannerrelease.SignerProfile{
		ID: "signer-1", Name: "Production AWS KMS",
		Provider: scannerrelease.SignerAWSKMS, Algorithm: "ed25519",
		KeyReference:     "aws-kms://arn:aws:kms:us-east-1:123456789012:key/key-id",
		WorkloadIdentity: true, Identity: "arn:aws:iam::123456789012:role/wolf",
		Issuer: "https://sts.amazonaws.com", Subject: "wolf-release-builder",
		TrustRootReference: "secret://signing/aws-root",
		State:              scannerrelease.SignerActive, Revision: 1,
	}
}

func testBinding() Binding {
	return Binding{
		DefinitionCommit: "1111111111111111111111111111111111111111",
		LockDigest:       testLockDigest, PolicyID: "policy-1", PolicyRevision: 7,
	}
}
