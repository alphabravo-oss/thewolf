package scannerdiscovery

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	scannerlock "github.com/alphabravocompany/thewolf/internal/scannertools/lock"
	"github.com/alphabravocompany/thewolf/internal/scannertools/manifest"
)

func TestCompleteScopeIncludesToolsImagesBasesAndToolchains(t *testing.T) {
	m, lock := repositoryDefinition(t)
	engine := testEngine(m, lock, resolverFunc{
		name: "all-current",
		resolve: func(_ context.Context, item Item) (Observation, error) {
			return Observation{Status: StatusCurrent, AvailableValue: item.CurrentValue}, nil
		},
	})
	run, err := engine.Discover(context.Background(), CompleteScope())
	if err != nil {
		t.Fatal(err)
	}
	wantTotal := len(lock.Tools) + len(lock.UpstreamImages) + len(lock.BaseImages) + len(lock.Toolchains)
	if run.Counts.Total != wantTotal || run.Counts.Covered != wantTotal || run.Coverage != 1 {
		t.Fatalf("counts = %+v, coverage=%v, want %d covered", run.Counts, run.Coverage, wantTotal)
	}
	if run.State != RunCompleted {
		t.Fatalf("state = %q", run.State)
	}
	kinds := map[ComponentKind]int{}
	for index, result := range run.Items {
		kinds[result.Item.ID.Kind]++
		if (result.Item.ID.Kind == ComponentBaseImage ||
			result.Item.ID.Kind == ComponentUpstreamImage ||
			result.Item.ID.Kind == ComponentToolchain) &&
			!stringsContains(result.Item.CurrentDigest, "sha256:") {
			t.Fatalf("%s lacks a lock-derived digest: %+v", result.Item.ID.String(), result.Item)
		}
		if index > 0 && run.Items[index-1].Item.ID.String() >= result.Item.ID.String() {
			t.Fatal("results are not sorted deterministically")
		}
	}
	if kinds[ComponentTool] != 49 || kinds[ComponentUpstreamImage] != 22 ||
		kinds[ComponentBaseImage] != 4 || kinds[ComponentToolchain] != 7 {
		t.Fatalf("component kinds = %#v", kinds)
	}
}

func TestSelectedToolIncludesItsUpstreamImage(t *testing.T) {
	m, lock := repositoryDefinition(t)
	engine := testEngine(m, lock, currentResolver())
	run, err := engine.Discover(context.Background(), SelectedTools("semgrep", "bandit", "semgrep"))
	if err != nil {
		t.Fatal(err)
	}
	got := itemIDs(run.Items)
	want := []string{"tool:bandit", "tool:semgrep", "upstream_image:semgrep"}
	if !equalStrings(got, want) {
		t.Fatalf("selected IDs = %v, want %v", got, want)
	}
	if !equalStrings(run.Scope.Tools, []string{"bandit", "semgrep"}) {
		t.Fatalf("normalized tools = %v", run.Scope.Tools)
	}
}

func TestSelectedScopeRejectsUnknownComponent(t *testing.T) {
	m, lock := repositoryDefinition(t)
	engine := testEngine(m, lock, currentResolver())
	_, err := engine.Discover(context.Background(), Scope{
		Mode:       ScopeSelected,
		Components: []ComponentID{{Kind: ComponentBaseImage, Name: "missing"}},
	})
	if err == nil {
		t.Fatal("expected invalid-selection error")
	}
}

