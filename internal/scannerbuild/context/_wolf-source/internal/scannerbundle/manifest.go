// Package scannerbundle implements the deterministic, signed bundle format
// used to move immutable scanner releases into private and air-gapped
// registries.
package scannerbundle

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	ManifestSchema = "wolf.scanner-release/v1"
	BundleSchema   = "wolf.scanner-release-bundle/v1"
	BundleSchemaV2 = "wolf.scanner-release-bundle/v2"

	ManifestPath  = "release-manifest.json"
	SignaturePath = "signatures/release-manifest.ed25519.json"
	IndexPath     = "bundle-index.json"
)

var (
	releaseIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	hexCommitPattern = regexp.MustCompile(`^[a-fA-F0-9]{7,128}$`)
)

// ReleaseManifest binds a release identity to every scanner image and
// supporting artifact needed to verify and import it.
type ReleaseManifest struct {
	SchemaVersion      string            `json:"schema_version"`
	ReleaseID          string            `json:"release_id"`
	LockDigest         string            `json:"lock_digest"`
	DefinitionCommit   string            `json:"definition_commit"`
	BuildPolicyDigest  string            `json:"build_policy_digest"`
	MinimumWolfVersion string            `json:"minimum_wolf_version,omitempty"`
	GeneratedAt        time.Time         `json:"generated_at"`
	Images             []ReleaseImage    `json:"images"`
	Artifacts          []ReleaseArtifact `json:"artifacts,omitempty"`
	OCIRecords         []OCIRecord       `json:"oci_records,omitempty"`
	Metadata           map[string]string `json:"metadata,omitempty"`
}

// ReleaseImage is one immutable image in a scanner release. Digest is the OCI
// image-index or image-manifest digest; Platforms maps a platform string to its
// immutable child manifest digest.
type ReleaseImage struct {
	Key        string            `json:"key"`
	Kind       string            `json:"kind"`
	Reference  string            `json:"reference"`
	Digest     string            `json:"digest"`
	Platforms  map[string]string `json:"platforms"`
	Tools      []string          `json:"tools,omitempty"`
	Entrypoint string            `json:"entrypoint,omitempty"`
	Size       int64             `json:"size,omitempty"`
	Required   bool              `json:"required"`
	// SourceReference/SourceDigest preserve the connected-registry identity
	// when a platform-selective transfer produces a derived OCI index.
	SourceReference string   `json:"source_reference,omitempty"`
	SourceDigest    string   `json:"source_digest,omitempty"`
	BlobDigests     []string `json:"blob_digests,omitempty"`
}

// ReleaseArtifact describes evidence or offline content carried by the
// release bundle. BundlePath is empty for evidence retained solely as an OCI
// referrer or remote content-addressed object.
type ReleaseArtifact struct {
	Key              string   `json:"key"`
	Type             string   `json:"type"`
	MediaType        string   `json:"media_type"`
	Digest           string   `json:"digest"`
	Size             int64    `json:"size"`
	BundlePath       string   `json:"bundle_path,omitempty"`
	StorageDigest    string   `json:"storage_digest,omitempty"`
	StorageReference string   `json:"storage_reference,omitempty"`
	StorageMediaType string   `json:"storage_media_type,omitempty"`
	StorageSize      int64    `json:"storage_size,omitempty"`
	OCIClosure       []string `json:"oci_closure,omitempty"`
}

// OCIRecord describes one exact file in a v2 OCI transfer closure. Records
// are signed as part of the release manifest and may share a bundle path with
// a higher-level evidence artifact when digest, size, and media type agree.
type OCIRecord struct {
	Digest     string `json:"digest"`
	Size       int64  `json:"size"`
	MediaType  string `json:"media_type"`
	Kind       string `json:"kind"`
	BundlePath string `json:"bundle_path"`
}

