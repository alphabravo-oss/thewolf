package scannerbundle

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

func TestBundleSignedRoundTripIsDeterministic(t *testing.T) {
	t.Parallel()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("deterministic OCI layout bytes")
	manifest := testManifest(payload)
	source := sourceFor("artifacts/scanners-default.oci.tar", payload)
	opts := WriteOptions{
		Manifest: manifest,
		Sources:  []Source{source},
		Signer: Ed25519Signer{
			KeyID:      "offline-root-2026",
			PrivateKey: privateKey,
		},
		SourceDateEpoch: manifest.GeneratedAt,
	}

	var first, second bytes.Buffer
	if err := Write(context.Background(), &first, opts); err != nil {
		t.Fatalf("Write first bundle: %v", err)
	}
	if err := Write(context.Background(), &second, opts); err != nil {
		t.Fatalf("Write second bundle: %v", err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatal("identical inputs produced different bundle bytes")
	}

	destination := t.TempDir()
	imported, err := Read(
		context.Background(),
		bytes.NewReader(first.Bytes()),
		destination,
		ReadOptions{Verifier: Ed25519TrustStore{"offline-root-2026": publicKey}},
	)
	if err != nil {
		t.Fatalf("Read signed bundle: %v", err)
	}
	if imported.Manifest.ReleaseID != manifest.ReleaseID {
		t.Fatalf("release ID = %q, want %q", imported.Manifest.ReleaseID, manifest.ReleaseID)
	}
	if imported.Signature == nil || imported.Signature.KeyID != "offline-root-2026" {
		t.Fatalf("signature = %#v", imported.Signature)
	}
	gotPayload, err := os.ReadFile(destination + "/artifacts/scanners-default.oci.tar")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotPayload, payload) {
		t.Fatal("imported artifact differs from exported artifact")
	}
}

