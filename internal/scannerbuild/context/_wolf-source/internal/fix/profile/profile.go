// Package profile is the fixer control surface: which harnesses exist, which
// subscription models they expose, and the effort/speed dials t3code-style
// UIs put in front of Claude Code, Codex, and OpenCode.
package profile

import "strings"

// Engine is one agent harness the worker can drive.
type Engine struct {
	Name         string   `json:"name"`
	Command      string   `json:"command,omitempty"`
	Label        string   `json:"label"`
	Auth         string   `json:"auth"` // oauth+api_key | api_key
	Login        []string `json:"login,omitempty"`
	SessionPaths []string `json:"session_paths,omitempty"`
	Models       []Model  `json:"models"`
	Efforts      []Effort `json:"efforts,omitempty"`
}

// Model is a subscription-oriented model the harness accepts.
type Model struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	ContextK int    `json:"context_k,omitempty"`
	Plan     string `json:"plan,omitempty"` // pro | team | api
	Default  bool   `json:"default,omitempty"`
	Speed    string   `json:"speed,omitempty"` // fast | balanced | deep
	Provider string   `json:"provider,omitempty"`
	Efforts  []Effort `json:"efforts,omitempty"`
}

// Effort is a reasoning/speed dial. Claude uses --effort, Codex uses
// model_reasoning_effort, OpenCode uses --variant.
type Effort struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Hint  string `json:"hint,omitempty"`
}

// Setting keys for org-wide defaults (settings KV).
const (
	SettingModel   = "fixer_model"
	SettingEffort  = "fixer_effort"
	SettingVariant = "fixer_variant"
)

// OverlayLive replaces static picker models with what the worker just
// reported (OpenCode `opencode models`, etc.). Unknown harnesses stay
// on the baked catalog.
func OverlayLive(catalog []Engine, live map[string][]Model) []Engine {
	if len(live) == 0 {
		return catalog
	}
	out := append([]Engine(nil), catalog...)
	for i := range out {
		models := live[out[i].Name]
		if len(models) == 0 {
			models = live[out[i].Command]
		}
		if len(models) == 0 {
			continue
		}
		out[i].Models = models
		if efforts := unionEfforts(models); len(efforts) > 0 {
			out[i].Efforts = efforts
		}
	}
	return out
}

func unionEfforts(models []Model) []Effort {
	var out []Effort
	seen := map[string]bool{}
	for _, m := range models {
		for _, e := range m.Efforts {
			if seen[e.ID] {
				continue
			}
			seen[e.ID] = true
			out = append(out, e)
		}
	}
	return out
}

