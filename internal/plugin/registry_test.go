package plugin

import (
	"context"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

// --- Mock Plugin ---

type mockPlugin struct {
	name      string
	category  models.Category
	languages []models.Language
	available bool
}

func (m *mockPlugin) Name() string                { return m.name }
func (m *mockPlugin) Category() models.Category   { return m.category }
func (m *mockPlugin) Languages() []models.Language { return m.languages }
func (m *mockPlugin) CheckAvailable() bool         { return m.available }

func (m *mockPlugin) Execute(_ context.Context, _ models.ExecuteOpts) ([]models.Finding, error) {
	return nil, nil
}

// --- Tests ---

func TestRegister(t *testing.T) {
	t.Run("register and retrieve by name", func(t *testing.T) {
		r := NewRegistry()
		p := &mockPlugin{name: "semgrep", category: models.CategorySAST, available: true}
		r.Register(p)

		got, err := r.Get("semgrep")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Name() != "semgrep" {
			t.Errorf("got name %q, want %q", got.Name(), "semgrep")
		}
	})

	t.Run("get unknown plugin returns error", func(t *testing.T) {
		r := NewRegistry()
		_, err := r.Get("nonexistent")
		if err == nil {
			t.Error("expected error for unknown plugin")
		}
	})

	t.Run("register overwrites existing", func(t *testing.T) {
		r := NewRegistry()
		p1 := &mockPlugin{name: "tool", category: models.CategorySAST, available: true}
		p2 := &mockPlugin{name: "tool", category: models.CategorySCA, available: false}
		r.Register(p1)
		r.Register(p2)

		got, err := r.Get("tool")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Category() != models.CategorySCA {
			t.Errorf("expected overwritten plugin with category %s, got %s", models.CategorySCA, got.Category())
		}
	})
}

func TestGetByLanguage(t *testing.T) {
	r := NewRegistry()
	goPlugin := &mockPlugin{name: "gosec", languages: []models.Language{models.LangGo}, available: true}
	pyPlugin := &mockPlugin{name: "bandit", languages: []models.Language{models.LangPython}, available: true}
	allPlugin := &mockPlugin{name: "gitleaks", languages: nil, available: true} // nil = supports all
	multiPlugin := &mockPlugin{name: "semgrep", languages: []models.Language{models.LangPython, models.LangGo, models.LangJavaScript}, available: true}

	r.Register(goPlugin)
	r.Register(pyPlugin)
	r.Register(allPlugin)
	r.Register(multiPlugin)

	t.Run("Go plugins include go-specific and all-lang", func(t *testing.T) {
		result := r.GetByLanguage(models.LangGo)
		names := pluginNames(result)
		if !contains(names, "gosec") {
			t.Error("expected gosec")
		}
		if !contains(names, "gitleaks") {
			t.Error("expected gitleaks (supports all)")
		}
		if !contains(names, "semgrep") {
			t.Error("expected semgrep (supports Go)")
		}
		if contains(names, "bandit") {
			t.Error("did not expect bandit for Go")
		}
	})

	t.Run("Python plugins include python-specific and all-lang", func(t *testing.T) {
		result := r.GetByLanguage(models.LangPython)
		names := pluginNames(result)
		if !contains(names, "bandit") {
			t.Error("expected bandit")
		}
		if !contains(names, "gitleaks") {
			t.Error("expected gitleaks (supports all)")
		}
		if !contains(names, "semgrep") {
			t.Error("expected semgrep (supports Python)")
		}
	})

	t.Run("unsupported language returns only all-lang plugins", func(t *testing.T) {
		result := r.GetByLanguage(models.LangRust)
		names := pluginNames(result)
		if len(names) != 1 || names[0] != "gitleaks" {
			t.Errorf("expected only gitleaks for Rust, got %v", names)
		}
	})
}

func TestGetAll(t *testing.T) {
	t.Run("empty registry returns empty", func(t *testing.T) {
		r := NewRegistry()
		result := r.GetAll()
		if len(result) != 0 {
			t.Errorf("expected 0 plugins, got %d", len(result))
		}
	})

	t.Run("returns all registered plugins", func(t *testing.T) {
		r := NewRegistry()
		r.Register(&mockPlugin{name: "a"})
		r.Register(&mockPlugin{name: "b"})
		r.Register(&mockPlugin{name: "c"})

		result := r.GetAll()
		if len(result) != 3 {
			t.Errorf("expected 3 plugins, got %d", len(result))
		}
	})
}

func TestGetByCategory(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockPlugin{name: "semgrep", category: models.CategorySAST})
	r.Register(&mockPlugin{name: "trivy", category: models.CategorySCA})
	r.Register(&mockPlugin{name: "gosec", category: models.CategorySAST})
	r.Register(&mockPlugin{name: "gitleaks", category: models.CategorySecrets})

	t.Run("filter SAST", func(t *testing.T) {
		result := r.GetByCategory(models.CategorySAST)
		names := pluginNames(result)
		if len(names) != 2 {
			t.Fatalf("expected 2 SAST plugins, got %d: %v", len(names), names)
		}
		if !contains(names, "semgrep") || !contains(names, "gosec") {
			t.Errorf("expected semgrep and gosec, got %v", names)
		}
	})

	t.Run("filter secrets", func(t *testing.T) {
		result := r.GetByCategory(models.CategorySecrets)
		if len(result) != 1 || result[0].Name() != "gitleaks" {
			t.Errorf("expected gitleaks only, got %v", pluginNames(result))
		}
	})

	t.Run("no match returns empty", func(t *testing.T) {
		result := r.GetByCategory(models.CategoryContainer)
		if len(result) != 0 {
			t.Errorf("expected 0, got %d", len(result))
		}
	})
}

// --- Helpers ---

func pluginNames(plugins []models.Plugin) []string {
	names := make([]string, len(plugins))
	for i, p := range plugins {
		names[i] = p.Name()
	}
	return names
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
