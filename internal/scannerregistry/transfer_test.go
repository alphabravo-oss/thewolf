package scannerregistry

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/scannerbundle"
)

func TestFetchTransferClosureSelectsPlatformAndIncludesTrustGraph(t *testing.T) {
	t.Parallel()
	fixture := newTransferRegistryFixture(t)
	server := httptest.NewServer(fixture)
	t.Cleanup(server.Close)
	host := strings.TrimPrefix(server.URL, "http://")
	client := Client{
		HTTP: server.Client(),
		Endpoints: map[string]Endpoint{
			host: {BaseURL: server.URL},
		},
		MaxBlobBytes: 1 << 20,
	}
	reference := Reference{
		Registry: host, Repository: fixture.repository, Digest: fixture.rootDigest,
	}
	closure, err := client.FetchTransferClosure(
		context.Background(), reference, []string{"linux/amd64"}, t.TempDir(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if closure.SourceDigest != fixture.rootDigest ||
		closure.SourceReference != reference.String() ||
		closure.RootDigest == fixture.rootDigest {
		t.Fatalf("closure identity = %#v", closure)
	}
	if got := closure.Platforms["linux/amd64"]; got != fixture.amdManifestDigest {
		t.Fatalf("selected platform digest = %q", got)
	}
	if _, exists := closure.Platforms["linux/arm64"]; exists {
		t.Fatalf("unexpected arm64 platform in %#v", closure.Platforms)
	}
	kinds := make(map[string]string, len(closure.Blobs))
	for _, blob := range closure.Blobs {
		kinds[blob.Digest] = blob.Kind
		content, readErr := os.ReadFile(blob.Path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if digestBytes(content) != blob.Digest || int64(len(content)) != blob.Size {
			t.Fatalf("staged blob mismatch: %#v", blob)
		}
	}
	for digest, kind := range map[string]string{
		closure.RootDigest:          "oci-image-index",
		fixture.rootDigest:          "oci-source-index",
		fixture.amdManifestDigest:   "oci-image-manifest",
		fixture.configDigest:        "oci-image-blob",
		fixture.amdLayerDigest:      "oci-image-blob",
		fixture.trustManifestDigest: "oci-trust-manifest",
		fixture.attestationDigest:   "oci-image-blob",
	} {
		if kinds[digest] != kind {
			t.Fatalf("blob %s kind = %q, want %q; all=%#v", digest, kinds[digest], kind, kinds)
		}
	}
	if _, exists := kinds[fixture.armManifestDigest]; exists {
		t.Fatal("unselected arm64 manifest was included")
	}
	if _, exists := kinds[fixture.armLayerDigest]; exists {
		t.Fatal("unselected arm64 layer was included")
	}

	var (
		records []scannerbundle.OCIRecord
		sources []scannerbundle.Source
		digests []string
	)
	for _, blob := range closure.Blobs {
		record := scannerbundle.OCIRecord{
			Digest: blob.Digest, Size: blob.Size, MediaType: blob.MediaType,
			Kind: blob.Kind, BundlePath: scannerbundle.OCIPath(blob.Digest),
		}
		records = append(records, record)
		digests = append(digests, blob.Digest)
		path := blob.Path
		sources = append(sources, scannerbundle.Source{
			Path: record.BundlePath, Size: record.Size, Digest: record.Digest,
			Open: func() (io.ReadCloser, error) { return os.Open(path) },
		})
	}
	manifest := scannerbundle.ReleaseManifest{
		SchemaVersion: scannerbundle.ManifestSchema,
		ReleaseID:     "selective-transfer", LockDigest: "sha256:" + strings.Repeat("a", 64),
		DefinitionCommit:  strings.Repeat("b", 40),
		BuildPolicyDigest: "sha256:" + strings.Repeat("c", 64),
		GeneratedAt:       time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC),
		Images: []scannerbundle.ReleaseImage{{
			Key: "default", Kind: "wolf",
			Reference: host + "/" + fixture.repository + "@" + closure.RootDigest,
			Digest:    closure.RootDigest, Platforms: closure.Platforms,
			SourceReference: closure.SourceReference, SourceDigest: closure.SourceDigest,
			BlobDigests: digests, Required: true,
		}},
		OCIRecords: records,
	}
	var encoded bytes.Buffer
	if err := scannerbundle.Write(context.Background(), &encoded, scannerbundle.WriteOptions{
		Manifest: manifest, Sources: sources, SourceDateEpoch: manifest.GeneratedAt,
		SchemaVersion: scannerbundle.BundleSchemaV2,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := scannerbundle.Read(
		context.Background(), bytes.NewReader(encoded.Bytes()), t.TempDir(),
		scannerbundle.ReadOptions{AllowUnsigned: true},
	); err != nil {
		t.Fatalf("verify platform-selective bundle closure: %v", err)
	}
}

func TestPushBundleImageIsIdempotentAndReadsBackEveryManifest(t *testing.T) {
	t.Parallel()
	fixture := newTransferRegistryFixture(t)
	sourceServer := httptest.NewServer(fixture)
	t.Cleanup(sourceServer.Close)
	sourceHost := strings.TrimPrefix(sourceServer.URL, "http://")
	sourceClient := Client{
		HTTP: sourceServer.Client(),
		Endpoints: map[string]Endpoint{
			sourceHost: {BaseURL: sourceServer.URL},
		},
		MaxBlobBytes: 1 << 20,
	}
	closure, err := sourceClient.FetchTransferClosure(
		context.Background(),
		Reference{
			Registry: sourceHost, Repository: fixture.repository, Digest: fixture.rootDigest,
		},
		[]string{"linux/amd64"},
		t.TempDir(),
	)
	if err != nil {
		t.Fatal(err)
	}

	bundleRoot := t.TempDir()
	records := make(map[string]scannerbundle.OCIRecord, len(closure.Blobs))
	var digests []string
	for _, blob := range closure.Blobs {
		bundlePath := scannerbundle.OCIPath(blob.Digest)
		destination := filepath.Join(bundleRoot, filepath.FromSlash(bundlePath))
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			t.Fatal(err)
		}
		content, readErr := os.ReadFile(blob.Path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if err := os.WriteFile(destination, content, 0o600); err != nil {
			t.Fatal(err)
		}
		records[blob.Digest] = scannerbundle.OCIRecord{
			Digest: blob.Digest, Size: blob.Size, MediaType: blob.MediaType,
			Kind: blob.Kind, BundlePath: bundlePath,
		}
		digests = append(digests, blob.Digest)
	}
	sort.Strings(digests)

	destination := newWritableRegistry()
	destinationServer := httptest.NewServer(destination)
	t.Cleanup(destinationServer.Close)
	destinationHost := strings.TrimPrefix(destinationServer.URL, "http://")
	client := Client{
		HTTP: destinationServer.Client(),
		Endpoints: map[string]Endpoint{
			destinationHost: {BaseURL: destinationServer.URL},
		},
	}
	image := scannerbundle.ReleaseImage{
		Key: "default", Kind: "wolf",
		Reference:   sourceHost + "/" + fixture.repository + "@" + closure.RootDigest,
		Digest:      closure.RootDigest,
		Platforms:   closure.Platforms,
		BlobDigests: digests,
		Required:    true,
	}
	for attempt := 0; attempt < 2; attempt++ {
		result, pushErr := client.PushBundleImage(
			context.Background(), destinationHost, "private/scanners",
			image, records, bundleRoot,
		)
		if pushErr != nil {
			t.Fatalf("push attempt %d: %v", attempt+1, pushErr)
		}
		if !result.ReadBack || result.Digest != closure.RootDigest ||
			result.Reference != destinationHost+"/private/scanners@"+closure.RootDigest {
			t.Fatalf("push result = %#v", result)
		}
	}
	destination.mu.Lock()
	defer destination.mu.Unlock()
	if destination.blobPuts != destination.expectedBlobCount(records) {
		t.Fatalf("blob PUTs = %d, want %d", destination.blobPuts, destination.expectedBlobCount(records))
	}
	if destination.manifestPuts != destination.expectedManifestCount(records) {
		t.Fatalf("manifest PUTs = %d, want %d", destination.manifestPuts, destination.expectedManifestCount(records))
	}
	for digest, record := range records {
		if strings.Contains(record.Kind, "manifest") || strings.Contains(record.Kind, "index") {
			if destination.manifestGets[digest] < 2 {
				t.Fatalf("manifest %s readback count = %d", digest, destination.manifestGets[digest])
			}
		}
	}
}

func TestPushBundleArtifactIsIdempotentAndReadsBackEntireGraph(t *testing.T) {
	t.Parallel()
	config := []byte(`{"mediaType":"application/vnd.wolf.signature.config.v1+json"}`)
	payload := []byte(`{"critical":{"identity":{"docker-reference":"wolf/scanners"}}}`)
	configDigest := digestBytes(config)
	payloadDigest := digestBytes(payload)
	child := mustRegistryJSON(t, map[string]any{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.image.manifest.v1+json",
		"config": descriptorJSON(
			"application/vnd.wolf.signature.config.v1+json", configDigest, len(config),
		),
		"layers": []any{descriptorJSON(
			"application/vnd.dev.cosign.simplesigning.v1+json", payloadDigest, len(payload),
		)},
	})
	childDigest := digestBytes(child)
	root := mustRegistryJSON(t, map[string]any{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.image.index.v1+json",
		"manifests": []any{descriptorJSON(
			"application/vnd.oci.image.manifest.v1+json", childDigest, len(child),
		)},
	})
	rootDigest := digestBytes(root)
	rootDirectory := t.TempDir()
	records := make(map[string]scannerbundle.OCIRecord)
	for digest, item := range map[string]struct {
		value     []byte
		mediaType string
		kind      string
	}{
		configDigest:  {config, "application/vnd.wolf.signature.config.v1+json", "oci-blob"},
		payloadDigest: {payload, "application/vnd.dev.cosign.simplesigning.v1+json", "oci-blob"},
		childDigest:   {child, "application/vnd.oci.image.manifest.v1+json", "oci-trust-manifest"},
		rootDigest:    {root, "application/vnd.oci.image.index.v1+json", "oci-artifact-index"},
	} {
		bundlePath := scannerbundle.OCIPath(digest)
		path := filepath.Join(rootDirectory, filepath.FromSlash(bundlePath))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, item.value, 0o600); err != nil {
			t.Fatal(err)
		}
		records[digest] = scannerbundle.OCIRecord{
			Digest: digest, Size: int64(len(item.value)), MediaType: item.mediaType,
			Kind: item.kind, BundlePath: bundlePath,
		}
	}
	destination := newWritableRegistry()
	destinationServer := httptest.NewServer(destination)
	t.Cleanup(destinationServer.Close)
	destinationHost := strings.TrimPrefix(destinationServer.URL, "http://")
	client := Client{
		HTTP:      destinationServer.Client(),
		Endpoints: map[string]Endpoint{destinationHost: {BaseURL: destinationServer.URL}},
	}
	artifact := scannerbundle.ReleaseArtifact{
		Key: "image-signature-primary", Type: "image-signature",
		MediaType: "application/vnd.dev.cosign.simplesigning.v1+json",
		Digest:    payloadDigest, Size: int64(len(payload)),
		StorageDigest: rootDigest, StorageReference: "source.example/wolf/signatures@" + rootDigest,
		StorageMediaType: "application/vnd.oci.image.index.v1+json", StorageSize: int64(len(root)),
		OCIClosure: []string{configDigest, payloadDigest, childDigest, rootDigest},
	}
	for attempt := 0; attempt < 2; attempt++ {
		result, err := client.PushBundleArtifact(
			context.Background(), destinationHost, "private/scanners", artifact, records, rootDirectory,
		)
		if err != nil {
			t.Fatalf("push attempt %d: %v", attempt+1, err)
		}
		if !result.ReadBack || result.Digest != rootDigest ||
			result.Reference != destinationHost+"/private/scanners@"+rootDigest {
			t.Fatalf("push result=%+v", result)
		}
	}
	destination.mu.Lock()
	defer destination.mu.Unlock()
	if destination.blobPuts != 2 || destination.manifestPuts != 2 {
		t.Fatalf("puts blobs=%d manifests=%d", destination.blobPuts, destination.manifestPuts)
	}
	if destination.manifestGets[rootDigest] < 4 || destination.manifestGets[childDigest] < 2 {
		t.Fatalf("manifest readbacks=%+v", destination.manifestGets)
	}
}

type transferRegistryFixture struct {
	t                   *testing.T
	repository          string
	manifests           map[string][]byte
	manifestMediaTypes  map[string]string
	blobs               map[string][]byte
	referrers           map[string][]Descriptor
	rootDigest          string
	amdManifestDigest   string
	armManifestDigest   string
	configDigest        string
	amdLayerDigest      string
	armLayerDigest      string
	trustManifestDigest string
	attestationDigest   string
}

func newTransferRegistryFixture(t *testing.T) *transferRegistryFixture {
	t.Helper()
	fixture := &transferRegistryFixture{
		t: t, repository: "source/scanners",
		manifests:          make(map[string][]byte),
		manifestMediaTypes: make(map[string]string),
		blobs:              make(map[string][]byte),
		referrers:          make(map[string][]Descriptor),
	}
	config := []byte(`{"architecture":"amd64","os":"linux"}`)
	amdLayer := []byte("amd64-scanner-layer")
	armLayer := []byte("arm64-scanner-layer")
	attestation := []byte(`{"_type":"https://in-toto.io/Statement/v1","predicateType":"https://slsa.dev/provenance/v1"}`)
	fixture.configDigest = digestBytes(config)
	fixture.amdLayerDigest = digestBytes(amdLayer)
	fixture.armLayerDigest = digestBytes(armLayer)
	fixture.attestationDigest = digestBytes(attestation)
	fixture.blobs[fixture.configDigest] = config
	fixture.blobs[fixture.amdLayerDigest] = amdLayer
	fixture.blobs[fixture.armLayerDigest] = armLayer
	fixture.blobs[fixture.attestationDigest] = attestation

	amdManifest := mustRegistryJSON(t, map[string]any{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.image.manifest.v1+json",
		"config": descriptorJSON(
			"application/vnd.oci.image.config.v1+json", fixture.configDigest, len(config),
		),
		"layers": []any{descriptorJSON(
			"application/vnd.oci.image.layer.v1.tar+gzip", fixture.amdLayerDigest, len(amdLayer),
		)},
	})
	armManifest := mustRegistryJSON(t, map[string]any{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.image.manifest.v1+json",
		"config": descriptorJSON(
			"application/vnd.oci.image.config.v1+json", fixture.configDigest, len(config),
		),
		"layers": []any{descriptorJSON(
			"application/vnd.oci.image.layer.v1.tar+gzip", fixture.armLayerDigest, len(armLayer),
		)},
	})
	fixture.amdManifestDigest = digestBytes(amdManifest)
	fixture.armManifestDigest = digestBytes(armManifest)
	fixture.manifests[fixture.amdManifestDigest] = amdManifest
	fixture.manifests[fixture.armManifestDigest] = armManifest
	fixture.manifestMediaTypes[fixture.amdManifestDigest] = "application/vnd.oci.image.manifest.v1+json"
	fixture.manifestMediaTypes[fixture.armManifestDigest] = "application/vnd.oci.image.manifest.v1+json"

	root := mustRegistryJSON(t, map[string]any{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.image.index.v1+json",
		"manifests": []any{
			map[string]any{
				"mediaType": "application/vnd.oci.image.manifest.v1+json",
				"digest":    fixture.amdManifestDigest, "size": len(amdManifest),
				"platform": map[string]string{"os": "linux", "architecture": "amd64"},
			},
			map[string]any{
				"mediaType": "application/vnd.oci.image.manifest.v1+json",
				"digest":    fixture.armManifestDigest, "size": len(armManifest),
				"platform": map[string]string{"os": "linux", "architecture": "arm64"},
			},
		},
	})
	fixture.rootDigest = digestBytes(root)
	fixture.manifests[fixture.rootDigest] = root
	fixture.manifestMediaTypes[fixture.rootDigest] = "application/vnd.oci.image.index.v1+json"

	trustManifest := mustRegistryJSON(t, map[string]any{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.image.manifest.v1+json",
		"artifactType":  "application/vnd.in-toto+json",
		"subject": descriptorJSON(
			"application/vnd.oci.image.index.v1+json", fixture.rootDigest, len(root),
		),
		"layers": []any{descriptorJSON(
			"application/vnd.in-toto+json", fixture.attestationDigest, len(attestation),
		)},
	})
	fixture.trustManifestDigest = digestBytes(trustManifest)
	fixture.manifests[fixture.trustManifestDigest] = trustManifest
	fixture.manifestMediaTypes[fixture.trustManifestDigest] = "application/vnd.oci.image.manifest.v1+json"
	fixture.referrers[fixture.rootDigest] = []Descriptor{{
		MediaType: "application/vnd.oci.image.manifest.v1+json",
		Digest:    fixture.trustManifestDigest, Size: int64(len(trustManifest)),
		ArtifactType: "application/vnd.in-toto+json",
	}}
	return fixture
}

func (f *transferRegistryFixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	manifestPrefix := "/v2/" + f.repository + "/manifests/"
	blobPrefix := "/v2/" + f.repository + "/blobs/"
	referrersPrefix := "/v2/" + f.repository + "/referrers/"
	switch {
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, manifestPrefix):
		digest := strings.TrimPrefix(r.URL.Path, manifestPrefix)
		value, exists := f.manifests[digest]
		if !exists {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", f.manifestMediaTypes[digest])
		w.Header().Set("Docker-Content-Digest", digest)
		_, _ = w.Write(value)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, blobPrefix):
		digest := strings.TrimPrefix(r.URL.Path, blobPrefix)
		value, exists := f.blobs[digest]
		if !exists {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Length", stringInt(len(value)))
		_, _ = w.Write(value)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, referrersPrefix):
		subject := strings.TrimPrefix(r.URL.Path, referrersPrefix)
		value := mustRegistryJSON(f.t, map[string]any{
			"schemaVersion": 2,
			"mediaType":     "application/vnd.oci.image.index.v1+json",
			"manifests":     f.referrers[subject],
		})
		w.Header().Set("Content-Type", "application/vnd.oci.image.index.v1+json")
		_, _ = w.Write(value)
	default:
		http.NotFound(w, r)
	}
}

type writableRegistry struct {
	mu           sync.Mutex
	blobs        map[string][]byte
	manifests    map[string][]byte
	mediaTypes   map[string]string
	manifestGets map[string]int
	blobPuts     int
	manifestPuts int
	nextUploadID int
}

func newWritableRegistry() *writableRegistry {
	return &writableRegistry{
		blobs: make(map[string][]byte), manifests: make(map[string][]byte),
		mediaTypes: make(map[string]string), manifestGets: make(map[string]int),
	}
}

func (r *writableRegistry) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	const repository = "private/scanners"
	blobPrefix := "/v2/" + repository + "/blobs/"
	manifestPrefix := "/v2/" + repository + "/manifests/"
	switch {
	case request.Method == http.MethodHead && strings.HasPrefix(request.URL.Path, blobPrefix):
		if _, exists := r.blobs[strings.TrimPrefix(request.URL.Path, blobPrefix)]; !exists {
			http.NotFound(w, request)
			return
		}
		w.WriteHeader(http.StatusOK)
	case request.Method == http.MethodPost &&
		request.URL.Path == "/v2/"+repository+"/blobs/uploads/":
		r.nextUploadID++
		w.Header().Set("Location", "/uploads/"+stringInt(r.nextUploadID))
		w.WriteHeader(http.StatusAccepted)
	case request.Method == http.MethodPut && strings.HasPrefix(request.URL.Path, "/uploads/"):
		value, err := io.ReadAll(request.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		digest := request.URL.Query().Get("digest")
		if digestBytes(value) != digest {
			http.Error(w, "digest mismatch", http.StatusBadRequest)
			return
		}
		r.blobs[digest] = value
		r.blobPuts++
		w.Header().Set("Docker-Content-Digest", digest)
		w.WriteHeader(http.StatusCreated)
	case request.Method == http.MethodHead && strings.HasPrefix(request.URL.Path, manifestPrefix):
		digest := strings.TrimPrefix(request.URL.Path, manifestPrefix)
		if _, exists := r.manifests[digest]; !exists {
			http.NotFound(w, request)
			return
		}
		w.Header().Set("Docker-Content-Digest", digest)
		w.WriteHeader(http.StatusOK)
	case request.Method == http.MethodPut && strings.HasPrefix(request.URL.Path, manifestPrefix):
		value, err := io.ReadAll(request.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		digest := strings.TrimPrefix(request.URL.Path, manifestPrefix)
		if digestBytes(value) != digest {
			http.Error(w, "digest mismatch", http.StatusBadRequest)
			return
		}
		r.manifests[digest] = value
		r.mediaTypes[digest] = request.Header.Get("Content-Type")
		r.manifestPuts++
		w.Header().Set("Docker-Content-Digest", digest)
		w.WriteHeader(http.StatusCreated)
	case request.Method == http.MethodGet && strings.HasPrefix(request.URL.Path, manifestPrefix):
		digest := strings.TrimPrefix(request.URL.Path, manifestPrefix)
		value, exists := r.manifests[digest]
		if !exists {
			http.NotFound(w, request)
			return
		}
		r.manifestGets[digest]++
		w.Header().Set("Content-Type", r.mediaTypes[digest])
		w.Header().Set("Docker-Content-Digest", digest)
		_, _ = w.Write(value)
	default:
		http.NotFound(w, request)
	}
}

func (r *writableRegistry) expectedBlobCount(records map[string]scannerbundle.OCIRecord) int {
	result := 0
	for _, record := range records {
		if !strings.Contains(record.Kind, "manifest") &&
			!strings.Contains(record.Kind, "index") {
			result++
		}
	}
	return result
}

func (r *writableRegistry) expectedManifestCount(records map[string]scannerbundle.OCIRecord) int {
	return len(records) - r.expectedBlobCount(records)
}

func mustRegistryJSON(t *testing.T, value any) []byte {
	t.Helper()
	result, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func descriptorJSON(mediaType, digest string, size int) map[string]any {
	return map[string]any{
		"mediaType": mediaType,
		"digest":    digest,
		"size":      size,
	}
}

func stringInt(value int) string {
	return strconv.Itoa(value)
}
