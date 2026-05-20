package cli

import (
	"testing"
)

func TestConfigSaveLoadRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cfg := &Config{
		CurrentContext: "staging",
		Contexts: map[string]Context{
			"local":   {Server: "http://localhost:8778", Token: "wolf_local"},
			"staging": {Server: "https://wolf.staging", Token: "wolf_stg"},
		},
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got.CurrentContext != "staging" {
		t.Errorf("current context: got %q", got.CurrentContext)
	}
	if got.Contexts["local"].Server != "http://localhost:8778" {
		t.Errorf("local server did not round-trip: %+v", got.Contexts["local"])
	}

	active, ok := got.Active("")
	if !ok || active.Token != "wolf_stg" {
		t.Errorf("Active() should return the staging context, got %+v ok=%v", active, ok)
	}
	named, ok := got.Active("local")
	if !ok || named.Token != "wolf_local" {
		t.Errorf("Active(\"local\") wrong: %+v ok=%v", named, ok)
	}
}

func TestLoadConfigMissingFileIsEmpty(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig on missing file should not error: %v", err)
	}
	if len(cfg.Contexts) != 0 {
		t.Errorf("expected empty contexts, got %v", cfg.Contexts)
	}
	if _, ok := cfg.Active(""); ok {
		t.Error("Active() on empty config should report not-found")
	}
}