func TestBundleRequiresSignatureByDefault(t *testing.T) {
	t.Parallel()
	payload := []byte("unsigned")
	var bundle bytes.Buffer
	if err := Write(context.Background(), &bundle, WriteOptions{
		Manifest: testManifest(payload),
		Sources:  []Source{sourceFor("artifacts/scanners-default.oci.tar", payload)},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(context.Background(), bytes.NewReader(bundle.Bytes()), t.TempDir(), ReadOptions{}); err == nil ||
		!strings.Contains(err.Error(), "signed release bundle is required") {
		t.Fatalf("Read unsigned error = %v", err)
	}

	imported, err := Read(
		context.Background(),
		bytes.NewReader(bundle.Bytes()),
		t.TempDir(),
		ReadOptions{AllowUnsigned: true},
	)
	if err != nil {
		t.Fatalf("explicit unsigned import: %v", err)
	}
	if imported.Signature != nil {
		t.Fatal("unsigned import unexpectedly has a signature")
	}
}

func TestBundleRejectsUntrustedSignature(t *testing.T) {
	t.Parallel()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	otherPublic, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("signed")
	var bundle bytes.Buffer
	if err := Write(context.Background(), &bundle, WriteOptions{
		Manifest: testManifest(payload),
		Sources:  []Source{sourceFor("artifacts/scanners-default.oci.tar", payload)},
		Signer:   Ed25519Signer{KeyID: "expected", PrivateKey: privateKey},
	}); err != nil {
		t.Fatal(err)
	}
	_, err = Read(
		context.Background(),
		bytes.NewReader(bundle.Bytes()),
		t.TempDir(),
		ReadOptions{Verifier: Ed25519TrustStore{"expected": otherPublic}},
	)
	if err == nil || !strings.Contains(err.Error(), "signature is invalid") {
		t.Fatalf("Read untrusted error = %v", err)
	}
}

func TestBundleWriterRejectsArtifactMismatchAndExtraSource(t *testing.T) {
	t.Parallel()
	payload := []byte("expected")
	manifest := testManifest(payload)
	bad := sourceFor("artifacts/scanners-default.oci.tar", []byte("different"))
	var output bytes.Buffer
	if err := Write(context.Background(), &output, WriteOptions{
		Manifest: manifest,
		Sources:  []Source{bad},
	}); err == nil || !strings.Contains(err.Error(), "does not match release artifact") {
		t.Fatalf("artifact mismatch error = %v", err)
	}

	extra := sourceFor("artifacts/extra", []byte("extra"))
	if err := Write(context.Background(), &output, WriteOptions{
		Manifest: manifest,
		Sources: []Source{
			sourceFor("artifacts/scanners-default.oci.tar", payload),
			extra,
		},
	}); err == nil || !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("extra source error = %v", err)
	}
}

func TestBundleReaderRejectsUnsafeArchiveEntries(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		entries []rawEntry
		want    string
	}{
		{
			name: "traversal",
			entries: []rawEntry{
				{name: "../escape", value: []byte("bad"), typeflag: tar.TypeReg},
			},
			want: "unsafe bundle entry",
		},
		{
			name: "absolute",
			entries: []rawEntry{
				{name: "/escape", value: []byte("bad"), typeflag: tar.TypeReg},
			},
			want: "unsafe bundle entry",
		},
		{
			name: "symlink",
			entries: []rawEntry{
				{name: "escape", typeflag: tar.TypeSymlink, linkname: "../../outside"},
			},
			want: "forbidden type",
		},
		{
			name: "duplicate",
			entries: []rawEntry{
				{name: "same", value: []byte("one"), typeflag: tar.TypeReg},
				{name: "same", value: []byte("two"), typeflag: tar.TypeReg},
			},
			want: "duplicate bundle entry",
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			bundle := rawBundle(t, tt.entries)
			if _, err := Read(
				context.Background(),
				bytes.NewReader(bundle),
				t.TempDir(),
				ReadOptions{AllowUnsigned: true},
			); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Read error = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestBundleReaderEnforcesLimitsBeforeExtraction(t *testing.T) {
	t.Parallel()
	bundle := rawBundle(t, []rawEntry{
		{name: "large", value: []byte("0123456789"), typeflag: tar.TypeReg},
	})
	if _, err := Read(
		context.Background(),
		bytes.NewReader(bundle),
		t.TempDir(),
		ReadOptions{AllowUnsigned: true, MaxFileBytes: 5},
	); err == nil || !strings.Contains(err.Error(), "exceeds maximum size") {
		t.Fatalf("Read oversized error = %v", err)
	}
}

func TestBundleReaderRejectsCorruptAndTruncatedStreams(t *testing.T) {
	t.Parallel()
	payload := []byte("complete payload")
	var output bytes.Buffer
	if err := Write(context.Background(), &output, WriteOptions{
		Manifest: testManifest(payload),
		Sources: []Source{
			sourceFor("artifacts/scanners-default.oci.tar", payload),
		},
	}); err != nil {
		t.Fatal(err)
	}
	complete := output.Bytes()
	truncated := append([]byte(nil), complete[:len(complete)/2]...)
	corrupt := append([]byte(nil), complete...)
	corrupt[len(corrupt)/2] ^= 0xff
	for name, input := range map[string][]byte{
		"truncated": truncated,
		"corrupt":   corrupt,
	} {
		name, input := name, input
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := Read(
				context.Background(), bytes.NewReader(input), t.TempDir(),
				ReadOptions{AllowUnsigned: true},
			); err == nil {
				t.Fatalf("Read accepted %s bundle stream", name)
			}
		})
	}
}

func TestBundleReaderRejectsIndexDigestMismatch(t *testing.T) {
	t.Parallel()
	payload := []byte("artifact")
	manifest := testManifest(payload)
	manifestBytes, err := manifest.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	records := []FileRecord{
		{Path: ManifestPath, Size: int64(len(manifestBytes)), Digest: digestBytes(manifestBytes)},
		{
			Path:   "artifacts/scanners-default.oci.tar",
			Size:   int64(len(payload)),
			Digest: "sha256:" + strings.Repeat("0", 64),
		},
	}
	indexBytes, err := json.Marshal(Index{
		SchemaVersion:  BundleSchema,
		ReleaseID:      manifest.ReleaseID,
		ManifestDigest: digestBytes(manifestBytes),
		CreatedAt:      manifest.GeneratedAt,
		Files:          records,
	})
	if err != nil {
		t.Fatal(err)
	}
	bundle := rawBundle(t, []rawEntry{
		{name: ManifestPath, value: manifestBytes, typeflag: tar.TypeReg},
		{name: "artifacts/scanners-default.oci.tar", value: payload, typeflag: tar.TypeReg},
		{name: IndexPath, value: indexBytes, typeflag: tar.TypeReg},
	})
	if _, err := Read(
		context.Background(),
		bytes.NewReader(bundle),
		t.TempDir(),
		ReadOptions{AllowUnsigned: true},
	); err == nil || !strings.Contains(err.Error(), "does not match its index record") {
		t.Fatalf("Read mismatch error = %v", err)
	}
}

func TestManifestValidationRejectsMutableOrIncompleteIdentity(t *testing.T) {
	t.Parallel()
	manifest := testManifest([]byte("payload"))
	manifest.Images[0].Digest = "latest"
	if err := manifest.Validate(); err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("Validate mutable digest error = %v", err)
	}
	manifest = testManifest([]byte("payload"))
	manifest.Images[0].Platforms = nil
	if err := manifest.Validate(); err == nil || !strings.Contains(err.Error(), "platforms") {
		t.Fatalf("Validate missing platform error = %v", err)
	}
}

