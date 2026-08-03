package scannersigning

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

var ErrAmbiguousSigningResult = errors.New("signing result is ambiguous")

type Service struct {
	Adapter                Adapter
	JournalRoot            string
	Now                    func() time.Time
	RequireDurableArtifact bool
}

func PrepareRequest(
	profile scannerrelease.SignerProfile,
	artifact Artifact,
	binding Binding,
	operationID string,
) (Request, error) {
	if err := ValidateProfile(profile); err != nil {
		return Request{}, err
	}
	if profile.State != scannerrelease.SignerActive {
		return Request{}, fmt.Errorf("signer profile %q is not active", profile.ID)
	}
	if !ValidDigest(artifact.Digest) || strings.TrimSpace(artifact.MediaType) == "" {
		return Request{}, errors.New("signing artifact digest and media type are required")
	}
	if artifact.URI != "" {
		parsed, err := url.Parse(artifact.URI)
		if err != nil || parsed.Scheme == "" || parsed.User != nil ||
			parsed.RawQuery != "" || parsed.Fragment != "" {
			return Request{}, errors.New("signing artifact URI is unsafe")
		}
	}
	if !ValidDigest(binding.LockDigest) || binding.PolicyRevision <= 0 ||
		strings.TrimSpace(binding.PolicyID) == "" ||
		!commitPattern.MatchString(binding.DefinitionCommit) {
		return Request{}, errors.New("signing immutable binding is invalid")
	}
	if !ValidDigest(operationID) {
		return Request{}, errors.New("signing operation ID must be sha256")
	}
	profileDigest, err := ProfileDigest(profile)
	if err != nil {
		return Request{}, err
	}
	payloadValue, err := json.Marshal(struct {
		SchemaVersion string   `json:"schema_version"`
		OperationID   string   `json:"operation_id"`
		ProfileDigest string   `json:"profile_digest"`
		Artifact      Artifact `json:"artifact"`
		Binding       Binding  `json:"binding"`
	}{
		RequestSchema, operationID, profileDigest, artifact, binding,
	})
	if err != nil {
		return Request{}, err
	}
	return Request{
		SchemaVersion: RequestSchema,
		OperationID:   operationID, ProfileDigest: profileDigest,
		Profile: profile, Artifact: artifact, Binding: binding,
		KeyReference:       profile.KeyReference,
		SecretReference:    profile.SecretReference,
		TrustRootReference: profile.TrustRootReference,
		Payload:            base64.RawStdEncoding.EncodeToString(payloadValue),
		RequestDigest:      digest(payloadValue),
	}, nil
}

func (s Service) Sign(
	ctx context.Context,
	profile scannerrelease.SignerProfile,
	artifact Artifact,
	binding Binding,
	operationID string,
) (Evidence, Result, error) {
	if s.Adapter == nil {
		return Evidence{}, Result{}, errors.New("signer adapter is required")
	}
	request, err := PrepareRequest(profile, artifact, binding, operationID)
	if err != nil {
		return Evidence{}, Result{}, err
	}
	if strings.TrimSpace(s.JournalRoot) == "" || !filepath.IsAbs(s.JournalRoot) {
		return Evidence{}, Result{}, errors.New("absolute signer journal root is required")
	}
	directory := filepath.Join(
		s.JournalRoot, strings.TrimPrefix(operationID, "sha256:"),
	)
	resultPath := filepath.Join(directory, "result.json")
	if result, found, err := loadResult(resultPath, request); err != nil {
		return Evidence{}, Result{}, err
	} else if found {
		evidence, err := verifyResult(request, result, s.now(), s.RequireDurableArtifact)
		return evidence, result, err
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return Evidence{}, Result{}, err
	}
	startedPath := filepath.Join(directory, "started")
	if _, err := os.Stat(startedPath); err == nil {
		return Evidence{}, Result{}, fmt.Errorf(
			"%w for operation %s", ErrAmbiguousSigningResult, operationID,
		)
	} else if !os.IsNotExist(err) {
		return Evidence{}, Result{}, err
	}
	if err := writeAtomic(
		startedPath, []byte(request.RequestDigest+"\n"),
	); err != nil {
		return Evidence{}, Result{}, err
	}
	result, err := s.Adapter.Sign(ctx, request)
	if err != nil {
		return Evidence{}, Result{}, err
	}
	evidence, err := verifyResult(request, result, s.now(), s.RequireDurableArtifact)
	if err != nil {
		return Evidence{}, Result{}, err
	}
	value, err := json.Marshal(result)
	if err != nil {
		return Evidence{}, Result{}, err
	}
	if err := writeAtomic(resultPath, value); err != nil {
		return Evidence{}, Result{}, err
	}
	return evidence, result, nil
}