// Catalog is the picker t3code shows: harness → models → effort.
func Catalog() []Engine {
	efforts := []Effort{
		{ID: "low", Label: "Low", Hint: "fastest, cheapest — small mechanical edits"},
		{ID: "medium", Label: "Medium", Hint: "default — everyday fixes"},
		{ID: "high", Label: "High", Hint: "harder bugs, more reasoning"},
		{ID: "xhigh", Label: "Extra high", Hint: "subscription max for tough remediations"},
	}
	return []Engine{
		{
			Name:    "claude-code",
			Command: "claude",
			Label:   "Claude Code",
			Auth:    "oauth+api_key",
			Login:   []string{"claude", "auth", "login"},
			SessionPaths: []string{
				"~/.claude.json",
				"~/.config/claude",
				"~/.claude",
			},
			Efforts: efforts,
			Models: []Model{
				{ID: "sonnet", Label: "Sonnet (latest)", ContextK: 200, Plan: "pro", Speed: "balanced", Default: true, Provider: "anthropic"},
				{ID: "opus", Label: "Opus (latest)", ContextK: 200, Plan: "pro", Speed: "deep", Provider: "anthropic"},
				{ID: "haiku", Label: "Haiku (latest)", ContextK: 200, Plan: "pro", Speed: "fast", Provider: "anthropic"},
				{ID: "claude-sonnet-4-6", Label: "Sonnet 4.6", ContextK: 200, Plan: "pro", Speed: "balanced", Provider: "anthropic"},
				{ID: "claude-opus-4-6", Label: "Opus 4.6", ContextK: 200, Plan: "pro", Speed: "deep", Provider: "anthropic"},
				{ID: "claude-opus-5", Label: "Opus 5", ContextK: 200, Plan: "pro", Speed: "deep", Provider: "anthropic"},
			},
		},
		{
			Name:    "codex",
			Command: "codex",
			Label:   "Codex",
			Auth:    "oauth+api_key",
			Login:   []string{"codex", "login"},
			SessionPaths: []string{
				"~/.codex",
			},
			Efforts: efforts,
			Models: []Model{
				{ID: "gpt-5.4-codex", Label: "GPT-5.4 Codex", ContextK: 256, Plan: "pro", Speed: "balanced", Default: true, Provider: "openai"},
				{ID: "gpt-5.4", Label: "GPT-5.4", ContextK: 256, Plan: "pro", Speed: "deep", Provider: "openai"},
				{ID: "gpt-5.4-mini", Label: "GPT-5.4 Mini", ContextK: 128, Plan: "pro", Speed: "fast", Provider: "openai"},
				{ID: "o4-mini", Label: "o4-mini", ContextK: 128, Plan: "pro", Speed: "fast", Provider: "openai"},
			},
		},
		{
			Name:    "opencode",
			Command: "opencode",
			Label:   "OpenCode",
			Auth:    "oauth+api_key",
			Login:   []string{"opencode", "auth", "login"},
			SessionPaths: []string{
				"~/.local/share/opencode/auth.json",
				"~/.config/opencode",
			},
			Efforts: []Effort{
				{ID: "low", Label: "Low", Hint: "--variant low"},
				{ID: "medium", Label: "Medium", Hint: "--variant medium"},
				{ID: "high", Label: "High", Hint: "--variant high"},
				{ID: "max", Label: "Max", Hint: "--variant max"},
			},
			Models: []Model{
				{ID: "anthropic/claude-sonnet-4-6", Label: "Claude Sonnet (via OpenCode)", ContextK: 200, Plan: "pro", Speed: "balanced", Default: true, Provider: "anthropic"},
				{ID: "anthropic/claude-opus-4-6", Label: "Claude Opus (via OpenCode)", ContextK: 200, Plan: "pro", Speed: "deep", Provider: "anthropic"},
				{ID: "openai/gpt-5.4-codex", Label: "GPT-5.4 Codex (via OpenCode)", ContextK: 256, Plan: "pro", Speed: "balanced", Provider: "openai"},
				{ID: "openai/gpt-5.4", Label: "GPT-5.4 (via OpenCode)", ContextK: 256, Plan: "pro", Speed: "deep", Provider: "openai"},
			},
		},
		{
			Name:  "grok",
			Label: "Grok (xAI)",
			Auth:  "api_key",
			Models: []Model{
				{ID: "grok-4", Label: "Grok 4", ContextK: 256, Plan: "api", Speed: "balanced", Default: true, Provider: "xai"},
				{ID: "grok-3", Label: "Grok 3", ContextK: 128, Plan: "api", Speed: "balanced", Provider: "xai"},
				{ID: "grok-3-mini", Label: "Grok 3 Mini", ContextK: 128, Plan: "api", Speed: "fast", Provider: "xai"},
			},
		},
		{
			Name:  "api",
			Label: "Direct API",
			Auth:  "api_key",
			Models: []Model{
				{ID: "claude-sonnet-4-6", Label: "Anthropic Sonnet (API)", ContextK: 200, Plan: "api", Speed: "balanced", Default: true, Provider: "anthropic"},
				{ID: "gpt-5.4", Label: "OpenAI GPT-5.4 (API)", ContextK: 256, Plan: "api", Speed: "balanced", Provider: "openai"},
				{ID: "grok-4", Label: "Grok 4 (xAI API)", ContextK: 256, Plan: "api", Speed: "balanced", Provider: "xai"},
			},
		},
	}
}

// DefaultModel returns the default model id for an engine name.
func DefaultModel(engine string) string {
	for _, e := range Catalog() {
		if e.Name == engine {
			for _, m := range e.Models {
				if m.Default {
					return m.ID
				}
			}
			if len(e.Models) > 0 {
				return e.Models[0].ID
			}
		}
	}
	return ""
}

// NormalizeEffort maps common aliases onto a dial the CLIs accept.
func NormalizeEffort(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "default", "balanced":
		return "medium"
	case "fast", "min", "minimal":
		return "low"
	case "extra", "extra-high", "extra_high", "x-high":
		return "xhigh"
	case "max", "ultracode", "ultra":
		return "max"
	default:
		return strings.ToLower(strings.TrimSpace(raw))
	}
}

// ByName returns a catalog engine or nil.
func ByName(name string) *Engine {
	for i, e := range Catalog() {
		if e.Name == name || e.Command == name {
			eng := Catalog()[i]
			return &eng
		}
	}
	return nil
}
