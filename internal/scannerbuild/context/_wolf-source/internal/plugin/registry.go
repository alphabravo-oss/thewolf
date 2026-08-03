package plugin

import (
	"fmt"
	"sync"

	"github.com/alphabravocompany/thewolf/internal/models"
)

// Registry manages plugin registration and lookup.
type Registry struct {
	mu      sync.RWMutex
	plugins map[string]models.Plugin
}

// NewRegistry creates a new empty plugin registry.
func NewRegistry() *Registry {
	return &Registry{
		plugins: make(map[string]models.Plugin),
	}
}

// Global is the default global plugin registry.
var Global = NewRegistry()

// Register adds a plugin to the registry.
func (r *Registry) Register(p models.Plugin) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.plugins[p.Name()] = p
}

// Get returns a plugin by name.
func (r *Registry) Get(name string) (models.Plugin, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.plugins[name]
	if !ok {
		return nil, fmt.Errorf("plugin %q not found", name)
	}
	return p, nil
}

// GetByLanguage returns all plugins that support the given language.
func (r *Registry) GetByLanguage(lang models.Language) []models.Plugin {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []models.Plugin
	for _, p := range r.plugins {
		langs := p.Languages()
		if len(langs) == 0 {
			// Empty languages means supports all
			result = append(result, p)
			continue
		}
		for _, l := range langs {
			if l == lang {
				result = append(result, p)
				break
			}
		}
	}
	return result
}

// GetByCategory returns all plugins in the given category.
func (r *Registry) GetByCategory(cat models.Category) []models.Plugin {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []models.Plugin
	for _, p := range r.plugins {
		if p.Category() == cat {
			result = append(result, p)
		}
	}
	return result
}

// GetAll returns all registered plugins.
func (r *Registry) GetAll() []models.Plugin {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]models.Plugin, 0, len(r.plugins))
	for _, p := range r.plugins {
		result = append(result, p)
	}
	return result
}

// Register adds a plugin to the global registry.
func Register(p models.Plugin) {
	Global.Register(p)
}
