package latest

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/scannertools/httpcache"
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

func TestCheckerConditionalCacheKeepsNotModifiedResultKnown(t *testing.T) {
	tests := []struct {
		name              string
		responseHeader    map[string]string
		conditionalHeader string
		conditionalValue  string
	}{
		{
			name: "etag", responseHeader: map[string]string{"ETag": `"pypi-v1"`},
			conditionalHeader: "If-None-Match", conditionalValue: `"pypi-v1"`,
		},
		{
			name: "last modified",
			responseHeader: map[string]string{
				"Last-Modified": "Wed, 30 Jul 2025 12:00:00 GMT",
			},
			conditionalHeader: "If-Modified-Since",
			conditionalValue:  "Wed, 30 Jul 2025 12:00:00 GMT",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			checker := Checker{
				Cache: httpcache.NewMemoryStore(),
				Client: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
					calls++
					if calls == 1 {
						return response(
							http.StatusOK,
							`{"info":{"version":"1.8.0"}}`,
							test.responseHeader,
						), nil
					}
					if got := request.Header.Get(test.conditionalHeader); got != test.conditionalValue {
						t.Fatalf("%s = %q", test.conditionalHeader, got)
					}
					return response(http.StatusNotModified, "", nil), nil
				})},
			}
			tool := manifest.Tool{
				PinnedVersion: "1.7.10",
				UpdateSource:  manifest.UpdateSource{Type: "pypi", Package: "bandit"},
			}
			first := checker.Check(context.Background(), "bandit", tool)
			second := checker.Check(context.Background(), "bandit", tool)
			if first.Status != models.ScannerVersionUpdateAvailable ||
				second.Status != models.ScannerVersionUpdateAvailable ||
				second.LatestVersion != "1.8.0" {
				t.Fatalf("first=%#v second=%#v", first, second)
			}
		})
	}
}

func TestCheckerPyPISkipsYankedLatest(t *testing.T) {
	checker := Checker{Client: fixtureClient(t, map[string]string{
		"https://pypi.org/pypi/bandit/json": `{
			"info":{"version":"1.9.0"},
			"releases":{
				"1.8.0":[{"yanked":false}],
				"1.9.0":[{"yanked":true}]
			}
		}`,
	})}
	got := checker.Check(context.Background(), "bandit", manifest.Tool{
		PinnedVersion: "1.7.10",
		UpdateSource:  manifest.UpdateSource{Type: "pypi", Package: "bandit"},
	})
	if got.Status != models.ScannerVersionUpdateAvailable || got.LatestVersion != "1.8.0" {
		t.Fatalf("unexpected yanked-release result: %#v", got)
	}
}

func TestCheckerUnavailableSourceFails(t *testing.T) {
	checker := Checker{Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(http.StatusServiceUnavailable, ``, nil), nil
	})}}
	got := checker.Check(context.Background(), "bandit", manifest.Tool{
		PinnedVersion: "1.7.10",
		UpdateSource:  manifest.UpdateSource{Type: "pypi", Package: "bandit"},
	})
	if got.Status != models.ScannerVersionCheckFailed || !strings.Contains(got.Error, "Service Unavailable") {
		t.Fatalf("unavailable result = %#v", got)
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
	checker := Checker{
		Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
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
		})},
		LookupIP: publicLookup,
	}
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

func TestCheckerDockerRegistryRejectsAttackerControlledBearerRealm(t *testing.T) {
	requested := false
	checker := Checker{
		Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			requested = true
			return nil, nil
		})},
		LookupIP: publicLookup,
	}
	_, err := checker.dockerBearerToken(
		context.Background(),
		`Bearer realm="https://169.254.169.254/latest/meta-data/"`,
		"registry-1.docker.io",
		"semgrep/semgrep",
	)
	if err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("error = %v", err)
	}
	if requested {
		t.Fatal("attacker-controlled realm was requested")
	}
}

func TestCheckerDockerRegistryRejectsPrivateAuthResolution(t *testing.T) {
	requested := false
	checker := Checker{
		Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			requested = true
			return nil, nil
		})},
		LookupIP: func(context.Context, string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("169.254.169.254")}, nil
		},
	}
	_, err := checker.dockerBearerToken(
		context.Background(),
		`Bearer realm="https://auth.docker.io/token"`,
		"registry-1.docker.io",
		"semgrep/semgrep",
	)
	if err == nil || !strings.Contains(err.Error(), "private or non-routable") {
		t.Fatalf("error = %v", err)
	}
	if requested {
		t.Fatal("private auth address was requested")
	}
}

