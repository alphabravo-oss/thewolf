package scannerdiscovery

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/scannertools/httpcache"
	"github.com/alphabravocompany/thewolf/internal/scannertools/latest"
	scannerlock "github.com/alphabravocompany/thewolf/internal/scannertools/lock"
	"github.com/alphabravocompany/thewolf/internal/scannertools/manifest"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestLatestToolResolverMapsLegacyResult(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != "https://pypi.org/pypi/demo/json" {
			t.Fatalf("unexpected URL %s", request.URL)
		}
		return &http.Response{
			StatusCode: http.StatusOK, Status: "200 OK",
			Header: http.Header{},
			Body:   io.NopCloser(strings.NewReader(`{"info":{"version":"1.2.0"}}`)),
		}, nil
	})}
	definition := manifest.Tool{
		PinnedVersion: "1.0.0",
		UpdateSource:  manifest.UpdateSource{Type: "pypi", Package: "demo"},
	}
	resolver := LatestToolResolver{Checker: latest.Checker{Client: client}}
	got, err := resolver.Resolve(context.Background(), Item{
		ID:             ComponentID{Kind: ComponentTool, Name: "demo"},
		ToolDefinition: &definition,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusUpdate || got.AvailableValue != "1.2.0" ||
		got.Evidence.SourceURL != "https://pypi.org/pypi/demo/json" {
		t.Fatalf("observation = %+v", got)
	}
}

func TestDefaultResolversShareOneConditionalCache(t *testing.T) {
	resolvers := DefaultResolvers(latest.Checker{}, nil)
	latestResolver, ok := resolvers[0].(LatestToolResolver)
	if !ok || latestResolver.Checker.Cache == nil {
		t.Fatalf("latest resolver cache = %#v", resolvers[0])
	}
	imageResolver, ok := resolvers[1].(ImageDigestResolver)
	if !ok || imageResolver.Resolver == nil || imageResolver.Resolver.Cache == nil {
		t.Fatalf("image resolver cache = %#v", resolvers[1])
	}
	toolchainResolver, ok := resolvers[2].(ToolchainResolver)
	if !ok || toolchainResolver.Cache == nil {
		t.Fatalf("toolchain resolver cache = %#v", resolvers[2])
	}
	if latestResolver.Checker.Cache != imageResolver.Resolver.Cache ||
		latestResolver.Checker.Cache != toolchainResolver.Cache {
		t.Fatal("production resolvers do not share the worker-lifetime cache")
	}
}

func TestLatestToolResolverPreservesUnsupportedClassification(t *testing.T) {
	definition := manifest.Tool{
		PinnedVersion: "1.0.0",
		UpdateSource:  manifest.UpdateSource{Type: "vendor_portal"},
	}
	resolver := LatestToolResolver{}
	_, err := resolver.Resolve(context.Background(), Item{
		ID:             ComponentID{Kind: ComponentTool, Name: "demo"},
		ToolDefinition: &definition,
	})
	classified, ok := err.(*ClassifiedError)
	if !ok || classified.Class != ErrorUnsupported {
		t.Fatalf("error = %#v", err)
	}
}

func TestLatestToolResolverClassifiesNotModifiedCacheMissAsInvalid(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusNotModified,
			Status:     "304 Not Modified",
			Header:     make(http.Header),
			Body:       http.NoBody,
		}, nil
	})}
	definition := manifest.Tool{
		PinnedVersion: "1.0.0",
		UpdateSource:  manifest.UpdateSource{Type: "pypi", Package: "demo"},
	}
	resolver := LatestToolResolver{Checker: latest.Checker{
		Client: client,
		Cache:  httpcache.NewMemoryStore(),
	}}
	_, err := resolver.Resolve(context.Background(), Item{
		ID:             ComponentID{Kind: ComponentTool, Name: "demo"},
		ToolDefinition: &definition,
	})
	classified, ok := err.(*ClassifiedError)
	if !ok || classified.Class != ErrorInvalidResponse {
		t.Fatalf("error = %#v", err)
	}
}

func TestImageDigestResolverDetectsTagMutation(t *testing.T) {
	digest := "sha256:" + strings.Repeat("b", 64)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodHead || request.URL.Path != "/v2/acme/demo/manifests/1.0.0" {
			t.Fatalf("unexpected registry request %s %s", request.Method, request.URL.Path)
		}
		writer.Header().Set("Docker-Content-Digest", digest)
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	registry := &scannerlock.ImageResolver{
		Client: server.Client(),
		RegistryBase: func(string) string {
			return server.URL
		},
	}
	resolver := ImageDigestResolver{Resolver: registry}
	got, err := resolver.Resolve(context.Background(), Item{
		ID:           ComponentID{Kind: ComponentUpstreamImage, Name: "demo"},
		CurrentValue: "sha256:" + strings.Repeat("a", 64),
		Source: Source{
			Type: "oci_registry", Host: "registry.example",
			Reference: "registry.example/acme/demo:1.0.0",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusUpdate || got.AvailableValue != digest || !got.Facts.SourceChanged ||
		got.Facts.RebuildOnly {
		t.Fatalf("observation = %+v", got)
	}
}

func TestBaseDigestChangeIsRebuildOnly(t *testing.T) {
	digest := "sha256:" + strings.Repeat("b", 64)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Docker-Content-Digest", digest)
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	resolver := ImageDigestResolver{Resolver: &scannerlock.ImageResolver{
		Client: server.Client(),
		RegistryBase: func(string) string {
			return server.URL
		},
	}}
	got, err := resolver.Resolve(context.Background(), Item{
		ID:           ComponentID{Kind: ComponentBaseImage, Name: "default"},
		CurrentValue: "sha256:" + strings.Repeat("a", 64),
		Source:       Source{Type: "oci_registry", Reference: "debian:trixie-slim"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusUpdate || !got.Facts.RebuildOnly || got.Facts.SourceChanged {
		t.Fatalf("observation = %+v", got)
	}
}