func (s Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func verifyResult(
	request Request, result Result, now time.Time, requireDurableArtifact bool,
) (Evidence, error) {
	profile := request.Profile
	if result.SchemaVersion != ResultSchema ||
		result.OperationID != request.OperationID ||
		result.ExternalOperationID != request.OperationID ||
		result.RequestDigest != request.RequestDigest ||
		result.ProfileDigest != request.ProfileDigest {
		return Evidence{}, errors.New("signer adapter immutable result binding mismatch")
	}
	if result.Algorithm != profile.Algorithm ||
		result.Identity != profile.Identity ||
		result.Issuer != profile.Issuer ||
		result.Subject != profile.Subject ||
		result.TrustRootReference != profile.TrustRootReference ||
		!result.TrustVerified {
		return Evidence{}, errors.New("signer adapter identity policy mismatch")
	}
	if strings.TrimSpace(result.KeyVersion) == "" || strings.TrimSpace(result.SignatureURI) == "" {
		return Evidence{}, errors.New("signer adapter key version and signature URI are required")
	}
	parsed, err := url.Parse(result.SignatureURI)
	if err != nil || parsed.Scheme == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		return Evidence{}, errors.New("signer adapter signature URI is unsafe")
	}
	signature, err := base64.RawStdEncoding.DecodeString(result.Signature)
	if err != nil || len(signature) == 0 {
		return Evidence{}, errors.New("signer adapter signature is invalid")
	}
	payload, err := base64.RawStdEncoding.DecodeString(request.Payload)
	if err != nil || digest(payload) != request.RequestDigest {
		return Evidence{}, errors.New("signing request payload digest mismatch")
	}
	if err := verifyPublicSignature(profile.Algorithm, payload, signature, result); err != nil {
		return Evidence{}, err
	}
	signatureDigest := digest(signature)
	if requireDurableArtifact {
		artifactURI, err := url.Parse(result.SignatureURI)
		if err != nil || artifactURI.User != nil || artifactURI.RawQuery != "" ||
			artifactURI.Fragment != "" || !durableArtifactScheme(artifactURI.Scheme) ||
			!ValidDigest(result.SignatureArtifactDigest) ||
			!strings.Contains(result.SignatureURI, result.SignatureArtifactDigest) ||
			strings.TrimSpace(result.SignatureMediaType) == "" ||
			result.SignatureArtifactSize <= 0 ||
			!result.SignatureReadBackVerified ||
			result.ArtifactSubjectDigest != request.Artifact.Digest ||
			result.StoredSignatureDigest != signatureDigest {
			return Evidence{}, errors.New(
				"signer adapter did not return an immutable, subject-bound, read-back-verified signature artifact",
			)
		}
		if result.CertificatePEM != "" {
			if !ValidDigest(result.CertificateDigest) ||
				result.CertificateDigest != digest([]byte(result.CertificatePEM)) {
				return Evidence{}, errors.New("signer certificate artifact digest is invalid")
			}
		}
	}
	return Evidence{
		SchemaVersion: "wolf.scanner-signing-evidence/v1",
		OperationID:   request.OperationID, RequestDigest: request.RequestDigest,
		ProfileID: profile.ID, ProfileRevision: profile.Revision,
		ProfileDigest: request.ProfileDigest, Provider: string(profile.Provider),
		Algorithm: result.Algorithm, ArtifactDigest: request.Artifact.Digest,
		SignatureDigest: signatureDigest, SignatureURI: result.SignatureURI,
		KeyReference: MaskReference(profile.KeyReference), KeyVersion: result.KeyVersion,
		ExpectedIdentity: profile.Identity, ObservedIdentity: result.Identity,
		ExpectedIssuer: profile.Issuer, ObservedIssuer: result.Issuer,
		ExpectedSubject: profile.Subject, ObservedSubject: result.Subject,
		ExpectedTrustRoot: MaskReference(profile.TrustRootReference),
		ObservedTrustRoot: MaskReference(result.TrustRootReference),
		TrustVerified:     result.TrustVerified,
		TransparencyLogID: result.TransparencyLogID,
		Verified:          true, VerifiedAt: now,
		SignatureArtifactDigest: result.SignatureArtifactDigest,
		SignatureMediaType:      result.SignatureMediaType,
		SignatureArtifactSize:   result.SignatureArtifactSize,
		ArtifactSubjectDigest:   result.ArtifactSubjectDigest,
		CertificateDigest:       result.CertificateDigest,
	}, nil
}

