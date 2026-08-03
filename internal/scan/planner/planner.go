// Package planner explains scanner selection before a scan runs.
package planner

import (
	"sort"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/scannertools/manifest"
)

const (
	ReasonExplicitlySelected  = "explicitly_selected"
	ReasonExplicitlyDisabled  = "explicitly_disabled"
	ReasonLanguageMatch       = "language_match"
	ReasonAllLanguageScanner  = "all_language_scanner"
	ReasonAllScanners         = "all_scanners_requested"
	ReasonNoLanguagesFallback = "no_languages_fallback_all"
	ReasonNotExplicit         = "not_in_explicit_tool_list"
	ReasonNoLanguageMatch     = "no_language_match"
	ReasonUnavailable         = "tool_unavailable"
	ReasonNotRegistered       = "tool_not_registered"
)

type Config struct {
	Registry          *plugin.Registry
	Manifest          *manifest.Manifest
	Languages         []models.Language
	Tools             []string
	DisabledTools     []string
	AllScanners       bool
	CheckAvailability bool
}

type Result struct {
	Run     []Decision `json:"run"`
	Skip    []Decision `json:"skip"`
	Summary Summary    `json:"summary"`
}

type Summary struct {
	RunCount      int      `json:"run_count"`
	SkipCount     int      `json:"skip_count"`
	LanguageCount int      `json:"language_count"`
	ExplicitTools []string `json:"explicit_tools,omitempty"`
	DisabledTools []string `json:"disabled_tools,omitempty"`
	AllScanners   bool     `json:"all_scanners,omitempty"`
}

type Decision struct {
	Tool            string   `json:"tool"`
	DisplayName     string   `json:"display_name,omitempty"`
	Category        string   `json:"category,omitempty"`
	Languages       []string `json:"languages,omitempty"`
	Selected        bool     `json:"selected"`
	Available       *bool    `json:"available,omitempty"`
	ReasonCode      string   `json:"reason_code"`
	Reason          string   `json:"reason"`
	IntegrationTier string   `json:"integration_tier,omitempty"`
	Bucket          string   `json:"bucket,omitempty"`
	Image           string   `json:"image,omitempty"`
	ResourceClass   string   `json:"resource_class,omitempty"`
	DefaultTimeout  string   `json:"default_timeout,omitempty"`
	NetworkRequired bool     `json:"network_required,omitempty"`
	Exclusive       bool     `json:"exclusive,omitempty"`
	PathScope       string   `json:"path_scope"`
}

func Build(cfg Config) Result {
	if cfg.Registry == nil {
		return Result{}
	}

	plugins := cfg.Registry.GetAll()
	sort.Slice(plugins, func(i, j int) bool { return plugins[i].Name() < plugins[j].Name() })
	byName := make(map[string]models.Plugin, len(plugins))
	for _, p := range plugins {
		byName[p.Name()] = p
	}

	disabled := set(cfg.DisabledTools)
	explicit := set(cfg.Tools)
	languages := setLanguages(cfg.Languages)

	var result Result
	result.Summary.LanguageCount = len(languages)
	result.Summary.ExplicitTools = sortedStrings(cfg.Tools)
	result.Summary.DisabledTools = sortedStrings(cfg.DisabledTools)
	result.Summary.AllScanners = cfg.AllScanners

	if len(explicit) > 0 {
		for _, name := range sortedStrings(cfg.Tools) {
			p, ok := byName[name]
			if !ok {
				result.Skip = append(result.Skip, missingDecision(name))
				continue
			}
			result.add(decideExplicit(cfg, p, disabled))
		}
		for _, p := range plugins {
			if explicit[p.Name()] {
				continue
			}
			result.Skip = append(result.Skip, decisionFor(cfg, p, false, ReasonNotExplicit, "not selected because an explicit tool list was provided"))
		}
	} else {
		for _, p := range plugins {
			result.add(decideAuto(cfg, p, languages, disabled))
		}
	}

	sortDecisions(result.Run)
	sortDecisions(result.Skip)
	result.Summary.RunCount = len(result.Run)
	result.Summary.SkipCount = len(result.Skip)
	return result
}

func (r *Result) add(d Decision) {
	if d.Selected {
		r.Run = append(r.Run, d)
	} else {
		r.Skip = append(r.Skip, d)
	}
}

