package enricher

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ImportGraph tracks intra-repo file dependencies.
type ImportGraph struct {
	Imports    map[string][]string // file -> files it imports (local only)
	ImportedBy map[string][]string // file -> files that import it (reverse)
}

const maxFilesForImportGraph = 10000

// BuildImportGraph constructs an import graph from the repository files.
// Only tracks intra-repo imports. Skips vendor/node_modules.
func BuildImportGraph(repoPath string, files []string, languages map[string]int) *ImportGraph {
	g := &ImportGraph{
		Imports:    make(map[string][]string),
		ImportedBy: make(map[string][]string),
	}

	if len(files) == 0 || len(files) > maxFilesForImportGraph {
		return g
	}

	// Build set of known files for resolution
	knownFiles := make(map[string]bool, len(files))
	for _, f := range files {
		knownFiles[f] = true
	}

	// Also collect all files from repoPath for resolution (walk up to cap)
	allFiles := make(map[string]bool)
	count := 0
	_ = filepath.Walk(repoPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			if info != nil && info.IsDir() {
				name := info.Name()
				if name == "vendor" || name == "node_modules" || name == ".git" || name == "__pycache__" {
					return filepath.SkipDir
				}
			}
			return nil
		}
		count++
		if count > maxFilesForImportGraph {
			return filepath.SkipAll
		}
		rel, _ := filepath.Rel(repoPath, path)
		if rel != "" {
			allFiles[rel] = true
		}
		return nil
	})

	for _, f := range files {
		absPath := f
		if !filepath.IsAbs(f) {
			absPath = filepath.Join(repoPath, f)
		}
		imports := parseImports(absPath, f, repoPath, languages, allFiles)
		if len(imports) > 0 {
			g.Imports[f] = imports
			for _, imp := range imports {
				g.ImportedBy[imp] = append(g.ImportedBy[imp], f)
			}
		}
	}

	return g
}

var (
	goImportRe       = regexp.MustCompile(`^\s*"([^"]+)"`)
	goImportBlockRe  = regexp.MustCompile(`^\s*import\s*\(`)
	goImportSingleRe = regexp.MustCompile(`^\s*import\s+"([^"]+)"`)
	pyImportRe       = regexp.MustCompile(`^\s*(?:from\s+(\S+)\s+import|import\s+(\S+))`)
	jsImportRe       = regexp.MustCompile(`(?:import\s+.*?from\s+['"]([^'"]+)['"]|require\s*\(\s*['"]([^'"]+)['"]\s*\))`)
)

func parseImports(absPath, relPath, repoPath string, languages map[string]int, allFiles map[string]bool) []string {
	ext := strings.ToLower(filepath.Ext(relPath))

	switch ext {
	case ".go":
		return parseGoImports(absPath, relPath, repoPath, allFiles)
	case ".py":
		return parsePythonImports(absPath, allFiles)
	case ".js", ".jsx", ".ts", ".tsx", ".mjs":
		return parseJSImports(absPath, relPath, allFiles)
	}

	return nil
}

func parseGoImports(absPath, relPath, repoPath string, allFiles map[string]bool) []string {
	// #nosec G304 -- reads source files inside the scan target dir
	f, err := os.Open(absPath)
	if err != nil {
		return nil
	}
	defer f.Close()

	// Get module path from go.mod
	modPath := readGoModModule(findGoMod(repoPath))

	var imports []string
	scanner := bufio.NewScanner(f)
	inBlock := false

	for scanner.Scan() {
		line := scanner.Text()

		if goImportBlockRe.MatchString(line) {
			inBlock = true
			continue
		}
		if inBlock && strings.TrimSpace(line) == ")" {
			inBlock = false
			continue
		}

		var importPath string
		if inBlock {
			m := goImportRe.FindStringSubmatch(line)
			if len(m) > 1 {
				importPath = m[1]
			}
		} else {
			m := goImportSingleRe.FindStringSubmatch(line)
			if len(m) > 1 {
				importPath = m[1]
			}
		}

		if importPath == "" || modPath == "" {
			continue
		}

		// Check if this is a local import
		if strings.HasPrefix(importPath, modPath) {
			localPath := strings.TrimPrefix(importPath, modPath+"/")
			// Find any .go files in that directory
			for af := range allFiles {
				if filepath.Dir(af) == localPath && strings.HasSuffix(af, ".go") {
					imports = appendUnique(imports, af)
				}
			}
		}
	}

	return imports
}

func parsePythonImports(absPath string, allFiles map[string]bool) []string {
	// #nosec G304 -- reads source files inside the scan target dir
	f, err := os.Open(absPath)
	if err != nil {
		return nil
	}
	defer f.Close()

	var imports []string
	scanner := bufio.NewScanner(f)

	for scanner.Scan() {
		line := scanner.Text()
		m := pyImportRe.FindStringSubmatch(line)
		if len(m) < 3 {
			continue
		}

		modulePath := m[1]
		if modulePath == "" {
			modulePath = m[2]
		}
		if modulePath == "" {
			continue
		}

		// Convert dotted path to file path
		parts := strings.Split(modulePath, ".")
		// Try as file
		candidate := strings.Join(parts, "/") + ".py"
		if allFiles[candidate] {
			imports = appendUnique(imports, candidate)
			continue
		}
		// Try as package
		candidate = strings.Join(parts, "/") + "/__init__.py"
		if allFiles[candidate] {
			imports = appendUnique(imports, candidate)
		}
	}

	return imports
}

func parseJSImports(absPath, relPath string, allFiles map[string]bool) []string {
	// #nosec G304 -- reads source files inside the scan target dir
	f, err := os.Open(absPath)
	if err != nil {
		return nil
	}
	defer f.Close()

	var imports []string
	scanner := bufio.NewScanner(f)
	dir := filepath.Dir(relPath)

	for scanner.Scan() {
		line := scanner.Text()
		m := jsImportRe.FindStringSubmatch(line)
		if len(m) < 3 {
			continue
		}

		importPath := m[1]
		if importPath == "" {
			importPath = m[2]
		}

		// Only track relative imports
		if !strings.HasPrefix(importPath, ".") {
			continue
		}

		resolved := resolveJSPath(dir, importPath, allFiles)
		if resolved != "" {
			imports = appendUnique(imports, resolved)
		}
	}

	return imports
}

func resolveJSPath(fromDir, importPath string, allFiles map[string]bool) string {
	candidate := filepath.Join(fromDir, importPath)
	candidate = filepath.Clean(candidate)

	// Try exact match
	if allFiles[candidate] {
		return candidate
	}

	// Try with extensions
	for _, ext := range []string{".ts", ".tsx", ".js", ".jsx", ".mjs"} {
		if allFiles[candidate+ext] {
			return candidate + ext
		}
	}

	// Try index files
	for _, idx := range []string{"/index.ts", "/index.tsx", "/index.js", "/index.jsx"} {
		if allFiles[candidate+idx] {
			return candidate + idx
		}
	}

	return ""
}

func appendUnique(slice []string, val string) []string {
	for _, s := range slice {
		if s == val {
			return slice
		}
	}
	return append(slice, val)
}
