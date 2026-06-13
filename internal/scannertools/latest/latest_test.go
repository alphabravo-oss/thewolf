package latest

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/scannertools/manifest"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCheckerPyPIUpdateAvailable(t *testing.T) {
	checker := Checker{
		Client: fixtureClient(t, map[string]string{
			"https://pypi.org/pypi/bandit/json": `{"info":{"version":"1.8.0"}}`,
		}),
		Now: func() time.Time { return time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC) },
	}
	got := checker.Check(context.Background(), "bandit", manifest.Tool{
		PinnedVersion: "1.7.10",
		UpdateSource:  manifest.UpdateSource{Type: "pypi", Package: "bandit"},
	})
	if got.Status != models.ScannerVersionUpdateAvailable {
		t.Fatalf("status = %q, want update_available: %#v", got.Status, got)
	}
	if got.LatestVersion != "1.8.0" {
		t.Fatalf("latest = %q", got.LatestVersion)
	}
}

func TestCheckerNPMCurrent(t *testing.T) {
	checker := Checker{Client: fixtureClient(t, map[string]string{
		"https://registry.npmjs.org/eslint": `{"dist-tags":{"latest":"9.13.0"}}`,
	})}
	got := checker.Check(context.Background(), "eslint", manifest.Tool{
		PinnedVersion: "9.13.0",
		UpdateSource:  manifest.UpdateSource{Type: "npm", Package: "eslint"},
	})
	if got.Status != models.ScannerVersionCurrent {
		t.Fatalf("status = %q, want current: %#v", got.Status, got)
	}
}

func TestCheckerDockerRegistryWithBearerChallenge(t *testing.T) {
	checker := Checker{Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.String() {
		case "https://registry-1.docker.io/v2/semgrep/semgrep/tags/list?n=100":
			if r.Header.Get("Authorization") == "" {
				return response(401, ``, map[string]string{
					"WWW-Authenticate": `Bearer realm="https://auth.docker.io/token",service="registry.docker.io"`,
				}), nil
			}
			return response(200, `{"tags":["1.91.0","1.92.0","1.94.1","edge"]}`, nil), nil
		case "https://auth.docker.io/token?scope=repository%3Asemgrep%2Fsemgrep%3Apull&service=registry.docker.io":
			return response(200, `{"token":"abc"}`, nil), nil
		default:
			t.Fatalf("unexpected request: %s", r.URL.String())
			return nil, nil
		}
	})}}
	got := checker.Check(context.Background(), "semgrep", manifest.Tool{
		PinnedVersion: "1.92.0",
		UpdateSource: manifest.UpdateSource{
			Type:       "docker_registry",
			Repository: "semgrep/semgrep",
			TagPattern: `^\d+\.\d+\.\d+$`,
		},
	})
	if got.Status != models.ScannerVersionUpdateAvailable {
		t.Fatalf("status = %q, want update_available: %#v", got.Status, got)
	}
	if got.LatestVersion != "1.94.1" {
		t.Fatalf("latest = %q", got.LatestVersion)
	}
}

func TestCheckerDockerRegistryFollowsPagination(t *testing.T) {
	checker := Checker{Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.String() {
		case "https://registry.example.com/v2/acme/tool/tags/list?n=100":
			return response(200, `{"tags":["1.0.0"]}`, map[string]string{
				"Link": `</v2/acme/tool/tags/list?n=100&last=1.0.0>; rel="next"`,
			}), nil
		case "https://registry.example.com/v2/acme/tool/tags/list?n=100&last=1.0.0":
			return response(200, `{"tags":["1.2.0"]}`, nil), nil
		default:
			t.Fatalf("unexpected request: %s", r.URL.String())
			return nil, nil
		}
	})}}
	got := checker.Check(context.Background(), "acme-tool", manifest.Tool{
		PinnedVersion: "1.0.0",
		UpdateSource: manifest.UpdateSource{
			Type:       "docker_registry",
			Repository: "registry.example.com/acme/tool",
			TagPattern: `^\d+\.\d+\.\d+$`,
		},
	})
	if got.Status != models.ScannerVersionUpdateAvailable || got.LatestVersion != "1.2.0" {
		t.Fatalf("unexpected paginated docker result: %#v", got)
	}
}

