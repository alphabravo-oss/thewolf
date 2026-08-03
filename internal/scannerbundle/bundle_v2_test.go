package scannerbundle

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestBundleV2VerifiesCompleteOCIClosureWithoutNetwork(t *testing.T) {
	t.Parallel()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	manifest, sources := completeOCIFixture(t)
	var bundle bytes.Buffer
	if err := Write(context.Background(), &bundle, WriteOptions{
		Manifest: manifest, Sources: sources,
		Signer:          Ed25519Signer{KeyID: "offline-v2", PrivateKey: privateKey},
		SourceDateEpoch: manifest.GeneratedAt, SchemaVersion: BundleSchemaV2,
	}); err != nil {
		t.Fatal(err)
	}
	imported, err := Read(
		context.Background(), bytes.NewReader(bundle.Bytes()), t.TempDir(),
		ReadOptions{Verifier: Ed25519TrustStore{"offline-v2": publicKey}},
	)
	if err != nil {
		t.Fatalf("no-network v2 read: %v", err)
	}
	if imported.SchemaVersion != BundleSchemaV2 ||
		len(imported.Manifest.Images[0].BlobDigests) < 5 {
		t.Fatalf("imported v2 = %#v", imported)
	}
}

func TestBundleV2RejectsWrongPlatformClosure(t *testing.T) {
	t.Parallel()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	manifest, sources := completeOCIFixture(t)
	manifest.Images[0].Platforms = map[string]string{
		"linux/arm64": "sha256:" + strings.Repeat("f", 64),
	}
	var bundle bytes.Buffer
	if err := Write(context.Background(), &bundle, WriteOptions{
		Manifest: manifest, Sources: sources,
		Signer:          Ed25519Signer{KeyID: "offline-v2", PrivateKey: privateKey},
		SourceDateEpoch: manifest.GeneratedAt, SchemaVersion: BundleSchemaV2,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(
		context.Background(), bytes.NewReader(bundle.Bytes()), t.TempDir(),
		ReadOptions{AllowUnsigned: true},
	); err == nil || !strings.Contains(err.Error(), "platform") {
		t.Fatalf("wrong-platform error = %v", err)
	}
}

func completeOCIFixture(t *testing.T) (ReleaseManifest, []Source) {
	t.Helper()
	config := []byte(`{"architecture":"amd64","os":"linux"}`)
	emptyConfig := []byte(`{}`)
	layer := []byte("scanner-layer")
	sbom := []byte(`{"spdxVersion":"SPDX-2.3","name":"scanner"}`)
	configDigest, emptyConfigDigest := digestBytes(config), digestBytes(emptyConfig)
	layerDigest, sbomDigest := digestBytes(layer), digestBytes(sbom)
	child := mustJSON(t, map[string]any{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.image.manifest.v1+json",
		"config": map[string]any{
			"mediaType": "application/vnd.oci.image.config.v1+json",
			"digest":    configDigest, "size": len(config),
		},
		"layers": []map[string]any{{
			"mediaType": "application/vnd.oci.image.layer.v1.tar+gzip",
			"digest":    layerDigest, "size": len(layer),
		}},
	})
	childDigest := digestBytes(child)
	index := mustJSON(t, map[string]any{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.image.index.v1+json",
		"manifests": []map[string]any{{
			"mediaType": "application/vnd.oci.image.manifest.v1+json",
			"digest":    childDigest, "size": len(child),
			"platform": map[string]string{"os": "linux", "architecture": "amd64"},
		}},
	})
	indexDigest := digestBytes(index)
	provenance := mustJSON(t, map[string]any{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.artifact.manifest.v1+json",
		"subject": map[string]any{
			"mediaType": "application/vnd.oci.image.index.v1+json",
			"digest":    indexDigest, "size": len(index),
		},
		"config": map[string]any{
			"mediaType": "application/vnd.oci.empty.v1+json",
			"digest":    emptyConfigDigest, "size": len(emptyConfig),
		},
		"layers": []map[string]any{{
			"mediaType": "application/spdx+json",
			"digest":    sbomDigest, "size": len(sbom),
		}},
	})
	provenanceDigest := digestBytes(provenance)
	values := map[string]struct {
		value, mediaType, kind string
	}{
		configDigest:      {string(config), "application/vnd.oci.image.config.v1+json", "oci-image-blob"},
		emptyConfigDigest: {string(emptyConfig), "application/vnd.oci.empty.v1+json", "oci-trust-blob"},
		layerDigest:       {string(layer), "application/vnd.oci.image.layer.v1.tar+gzip", "oci-image-blob"},
		sbomDigest:        {string(sbom), "application/spdx+json", "oci-trust-blob"},
		childDigest:       {string(child), "application/vnd.oci.image.manifest.v1+json", "oci-image-manifest"},
		indexDigest:       {string(index), "application/vnd.oci.image.index.v1+json", "oci-image-index"},
		provenanceDigest:  {string(provenance), "application/vnd.oci.artifact.manifest.v1+json", "oci-trust-manifest"},
	}
	var records []OCIRecord
	var sources []Source
	var digests []string
	for digest, value := range values {
		raw := []byte(value.value)
		records = append(records, OCIRecord{
			Digest: digest, Size: int64(len(raw)), MediaType: value.mediaType,
			Kind: value.kind, BundlePath: OCIPath(digest),
		})
		sources = append(sources, sourceFor(OCIPath(digest), raw))
		digests = append(digests, digest)
	}
	manifest := ReleaseManifest{
		SchemaVersion: ManifestSchema, ReleaseID: "scanner-set-2026.31.2",
		LockDigest:        "sha256:" + strings.Repeat("a", 64),
		DefinitionCommit:  strings.Repeat("b", 40),
		BuildPolicyDigest: "sha256:" + strings.Repeat("c", 64),
		GeneratedAt:       time.Date(2026, 7, 30, 14, 0, 0, 0, time.UTC),
		Images: []ReleaseImage{{
			Key: "default", Kind: "wolf",
			Reference:   "registry.example/wolf/scanners@" + indexDigest,
			Digest:      indexDigest,
			Platforms:   map[string]string{"linux/amd64": childDigest},
			BlobDigests: digests, Required: true,
		}},
		Artifacts: []ReleaseArtifact{
			{
				Key: "provenance", Type: "slsa-provenance",
				MediaType: "application/vnd.oci.artifact.manifest.v1+json",
				Digest:    provenanceDigest, Size: int64(len(provenance)),
				BundlePath: OCIPath(provenanceDigest),
			},
			{
				Key: "sbom", Type: "spdx-sbom", MediaType: "application/spdx+json",
				Digest: sbomDigest, Size: int64(len(sbom)),
				BundlePath: OCIPath(sbomDigest),
			},
		},
		OCIRecords: records,
	}
	return manifest, sources
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	result, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
