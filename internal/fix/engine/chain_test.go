package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/ai"
)

// withAuthProber swaps the package auth prober (and clears the cache) for a
// test, restoring both afterward. The replacement NEVER spawns a real CLI.
func withAuthProber(t *testing.T, authed map[string]bool) {
	t.Helper()
	prev := authProber
	resetAuthCache()
	authProber = func(_ context.Context, command string) error {
		if authed[command] {
			return nil
		}
		return errors.New("stub: not authed")
	}
	t.Cleanup(func() {
		authProber = prev
		resetAuthCache()
	})
}

func names(t []SubprocessEngine) []string {
	out := make([]string, len(t))
	for i, e := range t {
		out[i] = e.Name()
	}
	return out
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSelectEngine_AutoOrdersCLIThenAPI(t *testing.T) {
	withAuthProber(t, map[string]bool{"claude": true, "codex": true})
	ch, err := SelectEngine(context.Background(), ChainConfig{
		Engine:   "auto",
		Provider: &fakeProvider{},
	})
	if err != nil {
		t.Fatalf("SelectEngine: %v", err)
	}
	got := names(ch.Tiers())
	want := []string{"claude-code", "codex", "api"}
	if !eq(got, want) {
		t.Fatalf("tier order = %v, want %v", got, want)
	}
}

func TestSelectEngine_SkipsUnauthedCLIs(t *testing.T) {
	// claude unauthed, codex authed → codex then api.
	withAuthProber(t, map[string]bool{"codex": true})
	ch, err := SelectEngine(context.Background(), ChainConfig{
		Engine:   "auto",
		Provider: &fakeProvider{},
	})
	if err != nil {
		t.Fatalf("SelectEngine: %v", err)
	}
	got := names(ch.Tiers())
	want := []string{"codex", "api"}
	if !eq(got, want) {
		t.Fatalf("tier order = %v, want %v", got, want)
	}
}

func TestSelectEngine_CLIUnavailableFallsBackToAPI(t *testing.T) {
	// No CLI authed → only the API tier remains.
	withAuthProber(t, map[string]bool{})
	ch, err := SelectEngine(context.Background(), ChainConfig{
		Engine:   "auto",
		Provider: &fakeProvider{},
	})
	if err != nil {
		t.Fatalf("SelectEngine: %v", err)
	}
	got := names(ch.Tiers())
	if !eq(got, []string{"api"}) {
		t.Fatalf("expected API-only fallback, got %v", got)
	}
	if ch.Current().Name() != "api" {
		t.Errorf("Current() = %s, want api", ch.Current().Name())
	}
}

func TestSelectEngine_NoEngineAvailableIsError(t *testing.T) {
	// No CLI authed and a noop (unconfigured) provider → no tier at all.
	withAuthProber(t, map[string]bool{})
	_, err := SelectEngine(context.Background(), ChainConfig{
		Engine:   "auto",
		Provider: ai.NewNoopProvider(),
	})
	if err == nil {
		t.Fatal("expected an error when no engine is available")
	}
}

func TestSelectEngine_ExplicitAPINeedsProvider(t *testing.T) {
	if _, err := SelectEngine(context.Background(), ChainConfig{
		Engine:   "api",
		Provider: ai.NewNoopProvider(),
	}); err == nil {
		t.Error("explicit api without a provider should error")
	}
	ch, err := SelectEngine(context.Background(), ChainConfig{
		Engine:   "api",
		Provider: &fakeProvider{},
	})
	if err != nil {
		t.Fatalf("SelectEngine: %v", err)
	}
	if !eq(names(ch.Tiers()), []string{"api"}) {
		t.Errorf("explicit api should pin a single api tier, got %v", names(ch.Tiers()))
	}
}

func TestSelectEngine_ExplicitCLIPinsSingleTier(t *testing.T) {
	// Explicit CLI selection does not run the auth probe and does not chain.
	withAuthProber(t, map[string]bool{}) // even with nothing authed
	ch, err := SelectEngine(context.Background(), ChainConfig{Engine: "claude-code"})
	if err != nil {
		t.Fatalf("SelectEngine: %v", err)
	}
	if !eq(names(ch.Tiers()), []string{"claude-code"}) {
		t.Errorf("explicit claude-code should pin a single tier, got %v", names(ch.Tiers()))
	}
}

func TestChain_NextEscalatesThenExhausts(t *testing.T) {
	withAuthProber(t, map[string]bool{"claude": true})
	ch, err := SelectEngine(context.Background(), ChainConfig{
		Engine:   "auto",
		Provider: &fakeProvider{},
	})
	if err != nil {
		t.Fatalf("SelectEngine: %v", err)
	}
	if ch.Current().Name() != "claude-code" {
		t.Fatalf("first tier = %s, want claude-code", ch.Current().Name())
	}
	if nx := ch.Next(); nx == nil || nx.Name() != "api" {
		t.Fatalf("Next() = %v, want api", nx)
	}
	if nx := ch.Next(); nx != nil {
		t.Fatalf("chain should be exhausted, got %s", nx.Name())
	}
	if ch.Current() != nil {
		t.Error("Current() should be nil once exhausted")
	}
}

func TestCLIAuthed_CachesPerProcess(t *testing.T) {
	resetAuthCache()
	calls := 0
	prev := authProber
	authProber = func(_ context.Context, _ string) error {
		calls++
		return nil
	}
	t.Cleanup(func() { authProber = prev; resetAuthCache() })

	if !cliAuthed(context.Background(), "claude") {
		t.Fatal("expected authed")
	}
	if !cliAuthed(context.Background(), "claude") {
		t.Fatal("expected authed (cached)")
	}
	if calls != 1 {
		t.Errorf("auth prober should run once per process, ran %d times", calls)
	}
}
