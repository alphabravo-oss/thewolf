package scannersigning

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/alphabravocompany/thewolf/internal/scannerbundle"
	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

// ManifestSigner adapts the provider-neutral signing service to the portable
// release-bundle envelope. The signature covers a canonical claim containing
// the manifest digest and immutable release/policy binding.
type ManifestSigner struct {
	Service     Service
	Profile     scannerrelease.SignerProfile
	Binding     Binding
	ArtifactURI string
}

func (s ManifestSigner) SignManifest(
	ctx context.Context,
	canonicalManifest []byte,
) (scannerbundle.Signature, error) {
	manifestDigest := DigestValue(canonicalManifest)
	profileDigest, err := ProfileDigest(s.Profile)
	if err != nil {
		return scannerbundle.Signature{}, err
	}
	operationValue, err := json.Marshal(struct {
		SchemaVersion  string  `json:"schema_version"`
		ManifestDigest string  `json:"manifest_digest"`
		ProfileDigest  string  `json:"profile_digest"`
		Binding        Binding `json:"binding"`
	}{
		"wolf.scanner-bundle-signing-operation/v1",
		manifestDigest, profileDigest, s.Binding,
	})
	if err != nil {
		return scannerbundle.Signature{}, err
	}
	operationID := DigestValue(operationValue)
	evidence, result, err := s.Service.Sign(
		ctx, s.Profile, Artifact{
			URI: s.ArtifactURI, Digest: manifestDigest,
			MediaType: "application/vnd.wolf.scanner-release.manifest.v1+json",
		}, s.Binding, operationID,
	)
	if err != nil {
		return scannerbundle.Signature{}, err
	}
	request, err := PrepareRequest(
		s.Profile, Artifact{
			URI: s.ArtifactURI, Digest: manifestDigest,
			MediaType: "application/vnd.wolf.scanner-release.manifest.v1+json",
		}, s.Binding, operationID,
	)
	if err != nil {
		return scannerbundle.Signature{}, err
	}
	evidenceJSON, err := json.Marshal(evidence)
	if err != nil {
		return scannerbundle.Signature{}, err
	}
	return scannerbundle.Signature{
		Algorithm: result.Algorithm, KeyID: s.Profile.ID,
		ManifestDigest: manifestDigest, Value: result.Signature,
		ProfileRevision: s.Profile.Revision,
		RequestDigest:   request.RequestDigest, ProfileDigest: request.ProfileDigest,
		OperationID: operationID, SignatureURI: result.SignatureURI,
		SigningPayload: request.Payload, Identity: result.Identity,
		Issuer: result.Issuer, Subject: result.Subject,
		TrustRootDigest: DigestValue([]byte(result.TrustRootReference)),
		KeyVersion:      result.KeyVersion, PublicKeyPEM: result.PublicKeyPEM,
		CertificatePEM:    result.CertificatePEM,
		TransparencyLogID: result.TransparencyLogID,
		EvidenceDigest:    DigestValue(evidenceJSON),
	}, nil
}

// BundleTrustProfile is an offline allowlist entry. Trust is anchored in the
// policy-provided public key, never in a key embedded by the bundle.
type BundleTrustProfile struct {
	Algorithm       string
	ProfileDigest   string
	Identity        string
	Issuer          string
	Subject         string
	TrustRootDigest string
	PublicKeyPEM    string
	Revoked         bool
}

type BundleTrustStore map[string]BundleTrustProfile

func (s BundleTrustStore) VerifyManifest(
	ctx context.Context,
	canonicalManifest []byte,
	signature scannerbundle.Signature,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	trust, ok := s[signature.KeyID]
	if !ok {
		return fmt.Errorf("signature profile %q is not trusted", signature.KeyID)
	}
	if trust.Revoked {
		return fmt.Errorf("signature profile %q is revoked", signature.KeyID)
	}
	if signature.ManifestDigest != DigestValue(canonicalManifest) {
		return errors.New("signature manifest digest does not match release manifest")
	}
	if signature.Algorithm != trust.Algorithm ||
		signature.ProfileDigest != trust.ProfileDigest ||
		signature.Identity != trust.Identity ||
		signature.Issuer != trust.Issuer ||
		signature.Subject != trust.Subject ||
		signature.TrustRootDigest != trust.TrustRootDigest {
		return errors.New("bundle signer identity or trust policy mismatch")
	}
	payload, err := base64.RawStdEncoding.DecodeString(signature.SigningPayload)
	if err != nil || DigestValue(payload) != signature.RequestDigest {
		return errors.New("bundle signing payload digest mismatch")
	}
	var claim struct {
		SchemaVersion string   `json:"schema_version"`
		OperationID   string   `json:"operation_id"`
		ProfileDigest string   `json:"profile_digest"`
		Artifact      Artifact `json:"artifact"`
		Binding       Binding  `json:"binding"`
	}
	if err := json.Unmarshal(payload, &claim); err != nil {
		return fmt.Errorf("decode bundle signing claim: %w", err)
	}
	if claim.SchemaVersion != RequestSchema ||
		claim.OperationID != signature.OperationID ||
		claim.ProfileDigest != signature.ProfileDigest ||
		claim.Artifact.Digest != signature.ManifestDigest ||
		claim.Binding.LockDigest == "" || claim.Binding.PolicyID == "" {
		return errors.New("bundle signing claim immutable binding mismatch")
	}
	rawSignature, err := base64.RawStdEncoding.DecodeString(signature.Value)
	if err != nil || len(rawSignature) == 0 {
		return errors.New("bundle signature is invalid")
	}
	return verifyPublicSignature(
		signature.Algorithm, payload, rawSignature,
		Result{PublicKeyPEM: trust.PublicKeyPEM},
	)
}

var _ scannerbundle.ManifestSigner = ManifestSigner{}
var _ scannerbundle.ManifestVerifier = BundleTrustStore{}
