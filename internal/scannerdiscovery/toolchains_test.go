package scannerdiscovery

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alphabravocompany/thewolf/internal/scannertools/httpcache"
	"github.com/alphabravocompany/thewolf/internal/scannertools/latest"
	scannerlock "github.com/alphabravocompany/thewolf/internal/scannertools/lock"
)

func TestEveryLockedToolchainHasAnExplicitProductionStrategy(t *testing.T) {
	t.Parallel()
	m, lock := repositoryDefinition(t)
	items, _, err := buildItems(m, lock, CompleteScope())
	if err != nil {
		t.Fatal(err)
	}
	toolchains := make(map[string]Item)
	for _, item := range items {
		if item.ID.Kind == ComponentToolchain {
			toolchains[item.ID.Name] = item
		}
	}
	expected := map[string]struct {
		strategy string
		status   Status
	}{
		"go":     {strategy: "exact-go-release", status: StatusCurrent},
		"jdk":    {strategy: "unpinned-debian-suite-package", status: StatusHeld},
		"nodejs": {strategy: "mutable-major-channel", status: StatusHeld},
		"php":    {strategy: "unpinned-debian-suite-package", status: StatusHeld},
		"python": {strategy: "unpinned-debian-suite-package", status: StatusHeld},
		"ruby":   {strategy: "unpinned-debian-suite-package", status: StatusHeld},
		"rust":   {strategy: "exact-rust-channel-release", status: StatusCurrent},
	}
	if len(toolchains) != len(lock.Toolchains) || len(toolchains) != len(expected) {
		t.Fatalf("toolchain coverage changed: items=%d lock=%d expected=%d", len(toolchains), len(lock.Toolchains), len(expected))
	}

	server := newToolchainServer(t,
		goReleaseFixture(toolchains["go"].CurrentValue),
		rustChannelFixture(toolchains["rust"].CurrentValue),
	)
	resolver := ToolchainResolver{Endpoints: map[string]string{
		"go":   server.URL + "/go",
		"rust": server.URL + "/rust",
	}}
	defaults := DefaultResolvers(latest.Checker{}, nil)
	for name, expectedResult := range expected {
		item, ok := toolchains[name]
		if !ok {
			t.Fatalf("expected lock toolchain %q is missing", name)
		}
		defaultResolver := findResolver(defaults, item)
		if defaultResolver == nil || defaultResolver.Name() != resolver.Name() {
			t.Fatalf("%s default resolver = %#v", item.ID.String(), defaultResolver)
		}
		if !resolver.Supports(item) {
			t.Fatalf("%s is not supported", item.ID.String())
		}
		observation, resolveErr := resolver.Resolve(context.Background(), item)
		if resolveErr != nil {
			t.Fatalf("%s resolution: %v", item.ID.String(), resolveErr)
		}
		if observation.Status != expectedResult.status ||
			observation.Status == StatusUnsupported ||
			observation.Evidence.Attributes["strategy"] != expectedResult.strategy {
			t.Fatalf("%s observation = %+v", item.ID.String(), observation)
		}
		if observation.Status == StatusHeld {
			if observation.Evidence.Attributes["review"] != "manual" ||
				observation.Evidence.Detail == "" ||
				observation.AvailableValue != "" {
				t.Fatalf("%s manual hold lacks explicit review evidence: %+v", item.ID.String(), observation)
			}
		} else if !strings.HasPrefix(observation.AvailableDigest, "sha256:") ||
			!strings.HasPrefix(observation.Evidence.ResponseDigest, "sha256:") {
			t.Fatalf("%s exact metadata lacks immutable digests: %+v", item.ID.String(), observation)
		}
	}
}

