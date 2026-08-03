package scannerlock

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/scannertools/httpcache"
	"github.com/alphabravocompany/thewolf/internal/scannertools/manifest"
	"gopkg.in/yaml.v3"
)

const digestA = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const digestB = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func TestRepositoryLockIsDeterministicGolden(t *testing.T) {
	root, err := manifest.FindRepoRoot("")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, DefaultLockPath)
	golden, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	existing, err := Parse(golden)
	if err != nil {
		t.Fatal(err)
	}
	first, err := Generate(context.Background(), root, GenerateOptions{ExistingLock: existing})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Generate(context.Background(), root, GenerateOptions{ExistingLock: existing})
	if err != nil {
		t.Fatal(err)
	}
	firstBytes, err := first.MarshalYAML()
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := second.MarshalYAML()
	if err != nil {
		t.Fatal(err)
	}
	if string(firstBytes) != string(secondBytes) {
		t.Fatal("identical inputs produced different lock bytes")
	}
	if string(firstBytes) != string(golden) {
		t.Fatal("checked-in scanner-lock.yaml is stale")
	}
	if len(first.Tools) != 49 {
		t.Fatalf("lock tools = %d, want 49", len(first.Tools))
	}
}

func TestCanonicalDigestDetectsTampering(t *testing.T) {
	lock := generateFixtureLock(t, GenerateOptions{})
	original := lock.LockDigest
	lock.Tools["demo"] = Tool{
		Category: "changed", IntegrationTier: manifest.TierUpstream,
		Platforms:       []string{"linux/amd64"},
		UpdateSource:    UpdateSource{Type: "docker_registry", Repository: "acme/demo"},
		SourceIntegrity: SourceIntegrity{Status: "oci_digest_required"},
		ParserContract: ParserContract{
			Status: "quality_policy", Format: "json",
			Fixtures: []string{"scanners/quality/corpus.yaml", "scanners/quality/goldens/family-findings.json"},
		},
		License: LicensePolicy{Status: "undeclared"},
		Risk:    RiskPolicy{Classification: "high", ApprovalRequired: true},
	}
	if err := lock.Validate(); err == nil || !strings.Contains(err.Error(), "calculated") {
		t.Fatalf("tampered lock validation = %v", err)
	}
	if lock.LockDigest != original {
		t.Fatal("validation unexpectedly changed the recorded digest")
	}
}

func TestGenerateRejectsFixerBuildArgumentDrift(t *testing.T) {
	root := fixtureRepo(t)
	policyPath := filepath.Join(root, "scanners", "build-policy.yaml")
	data, err := os.ReadFile(policyPath)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(
		string(data),
		"    smokeCommand: [\"/usr/local/bin/wolf\", \"version\"]",
		"    buildArgs:\n      GO_VERSION: 9.9.9\n    smokeCommand: [\"/usr/local/bin/wolf\", \"version\"]",
		1,
	))
	if err := os.WriteFile(policyPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = Generate(context.Background(), root, GenerateOptions{})
	if err == nil || !strings.Contains(err.Error(), "does not match fixer/versions.env") {
		t.Fatalf("Generate fixer build-argument drift error = %v", err)
	}
}

func TestDefinitionInputsCoverFixerAndCompiledWolfSources(t *testing.T) {
	root := fixtureRepo(t)
	current, err := Generate(context.Background(), root, GenerateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		".dockerignore",
		"fixer/install-node-tools.sh",
		"fixer/go-tools/go.mod",
		"fixer/go-tools/go.sum",
		"cmd/wolf/main.go",
		"internal/demo/demo.go",
		"plugins/demo/demo.go",
	} {
		if !digestRE.MatchString(current.Definition.Inputs[path]) {
			t.Errorf("definition input %s is missing or invalid", path)
		}
	}

	for _, path := range []string{
		"fixer/install-node-tools.sh",
		"fixer/go-tools/go.sum",
		"cmd/wolf/main.go",
		"internal/demo/demo.go",
		"plugins/demo/demo.go",
	} {
		fullPath := filepath.Join(root, filepath.FromSlash(path))
		before, err := os.ReadFile(fullPath)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, append(before, []byte("\n# lock drift\n")...), 0o600); err != nil {
			t.Fatal(err)
		}
		next, err := Generate(context.Background(), root, GenerateOptions{})
		if err != nil {
			t.Fatalf("generate after %s drift: %v", path, err)
		}
		if next.Definition.Inputs[path] == current.Definition.Inputs[path] {
			t.Errorf("definition input digest did not detect %s drift", path)
		}
		if next.Definition.Digest == current.Definition.Digest {
			t.Errorf("definition digest did not detect %s drift", path)
		}
		current = next
	}
}

