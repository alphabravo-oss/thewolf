package registryauth

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestFetchBearerTokenAllowsExplicitLoopbackTestClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("scope"); got != "repository:acme/tool:pull" {
			t.Fatalf("scope = %q", got)
		}
		_, _ = io.WriteString(w, `{"token":"test-token"}`)
	}))
	defer server.Close()
	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	token, err := FetchBearerToken(
		context.Background(),
		`Bearer realm="`+server.URL+`/token",service="registry.test"`,
		FetchOptions{
			Client:            server.Client(),
			Registry:          serverURL.Host,
			Repository:        "acme/tool",
			AllowLoopbackHTTP: true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if token != "test-token" {
		t.Fatalf("token = %q", token)
	}
}

func TestFetchBearerTokenRejectsPrivateResolutionBeforeRequest(t *testing.T) {
	var requests atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		requests.Add(1)
		return nil, nil
	})}
	_, err := FetchBearerToken(
		context.Background(),
		`Bearer realm="https://registry.example/token"`,
		FetchOptions{
			Client:     client,
			Registry:   "registry.example",
			Repository: "acme/tool",
			LookupIP: func(context.Context, string) ([]net.IP, error) {
				return []net.IP{net.ParseIP("10.0.0.8")}, nil
			},
		},
	)
	if err == nil || !strings.Contains(err.Error(), "private or non-routable") {
		t.Fatalf("error = %v", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("requests = %d, want 0", requests.Load())
	}
}

func TestFetchBearerTokenRevalidatesRedirectDestination(t *testing.T) {
	var redirectedRequests atomic.Int32
	redirected := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		redirectedRequests.Add(1)
		_, _ = io.WriteString(w, `{"token":"stolen"}`)
	}))
	defer redirected.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, redirected.URL+"/token", http.StatusFound)
	}))
	defer origin.Close()
	originURL, err := url.Parse(origin.URL)
	if err != nil {
		t.Fatal(err)
	}

	_, err = FetchBearerToken(
		context.Background(),
		`Bearer realm="`+origin.URL+`/token"`,
		FetchOptions{
			Client:            origin.Client(),
			Registry:          originURL.Host,
			Repository:        "acme/tool",
			AllowLoopbackHTTP: true,
		},
	)
	if err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("error = %v", err)
	}
	if redirectedRequests.Load() != 0 {
		t.Fatalf("redirect destination received %d requests", redirectedRequests.Load())
	}
}

func TestFetchBearerTokenRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"token":"`+strings.Repeat("a", maxTokenResponse)+`"}`)
	}))
	defer server.Close()
	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, err = FetchBearerToken(
		context.Background(),
		`Bearer realm="`+server.URL+`/token"`,
		FetchOptions{
			Client:            server.Client(),
			Registry:          serverURL.Host,
			Repository:        "acme/tool",
			AllowLoopbackHTTP: true,
		},
	)
	if err == nil || !strings.Contains(err.Error(), "response exceeds") {
		t.Fatalf("error = %v", err)
	}
}

func TestFetchBearerTokenHonorsCallerDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = FetchBearerToken(
		ctx,
		`Bearer realm="`+server.URL+`/token"`,
		FetchOptions{
			Client:            server.Client(),
			Registry:          serverURL.Host,
			Repository:        "acme/tool",
			AllowLoopbackHTTP: true,
		},
	)
	if err == nil || !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("error = %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