func TestCompleteDiscoveryTreatsManualToolchainStrategiesAsCovered(t *testing.T) {
	t.Parallel()
	m, lock := repositoryDefinition(t)
	items, _, err := buildItems(m, lock, CompleteScope())
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]Item, len(items))
	for _, item := range items {
		byID[item.ID.String()] = item
	}
	server := newToolchainServer(t,
		goReleaseFixture(byID["toolchain:go"].CurrentValue),
		rustChannelFixture(byID["toolchain:rust"].CurrentValue),
	)
	nonToolchain := resolverFunc{
		name: "fixture-non-toolchain",
		supports: func(item Item) bool {
			return item.ID.Kind != ComponentToolchain
		},
		resolve: func(_ context.Context, item Item) (Observation, error) {
			return Observation{Status: StatusCurrent, AvailableValue: item.CurrentValue}, nil
		},
	}
	engine := testEngine(m, lock,
		nonToolchain,
		ToolchainResolver{Endpoints: map[string]string{
			"go": server.URL + "/go", "rust": server.URL + "/rust",
		}},
	)
	run, err := engine.Discover(context.Background(), CompleteScope())
	if err != nil {
		t.Fatal(err)
	}
	if run.State != RunCompleted || run.Coverage != 1 ||
		run.Counts.Covered != run.Counts.Total ||
		run.Counts.Held != 5 || run.Counts.Unsupported != 0 ||
		run.Counts.Unreachable != 0 || run.Counts.Unknown != 0 {
		t.Fatalf("complete discovery = state=%s coverage=%v counts=%+v", run.State, run.Coverage, run.Counts)
	}
	for _, result := range run.Items {
		if result.Item.ID.Kind == ComponentToolchain &&
			result.Status == StatusHeld &&
			result.Risk.Level != RiskHigh {
			t.Fatalf("%s held risk = %+v", result.Item.ID.String(), result.Risk)
		}
	}
}

func TestExactToolchainFreshnessClassification(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		toolchain  string
		current    string
		goVersions []string
		rust       string
		status     Status
		available  string
	}{
		{
			name: "Go exact current", toolchain: "go", current: "1.26.3",
			goVersions: []string{"1.26.3", "1.25.9"}, status: StatusCurrent, available: "1.26.3",
		},
		{
			name: "Go update", toolchain: "go", current: "1.26.2",
			goVersions: []string{"1.26.3", "1.26.2"}, status: StatusUpdate, available: "1.26.3",
		},
		{
			name: "Go missing current is held", toolchain: "go", current: "1.24.7",
			goVersions: []string{"1.26.3", "1.26.2"}, status: StatusHeld, available: "1.26.3",
		},
		{
			name: "Go non-exact current is held", toolchain: "go", current: "1.26.3rc1",
			goVersions: []string{"1.26.3"}, status: StatusHeld, available: "1.26.3",
		},
		{
			name: "Rust exact current", toolchain: "rust", current: "1.82.0",
			rust: "1.82.0", status: StatusCurrent, available: "1.82.0",
		},
		{
			name: "Rust update", toolchain: "rust", current: "1.82.0",
			rust: "1.83.0", status: StatusUpdate, available: "1.83.0",
		},
		{
			name: "Rust lock ahead is held", toolchain: "rust", current: "1.84.0",
			rust: "1.83.0", status: StatusHeld, available: "1.83.0",
		},
		{
			name: "Rust non-exact current is held", toolchain: "rust", current: "1.83.0-beta.1",
			rust: "1.83.0", status: StatusHeld, available: "1.83.0",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			goBody := goReleaseFixture(test.goVersions...)
			if len(test.goVersions) == 0 {
				goBody = goReleaseFixture("1.26.3")
			}
			rustVersion := test.rust
			if rustVersion == "" {
				rustVersion = "1.82.0"
			}
			server := newToolchainServer(t, goBody, rustChannelFixture(rustVersion))
			resolver := ToolchainResolver{Endpoints: map[string]string{
				"go": server.URL + "/go", "rust": server.URL + "/rust",
			}}
			observation, err := resolver.Resolve(context.Background(), Item{
				ID:           ComponentID{Kind: ComponentToolchain, Name: test.toolchain},
				CurrentValue: test.current,
				Source:       Source{Type: "toolchain"},
				Metadata:     map[string]string{},
			})
			if err != nil {
				t.Fatal(err)
			}
			if observation.Status != test.status || observation.AvailableValue != test.available {
				t.Fatalf("observation = %+v", observation)
			}
			if observation.Status == StatusHeld &&
				observation.Evidence.Attributes["review"] != "manual" {
				t.Fatalf("held observation lacks review marker: %+v", observation)
			}
			risk := ClassifyRisk(
				Item{CurrentValue: test.current, DefinitionRisk: RiskHigh},
				observation,
			)
			if observation.Status == StatusCurrent && risk.Level != RiskNone {
				t.Fatalf("current observation risk = %+v", risk)
			}
			if (observation.Status == StatusUpdate || observation.Status == StatusHeld) &&
				risk.Level != RiskHigh {
				t.Fatalf("%s observation risk = %+v", observation.Status, risk)
			}
		})
	}
}