func TestParseRejectsUnknownSchemaFields(t *testing.T) {
	lock := generateFixtureLock(t, GenerateOptions{})
	data, err := lock.MarshalYAML()
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, []byte("unknownField: true\n")...)
	if _, err := Parse(data); err == nil || !strings.Contains(err.Error(), "field unknownField") {
		t.Fatalf("Parse unknown field = %v", err)
	}
}

func TestParseGenerationCacheReusesOnlyWellFormedUpstreamResolution(t *testing.T) {
	t.Parallel()
	lock := generateFixtureLock(t, GenerateOptions{})
	resolved := lock.UpstreamImages["demo"]
	resolved.Digest = digestA
	resolved.ResolvedReference = "registry.example/acme/demo@" + digestA
	lock.UpstreamImages["demo"] = resolved
	// Simulate an additive schema transition: the prior lock is no longer
	// valid release evidence, but its exact upstream resolution remains useful
	// while producing the replacement lock.
	lock.ReleaseInputs.FixerVariants = nil
	data, err := yaml.Marshal(lock)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(data); err == nil {
		t.Fatal("incomplete prior lock was accepted as release evidence")
	}
	cache, err := ParseGenerationCache(data)
	if err != nil {
		t.Fatalf("ParseGenerationCache: %v", err)
	}
	if got := cache.UpstreamImages["demo"]; got.DeclaredReference == "" || got.Digest != digestA {
		t.Fatalf("upstream cache = %#v", got)
	}

	cache.UpstreamImages["demo"] = UpstreamImage{
		DeclaredReference: "registry.example/acme/demo:1.0.0",
		ResolvedReference: "registry.example/acme/demo@" + digestA,
		Digest:            digestB,
	}
	bad, err := yaml.Marshal(cache)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseGenerationCache(bad); err == nil || !strings.Contains(err.Error(), "mismatched") {
		t.Fatalf("mismatched generation cache error = %v", err)
	}
}

func TestImageResolverResolvesRegistryDigest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead || r.URL.Path != "/v2/acme/demo/manifests/1.0.0" {
			t.Fatalf("unexpected registry request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Docker-Content-Digest", digestA)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	resolver := &ImageResolver{
		Client: server.Client(),
		RegistryBase: func(string) string {
			return server.URL
		},
	}
	got, err := resolver.Resolve(context.Background(), "registry.example/acme/demo:1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if got.Digest != digestA {
		t.Fatalf("digest = %q, want %q", got.Digest, digestA)
	}
}

func TestImageResolverReusesDigestOnNotModified(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead || r.URL.Path != "/v2/acme/demo/manifests/1.0.0" {
			t.Fatalf("unexpected registry request: %s %s", r.Method, r.URL.Path)
		}
		switch calls.Add(1) {
		case 1:
			w.Header().Set("ETag", `"manifest-v1"`)
			w.Header().Set("Docker-Content-Digest", digestA)
			w.WriteHeader(http.StatusOK)
		case 2:
			if got := r.Header.Get("If-None-Match"); got != `"manifest-v1"` {
				t.Fatalf("If-None-Match = %q", got)
			}
			w.WriteHeader(http.StatusNotModified)
		default:
			t.Fatalf("unexpected call %d", calls.Load())
		}
	}))
	defer server.Close()

	resolver := &ImageResolver{
		Client: server.Client(),
		RegistryBase: func(string) string {
			return server.URL
		},
		Cache: httpcache.NewMemoryStore(),
	}
	for range 2 {
		got, err := resolver.Resolve(context.Background(), "registry.example/acme/demo:1.0.0")
		if err != nil {
			t.Fatal(err)
		}
		if got.Digest != digestA {
			t.Fatalf("digest = %q, want %q", got.Digest, digestA)
		}
	}
}