// Signature is the portable offline signature envelope. Managed factories can
// retain Cosign signatures as OCI referrers while also producing this envelope
// for offline bundle verification.
type Signature struct {
	Algorithm         string `json:"algorithm"`
	KeyID             string `json:"key_id"`
	ManifestDigest    string `json:"manifest_digest"`
	Value             string `json:"value"`
	ProfileRevision   int64  `json:"profile_revision,omitempty"`
	RequestDigest     string `json:"request_digest,omitempty"`
	ProfileDigest     string `json:"profile_digest,omitempty"`
	OperationID       string `json:"operation_id,omitempty"`
	SignatureURI      string `json:"signature_uri,omitempty"`
	SigningPayload    string `json:"signing_payload,omitempty"`
	Identity          string `json:"identity,omitempty"`
	Issuer            string `json:"issuer,omitempty"`
	Subject           string `json:"subject,omitempty"`
	TrustRootDigest   string `json:"trust_root_digest,omitempty"`
	KeyVersion        string `json:"key_version,omitempty"`
	PublicKeyPEM      string `json:"public_key_pem,omitempty"`
	CertificatePEM    string `json:"certificate_pem,omitempty"`
	TransparencyLogID string `json:"transparency_log_id,omitempty"`
	EvidenceDigest    string `json:"evidence_digest,omitempty"`
}

type ManifestSigner interface {
	SignManifest(ctx context.Context, canonicalManifest []byte) (Signature, error)
}

type ManifestVerifier interface {
	VerifyManifest(ctx context.Context, canonicalManifest []byte, signature Signature) error
}

