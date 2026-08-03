package scannersigning

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"testing"
)

func TestManifestSignerAndOfflineTrustPolicyRoundTrip(t *testing.T) {
	t.Parallel()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	profile := testProfile()
	profileDigest, err := ProfileDigest(profile)
	if err != nil {
		t.Fatal(err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	publicPEM := string(pem.EncodeToMemory(&pem.Block{
		Type: "PUBLIC KEY", Bytes: publicDER,
	}))
	signer := ManifestSigner{
		Service: Service{
			Adapter:     &fakeKMSAdapter{private: private, public: public},
			JournalRoot: t.TempDir(),
		},
		Profile: profile,
		Binding: Binding{
			DefinitionCommit: "1111111111111111111111111111111111111111",
			LockDigest:       "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			PolicyID:         "global", PolicyRevision: 7,
		},
		ArtifactURI: "wolf-bundle://release/release-1/manifest",
	}
	manifest := []byte(`{"release_id":"release-1"}`)
	signature, err := signer.SignManifest(context.Background(), manifest)
	if err != nil {
		t.Fatal(err)
	}
	trust := BundleTrustStore{profile.ID: {
		Algorithm: profile.Algorithm, ProfileDigest: profileDigest,
		Identity: profile.Identity, Issuer: profile.Issuer,
		Subject:         profile.Subject,
		TrustRootDigest: DigestValue([]byte(profile.TrustRootReference)),
		PublicKeyPEM:    publicPEM,
	}}
	if err := trust.VerifyManifest(
		context.Background(), manifest, signature,
	); err != nil {
		t.Fatalf("verify bundle signature: %v", err)
	}
	signature.Issuer = "https://attacker.invalid"
	if err := trust.VerifyManifest(
		context.Background(), manifest, signature,
	); err == nil {
		t.Fatal("issuer mismatch accepted")
	}
}