func TestImageResolverPreservesLoopbackRegistryBearerFlow(t *testing.T) {
	var authenticated atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/acme/demo/manifests/1.0.0":
			if r.Method != http.MethodHead {
				t.Fatalf("method = %s", r.Method)
			}
			if r.Header.Get("Authorization") == "" {
				w.Header().Set(
					"WWW-Authenticate",
					`Bearer realm="http://`+r.Host+`/token",service="test-registry"`,
				)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if r.Header.Get("Authorization") != "Bearer test-token" {
				t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
			}
			authenticated.Store(true)
			w.Header().Set("Docker-Content-Digest", digestA)
		case "/token":
			_, _ = io.WriteString(w, `{"token":"test-token"}`)
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	registry := strings.TrimPrefix(server.URL, "http://")
	resolver := &ImageResolver{
		Client: server.Client(),
		RegistryBase: func(string) string {
			return server.URL
		},
	}
	got, err := resolver.Resolve(context.Background(), registry+"/acme/demo:1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if got.Digest != digestA || !authenticated.Load() {
		t.Fatalf("resolved image = %#v, authenticated = %v", got, authenticated.Load())
	}
}

func TestImageResolverRejectsPrivateBearerRealmBeforeRequest(t *testing.T) {
	var requested atomic.Bool
	resolver := &ImageResolver{
		Client: &http.Client{Transport: imageRoundTripFunc(func(*http.Request) (*http.Response, error) {
			requested.Store(true)
			return nil, nil
		})},
		LookupIP: func(context.Context, string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("10.0.0.9")}, nil
		},
	}
	_, err := resolver.bearerToken(
		context.Background(),
		`Bearer realm="https://registry.example/token"`,
		"registry.example",
		"acme/demo",
	)
	if err == nil || !strings.Contains(err.Error(), "private or non-routable") {
		t.Fatalf("error = %v", err)
	}
	if requested.Load() {
		t.Fatal("private auth address was requested")
	}
}

func TestGenerateRejectsMutableTagDigestChange(t *testing.T) {
	root := fixtureRepo(t)
	firstResolver := staticResolver(t, digestA)
	first, err := Generate(context.Background(), root, GenerateOptions{
		RefreshImages: true, ImageResolver: firstResolver,
	})
	if err != nil {
		t.Fatal(err)
	}
	secondResolver := staticResolver(t, digestB)
	_, err = Generate(context.Background(), root, GenerateOptions{
		ExistingLock: first, RefreshImages: true, ImageResolver: secondResolver,
	})
	var mutation *TagMutationError
	if !errors.As(err, &mutation) {
		t.Fatalf("Generate tag mutation = %v, want TagMutationError", err)
	}
	accepted, err := Generate(context.Background(), root, GenerateOptions{
		ExistingLock: first, RefreshImages: true, AllowTagMutation: true,
		ImageResolver: secondResolver,
	})
	if err != nil {
		t.Fatal(err)
	}
	if accepted.UpstreamImages["demo"].Digest != digestB {
		t.Fatalf("accepted digest = %q", accepted.UpstreamImages["demo"].Digest)
	}
}

func TestValidateResolvedRejectsMutableUnresolvedImages(t *testing.T) {
	lock := generateFixtureLock(t, GenerateOptions{})
	if err := lock.ValidateResolved(); err == nil || !strings.Contains(err.Error(), "demo") {
		t.Fatalf("ValidateResolved = %v", err)
	}
}