func (m ReleaseManifest) Validate() error {
	if m.SchemaVersion != ManifestSchema {
		return fmt.Errorf("unsupported manifest schema %q", m.SchemaVersion)
	}
	if !releaseIDPattern.MatchString(m.ReleaseID) {
		return fmt.Errorf("invalid release_id %q", m.ReleaseID)
	}
	if err := validateDigest("lock_digest", m.LockDigest); err != nil {
		return err
	}
	if err := validateDigest("build_policy_digest", m.BuildPolicyDigest); err != nil {
		return err
	}
	if !hexCommitPattern.MatchString(m.DefinitionCommit) {
		return fmt.Errorf("definition_commit must be a 7-128 character hexadecimal commit")
	}
	if m.GeneratedAt.IsZero() {
		return errors.New("generated_at is required")
	}
	if m.GeneratedAt.Location() != time.UTC {
		return errors.New("generated_at must use UTC")
	}
	if len(m.Images) == 0 {
		return errors.New("at least one release image is required")
	}

	imageKeys := make(map[string]struct{}, len(m.Images))
	toolImages := make(map[string]string)
	hasDefault := false
	for i, image := range m.Images {
		prefix := fmt.Sprintf("images[%d]", i)
		if strings.TrimSpace(image.Key) == "" {
			return fmt.Errorf("%s.key is required", prefix)
		}
		if _, exists := imageKeys[image.Key]; exists {
			return fmt.Errorf("duplicate image key %q", image.Key)
		}
		imageKeys[image.Key] = struct{}{}
		if strings.TrimSpace(image.Reference) == "" {
			return fmt.Errorf("%s.reference is required", prefix)
		}
		if err := validateDigest(prefix+".digest", image.Digest); err != nil {
			return err
		}
		if image.SourceDigest != "" {
			if err := validateDigest(prefix+".source_digest", image.SourceDigest); err != nil {
				return err
			}
			if image.SourceReference == "" ||
				!strings.HasSuffix(image.SourceReference, "@"+image.SourceDigest) {
				return fmt.Errorf("%s.source_reference must end with @%s", prefix, image.SourceDigest)
			}
		}
		seenBlobs := make(map[string]struct{}, len(image.BlobDigests))
		for _, blobDigest := range image.BlobDigests {
			if err := validateDigest(prefix+".blob_digests", blobDigest); err != nil {
				return err
			}
			if _, duplicate := seenBlobs[blobDigest]; duplicate {
				return fmt.Errorf("%s.blob_digests contains duplicate %s", prefix, blobDigest)
			}
			seenBlobs[blobDigest] = struct{}{}
		}
		at := strings.LastIndexByte(image.Reference, '@')
		if at < 1 || image.Reference[at+1:] != image.Digest {
			return fmt.Errorf("%s.reference must end with @%s", prefix, image.Digest)
		}
		switch image.Kind {
		case "wolf":
			if image.Entrypoint != "" {
				return fmt.Errorf("%s.entrypoint is only valid for upstream images", prefix)
			}
		case "upstream":
			if len(image.Tools) == 0 {
				return fmt.Errorf("%s.tools must not be empty for an upstream image", prefix)
			}
		case "fixer":
			if len(image.Tools) != 0 || image.Entrypoint != "" {
				return fmt.Errorf("%s fixer image cannot own scanner tools or an entrypoint", prefix)
			}
		default:
			return fmt.Errorf("%s.kind must be wolf, upstream, or fixer", prefix)
		}
		if image.Key == "default" {
			if image.Kind != "wolf" {
				return errors.New("default image must have kind wolf")
			}
			hasDefault = true
		} else if image.Kind == "wolf" && len(image.Tools) == 0 {
			return fmt.Errorf("%s.tools must not be empty for a non-default wolf image", prefix)
		}
		seenTools := make(map[string]struct{}, len(image.Tools))
		for _, tool := range image.Tools {
			tool = strings.TrimSpace(tool)
			if tool == "" {
				return fmt.Errorf("%s.tools contains an empty tool", prefix)
			}
			if _, duplicate := seenTools[tool]; duplicate {
				return fmt.Errorf("%s.tools contains duplicate tool %q", prefix, tool)
			}
			seenTools[tool] = struct{}{}
			if other, duplicate := toolImages[tool]; duplicate {
				return fmt.Errorf("tool %q is assigned to both image %q and %q", tool, other, image.Key)
			}
			toolImages[tool] = image.Key
		}
		if image.Size < 0 {
			return fmt.Errorf("%s.size must not be negative", prefix)
		}
		if len(image.Platforms) == 0 {
			return fmt.Errorf("%s.platforms must not be empty", prefix)
		}
		for platform, digest := range image.Platforms {
			if strings.TrimSpace(platform) == "" || !strings.Contains(platform, "/") {
				return fmt.Errorf("%s has invalid platform %q", prefix, platform)
			}
			if err := validateDigest(prefix+".platforms["+platform+"]", digest); err != nil {
				return err
			}
		}
	}
	if !hasDefault {
		return errors.New("release manifest requires a default wolf image")
	}

	artifactKeys := make(map[string]struct{}, len(m.Artifacts))
	type artifactPathIdentity struct {
		digest    string
		mediaType string
		size      int64
	}
	artifactPaths := make(map[string]artifactPathIdentity, len(m.Artifacts))
	for i, artifact := range m.Artifacts {
		prefix := fmt.Sprintf("artifacts[%d]", i)
		if strings.TrimSpace(artifact.Key) == "" {
			return fmt.Errorf("%s.key is required", prefix)
		}
		if _, exists := artifactKeys[artifact.Key]; exists {
			return fmt.Errorf("duplicate artifact key %q", artifact.Key)
		}
		artifactKeys[artifact.Key] = struct{}{}
		if strings.TrimSpace(artifact.Type) == "" {
			return fmt.Errorf("%s.type is required", prefix)
		}
		if strings.TrimSpace(artifact.MediaType) == "" {
			return fmt.Errorf("%s.media_type is required", prefix)
		}
		if err := validateDigest(prefix+".digest", artifact.Digest); err != nil {
			return err
		}
		if artifact.Size < 0 {
			return fmt.Errorf("%s.size must not be negative", prefix)
		}
		storageDeclared := artifact.StorageDigest != "" || artifact.StorageReference != "" || artifact.StorageMediaType != "" ||
			artifact.StorageSize != 0 || len(artifact.OCIClosure) != 0
		if storageDeclared {
			if err := validateDigest(prefix+".storage_digest", artifact.StorageDigest); err != nil {
				return err
			}
			if !strings.HasSuffix(artifact.StorageReference, "@"+artifact.StorageDigest) {
				return fmt.Errorf("%s storage reference must end with its digest", prefix)
			}
			if strings.TrimSpace(artifact.StorageMediaType) == "" || artifact.StorageSize <= 0 ||
				len(artifact.OCIClosure) == 0 {
				return fmt.Errorf("%s storage media type, size, and OCI closure are required together", prefix)
			}
			seenClosure := make(map[string]struct{}, len(artifact.OCIClosure))
			for _, digest := range artifact.OCIClosure {
				if err := validateDigest(prefix+".oci_closure", digest); err != nil {
					return err
				}
				if _, duplicate := seenClosure[digest]; duplicate {
					return fmt.Errorf("%s.oci_closure contains duplicate %s", prefix, digest)
				}
				seenClosure[digest] = struct{}{}
			}
			if _, exists := seenClosure[artifact.StorageDigest]; !exists {
				return fmt.Errorf("%s storage digest is outside its OCI closure", prefix)
			}
			if _, exists := seenClosure[artifact.Digest]; !exists {
				return fmt.Errorf("%s payload digest is outside its OCI closure", prefix)
			}
		}
		if artifact.BundlePath != "" {
			clean, err := cleanBundlePath(artifact.BundlePath)
			if err != nil {
				return fmt.Errorf("%s.bundle_path: %w", prefix, err)
			}
			if clean != artifact.BundlePath {
				return fmt.Errorf("%s.bundle_path must be canonical", prefix)
			}
			if isReservedPath(clean) {
				return fmt.Errorf("%s.bundle_path %q is reserved", prefix, clean)
			}
			identity := artifactPathIdentity{
				digest: artifact.Digest, mediaType: artifact.MediaType, size: artifact.Size,
			}
			if existing, exists := artifactPaths[clean]; exists && existing != identity {
				return fmt.Errorf("artifact bundle_path %q has conflicting content identity", clean)
			}
			artifactPaths[clean] = identity
		}
	}
	ociDigests := make(map[string]struct{}, len(m.OCIRecords))
	for i, record := range m.OCIRecords {
		prefix := fmt.Sprintf("oci_records[%d]", i)
		if err := validateDigest(prefix+".digest", record.Digest); err != nil {
			return err
		}
		if _, duplicate := ociDigests[record.Digest]; duplicate {
			return fmt.Errorf("duplicate OCI record digest %q", record.Digest)
		}
		ociDigests[record.Digest] = struct{}{}
		if record.Size < 0 || record.MediaType == "" ||
			!strings.HasPrefix(record.Kind, "oci-") {
			return fmt.Errorf("%s size, media_type, or kind is invalid", prefix)
		}
		clean, err := cleanBundlePath(record.BundlePath)
		if err != nil || clean != record.BundlePath ||
			record.BundlePath != OCIPath(record.Digest) {
			return fmt.Errorf("%s.bundle_path is not the canonical OCI digest path", prefix)
		}
	}
	return nil
}