func TestPartialCoveragePreservesExplicitSemantics(t *testing.T) {
	m, lock := repositoryDefinition(t)
	statuses := map[string]Status{
		"tool:bandit":   StatusCurrent,
		"tool:brakeman": StatusUpdate,
		"tool:ruff":     StatusYanked,
		"tool:mypy":     StatusUnknown,
	}
	resolver := resolverFunc{
		name: "semantic-fixture",
		resolve: func(_ context.Context, item Item) (Observation, error) {
			switch item.ID.String() {
			case "tool:eslint":
				return Observation{}, &ClassifiedError{Class: ErrorUnavailable, Err: errors.New("503 upstream unavailable")}
			case "tool:codeql":
				return Observation{}, &ClassifiedError{Class: ErrorUnsupported, Err: errors.New("unsupported vendor source")}
			default:
				status := statuses[item.ID.String()]
				return Observation{Status: status, AvailableValue: "9.9.9"}, nil
			}
		},
	}
	engine := testEngine(m, lock, resolver)
	engine.Config.MaxAttempts = 1
	engine.HoldPolicy = StaticHoldPolicy{
		"tool:semgrep": {Held: true, Reason: "major updates require change review"},
	}
	var components []ComponentID
	for _, name := range []string{"bandit", "brakeman", "eslint", "codeql", "semgrep", "ruff", "mypy"} {
		components = append(components, ComponentID{Kind: ComponentTool, Name: name})
	}
	run, err := engine.Discover(context.Background(), Scope{Mode: ScopeSelected, Components: components})
	if err != nil {
		t.Fatal(err)
	}
	if run.State != RunPartial || run.Counts.Total != 7 || run.Counts.Covered != 4 {
		t.Fatalf("run state/counts = %s %+v", run.State, run.Counts)
	}
	if run.Coverage != 4.0/7.0 {
		t.Fatalf("coverage = %v", run.Coverage)
	}
	if run.Counts.Current != 1 || run.Counts.UpdateAvailable != 1 ||
		run.Counts.Unreachable != 1 || run.Counts.Unsupported != 1 ||
		run.Counts.Held != 1 || run.Counts.Yanked != 1 || run.Counts.Unknown != 1 {
		t.Fatalf("semantic counts = %+v", run.Counts)
	}
}

func TestBoundedGlobalAndPerHostConcurrency(t *testing.T) {
	m, lock := repositoryDefinition(t)
	var mu sync.Mutex
	global, maxGlobal := 0, 0
	byHost := map[string]int{}
	maxByHost := map[string]int{}
	resolver := resolverFunc{
		name: "concurrency-probe",
		resolve: func(_ context.Context, item Item) (Observation, error) {
			host := concurrencyHost(item)
			mu.Lock()
			global++
			byHost[host]++
			if global > maxGlobal {
				maxGlobal = global
			}
			if byHost[host] > maxByHost[host] {
				maxByHost[host] = byHost[host]
			}
			mu.Unlock()
			time.Sleep(2 * time.Millisecond)
			mu.Lock()
			global--
			byHost[host]--
			mu.Unlock()
			return Observation{Status: StatusCurrent, AvailableValue: item.CurrentValue}, nil
		},
	}
	engine := testEngine(m, lock, resolver)
	engine.Config.MaxConcurrency = 6
	engine.Config.PerHostConcurrency = 2
	if _, err := engine.Discover(context.Background(), CompleteScope()); err != nil {
		t.Fatal(err)
	}
	if maxGlobal > 6 || maxGlobal < 2 {
		t.Fatalf("max global concurrency = %d", maxGlobal)
	}
	for host, maximum := range maxByHost {
		if maximum > 2 {
			t.Fatalf("host %s max concurrency = %d", host, maximum)
		}
	}
}

func TestTransientRetryUsesBackoffAndEventuallySucceeds(t *testing.T) {
	m, lock := repositoryDefinition(t)
	var mu sync.Mutex
	attempts := 0
	resolver := resolverFunc{
		name: "flaky",
		resolve: func(_ context.Context, item Item) (Observation, error) {
			mu.Lock()
			defer mu.Unlock()
			attempts++
			if attempts < 3 {
				return Observation{}, &ClassifiedError{
					Class: ErrorRateLimited, RetryAfter: 17 * time.Millisecond,
					Err: errors.New("429 token=do-not-persist"),
				}
			}
			return Observation{Status: StatusCurrent, AvailableValue: item.CurrentValue}, nil
		},
	}
	sleeper := &recordingSleeper{}
	engine := testEngine(m, lock, resolver)
	engine.Config.MaxAttempts = 3
	engine.Sleeper = sleeper
	run, err := engine.Discover(context.Background(), SelectedTools("bandit"))
	if err != nil {
		t.Fatal(err)
	}
	if run.Items[0].Status != StatusCurrent || run.Items[0].Attempts != 3 {
		t.Fatalf("result = %+v", run.Items[0])
	}
	if !equalDurations(sleeper.delays, []time.Duration{17 * time.Millisecond, 17 * time.Millisecond}) {
		t.Fatalf("backoff delays = %v", sleeper.delays)
	}
}