func durableArtifactScheme(scheme string) bool {
	switch strings.ToLower(scheme) {
	case "oci", "https", "s3", "gs", "azblob":
		return true
	default:
		return false
	}
}

func verifyPublicSignature(
	algorithm string,
	payload, signature []byte,
	result Result,
) error {
	publicKey, err := parsePublicKey(result)
	if err != nil {
		return err
	}
	hash := sha256.Sum256(payload)
	switch algorithm {
	case "ed25519":
		key, ok := publicKey.(ed25519.PublicKey)
		if !ok || !ed25519.Verify(key, payload, signature) {
			return errors.New("Ed25519 signer result verification failed")
		}
	case "ecdsa-p256-sha256", "cosign-keyless":
		key, ok := publicKey.(*ecdsa.PublicKey)
		if !ok || key.Curve.Params().Name != "P-256" {
			return errors.New("signer result does not contain a P-256 public key")
		}
		var decoded struct{ R, S *big.Int }
		if _, err := asn1.Unmarshal(signature, &decoded); err != nil ||
			decoded.R == nil || decoded.S == nil ||
			!ecdsa.Verify(key, hash[:], decoded.R, decoded.S) {
			return errors.New("ECDSA signer result verification failed")
		}
	case "rsa-pss-sha256":
		key, ok := publicKey.(*rsa.PublicKey)
		if !ok || rsa.VerifyPSS(
			key, crypto.SHA256, hash[:], signature, nil,
		) != nil {
			return errors.New("RSA-PSS signer result verification failed")
		}
	default:
		return fmt.Errorf("unsupported verification algorithm %q", algorithm)
	}
	return nil
}

func parsePublicKey(result Result) (crypto.PublicKey, error) {
	value := result.PublicKeyPEM
	if result.CertificatePEM != "" {
		value = result.CertificatePEM
	}
	block, _ := pem.Decode([]byte(value))
	if block == nil {
		return nil, errors.New("signer result public key or certificate is required")
	}
	if block.Type == "CERTIFICATE" {
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse signer certificate: %w", err)
		}
		return certificate.PublicKey, nil
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse signer public key: %w", err)
	}
	return key, nil
}

func loadResult(path string, request Request) (Result, bool, error) {
	value, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Result{}, false, nil
	}
	if err != nil {
		return Result{}, false, err
	}
	if len(value) > maxAdapterOutput {
		return Result{}, false, errors.New("cached signing result exceeds size limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	var result Result
	if err := decoder.Decode(&result); err != nil {
		return Result{}, false, err
	}
	if result.RequestDigest != request.RequestDigest {
		return Result{}, false, errors.New("cached signing request digest mismatch")
	}
	return result, true, nil
}

func writeAtomic(path string, value []byte) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(directory, ".signing-*.tmp")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(value); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}