// CanonicalJSON returns the exact byte representation that is hashed and
// signed. Slices are ordered by stable identity and metadata keys are sorted by
// encoding/json.
func (m ReleaseManifest) CanonicalJSON() ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	canonical := m
	canonical.GeneratedAt = canonical.GeneratedAt.UTC().Truncate(time.Second)
	canonical.Images = append([]ReleaseImage(nil), canonical.Images...)
	canonical.Artifacts = append([]ReleaseArtifact(nil), canonical.Artifacts...)
	canonical.OCIRecords = append([]OCIRecord(nil), canonical.OCIRecords...)
	for index := range canonical.Images {
		canonical.Images[index].Tools = append([]string(nil), canonical.Images[index].Tools...)
		canonical.Images[index].BlobDigests = append([]string(nil), canonical.Images[index].BlobDigests...)
		sort.Strings(canonical.Images[index].Tools)
		sort.Strings(canonical.Images[index].BlobDigests)
	}
	for index := range canonical.Artifacts {
		canonical.Artifacts[index].OCIClosure = append([]string(nil), canonical.Artifacts[index].OCIClosure...)
		sort.Strings(canonical.Artifacts[index].OCIClosure)
	}
	sort.Slice(canonical.Images, func(i, j int) bool {
		return canonical.Images[i].Key < canonical.Images[j].Key
	})
	sort.Slice(canonical.Artifacts, func(i, j int) bool {
		return canonical.Artifacts[i].Key < canonical.Artifacts[j].Key
	})
	sort.Slice(canonical.OCIRecords, func(i, j int) bool {
		return canonical.OCIRecords[i].Digest < canonical.OCIRecords[j].Digest
	})
	return json.Marshal(canonical)
}

