package scannersigning

import (
	"time"

	"github.com/alphabravocompany/thewolf/internal/scannerrelease"
)

type Binding struct {
	DefinitionCommit string `json:"definition_commit"`
	LockDigest       string `json:"lock_digest"`
	PolicyID         string `json:"policy_id"`
	PolicyRevision   int64  `json:"policy_revision"`
}

type Artifact struct {
	URI       string `json:"uri"`
	Digest    string `json:"digest"`
	MediaType string `json:"media_type"`
}

type Request struct {
	SchemaVersion      string                       `json:"schema_version"`
	OperationID        string                       `json:"operation_id"`
	RequestDigest      string                       `json:"request_digest"`
	ProfileDigest      string                       `json:"profile_digest"`
	Profile            scannerrelease.SignerProfile `json:"profile"`
	KeyReference       string                       `json:"key_reference"`
	SecretReference    string                       `json:"secret_reference,omitempty"`
	TrustRootReference string                       `json:"trust_root_reference"`
	Artifact           Artifact                     `json:"artifact"`
	Binding            Binding                      `json:"binding"`
	Payload            string                       `json:"payload"`
}

type Result struct {
	SchemaVersion       string `json:"schema_version"`
	OperationID         string `json:"operation_id"`
	RequestDigest       string `json:"request_digest"`
	ProfileDigest       string `json:"profile_digest"`
	Algorithm           string `json:"algorithm"`
	Signature           string `json:"signature"`
	SignatureURI        string `json:"signature_uri"`
	PublicKeyPEM        string `json:"public_key_pem,omitempty"`
	CertificatePEM      string `json:"certificate_pem,omitempty"`
	KeyVersion          string `json:"key_version"`
	Identity            string `json:"identity"`
	Issuer              string `json:"issuer"`
	Subject             string `json:"subject"`
	TrustRootReference  string `json:"trust_root_reference"`
	TrustVerified       bool   `json:"trust_verified"`
	TransparencyLogID   string `json:"transparency_log_id,omitempty"`
	ExternalOperationID string `json:"external_operation_id"`
	// Durable artifact fields bind the externally stored OCI/object envelope
	// to the verified signature bytes and exact signed subject. Managed release
	// signing requires these fields; offline callers may opt out explicitly.
	SignatureArtifactDigest   string `json:"signature_artifact_digest,omitempty"`
	SignatureMediaType        string `json:"signature_media_type,omitempty"`
	SignatureArtifactSize     int64  `json:"signature_artifact_size_bytes,omitempty"`
	SignatureReadBackVerified bool   `json:"signature_read_back_verified,omitempty"`
	ArtifactSubjectDigest     string `json:"artifact_subject_digest,omitempty"`
	StoredSignatureDigest     string `json:"stored_signature_digest,omitempty"`
	CertificateDigest         string `json:"certificate_digest,omitempty"`
}

type Evidence struct {
	SchemaVersion           string    `json:"schema_version"`
	OperationID             string    `json:"operation_id"`
	RequestDigest           string    `json:"request_digest"`
	ProfileID               string    `json:"profile_id"`
	ProfileRevision         int64     `json:"profile_revision"`
	ProfileDigest           string    `json:"profile_digest"`
	Provider                string    `json:"provider"`
	Algorithm               string    `json:"algorithm"`
	ArtifactDigest          string    `json:"artifact_digest"`
	SignatureDigest         string    `json:"signature_digest"`
	SignatureURI            string    `json:"signature_uri"`
	KeyReference            string    `json:"key_reference"`
	KeyVersion              string    `json:"key_version"`
	ExpectedIdentity        string    `json:"expected_identity"`
	ObservedIdentity        string    `json:"observed_identity"`
	ExpectedIssuer          string    `json:"expected_issuer"`
	ObservedIssuer          string    `json:"observed_issuer"`
	ExpectedSubject         string    `json:"expected_subject"`
	ObservedSubject         string    `json:"observed_subject"`
	ExpectedTrustRoot       string    `json:"expected_trust_root"`
	ObservedTrustRoot       string    `json:"observed_trust_root"`
	TrustVerified           bool      `json:"trust_verified"`
	TransparencyLogID       string    `json:"transparency_log_id,omitempty"`
	Verified                bool      `json:"verified"`
	VerifiedAt              time.Time `json:"verified_at"`
	SignatureArtifactDigest string    `json:"signature_artifact_digest,omitempty"`
	SignatureMediaType      string    `json:"signature_media_type,omitempty"`
	SignatureArtifactSize   int64     `json:"signature_artifact_size_bytes,omitempty"`
	ArtifactSubjectDigest   string    `json:"artifact_subject_digest,omitempty"`
	CertificateDigest       string    `json:"certificate_digest,omitempty"`
}