func TestCheckerGitHubFallsBackToTags(t *testing.T) {
	checker := Checker{Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.String() {
		case "https://api.github.com/repos/acme/tool/releases/latest":
			return response(404, `{"message":"Not Found"}`, nil), nil
		case "https://api.github.com/repos/acme/tool/tags?per_page=100":
			return response(200, `[{"name":"v1.1.0"},{"name":"v1.3.0"},{"name":"nightly"}]`, nil), nil
		default:
			t.Fatalf("unexpected request: %s", r.URL.String())
			return nil, nil
		}
	})}}
	got := checker.Check(context.Background(), "acme-tool", manifest.Tool{
		PinnedVersion: "1.1.0",
		UpdateSource: manifest.UpdateSource{
			Type:       "github_releases",
			Owner:      "acme",
			Repo:       "tool",
			TagPattern: `^v\d+\.\d+\.\d+$`,
		},
	})
	if got.Status != models.ScannerVersionUpdateAvailable || got.LatestVersion != "1.3.0" {
		t.Fatalf("unexpected github tag fallback result: %#v", got)
	}
}

func TestCheckerRubyGemsUpdateAvailable(t *testing.T) {
	checker := Checker{Client: fixtureClient(t, map[string]string{
		"https://rubygems.org/api/v1/gems/brakeman.json": `{"version":"6.3.1"}`,
	})}
	got := checker.Check(context.Background(), "brakeman", manifest.Tool{
		PinnedVersion: "6.2.2",
		UpdateSource:  manifest.UpdateSource{Type: "rubygems", Package: "brakeman"},
	})
	if got.Status != models.ScannerVersionUpdateAvailable || got.LatestVersion != "6.3.1" {
		t.Fatalf("unexpected rubygems result: %#v", got)
	}
}

func TestCheckerGoModuleUpdateAvailable(t *testing.T) {
	checker := Checker{Client: fixtureClient(t, map[string]string{
		"https://proxy.golang.org/honnef.co/go/tools/@v/list": "v0.6.0\nv0.7.0\nv0.7.1\n",
	})}
	got := checker.Check(context.Background(), "staticcheck", manifest.Tool{
		PinnedVersion: "0.7.0",
		UpdateSource:  manifest.UpdateSource{Type: "go_module", Module: "honnef.co/go/tools"},
	})
	if got.Status != models.ScannerVersionUpdateAvailable || got.LatestVersion != "0.7.1" {
		t.Fatalf("unexpected go module result: %#v", got)
	}
}

func TestCheckerPackagistUpdateAvailable(t *testing.T) {
	checker := Checker{Client: fixtureClient(t, map[string]string{
		"https://repo.packagist.org/p2/phpstan/phpstan.json": `{"package":{"versions":{"1.12.7":{},"1.12.8":{},"dev-main":{}}}}`,
	})}
	got := checker.Check(context.Background(), "phpstan", manifest.Tool{
		PinnedVersion: "1.12.7",
		UpdateSource:  manifest.UpdateSource{Type: "packagist", Package: "phpstan/phpstan"},
	})
	if got.Status != models.ScannerVersionUpdateAvailable || got.LatestVersion != "1.12.8" {
		t.Fatalf("unexpected packagist result: %#v", got)
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"1.2.3", "1.2.3", 0},
		{"v1.3.0", "1.2.9", 1},
		{"1.2.0-alpine", "1.2.0", 0},
		{"1.2.0", "1.2.1", -1},
		{"5.20.4", "5.9.0", 1},
	}
	for _, tt := range tests {
		got := CompareVersions(tt.a, tt.b)
		if got != tt.want {
			t.Fatalf("CompareVersions(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestEscapeGoModulePath(t *testing.T) {
	if got := escapeGoModulePath("github.com/AlphaBravo/Foo"); got != "github.com/!alpha!bravo/!foo" {
		t.Fatalf("escapeGoModulePath = %q", got)
	}
}

func fixtureClient(t *testing.T, fixtures map[string]string) *http.Client {
	t.Helper()
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, ok := fixtures[r.URL.String()]
		if !ok {
			t.Fatalf("unexpected request: %s", r.URL.String())
		}
		return response(200, body, nil), nil
	})}
}

func response(status int, body string, headers map[string]string) *http.Response {
	resp := &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{},
	}
	for k, v := range headers {
		resp.Header.Set(k, v)
	}
	return resp
}