func TestManifestAcceptsFixerSupplyChainImagesWithoutScannerOwnership(t *testing.T) {
	t.Parallel()
	manifest := testManifest([]byte("payload"))
	manifest.Images = append(manifest.Images, ReleaseImage{
		Key:       "fixer-codex",
		Kind:      "fixer",
		Reference: "ghcr.io/alphabravocompany/wolf-fixer-codex@sha256:" + strings.Repeat("f", 64),
		Digest:    "sha256:" + strings.Repeat("f", 64),
		Platforms: map[string]string{
			"linux/amd64": "sha256:" + strings.Repeat("1", 64),
			"linux/arm64": "sha256:" + strings.Repeat("2", 64),
		},
		Required: true,
	})
	if err := manifest.Validate(); err != nil {
		t.Fatalf("Validate release with fixer: %v", err)
	}
	manifest.Images[1].Tools = []string{"semgrep"}
	if err := manifest.Validate(); err == nil || !strings.Contains(err.Error(), "cannot own scanner tools") {
		t.Fatalf("fixer scanner ownership error = %v", err)
	}
}

func testManifest(payload []byte) ReleaseManifest {
	artifactPath := "artifacts/scanners-default.oci.tar"
	return ReleaseManifest{
		SchemaVersion:     ManifestSchema,
		ReleaseID:         "scanner-set-2026.31.1",
		LockDigest:        "sha256:" + strings.Repeat("a", 64),
		DefinitionCommit:  strings.Repeat("b", 40),
		BuildPolicyDigest: "sha256:" + strings.Repeat("c", 64),
		GeneratedAt:       time.Date(2026, 7, 30, 14, 0, 0, 0, time.UTC),
		Images: []ReleaseImage{
			{
				Key:       "default",
				Kind:      "wolf",
				Reference: "ghcr.io/alphabravocompany/wolf-scanners@sha256:" + strings.Repeat("d", 64),
				Digest:    "sha256:" + strings.Repeat("d", 64),
				Platforms: map[string]string{
					"linux/amd64": "sha256:" + strings.Repeat("e", 64),
				},
				Required: true,
			},
		},
		Artifacts: []ReleaseArtifact{
			{
				Key:        "default-oci-layout",
				Type:       "oci-layout",
				MediaType:  "application/vnd.oci.image.layout.v1+tar",
				Digest:     digestBytes(payload),
				Size:       int64(len(payload)),
				BundlePath: artifactPath,
			},
		},
	}
}

func TestBundleAllowsMultipleArtifactIdentitiesToShareExactPayload(t *testing.T) {
	payload := []byte("shared-content-addressed-evidence")
	manifest := testManifest(payload)
	shared := manifest.Artifacts[0]
	shared.Key = "default-oci-layout-copy"
	shared.Type = "oci-layout-copy"
	manifest.Artifacts = append(manifest.Artifacts, shared)
	var bundle bytes.Buffer
	if err := Write(context.Background(), &bundle, WriteOptions{
		Manifest:        manifest,
		Sources:         []Source{sourceFor(manifest.Artifacts[0].BundlePath, payload)},
		SourceDateEpoch: manifest.GeneratedAt,
	}); err != nil {
		t.Fatalf("write shared artifact payload: %v", err)
	}
	if _, err := Read(
		context.Background(), bytes.NewReader(bundle.Bytes()), t.TempDir(),
		ReadOptions{AllowUnsigned: true},
	); err != nil {
		t.Fatalf("read shared artifact payload: %v", err)
	}
	manifest.Artifacts[1].Size++
	if err := manifest.Validate(); err == nil || !strings.Contains(err.Error(), "conflicting content identity") {
		t.Fatalf("conflicting shared artifact error=%v", err)
	}
}

func sourceFor(name string, value []byte) Source {
	return Source{
		Path:   name,
		Size:   int64(len(value)),
		Digest: digestBytes(value),
		Open: func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(value)), nil
		},
	}
}

type rawEntry struct {
	name     string
	value    []byte
	typeflag byte
	linkname string
}

func rawBundle(t *testing.T, entries []rawEntry) []byte {
	t.Helper()
	var output bytes.Buffer
	encoder, err := zstd.NewWriter(&output, zstd.WithEncoderConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(encoder)
	for _, entry := range entries {
		size := int64(len(entry.value))
		if entry.typeflag == tar.TypeSymlink {
			size = 0
		}
		if err := tw.WriteHeader(&tar.Header{
			Name:     entry.name,
			Typeflag: entry.typeflag,
			Linkname: entry.linkname,
			Mode:     0o644,
			Size:     size,
		}); err != nil {
			t.Fatal(err)
		}
		if size > 0 {
			if _, err := tw.Write(entry.value); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := encoder.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
