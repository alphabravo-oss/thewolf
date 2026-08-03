package enricher

import (
	"encoding/json"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/scan/mapper"
)

// EnrichConfig holds the configuration for enriching findings with code context.
type EnrichConfig struct {
	RepoPath  string
	RepoMap   *mapper.RepoMap
	Languages map[string]int
}

// Enrich enriches findings with code context: module name, enclosing symbol,
// file purpose, and dependents. Findings are modified in place.
func Enrich(findings []models.Finding, cfg EnrichConfig) []models.Finding {
	if cfg.RepoMap == nil {
		return findings
	}

	// Build lookup maps once
	annotationMap := make(map[string]mapper.FileAnnotation)
	if cfg.RepoMap.Annotations != nil {
		for _, a := range cfg.RepoMap.Annotations {
			annotationMap[a.FilePath] = a
		}
	}

	// Detect primary language for module extraction
	primaryLang := detectPrimaryLanguage(cfg.Languages)

	// Build file list from findings
	fileSet := make(map[string]bool)
	for _, f := range findings {
		fileSet[f.FilePath] = true
	}
	files := make([]string, 0, len(fileSet))
	for f := range fileSet {
		files = append(files, f)
	}

	// Build import graph once for all findings
	graph := BuildImportGraph(cfg.RepoPath, files, cfg.Languages)

	// Enrich each finding
	for i := range findings {
		f := &findings[i]

		// 1. Module name
		f.ModuleName = ExtractModuleName(f.FilePath, cfg.RepoPath, primaryLang)

		// 2. Enclosing symbol
		name, kind := ResolveEnclosingSymbol(cfg.RepoMap.Symbols, f.FilePath, f.LineStart)
		f.FunctionName = name
		f.SymbolKind = kind

		// 3. File purpose from annotations
		if ann, ok := annotationMap[f.FilePath]; ok {
			f.FilePurpose = ann.Purpose
		}

		// 4. Dependents
		deps := FindDependents(graph, f.FilePath, 2)
		if deps == nil {
			deps = []string{}
		}
		depsJSON, _ := json.Marshal(deps)
		f.DependentsJSON = string(depsJSON)
	}

	return findings
}

func detectPrimaryLanguage(langs map[string]int) string {
	best := ""
	bestCount := 0
	for lang, count := range langs {
		if count > bestCount {
			best = lang
			bestCount = count
		}
	}
	return best
}