func TestToolchainNetworkClientIsBoundedAndOriginAllowlisted(t *testing.T) {
	t.Parallel()
	t.Run("response size", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(writer, strings.Repeat("x", 65))
		}))
		t.Cleanup(server.Close)
		resolver := ToolchainResolver{
			Endpoints:        map[string]string{"go": server.URL},
			MaxResponseBytes: 64,
		}
		_, err := resolver.Resolve(context.Background(), toolchainItem("go", "1.26.3"))
		assertClassifiedError(t, err, ErrorInvalidResponse, "maximum size")
	})

	t.Run("cross origin redirect", func(t *testing.T) {
		t.Parallel()
		var attackerRequests atomic.Int32
		attacker := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			attackerRequests.Add(1)
		}))
		t.Cleanup(attacker.Close)
		source := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			http.Redirect(writer, request, attacker.URL+"/metadata", http.StatusFound)
		}))
		t.Cleanup(source.Close)
		resolver := ToolchainResolver{Endpoints: map[string]string{"go": source.URL}}
		_, err := resolver.Resolve(context.Background(), toolchainItem("go", "1.26.3"))
		if err == nil || !strings.Contains(err.Error(), "configured origin") {
			t.Fatalf("redirect error = %v", err)
		}
		if attackerRequests.Load() != 0 {
			t.Fatalf("cross-origin redirect sent %d attacker requests", attackerRequests.Load())
		}
	})

	t.Run("item metadata cannot select destination", func(t *testing.T) {
		t.Parallel()
		var attackerRequests atomic.Int32
		attacker := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			attackerRequests.Add(1)
		}))
		t.Cleanup(attacker.Close)
		source := newToolchainServer(t, goReleaseFixture("1.26.3"), rustChannelFixture("1.82.0"))
		item := toolchainItem("go", "1.26.3")
		item.Source.URL = attacker.URL + "/metadata"
		resolver := ToolchainResolver{Endpoints: map[string]string{"go": source.URL + "/go"}}
		observation, err := resolver.Resolve(context.Background(), item)
		if err != nil || observation.Status != StatusCurrent {
			t.Fatalf("observation=%+v err=%v", observation, err)
		}
		if attackerRequests.Load() != 0 {
			t.Fatalf("item-selected endpoint received %d requests", attackerRequests.Load())
		}
	})

	t.Run("request timeout", func(t *testing.T) {
		t.Parallel()
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			<-request.Context().Done()
		}))
		t.Cleanup(server.Close)
		resolver := ToolchainResolver{
			Endpoints:      map[string]string{"go": server.URL},
			RequestTimeout: 10 * time.Millisecond,
		}
		started := time.Now()
		_, err := resolver.Resolve(context.Background(), toolchainItem("go", "1.26.3"))
		if err == nil || time.Since(started) > time.Second {
			t.Fatalf("bounded request err=%v elapsed=%s", err, time.Since(started))
		}
		assertClassifiedError(t, err, ErrorTransientNetwork, "deadline")
	})

	t.Run("unconfigured online source becomes manual hold", func(t *testing.T) {
		t.Parallel()
		resolver := ToolchainResolver{Endpoints: map[string]string{}}
		observation, err := resolver.Resolve(context.Background(), toolchainItem("go", "1.26.3"))
		if err != nil || observation.Status != StatusHeld ||
			observation.Evidence.Attributes["review"] != "manual" {
			t.Fatalf("observation=%+v err=%v", observation, err)
		}
	})
}

