package planner

import (
	"context"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/scannertools/manifest"
)

type mockPlugin struct {
	name      string
	category  models.Category
	languages []models.Language
	available bool
}

func (m mockPlugin) Name() string                 { return m.name }
func (m mockPlugin) Category() models.Category    { return m.category }
func (m mockPlugin) Languages() []models.Language { return m.languages }
func (m mockPlugin) CheckAvailable() bool         { return m.available }
func (m mockPlugin) Execute(context.Context, models.ExecuteOpts) ([]models.Finding, error) {
	return nil, nil
}

func TestBuildAutoLanguagePlan(t *testing.T) {
	reg := registry(
		mockPlugin{name: "gosec", category: models.CategorySAST, languages: []models.Language{models.LangGo}, available: true},
		mockPlugin{name: "bandit", category: models.CategorySAST, languages: []models.Language{models.LangPython}, available: true},
		mockPlugin{name: "gitleaks", category: models.CategorySecrets, available: true},
	)

	result := Build(Config{
		Registry:  reg,
		Manifest:  testManifest(),
		Languages: []models.Language{models.LangGo},
	})

	if result.Summary.RunCount != 2 {
		t.Fatalf("run count = %d, want 2: %+v", result.Summary.RunCount, result)
	}
	if decision(result.Run, "gosec").ReasonCode != ReasonLanguageMatch {
		t.Fatalf("gosec reason = %+v", decision(result.Run, "gosec"))
	}
	if decision(result.Run, "gitleaks").ReasonCode != ReasonAllLanguageScanner {
		t.Fatalf("gitleaks reason = %+v", decision(result.Run, "gitleaks"))
	}
	if decision(result.Skip, "bandit").ReasonCode != ReasonNoLanguageMatch {
		t.Fatalf("bandit reason = %+v", decision(result.Skip, "bandit"))
	}
	if decision(result.Run, "gosec").IntegrationTier != manifest.TierDefault {
		t.Fatalf("expected manifest metadata on gosec decision: %+v", decision(result.Run, "gosec"))
	}
	if decision(result.Run, "gosec").ResourceClass != "medium" || decision(result.Run, "gosec").DefaultTimeout != "10m" {
		t.Fatalf("expected manifest resource metadata on gosec decision: %+v", decision(result.Run, "gosec"))
	}
	if !decision(result.Run, "gitleaks").NetworkRequired {
		t.Fatalf("expected network metadata on gitleaks decision: %+v", decision(result.Run, "gitleaks"))
	}
}

func TestBuildExplicitPlanAndMissingTools(t *testing.T) {
	reg := registry(
		mockPlugin{name: "gosec", category: models.CategorySAST, languages: []models.Language{models.LangGo}, available: true},
		mockPlugin{name: "bandit", category: models.CategorySAST, languages: []models.Language{models.LangPython}, available: true},
	)

	result := Build(Config{
		Registry:      reg,
		Tools:         []string{"gosec", "missing"},
		DisabledTools: []string{"bandit"},
	})

	if decision(result.Run, "gosec").ReasonCode != ReasonExplicitlySelected {
		t.Fatalf("gosec decision = %+v", decision(result.Run, "gosec"))
	}
	if decision(result.Skip, "missing").ReasonCode != ReasonNotRegistered {
		t.Fatalf("missing decision = %+v", decision(result.Skip, "missing"))
	}
	if decision(result.Skip, "bandit").ReasonCode != ReasonNotExplicit {
		t.Fatalf("bandit decision = %+v", decision(result.Skip, "bandit"))
	}
}

func TestBuildAvailabilityCheckMovesUnavailableToSkip(t *testing.T) {
	reg := registry(mockPlugin{name: "gosec", category: models.CategorySAST, languages: []models.Language{models.LangGo}, available: false})

	result := Build(Config{
		Registry:          reg,
		Languages:         []models.Language{models.LangGo},
		CheckAvailability: true,
	})

	d := decision(result.Skip, "gosec")
	if d.ReasonCode != ReasonUnavailable {
		t.Fatalf("decision = %+v", d)
	}
	if d.Available == nil || *d.Available {
		t.Fatalf("availability not recorded: %+v", d)
	}
}

func TestBuildAllScannersPlan(t *testing.T) {
	reg := registry(
		mockPlugin{name: "gosec", category: models.CategorySAST, languages: []models.Language{models.LangGo}, available: true},
		mockPlugin{name: "bandit", category: models.CategorySAST, languages: []models.Language{models.LangPython}, available: true},
	)

	result := Build(Config{
		Registry:    reg,
		Languages:   []models.Language{models.LangGo},
		AllScanners: true,
	})

	if !result.Summary.AllScanners {
		t.Fatalf("summary all_scanners = false")
	}
	if result.Summary.RunCount != 2 || result.Summary.SkipCount != 0 {
		t.Fatalf("summary = %+v", result.Summary)
	}
	if decision(result.Run, "bandit").ReasonCode != ReasonAllScanners {
		t.Fatalf("bandit decision = %+v", decision(result.Run, "bandit"))
	}
}

func registry(plugins ...models.Plugin) *plugin.Registry {
	reg := plugin.NewRegistry()
	for _, p := range plugins {
		reg.Register(p)
	}
	return reg
}

func testManifest() *manifest.Manifest {
	return &manifest.Manifest{Tools: map[string]manifest.Tool{
		"gosec": {
			DisplayName:     "Gosec",
			IntegrationTier: manifest.TierDefault,
			ResourceClass:   "medium",
			DefaultTimeout:  "10m",
		},
		"gitleaks": {
			DisplayName:     "Gitleaks",
			IntegrationTier: manifest.TierUpstream,
			Image:           manifest.Image{PinnedReference: "zricethezav/gitleaks:v8"},
			ResourceClass:   "network",
			DefaultTimeout:  "15m",
			NetworkRequired: true,
		},
	}}
}

func decision(values []Decision, tool string) Decision {
	for _, value := range values {
		if value.Tool == tool {
			return value
		}
	}
	return Decision{}
}