func TestExhaustedTransientFailureProvidesRetryAt(t *testing.T) {
	m, lock := repositoryDefinition(t)
	resolver := resolverFunc{
		name: "down",
		resolve: func(context.Context, Item) (Observation, error) {
			return Observation{}, &ClassifiedError{
				Class: ErrorUnavailable, RetryAfter: time.Minute,
				Err: errors.New("Authorization: Bearer top-secret"),
			}
		},
	}
	at := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	engine := testEngine(m, lock, resolver)
	engine.Config.MaxAttempts = 2
	engine.Sleeper = &recordingSleeper{}
	engine.Now = func() time.Time { return at }
	run, err := engine.Discover(context.Background(), SelectedTools("bandit"))
	if err != nil {
		t.Fatal(err)
	}
	result := run.Items[0]
	if result.Status != StatusUnreachable || result.Attempts != 2 || result.RetryAt == nil ||
		!result.RetryAt.Equal(at.Add(time.Minute)) {
		t.Fatalf("result = %+v", result)
	}
	if stringsContains(result.Error, "top-secret") || !stringsContains(result.Error, "[REDACTED]") {
		t.Fatalf("redacted error = %q", result.Error)
	}
}

func TestCancelledRunStillReturnsEverySelectedItem(t *testing.T) {
	m, lock := repositoryDefinition(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	engine := testEngine(m, lock, currentResolver())
	run, err := engine.Discover(ctx, SelectedTools("bandit", "semgrep"))
	if err != nil {
		t.Fatal(err)
	}
	if run.State != RunCancelled || len(run.Items) != 3 {
		t.Fatalf("cancelled run = state %s, items %d", run.State, len(run.Items))
	}
	for _, item := range run.Items {
		if item.Status != StatusUnreachable || item.ErrorClass != ErrorCancelled {
			t.Fatalf("cancelled item = %+v", item)
		}
	}
}

func TestDefaultRetryClassifier(t *testing.T) {
	tests := []struct {
		message string
		class   ErrorClass
		retry   bool
	}{
		{"GET source: 429 Too Many Requests", ErrorRateLimited, true},
		{"dial tcp: i/o timeout", ErrorTransientNetwork, true},
		{"GET source: 503 Service Unavailable", ErrorUnavailable, true},
		{"GET source: 401 Unauthorized", ErrorAuthentication, false},
		{"unsupported update source", ErrorUnsupported, false},
		{"source returned 304 without a usable cache entry", ErrorInvalidResponse, false},
		{"decode malformed response", ErrorUnknown, false},
	}
	classifier := DefaultRetryClassifier{}
	for _, test := range tests {
		got := classifier.Classify(errors.New(test.message))
		if got.Class != test.class || got.Retry != test.retry {
			t.Fatalf("Classify(%q) = %+v", test.message, got)
		}
	}
}

func TestExponentialBackoffCapsAndHonorsRetryAfter(t *testing.T) {
	backoff := ExponentialBackoff{Base: time.Second, Maximum: 4 * time.Second}
	if got := backoff.Delay(1, RetryDecision{}); got != time.Second {
		t.Fatalf("attempt 1 = %s", got)
	}
	if got := backoff.Delay(5, RetryDecision{}); got != 4*time.Second {
		t.Fatalf("capped delay = %s", got)
	}
	if got := backoff.Delay(5, RetryDecision{RetryAfter: 9 * time.Second}); got != 9*time.Second {
		t.Fatalf("retry-after delay = %s", got)
	}
}

func TestRunJSONExcludesInMemoryManifestDefinition(t *testing.T) {
	m, lock := repositoryDefinition(t)
	engine := testEngine(m, lock, currentResolver())
	run, err := engine.Discover(context.Background(), SelectedTools("bandit"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	if stringsContains(string(data), "ToolDefinition") || stringsContains(string(data), "display_name") {
		t.Fatalf("run JSON leaked in-memory definition: %s", data)
	}
}

func TestResultSinkReceivesPersistenceSafeResults(t *testing.T) {
	m, lock := repositoryDefinition(t)
	sink := &recordingSink{}
	engine := testEngine(m, lock, currentResolver())
	engine.ResultSink = sink
	run, err := engine.Discover(context.Background(), SelectedTools("bandit", "semgrep"))
	if err != nil {
		t.Fatal(err)
	}
	if len(sink.results) != len(run.Items) || len(sink.results) != 3 {
		t.Fatalf("sink results = %d, run items = %d", len(sink.results), len(run.Items))
	}
	for _, result := range sink.results {
		if result.Item.ToolDefinition != nil {
			t.Fatalf("sink received runtime manifest definition: %+v", result.Item)
		}
	}
}

func TestResultSinkFailureReturnsCompletedRunAndError(t *testing.T) {
	m, lock := repositoryDefinition(t)
	sink := &recordingSink{err: errors.New("database unavailable")}
	engine := testEngine(m, lock, currentResolver())
	engine.ResultSink = sink
	run, err := engine.Discover(context.Background(), SelectedTools("bandit"))
	if err == nil || !stringsContains(err.Error(), "database unavailable") {
		t.Fatalf("sink error = %v", err)
	}
	if len(run.Items) != 1 || run.State != RunCompleted {
		t.Fatalf("completed run was not returned with sink error: %+v", run)
	}
}

type resolverFunc struct {
	name     string
	supports func(Item) bool
	resolve  func(context.Context, Item) (Observation, error)
}

func (r resolverFunc) Name() string { return r.name }

func (r resolverFunc) Supports(item Item) bool {
	return r.supports == nil || r.supports(item)
}

func (r resolverFunc) Resolve(ctx context.Context, item Item) (Observation, error) {
	return r.resolve(ctx, item)
}

func currentResolver() Resolver {
	return resolverFunc{
		name: "current",
		resolve: func(_ context.Context, item Item) (Observation, error) {
			return Observation{Status: StatusCurrent, AvailableValue: item.CurrentValue}, nil
		},
	}
}

type recordingSleeper struct {
	delays []time.Duration
}

type recordingSink struct {
	results []ItemResult
	err     error
}

func (s *recordingSink) StoreDiscoveryResult(_ context.Context, result ItemResult) error {
	s.results = append(s.results, result)
	return s.err
}

func (s *recordingSleeper) Sleep(_ context.Context, delay time.Duration) error {
	s.delays = append(s.delays, delay)
	return nil
}

func testEngine(m *manifest.Manifest, lock *scannerlock.Lock, resolvers ...Resolver) Engine {
	at := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	return Engine{
		Manifest: m, Lock: lock, Resolvers: resolvers,
		Config: Config{
			MaxConcurrency: 4, PerHostConcurrency: 2,
			PerItemTimeout: time.Second, MaxAttempts: 3,
		},
		Now: func() time.Time { return at },
	}
}

func repositoryDefinition(t *testing.T) (*manifest.Manifest, *scannerlock.Lock) {
	t.Helper()
	root, err := manifest.FindRepoRoot("")
	if err != nil {
		t.Fatal(err)
	}
	m, err := manifest.LoadFile(root + "/scanners/tools.yaml")
	if err != nil {
		t.Fatal(err)
	}
	lock, err := scannerlock.LoadFile(root + "/scanners/scanner-lock.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return m, lock
}

func itemIDs(items []ItemResult) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.Item.ID.String())
	}
	return out
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func equalDurations(left, right []time.Duration) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func stringsContains(value, fragment string) bool {
	for index := 0; index+len(fragment) <= len(value); index++ {
		if value[index:index+len(fragment)] == fragment {
			return true
		}
	}
	return false
}
