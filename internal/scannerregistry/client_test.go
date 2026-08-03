package scannerregistry

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/scannerbundle"
)

func TestParseReferenceRequiresRegistryRepositoryAndDigest(t *testing.T) {
	t.Parallel()
	valid := "registry.example:5000/security/wolf-scanners@sha256:" + strings.Repeat("a", 64)
	reference, err := ParseReference(valid)
	if err != nil {
		t.Fatal(err)
	}
	if reference.Registry != "registry.example:5000" ||
		reference.Repository != "security/wolf-scanners" ||
		reference.String() != valid {
		t.Fatalf("reference = %#v", reference)
	}
	for _, invalid := range []string{
		"https://registry.example/repo@sha256:" + strings.Repeat("a", 64),
		"registry.example/repo:latest",
		"registry.example/../repo@sha256:" + strings.Repeat("a", 64),
		"registry.example/Repo@sha256:" + strings.Repeat("a", 64),
		"registry.example/repo@sha512:" + strings.Repeat("a", 64),
	} {
		if _, err := ParseReference(invalid); err == nil {
			t.Errorf("ParseReference(%q) unexpectedly succeeded", invalid)
		}
	}
}

func TestClientVerifiesReleaseManifestAndPlatformDigests(t *testing.T) {
	t.Parallel()
	amdDigest := "sha256:" + strings.Repeat("a", 64)
	armDigest := "sha256:" + strings.Repeat("b", 64)
	indexBytes, err := json.Marshal(map[string]any{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.image.index.v1+json",
		"manifests": []map[string]any{
			{
				"mediaType": "application/vnd.oci.image.manifest.v1+json",
				"digest":    amdDigest, "size": 100,
				"platform": map[string]string{"os": "linux", "architecture": "amd64"},
			},
			{
				"mediaType": "application/vnd.oci.image.manifest.v1+json",
				"digest":    armDigest, "size": 100,
				"platform": map[string]string{"os": "linux", "architecture": "arm64"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	indexDigest := digestBytes(indexBytes)
	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		switch r.URL.Path {
		case "/v2/":
			w.WriteHeader(http.StatusOK)
		case "/v2/security/wolf-scanners/manifests/" + indexDigest:
			w.Header().Set("Content-Type", "application/vnd.oci.image.index.v1+json")
			w.Header().Set("Docker-Content-Digest", indexDigest)
			_, _ = w.Write(indexBytes)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	host := strings.TrimPrefix(server.URL, "http://")
	client := Client{
		HTTP:      server.Client(),
		Endpoints: map[string]Endpoint{host: {BaseURL: server.URL}},
		Credentials: CredentialProviderFunc(func(context.Context, string) (string, error) {
			return "Bearer secret-token", nil
		}),
	}
	if err := client.Check(context.Background(), host); err != nil {
		t.Fatal(err)
	}
	release := testRelease(host, indexDigest, map[string]string{
		"linux/amd64": amdDigest,
		"linux/arm64": armDigest,
	})
	results, err := client.VerifyRelease(context.Background(), release)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || !results[0].Verified || len(results[0].Platforms) != 2 {
		t.Fatalf("verification = %#v", results)
	}
	if authorization != "Bearer secret-token" {
		t.Fatalf("authorization = %q", authorization)
	}
}

func TestClientRejectsRegistryDigestAndPlatformMismatch(t *testing.T) {
	t.Parallel()
	content := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[]}`)
	digest := digestBytes(content)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Docker-Content-Digest", "sha256:"+strings.Repeat("f", 64))
		_, _ = w.Write(content)
	}))
	defer server.Close()
	host := strings.TrimPrefix(server.URL, "http://")
	client := Client{HTTP: server.Client(), Endpoints: map[string]Endpoint{host: {BaseURL: server.URL}}}
	reference, err := ParseReference(host + "/security/wolf-scanners@" + digest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.FetchManifest(context.Background(), reference); err == nil ||
		!strings.Contains(err.Error(), "advertised digest") {
		t.Fatalf("FetchManifest digest error = %v", err)
	}

	if _, err := (Client{}).FetchManifest(context.Background(), reference); err == nil ||
		!strings.Contains(err.Error(), "not configured") {
		t.Fatalf("unconfigured registry error = %v", err)
	}
}

func TestClientEnforcesManifestSizeLimit(t *testing.T) {
	t.Parallel()
	content := []byte(`{"schemaVersion":2}`)
	digest := digestBytes(content)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(content)
	}))
	defer server.Close()
	host := strings.TrimPrefix(server.URL, "http://")
	reference, err := ParseReference(host + "/repo@" + digest)
	if err != nil {
		t.Fatal(err)
	}
	client := Client{
		HTTP: server.Client(), Endpoints: map[string]Endpoint{host: {BaseURL: server.URL}},
		MaxManifestBytes: 5,
	}
	if _, err := client.FetchManifest(context.Background(), reference); err == nil ||
		!strings.Contains(err.Error(), "exceeds maximum size") {
		t.Fatalf("FetchManifest size error = %v", err)
	}
}

func TestEnsureManifestAliasCreatesReplaysAndRejectsConflicts(t *testing.T) {
	t.Parallel()
	content := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[]}`)
	digest := digestBytes(content)
	conflicting := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[{"digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]}`)
	operation := "wolf-operation-" + strings.Repeat("b", 64)
	var (
		mu         sync.Mutex
		aliasValue []byte
		puts       int
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/manifests/"+digest):
			w.Header().Set("Content-Type", "application/vnd.oci.image.index.v1+json")
			w.Header().Set("Docker-Content-Digest", digest)
			_, _ = w.Write(content)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/manifests/"+operation):
			if aliasValue == nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/vnd.oci.image.index.v1+json")
			w.Header().Set("Docker-Content-Digest", digestBytes(aliasValue))
			_, _ = w.Write(aliasValue)
		case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/manifests/"+operation):
			puts++
			aliasValue, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusCreated)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	host := strings.TrimPrefix(server.URL, "http://")
	client := Client{HTTP: server.Client(), Endpoints: map[string]Endpoint{host: {BaseURL: server.URL}}}
	reference := Reference{Registry: host, Repository: "wolf/scanners", Digest: digest}
	if err := client.EnsureManifestAlias(context.Background(), reference, operation); err != nil {
		t.Fatal(err)
	}
	if err := client.EnsureManifestAlias(context.Background(), reference, operation); err != nil {
		t.Fatalf("idempotent alias replay: %v", err)
	}
	if puts != 1 {
		t.Fatalf("operation alias PUT count = %d, want 1", puts)
	}
	mu.Lock()
	aliasValue = conflicting
	mu.Unlock()
	if err := client.EnsureManifestAlias(context.Background(), reference, operation); err == nil ||
		!strings.Contains(err.Error(), "already names") {
		t.Fatalf("conflicting operation alias error = %v", err)
	}
	if err := client.EnsureManifestAlias(context.Background(), reference, "latest"); err == nil {
		t.Fatal("mutable non-operation alias was accepted")
	}
}

func TestClientExchangesOCIChallengeWithoutLeakingCredential(t *testing.T) {
	t.Parallel()

	var host string
	var tokenAuthorization string
	var registryAuthorizations []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/":
			registryAuthorizations = append(registryAuthorizations, r.Header.Get("Authorization"))
			if r.Header.Get("Authorization") != "Bearer registry-token" {
				w.Header().Set(
					"WWW-Authenticate",
					`Bearer realm="http://`+host+`/token",service="test-registry",scope="registry:catalog:*"`,
				)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.WriteHeader(http.StatusOK)
		case "/token":
			tokenAuthorization = r.Header.Get("Authorization")
			if r.URL.Query().Get("service") != "test-registry" ||
				r.URL.Query().Get("scope") != "registry:catalog:*" {
				t.Errorf("token query = %q", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"token":"registry-token"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	host = strings.TrimPrefix(server.URL, "http://")
	client := Client{
		HTTP:      server.Client(),
		Endpoints: map[string]Endpoint{host: {BaseURL: server.URL}},
		Credentials: CredentialProviderFunc(func(context.Context, string) (string, error) {
			return "Basic base64-credential", nil
		}),
	}
	if err := client.Check(context.Background(), host); err != nil {
		t.Fatal(err)
	}
	if tokenAuthorization != "Basic base64-credential" {
		t.Fatalf("token authorization = %q", tokenAuthorization)
	}
	if len(registryAuthorizations) != 2 ||
		registryAuthorizations[0] != "Basic base64-credential" ||
		registryAuthorizations[1] != "Bearer registry-token" {
		t.Fatalf("registry authorizations = %#v", registryAuthorizations)
	}
}

func TestClientRejectsUnapprovedBearerTokenHost(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(
			"WWW-Authenticate",
			`Bearer realm="https://credentials.attacker.invalid/token",service="registry"`,
		)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	host := strings.TrimPrefix(server.URL, "http://")
	client := Client{
		HTTP:      server.Client(),
		Endpoints: map[string]Endpoint{host: {BaseURL: server.URL}},
		Credentials: CredentialProviderFunc(func(context.Context, string) (string, error) {
			return "Basic must-not-leak", nil
		}),
	}
	err := client.Check(context.Background(), host)
	if err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("challenge error = %v", err)
	}
}

func TestParseBearerChallengeRejectsMalformedAndDuplicateParameters(t *testing.T) {
	t.Parallel()

	for _, value := range []string{
		`Basic realm="registry"`,
		`Bearer service="registry"`,
		`Bearer realm=https://registry.invalid/token`,
		`Bearer realm="https://registry.invalid/token",realm="https://other.invalid/token"`,
	} {
		if _, err := parseBearerChallenge(value); err == nil {
			t.Errorf("parseBearerChallenge(%q) unexpectedly succeeded", value)
		}
	}
}

func TestCopyManifestGraphResumesAfterInterruptedPublish(t *testing.T) {
	t.Parallel()
	config := []byte(`{"architecture":"amd64","os":"linux"}`)
	layer := []byte("scanner-layer")
	configDigest, layerDigest := digestBytes(config), digestBytes(layer)
	manifest, err := json.Marshal(map[string]any{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.image.manifest.v1+json",
		"config": map[string]any{
			"mediaType": "application/vnd.oci.image.config.v1+json",
			"digest":    configDigest, "size": len(config),
		},
		"layers": []map[string]any{{
			"mediaType": "application/vnd.oci.image.layer.v1.tar",
			"digest":    layerDigest, "size": len(layer),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	manifestDigest := digestBytes(manifest)
	source := newFakeOCIRegistry(t, false)
	source.manifests[manifestDigest] = manifest
	source.blobs[configDigest] = config
	source.blobs[layerDigest] = layer
	destination := newFakeOCIRegistry(t, true)
	client := Client{
		HTTP: http.DefaultClient,
		Endpoints: map[string]Endpoint{
			source.host:      {BaseURL: source.server.URL},
			destination.host: {BaseURL: destination.server.URL},
		},
		MaxBlobBytes: 1 << 20,
	}
	sourceReference := Reference{Registry: source.host, Repository: "wolf/scanners", Digest: manifestDigest}
	destinationReference := Reference{Registry: destination.host, Repository: "wolf/scanners", Digest: manifestDigest}
	if err := client.CopyManifestGraph(context.Background(), sourceReference, destinationReference); err == nil {
		t.Fatal("first copy unexpectedly survived injected manifest publish interruption")
	}
	if err := client.CopyManifestGraph(context.Background(), sourceReference, destinationReference); err != nil {
		t.Fatalf("resumed copy: %v", err)
	}
	destination.mu.Lock()
	defer destination.mu.Unlock()
	if string(destination.manifests[manifestDigest]) != string(manifest) {
		t.Fatal("destination manifest did not match source")
	}
	if destination.blobUploads[configDigest] != 1 || destination.blobUploads[layerDigest] != 1 {
		t.Fatalf("content-addressed blobs were uploaded again after restart: %#v", destination.blobUploads)
	}
}

func TestCopyManifestGraphCopiesAndReadsBackReferrerEvidenceClosure(t *testing.T) {
	t.Parallel()
	rootConfig := []byte(`{"architecture":"amd64","os":"linux"}`)
	rootConfigDigest := digestBytes(rootConfig)
	root, _ := json.Marshal(map[string]any{
		"schemaVersion": 2, "mediaType": "application/vnd.oci.image.manifest.v1+json",
		"config": map[string]any{
			"mediaType": "application/vnd.oci.image.config.v1+json",
			"digest":    rootConfigDigest, "size": len(rootConfig),
		},
		"layers": []any{},
	})
	rootDigest := digestBytes(root)
	evidencePayload := []byte("exact-sbom-evidence")
	evidenceDigest := digestBytes(evidencePayload)
	referrer, _ := json.Marshal(map[string]any{
		"schemaVersion": 2, "mediaType": "application/vnd.oci.image.manifest.v1+json",
		"artifactType": "application/spdx+json",
		"subject": map[string]any{
			"mediaType": "application/vnd.oci.image.manifest.v1+json",
			"digest":    rootDigest, "size": len(root),
		},
		"layers": []map[string]any{{
			"mediaType": "application/spdx+json", "digest": evidenceDigest,
			"size": len(evidencePayload),
		}},
	})
	referrerDigest := digestBytes(referrer)
	source := newFakeOCIRegistry(t, false)
	source.manifests[rootDigest] = root
	source.manifests[referrerDigest] = referrer
	source.blobs[rootConfigDigest] = rootConfig
	source.blobs[evidenceDigest] = evidencePayload
	source.referrers[rootDigest] = []Descriptor{{
		MediaType: "application/vnd.oci.image.manifest.v1+json",
		Digest:    referrerDigest, Size: int64(len(referrer)), ArtifactType: "application/spdx+json",
	}}
	destination := newFakeOCIRegistry(t, false)
	client := Client{
		HTTP: http.DefaultClient,
		Endpoints: map[string]Endpoint{
			source.host: {BaseURL: source.server.URL}, destination.host: {BaseURL: destination.server.URL},
		},
		MaxBlobBytes: 1 << 20,
	}
	sourceReference := Reference{Registry: source.host, Repository: "wolf/scanners", Digest: rootDigest}
	destinationReference := Reference{Registry: destination.host, Repository: "wolf/scanners", Digest: rootDigest}
	if err := client.CopyManifestGraph(context.Background(), sourceReference, destinationReference); err != nil {
		t.Fatal(err)
	}
	status, err := client.ReadEvidence(
		context.Background(), destinationReference,
		map[string]string{"sbom_payload": evidenceDigest, "sbom_manifest": referrerDigest},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !status["sbom_payload"] || !status["sbom_manifest"] {
		t.Fatalf("mirrored referrer evidence = %#v", status)
	}
	destination.mu.Lock()
	delete(destination.blobs, evidenceDigest)
	destination.mu.Unlock()
	// Referrer readback proves descriptor identity. A second graph copy must
	// also restore and verify the exact payload closure after destination drift.
	if err := client.CopyManifestGraph(context.Background(), sourceReference, destinationReference); err != nil {
		t.Fatal(err)
	}
	destination.mu.Lock()
	_, restored := destination.blobs[evidenceDigest]
	destination.mu.Unlock()
	if !restored {
		t.Fatal("referrer payload drift was not repaired")
	}
}

func TestReadEvidenceReportsExactDigestDrift(t *testing.T) {
	t.Parallel()
	subject := "sha256:" + strings.Repeat("a", 64)
	signatureManifest := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","layers":[]}`)
	provenanceManifest := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","artifactType":"application/vnd.in-toto+json","layers":[]}`)
	signature := digestBytes(signatureManifest)
	provenance := digestBytes(provenanceManifest)
	sbom := "sha256:" + strings.Repeat("d", 64)
	index, _ := json.Marshal(map[string]any{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.image.index.v1+json",
		"manifests": []map[string]any{
			{"mediaType": "application/vnd.dev.cosign.simplesigning.v1+json", "digest": signature, "size": 1},
			{"mediaType": "application/vnd.in-toto+json", "digest": provenance, "size": 1},
		},
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/wolf/scanners/referrers/" + subject:
			w.Header().Set("Content-Type", "application/vnd.oci.image.index.v1+json")
			_, _ = w.Write(index)
		case "/v2/wolf/scanners/manifests/" + signature:
			w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
			_, _ = w.Write(signatureManifest)
		case "/v2/wolf/scanners/manifests/" + provenance:
			w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
			_, _ = w.Write(provenanceManifest)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	host := strings.TrimPrefix(server.URL, "http://")
	client := Client{HTTP: server.Client(), Endpoints: map[string]Endpoint{host: {BaseURL: server.URL}}}
	status, err := client.ReadEvidence(context.Background(), Reference{
		Registry: host, Repository: "wolf/scanners", Digest: subject,
	}, map[string]string{
		"signature": signature, "provenance": provenance, "sbom": sbom,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !status["signature"] || !status["provenance"] || status["sbom"] {
		t.Fatalf("evidence drift status = %#v", status)
	}
}

type fakeOCIRegistry struct {
	server       *httptest.Server
	host         string
	mu           sync.Mutex
	manifests    map[string][]byte
	blobs        map[string][]byte
	referrers    map[string][]Descriptor
	blobUploads  map[string]int
	failManifest bool
}

func newFakeOCIRegistry(t *testing.T, failManifest bool) *fakeOCIRegistry {
	t.Helper()
	registry := &fakeOCIRegistry{
		manifests: make(map[string][]byte), blobs: make(map[string][]byte),
		referrers: make(map[string][]Descriptor), blobUploads: make(map[string]int),
		failManifest: failManifest,
	}
	registry.server = httptest.NewServer(http.HandlerFunc(registry.serveHTTP))
	registry.host = strings.TrimPrefix(registry.server.URL, "http://")
	t.Cleanup(registry.server.Close)
	return registry
}

func (f *fakeOCIRegistry) serveHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	const prefix = "/v2/wolf/scanners/"
	if r.URL.Path == "/v2/" {
		w.WriteHeader(http.StatusOK)
		return
	}
	if strings.HasPrefix(r.URL.Path, prefix+"referrers/") && r.Method == http.MethodGet {
		subject := strings.TrimPrefix(r.URL.Path, prefix+"referrers/")
		value, _ := json.Marshal(map[string]any{
			"schemaVersion": 2, "mediaType": "application/vnd.oci.image.index.v1+json",
			"manifests": f.referrers[subject],
		})
		w.Header().Set("Content-Type", "application/vnd.oci.image.index.v1+json")
		_, _ = w.Write(value)
		return
	}
	if strings.HasPrefix(r.URL.Path, prefix+"manifests/") {
		digest := strings.TrimPrefix(r.URL.Path, prefix+"manifests/")
		switch r.Method {
		case http.MethodHead:
			if _, exists := f.manifests[digest]; !exists {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Docker-Content-Digest", digest)
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			content, exists := f.manifests[digest]
			if !exists {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
			w.Header().Set("Docker-Content-Digest", digest)
			_, _ = w.Write(content)
		case http.MethodPut:
			if f.failManifest {
				f.failManifest = false
				http.Error(w, "interrupted", http.StatusServiceUnavailable)
				return
			}
			content, _ := io.ReadAll(r.Body)
			if digestBytes(content) != digest {
				http.Error(w, "digest mismatch", http.StatusBadRequest)
				return
			}
			f.manifests[digest] = content
			var artifact struct {
				MediaType    string     `json:"mediaType"`
				ArtifactType string     `json:"artifactType"`
				Subject      Descriptor `json:"subject"`
			}
			if json.Unmarshal(content, &artifact) == nil && artifact.Subject.Digest != "" {
				f.referrers[artifact.Subject.Digest] = append(
					f.referrers[artifact.Subject.Digest],
					Descriptor{
						MediaType: artifact.MediaType, Digest: digest,
						Size: int64(len(content)), ArtifactType: artifact.ArtifactType,
					},
				)
			}
			w.Header().Set("Docker-Content-Digest", digest)
			w.WriteHeader(http.StatusCreated)
		case http.MethodDelete:
			delete(f.manifests, digest)
			w.WriteHeader(http.StatusAccepted)
		}
		return
	}
	if strings.HasPrefix(r.URL.Path, prefix+"blobs/") &&
		r.URL.Path != prefix+"blobs/uploads/" {
		digest := strings.TrimPrefix(r.URL.Path, prefix+"blobs/")
		content, exists := f.blobs[digest]
		if !exists {
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodGet {
			_, _ = w.Write(content)
		} else {
			w.WriteHeader(http.StatusOK)
		}
		return
	}
	if r.URL.Path == prefix+"blobs/uploads/" && r.Method == http.MethodPost {
		w.Header().Set("Location", f.server.URL+prefix+"uploads/one")
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if r.URL.Path == prefix+"uploads/one" && r.Method == http.MethodPut {
		digest := r.URL.Query().Get("digest")
		content, _ := io.ReadAll(r.Body)
		if digestBytes(content) != digest {
			http.Error(w, "digest mismatch", http.StatusBadRequest)
			return
		}
		f.blobs[digest] = content
		f.blobUploads[digest]++
		w.WriteHeader(http.StatusCreated)
		return
	}
	http.NotFound(w, r)
}

func testRelease(host, digest string, platforms map[string]string) scannerbundle.ReleaseManifest {
	return scannerbundle.ReleaseManifest{
		SchemaVersion:     scannerbundle.ManifestSchema,
		ReleaseID:         "scanner-set-2026.31.1",
		LockDigest:        "sha256:" + strings.Repeat("c", 64),
		DefinitionCommit:  strings.Repeat("d", 40),
		BuildPolicyDigest: "sha256:" + strings.Repeat("e", 64),
		GeneratedAt:       time.Date(2026, 7, 30, 14, 0, 0, 0, time.UTC),
		Images: []scannerbundle.ReleaseImage{
			{
				Key: "default", Kind: "wolf",
				Reference: host + "/security/wolf-scanners@" + digest,
				Digest:    digest, Platforms: platforms, Required: true,
			},
		},
	}
}