func TestToolchainResolverReusesValidatedMetadataOnNotModified(t *testing.T) {
	var calls atomic.Int32
	body := goReleaseFixture("1.26.3", "1.25.9")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch calls.Add(1) {
		case 1:
			writer.Header().Set("ETag", `"go-releases-v1"`)
			_, _ = writer.Write(body)
		case 2:
			if got := request.Header.Get("If-None-Match"); got != `"go-releases-v1"` {
				t.Fatalf("If-None-Match = %q", got)
			}
			writer.WriteHeader(http.StatusNotModified)
		default:
			t.Fatalf("unexpected call %d", calls.Load())
		}
	}))
	defer server.Close()
	resolver := ToolchainResolver{
		Endpoints: map[string]string{"go": server.URL},
		Cache:     httpcache.NewMemoryStore(),
	}
	for range 2 {
		observation, err := resolver.Resolve(context.Background(), toolchainItem("go", "1.26.3"))
		if err != nil {
			t.Fatal(err)
		}
		if observation.Status != StatusCurrent ||
			observation.AvailableValue != "1.26.3" ||
			observation.Evidence.ETag != `"go-releases-v1"` {
			t.Fatalf("observation = %+v", observation)
		}
	}
}

func TestToolchainErrorsAndEvidenceAreRedactedByEngine(t *testing.T) {
	t.Parallel()
	m, lock := repositoryDefinition(t)
	t.Run("transport error", func(t *testing.T) {
		client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("Authorization: Bearer transport-secret password=also-secret")
		})}
		engine := testEngine(m, lock, ToolchainResolver{Client: client})
		engine.Config.MaxAttempts = 1
		run, err := engine.Discover(context.Background(), Scope{
			Mode: ScopeSelected,
			Components: []ComponentID{{
				Kind: ComponentToolchain, Name: "go",
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		encoded, marshalErr := json.Marshal(run.Items[0])
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if strings.Contains(string(encoded), "transport-secret") ||
			strings.Contains(string(encoded), "also-secret") ||
			!strings.Contains(run.Items[0].Error, "[REDACTED]") {
			t.Fatalf("unredacted result = %s", encoded)
		}
	})

	t.Run("invalid response evidence", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("ETag", "Bearer evidence-secret")
			_, _ = io.WriteString(writer, `{"token":"body-secret"}`)
		}))
		t.Cleanup(server.Close)
		engine := testEngine(m, lock, ToolchainResolver{
			Endpoints: map[string]string{"go": server.URL},
		})
		engine.Config.MaxAttempts = 1
		run, err := engine.Discover(context.Background(), Scope{
			Mode: ScopeSelected,
			Components: []ComponentID{{
				Kind: ComponentToolchain, Name: "go",
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		encoded, marshalErr := json.Marshal(run.Items[0])
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		for _, secret := range []string{"evidence-secret", "body-secret"} {
			if strings.Contains(string(encoded), secret) {
				t.Fatalf("result leaked %q: %s", secret, encoded)
			}
		}
		if !strings.Contains(run.Items[0].Evidence.ETag, "[REDACTED]") {
			t.Fatalf("ETag was not redacted: %+v", run.Items[0].Evidence)
		}
	})

	t.Run("credential endpoint rejected without persistence leak", func(t *testing.T) {
		engine := testEngine(m, lock, ToolchainResolver{
			Endpoints: map[string]string{
				"go": "https://go.dev/dl/?token=endpoint-secret",
			},
		})
		engine.Config.MaxAttempts = 1
		run, err := engine.Discover(context.Background(), Scope{
			Mode: ScopeSelected,
			Components: []ComponentID{{
				Kind: ComponentToolchain, Name: "go",
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		encoded, marshalErr := json.Marshal(run.Items[0])
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if strings.Contains(string(encoded), "endpoint-secret") ||
			run.Items[0].ErrorClass != ErrorInvalidResponse {
			t.Fatalf("credential endpoint result = %s", encoded)
		}
	})
}

func TestToolchainCurrentValueFromSharedVariableIsDeterministic(t *testing.T) {
	t.Parallel()
	values := map[string]string{}
	for iteration := 0; iteration < 100; iteration++ {
		got := toolchainCurrentValue(
			scannerlock.Toolchain{Values: map[string]string{"version_variable": "RUNTIME_VERSION"}},
			map[string]scannerlock.Tool{
				"z": {VersionVariable: "RUNTIME_VERSION", PinnedVersion: "2.0.0"},
				"a": {VersionVariable: "RUNTIME_VERSION", PinnedVersion: "1.0.0"},
			},
		)
		values[got] = ""
	}
	if len(values) != 1 {
		t.Fatalf("divergent shared variable produced nondeterministic values: %v", values)
	}
	if _, ok := values[""]; !ok {
		t.Fatalf("divergent shared variable was treated as an exact current pin: %v", values)
	}
	if got := toolchainCurrentValue(
		scannerlock.Toolchain{Values: map[string]string{"version_variable": "RUNTIME_VERSION"}},
		map[string]scannerlock.Tool{
			"z": {VersionVariable: "RUNTIME_VERSION", PinnedVersion: "1.0.0"},
			"a": {VersionVariable: "RUNTIME_VERSION", PinnedVersion: "1.0.0"},
		},
	); got != "1.0.0" {
		t.Fatalf("consistent shared variable = %q", got)
	}
}

func newToolchainServer(t *testing.T, goBody, rustBody []byte) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/go":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write(goBody)
		case "/rust":
			writer.Header().Set("Content-Type", "application/toml")
			_, _ = writer.Write(rustBody)
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func goReleaseFixture(versions ...string) []byte {
	type releaseFile struct {
		Filename string `json:"filename"`
		OS       string `json:"os"`
		Arch     string `json:"arch"`
		Kind     string `json:"kind"`
		SHA256   string `json:"sha256"`
	}
	type release struct {
		Version string        `json:"version"`
		Stable  bool          `json:"stable"`
		Files   []releaseFile `json:"files"`
	}
	out := make([]release, 0, len(versions))
	for _, version := range versions {
		out = append(out, release{
			Version: "go" + version, Stable: true,
			Files: []releaseFile{
				{
					Filename: "go" + version + ".linux-amd64.tar.gz",
					OS:       "linux", Arch: "amd64", Kind: "archive",
					SHA256: strings.Repeat("a", 64),
				},
				{
					Filename: "go" + version + ".linux-arm64.tar.gz",
					OS:       "linux", Arch: "arm64", Kind: "archive",
					SHA256: strings.Repeat("b", 64),
				},
			},
		})
	}
	body, err := json.Marshal(out)
	if err != nil {
		panic(err)
	}
	return body
}

func rustChannelFixture(version string) []byte {
	return []byte(`
manifest-version = "2"
date = "2026-07-30"
[pkg.rust]
version = "` + version + ` (ccccccccc 2026-07-30)"
git_commit_hash = "` + strings.Repeat("c", 40) + `"
[pkg.rust.target.x86_64-unknown-linux-gnu]
available = true
hash = "` + strings.Repeat("a", 64) + `"
[pkg.rust.target.aarch64-unknown-linux-gnu]
available = true
hash = "` + strings.Repeat("b", 64) + `"
`)
}

func toolchainItem(name, current string) Item {
	return Item{
		ID:           ComponentID{Kind: ComponentToolchain, Name: name},
		CurrentValue: current,
		Source:       Source{Type: "toolchain"},
		Metadata:     map[string]string{},
	}
}

func assertClassifiedError(t *testing.T, err error, class ErrorClass, contains string) {
	t.Helper()
	var classified *ClassifiedError
	if !errors.As(err, &classified) || classified.Class != class ||
		!strings.Contains(err.Error(), contains) {
		t.Fatalf("error = %#v, want class=%s containing %q", err, class, contains)
	}
}