func TestCheckerDockerRegistryPreservesGHCRBearerFlow(t *testing.T) {
	checker := Checker{
		Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.String() != "https://ghcr.io/token?scope=repository%3Agoogle%2Fosv-scanner%3Apull&service=ghcr.io" {
				t.Fatalf("unexpected request: %s", r.URL.String())
			}
			return response(http.StatusOK, `{"token":"ghcr-token"}`, nil), nil
		})},
		LookupIP: publicLookup,
	}
	token, err := checker.dockerBearerToken(
		context.Background(),
		`Bearer realm="https://ghcr.io/token",service="ghcr.io"`,
		"ghcr.io",
		"google/osv-scanner",
	)
	if err != nil {
		t.Fatal(err)
	}
	if token != "ghcr-token" {
		t.Fatalf("token = %q", token)
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

func TestCheckerDockerRegistryTagCacheKeepsNotModifiedResultKnown(t *testing.T) {
	var calls int
	checker := Checker{
		Cache: httpcache.NewMemoryStore(),
		Client: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			calls++
			if calls == 1 {
				return response(
					http.StatusOK,
					`{"tags":["1.0.0","1.2.0"]}`,
					map[string]string{"ETag": `"tags-v1"`},
				), nil
			}
			if got := request.Header.Get("If-None-Match"); got != `"tags-v1"` {
				t.Fatalf("If-None-Match = %q", got)
			}
			return response(http.StatusNotModified, "", nil), nil
		})},
	}
	tool := manifest.Tool{
		PinnedVersion: "1.0.0",
		UpdateSource: manifest.UpdateSource{
			Type: "docker_registry", Repository: "registry.example/acme/tool",
			TagPattern: `^\d+\.\d+\.\d+$`,
		},
	}
	for range 2 {
		got := checker.Check(context.Background(), "acme-tool", tool)
		if got.Status != models.ScannerVersionUpdateAvailable || got.LatestVersion != "1.2.0" {
			t.Fatalf("result = %#v", got)
		}
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

func TestCheckerRustStableChannel(t *testing.T) {
	checker := Checker{Client: fixtureClient(t, map[string]string{
		"https://static.rust-lang.org/dist/channel-rust-stable.toml": `
manifest-version = "2"
[pkg.rust]
version = "1.84.1 (e71f9a9a9 2025-01-27)"
[pkg.rust.target.x86_64-unknown-linux-gnu]
available = true
`,
	})}
	got := checker.Check(context.Background(), "clippy", manifest.Tool{
		PinnedVersion: "1.82.0",
		UpdateSource: manifest.UpdateSource{
			Type: "rust_channel", Channel: "stable",
		},
	})
	if got.Status != models.ScannerVersionUpdateAvailable || got.LatestVersion != "1.84.1" {
		t.Fatalf("unexpected rust channel result: %#v", got)
	}
}

func TestCheckerDebianPackageWithoutDirectPin(t *testing.T) {
	checker := Checker{Client: fixtureClient(t, map[string]string{
		"https://sources.debian.org/api/src/cppcheck/": `{
			"versions":[
				{"version":"2.13.0-2"},
				{"version":"1:2.17.1-1"}
			]
		}`,
	})}
	got := checker.Check(context.Background(), "cppcheck", manifest.Tool{
		UpdateSource: manifest.UpdateSource{
			Type: "debian_package", Package: "cppcheck",
		},
	})
	if got.Status != models.ScannerVersionUnknown || got.LatestVersion != "2.17.1" {
		t.Fatalf("unexpected Debian package result: %#v", got)
	}
	if got.LatestReference != "debian:cppcheck@2.17.1" {
		t.Fatalf("latest reference = %q", got.LatestReference)
	}
}

func TestCheckerToolchainNPM(t *testing.T) {
	checker := Checker{Client: fixtureClient(t, map[string]string{
		"https://registry.npmjs.org/npm": `{"dist-tags":{"latest":"11.4.2"}}`,
	})}
	got := checker.Check(context.Background(), "npm-audit", manifest.Tool{
		UpdateSource: manifest.UpdateSource{
			Type: "toolchain", Package: "npm",
		},
	})
	if got.Status != models.ScannerVersionUnknown || got.LatestVersion != "11.4.2" {
		t.Fatalf("unexpected toolchain result: %#v", got)
	}
}

func TestCheckerUnsupportedSourceFailsExplicitly(t *testing.T) {
	got := (Checker{}).Check(context.Background(), "demo", manifest.Tool{
		PinnedVersion: "1.0.0",
		UpdateSource:  manifest.UpdateSource{Type: "future_registry"},
	})
	if got.Status != models.ScannerVersionCheckFailed || !strings.Contains(got.Error, "unsupported update source") {
		t.Fatalf("unsupported result = %#v", got)
	}
}

func TestNormalizeDebianVersion(t *testing.T) {
	tests := map[string]string{
		"1:2.17.1-1":       "2.17.1",
		"2.12.0+dfsg-3+b1": "2.12.0",
		" 3.0.0 ":          "3.0.0",
	}
	for input, want := range tests {
		if got := normalizeDebianVersion(input); got != want {
			t.Fatalf("normalizeDebianVersion(%q) = %q, want %q", input, got, want)
		}
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

func publicLookup(context.Context, string) ([]net.IP, error) {
	return []net.IP{net.ParseIP("8.8.8.8")}, nil
}