func (m ReleaseManifest) Digest() (string, error) {
	canonical, err := m.CanonicalJSON()
	if err != nil {
		return "", err
	}
	return digestBytes(canonical), nil
}

type Ed25519Signer struct {
	KeyID      string
	PrivateKey ed25519.PrivateKey
}

func (s Ed25519Signer) SignManifest(ctx context.Context, canonicalManifest []byte) (Signature, error) {
	if err := ctx.Err(); err != nil {
		return Signature{}, err
	}
	if strings.TrimSpace(s.KeyID) == "" {
		return Signature{}, errors.New("signer key ID is required")
	}
	if len(s.PrivateKey) != ed25519.PrivateKeySize {
		return Signature{}, errors.New("invalid Ed25519 private key")
	}
	signature := ed25519.Sign(s.PrivateKey, canonicalManifest)
	return Signature{
		Algorithm:      "ed25519",
		KeyID:          s.KeyID,
		ManifestDigest: digestBytes(canonicalManifest),
		Value:          base64.RawStdEncoding.EncodeToString(signature),
	}, nil
}

// Ed25519TrustStore verifies offline signatures against explicitly trusted
// key IDs. It does not fall back to an arbitrary public key embedded in the
// bundle.
type Ed25519TrustStore map[string]ed25519.PublicKey

func (s Ed25519TrustStore) VerifyManifest(ctx context.Context, canonicalManifest []byte, signature Signature) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if signature.Algorithm != "ed25519" {
		return fmt.Errorf("unsupported signature algorithm %q", signature.Algorithm)
	}
	if signature.ManifestDigest != digestBytes(canonicalManifest) {
		return errors.New("signature manifest digest does not match release manifest")
	}
	key, ok := s[signature.KeyID]
	if !ok {
		return fmt.Errorf("signature key %q is not trusted", signature.KeyID)
	}
	if len(key) != ed25519.PublicKeySize {
		return fmt.Errorf("trusted key %q is invalid", signature.KeyID)
	}
	raw, err := base64.RawStdEncoding.DecodeString(signature.Value)
	if err != nil {
		return fmt.Errorf("decode signature: %w", err)
	}
	if !ed25519.Verify(key, canonicalManifest, raw) {
		return errors.New("release manifest signature is invalid")
	}
	return nil
}

func validateDigest(field, value string) error {
	algorithm, encoded, ok := strings.Cut(value, ":")
	if !ok || algorithm != "sha256" || len(encoded) != sha256.Size*2 {
		return fmt.Errorf("%s must be a sha256 digest", field)
	}
	if _, err := hex.DecodeString(encoded); err != nil {
		return fmt.Errorf("%s must be a sha256 digest", field)
	}
	return nil
}

func digestBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func cleanBundlePath(value string) (string, error) {
	if value == "" {
		return "", errors.New("path is empty")
	}
	if strings.ContainsRune(value, 0) || strings.Contains(value, `\`) {
		return "", errors.New("path contains an unsafe character")
	}
	if strings.HasPrefix(value, "/") {
		return "", errors.New("absolute paths are not allowed")
	}
	clean := path.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", errors.New("path escapes bundle root")
	}
	return clean, nil
}

func isReservedPath(value string) bool {
	return value == ManifestPath || value == SignaturePath || value == IndexPath
}