func decideExplicit(cfg Config, p models.Plugin, disabled map[string]bool) Decision {
	if disabled[p.Name()] {
		return decisionFor(cfg, p, false, ReasonExplicitlyDisabled, "explicitly disabled")
	}
	return withAvailability(cfg, p, decisionFor(cfg, p, true, ReasonExplicitlySelected, "explicitly selected"))
}

func decideAuto(cfg Config, p models.Plugin, languages map[models.Language]bool, disabled map[string]bool) Decision {
	if disabled[p.Name()] {
		return decisionFor(cfg, p, false, ReasonExplicitlyDisabled, "explicitly disabled")
	}
	if cfg.AllScanners {
		return withAvailability(cfg, p, decisionFor(cfg, p, true, ReasonAllScanners, "all scanners requested"))
	}
	if len(languages) == 0 {
		return withAvailability(cfg, p, decisionFor(cfg, p, true, ReasonNoLanguagesFallback, "no detected languages; fallback runs every registered scanner"))
	}
	pluginLangs := p.Languages()
	if len(pluginLangs) == 0 {
		return withAvailability(cfg, p, decisionFor(cfg, p, true, ReasonAllLanguageScanner, "scanner supports all languages"))
	}
	for _, lang := range pluginLangs {
		if languages[lang] {
			return withAvailability(cfg, p, decisionFor(cfg, p, true, ReasonLanguageMatch, "scanner supports a detected language"))
		}
	}
	return decisionFor(cfg, p, false, ReasonNoLanguageMatch, "scanner does not support the detected languages")
}

func withAvailability(cfg Config, p models.Plugin, d Decision) Decision {
	if !cfg.CheckAvailability {
		return d
	}
	available := p.CheckAvailable()
	d.Available = &available
	if !available {
		d.Selected = false
		d.ReasonCode = ReasonUnavailable
		d.Reason = "scanner is selected by policy but is not currently available"
	}
	return d
}

func decisionFor(cfg Config, p models.Plugin, selected bool, reasonCode, reason string) Decision {
	d := Decision{
		Tool:          p.Name(),
		Category:      string(p.Category()),
		Languages:     languageStrings(p.Languages()),
		Selected:      selected,
		ReasonCode:    reasonCode,
		Reason:        reason,
		ResourceClass: resourceClassFromCategory(p),
		PathScope:     "repository",
	}
	if cfg.Manifest != nil {
		if tool, ok := cfg.Manifest.Tools[p.Name()]; ok {
			d.DisplayName = tool.DisplayName
			d.IntegrationTier = tool.IntegrationTier
			d.Bucket = tool.Bucket
			d.Image = tool.Image.PinnedReference
			d.ResourceClass = tool.ResourceClass
			d.DefaultTimeout = tool.DefaultTimeout
			d.NetworkRequired = tool.NetworkRequired
			d.Exclusive = tool.Exclusive
			if tool.PathScope != "" {
				d.PathScope = tool.PathScope
			}
		}
	}
	if d.DisplayName == "" {
		d.DisplayName = p.Name()
	}
	return d
}

func missingDecision(name string) Decision {
	return Decision{
		Tool:       name,
		Selected:   false,
		ReasonCode: ReasonNotRegistered,
		Reason:     "tool is not registered",
	}
}

func resourceClassFromCategory(p models.Plugin) string {
	switch p.Category() {
	case models.CategorySCA, models.CategoryContainer, models.CategoryInfra, models.CategoryDAST:
		return "heavy"
	case models.CategorySecrets, models.CategoryQuality, models.CategoryDocs:
		return "light"
	default:
		return "medium"
	}
}

func set(values []string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		if value != "" {
			out[value] = true
		}
	}
	return out
}

func setLanguages(values []models.Language) map[models.Language]bool {
	out := make(map[models.Language]bool, len(values))
	for _, value := range values {
		if value != "" {
			out[value] = true
		}
	}
	return out
}

func languageStrings(values []models.Language) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, string(value))
	}
	sort.Strings(out)
	return out
}

func sortedStrings(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}

func sortDecisions(values []Decision) {
	sort.Slice(values, func(i, j int) bool { return values[i].Tool < values[j].Tool })
}