func TestDigestPinnedReferenceNeedsNoRegistryResolution(t *testing.T) {
	if got, ok := referenceDigest("registry.example/acme/demo@" + digestA); !ok || got != digestA {
		t.Fatalf("referenceDigest() = %q, %v", got, ok)
	}
	if _, ok := referenceDigest("registry.example/acme/demo:1.0.0"); ok {
		t.Fatal("mutable tag was classified as digest-pinned")
	}
}

func staticResolver(t *testing.T, digest string) *ImageResolver {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Docker-Content-Digest", digest)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	return &ImageResolver{
		Client: server.Client(),
		RegistryBase: func(string) string {
			return server.URL
		},
	}
}

func generateFixtureLock(t *testing.T, opts GenerateOptions) *Lock {
	t.Helper()
	lock, err := Generate(context.Background(), fixtureRepo(t), opts)
	if err != nil {
		t.Fatal(err)
	}
	return lock
}

func fixtureRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		".dockerignore":          ".git\n",
		"scanners/.dockerignore": "**/*\n!Dockerfile\n",
		"scanners/build-policy.yaml": `schemaVersion: wolf.scanners/build-policy/v1
variants:
  default:
    dockerfile: scanners/Dockerfile
    platforms: [linux/amd64]
fixerVariants:
  base:
    dockerfile: fixer/Dockerfile.base
    context: .
    image: wolf-fixer
    platforms: [linux/amd64]
    authMode: none
    smokeCommand: ["/usr/local/bin/wolf", "version"]
`,
		"scanners/toolchains.yaml": `base_images:
  default: registry.example/base@` + digestA + `
toolchains:
  go:
    version: "1.2.3"
`,
		"scanners/tools.yaml": `tools:
  demo:
    display_name: Demo
    category: sast
    resource_class: medium
    default_timeout: 10m
    plugin_package: plugins/demo
    integration_tier: upstream
    pinned_version: "1.0.0"
    version_variable: DEMO_VERSION
    image:
      pinned_reference: registry.example/acme/demo:1.0.0
      platforms: [linux/amd64]
    update_source:
      type: docker_registry
      repository: registry.example/acme/demo
`,
		"scanners/quality/policy.yaml": `schemaVersion: wolf.scanners/quality-policy/v1
tools:
  demo:
    parserOwned: true
    parserFormat: json
`,
		"scanners/quality/corpus.yaml":                  "fixture corpus\n",
		"scanners/quality/goldens/family-findings.json": "[]\n",
		"scanners/os-packages.yaml":                     "fixture policy\n",
		"scanners/os-packages.lock.yaml":                "fixture lock\n",
		"scanners/os-packages/pins/default-amd64.txt":   "fixture pin\n",
		"scanners/versions.env":                         "DEMO_VERSION=1.0.0\n",
		"scanners/Dockerfile":                           "FROM registry.example/base@" + digestA + "\n",
		"scanners/smoke-test.sh":                        "#!/bin/sh\n",
		"scanners/trufflehog-excludes.txt":              "\n",
		"scanners/wolf-tool-entry":                      "#!/bin/sh\n",
		"scanners/install/install.sh":                   "#!/bin/sh\n",
		"fixer/Dockerfile.base":                         "FROM registry.example/base@" + digestA + "\n",
		"fixer/versions.env":                            "GO_VERSION=1.2.3\n",
		"fixer/install-node-tools.sh":                   "#!/bin/sh\n",
		"fixer/go-tools/go.mod":                         "module example.invalid/fixer-tools\n\ngo 1.26\n",
		"fixer/go-tools/go.sum":                         "\n",
		"cmd/wolf/main.go":                              "package main\n",
		"internal/demo/demo.go":                         "package demo\n",
		"plugins/demo/demo.go":                          "package demo\n",
		"go.mod":                                        "module example.invalid/fixture\n\ngo 1.26\n",
		"go.sum":                                        "\n",
	}
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

type imageRoundTripFunc func(*http.Request) (*http.Response, error)

func (f imageRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
