// Package mapper — semantic annotation via AI providers.
package mapper

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/ai"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

// FileAnnotation holds the AI-generated semantic classification of a file.
type FileAnnotation struct {
	FilePath    string `json:"file_path"`
	Purpose     string `json:"purpose"`     // controller, model, service, config, migration, test, utility, etc.
	Importance  string `json:"importance"`   // critical, high, normal, low
	Description string `json:"description"` // brief description of what the file does
}

// ---------------------------------------------------------------------------
// Skip patterns — directories/files that should never be annotated.
// ---------------------------------------------------------------------------

var skipPrefixes = []string{
	"vendor/",
	"node_modules/",
	".git/",
	".github/",
	"dist/",
	"build/",
	"__pycache__/",
	".idea/",
	".vscode/",
}

var skipSuffixes = []string{
	".min.js",
	".min.css",
	".map",
	".lock",
	".sum",
	".png",
	".jpg",
	".jpeg",
	".gif",
	".ico",
	".svg",
	".woff",
	".woff2",
	".ttf",
	".eot",
	".pdf",
	".zip",
	".tar",
	".gz",
}

// maxFilesPerBatch controls how many files we send in a single AI prompt.
const maxFilesPerBatch = 80

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

// AnnotateFiles uses an AI provider to classify the purpose of each source
// file in the repo map. It gracefully returns an empty slice when the provider
// is unavailable or returns an error.
func AnnotateFiles(ctx context.Context, provider ai.Provider, rm *RepoMap, repoPath string) ([]FileAnnotation, error) {
	if provider == nil {
		return nil, nil
	}

	// Collect candidate files.
	candidates := candidateFiles(rm)
	if len(candidates) == 0 {
		return nil, nil
	}

	// Build batches.
	var allAnnotations []FileAnnotation
	for start := 0; start < len(candidates); start += maxFilesPerBatch {
		end := start + maxFilesPerBatch
		if end > len(candidates) {
			end = len(candidates)
		}
		batch := candidates[start:end]

		prompt := buildAnnotatePrompt(batch, rm)
		resp, err := provider.Complete(ctx, prompt)
		if err != nil {
			// AI failure is non-fatal — return what we have so far.
			return allAnnotations, nil
		}

		parsed, err := parseAnnotateResponse(resp, batch)
		if err != nil {
			// Parse failure is non-fatal — skip this batch.
			continue
		}
		allAnnotations = append(allAnnotations, parsed...)
	}

	return allAnnotations, nil
}

// ---------------------------------------------------------------------------
// Candidate selection
// ---------------------------------------------------------------------------

// candidateFiles returns the subset of files in the repo map that are worth
// sending to the AI for annotation (skips vendored, generated, binary, etc.).
func candidateFiles(rm *RepoMap) []string {
	seen := make(map[string]struct{})
	var files []string

	// Gather file paths from hashes (most complete source).
	for fp := range rm.FileHashes {
		if shouldSkipAnnotation(fp) {
			continue
		}
		if _, ok := seen[fp]; !ok {
			seen[fp] = struct{}{}
			files = append(files, fp)
		}
	}

	sort.Strings(files)
	return files
}

// shouldSkipAnnotation returns true for files that should not be annotated.
func shouldSkipAnnotation(path string) bool {
	lower := strings.ToLower(path)
	for _, prefix := range skipPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	for _, suffix := range skipSuffixes {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Prompt building
// ---------------------------------------------------------------------------

// buildAnnotatePrompt constructs a prompt asking the AI to classify a batch
// of files. It includes file paths and any known symbols to give the model
// more context.
func buildAnnotatePrompt(files []string, rm *RepoMap) string {
	// Build a quick symbol index: file -> top symbols.
	symbolIndex := make(map[string][]string)
	for _, sym := range rm.Symbols {
		if len(symbolIndex[sym.FilePath]) < 5 {
			symbolIndex[sym.FilePath] = append(symbolIndex[sym.FilePath], fmt.Sprintf("%s (%s)", sym.Name, sym.Kind))
		}
	}

	var b strings.Builder
	b.WriteString("Classify each file by its purpose in the codebase. Respond with ONLY a JSON array (no markdown fences) where each element has:\n")
	b.WriteString(`- "file_path": the exact file path as given`)
	b.WriteString("\n")
	b.WriteString(`- "purpose": one of: controller, model, service, config, migration, test, utility, middleware, handler, repository, schema, script, build, docs, proto, view, component, hook, store, style, type, interface, factory, adapter, cli, main`)
	b.WriteString("\n")
	b.WriteString(`- "importance": one of: critical, high, normal, low`)
	b.WriteString("\n")
	b.WriteString(`- "description": one sentence describing what the file does`)
	b.WriteString("\n\n")
	b.WriteString("Files to classify:\n\n")

	for _, fp := range files {
		fmt.Fprintf(&b, "- %s", fp)
		if syms, ok := symbolIndex[fp]; ok && len(syms) > 0 {
			fmt.Fprintf(&b, " [symbols: %s]", strings.Join(syms, ", "))
		}
		if stats, ok := rm.FileStats[fp]; ok {
			fmt.Fprintf(&b, " [lang: %s, LOC: %d]", stats.Language, stats.Code)
		}
		b.WriteString("\n")
	}

	return b.String()
}

// ---------------------------------------------------------------------------
// Response parsing
// ---------------------------------------------------------------------------

// parseAnnotateResponse parses the AI's JSON response into FileAnnotation
// structs. It validates that returned file paths match the requested batch.
func parseAnnotateResponse(resp string, requestedFiles []string) ([]FileAnnotation, error) {
	resp = strings.TrimSpace(resp)

	// Strip markdown code fences if present.
	if idx := strings.Index(resp, "```json"); idx != -1 {
		resp = resp[idx+7:]
	} else if idx := strings.Index(resp, "```"); idx != -1 {
		resp = resp[idx+3:]
	}
	if idx := strings.LastIndex(resp, "```"); idx != -1 {
		resp = resp[:idx]
	}
	resp = strings.TrimSpace(resp)

	var annotations []FileAnnotation
	if err := json.Unmarshal([]byte(resp), &annotations); err != nil {
		return nil, fmt.Errorf("parse annotation response: %w", err)
	}

	// Build allowed set for validation.
	allowed := make(map[string]struct{}, len(requestedFiles))
	for _, f := range requestedFiles {
		allowed[f] = struct{}{}
	}

	// Filter to only files we actually requested and normalize fields.
	var valid []FileAnnotation
	for _, a := range annotations {
		if _, ok := allowed[a.FilePath]; !ok {
			continue
		}
		a.Purpose = normalizePurpose(a.Purpose)
		a.Importance = normalizeImportance(a.Importance)
		valid = append(valid, a)
	}

	return valid, nil
}

// normalizePurpose ensures the purpose string is one of the accepted values.
func normalizePurpose(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	valid := map[string]bool{
		"controller": true, "model": true, "service": true,
		"config": true, "migration": true, "test": true,
		"utility": true, "middleware": true, "handler": true,
		"repository": true, "schema": true, "script": true,
		"build": true, "docs": true, "proto": true,
		"view": true, "component": true, "hook": true,
		"store": true, "style": true, "type": true,
		"interface": true, "factory": true, "adapter": true,
		"cli": true, "main": true,
	}
	if valid[raw] {
		return raw
	}
	return "utility"
}

// normalizeImportance ensures the importance string is one of the accepted values.
func normalizeImportance(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	switch raw {
	case "critical", "high", "normal", "low":
		return raw
	default:
		return "normal"
	}
}
