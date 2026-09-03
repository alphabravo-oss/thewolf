package profile

import (
	"testing"

	"github.com/alphabravocompany/thewolf/pkg/fixengine"
)

func TestCatalogHasHarnesses(t *testing.T) {
	got := map[string]bool{}
	for _, e := range Catalog() {
		got[e.Name] = true
		if len(e.Models) == 0 {
			t.Fatalf("%s has no models", e.Name)
		}
	}
	for _, name := range []string{"claude-code", "codex", "opencode", "api"} {
		if !got[name] {
			t.Fatalf("missing harness %s", name)
		}
	}
	for _, name := range fixengine.CommunityHarnesses() {
		if name == "api-patch" || name == "auto" {
			continue // picker catalog is the interactive set; these stay Community engines
		}
		if !got[name] {
			t.Fatalf("Community harness %s missing from picker catalog", name)
		}
	}
}

func TestNormalizeEffort(t *testing.T) {
	if NormalizeEffort("fast") != "low" {
		t.Fatal(NormalizeEffort("fast"))
	}
	if NormalizeEffort("x-high") != "xhigh" {
		t.Fatal(NormalizeEffort("x-high"))
	}
	if NormalizeEffort("") != "medium" {
		t.Fatal(NormalizeEffort(""))
	}
}

func TestDefaultModel(t *testing.T) {
	if DefaultModel("claude-code") == "" {
		t.Fatal("expected a default claude model")
	}
}

func TestOverlayLiveReplacesOpenCodeModels(t *testing.T) {
	live := map[string][]Model{
		"opencode": {
			{ID: "openai/gpt-5.6-sol", Label: "GPT-5.6 Sol", Default: true, Efforts: []Effort{{ID: "high", Label: "High"}}},
		},
	}
	got := OverlayLive(Catalog(), live)
	var oc *Engine
	for i := range got {
		if got[i].Name == "opencode" {
			oc = &got[i]
			break
		}
	}
	if oc == nil || len(oc.Models) != 1 || oc.Models[0].ID != "openai/gpt-5.6-sol" {
		t.Fatalf("opencode overlay = %+v", oc)
	}
	if len(oc.Efforts) != 1 || oc.Efforts[0].ID != "high" {
		t.Fatalf("efforts = %+v", oc.Efforts)
	}
}
