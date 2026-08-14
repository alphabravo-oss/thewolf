package enricher

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

func isCommentLine(line string) bool {
	return strings.HasPrefix(line, "//") || strings.HasPrefix(line, "/*")
}

// ExtractModuleName derives the module/package name for a file based on language conventions.
func ExtractModuleName(filePath, repoPath, language string) string {
	switch strings.ToLower(language) {
	case "go":
		return extractGoModule(filePath, repoPath)
	case "python":
		return extractPythonModule(filePath, repoPath)
	case "javascript", "typescript":
		return extractJSModule(filePath, repoPath)
	case "java":
		return extractJavaPackage(filePath)
	case "rust":
		return extractRustCrate(filePath, repoPath)
	default:
		// Fallback: use directory path
		dir := filepath.Dir(filePath)
		if dir == "." {
			return ""
		}
		return dir
	}
}

func extractGoModule(filePath, repoPath string) string {
	// Read go.mod for module path
	modPath := findGoMod(repoPath)
	modulePath := ""
	if modPath != "" {
		modulePath = readGoModModule(modPath)
	}

	// Read package declaration from file
	absPath := filePath
	if !filepath.IsAbs(filePath) {
		absPath = filepath.Join(repoPath, filePath)
	}
	pkgName := readPackageDecl(absPath)

	dir := filepath.Dir(filePath)
	if modulePath != "" && dir != "." {
		return modulePath + "/" + dir
	}
	if pkgName != "" {
		return pkgName
	}
	return dir
}

func findGoMod(repoPath string) string {
	path := filepath.Join(repoPath, "go.mod")
	if _, err := os.Stat(path); err == nil {
		return path
	}
	return ""
}

func readGoModModule(modPath string) string {
	// #nosec G304 -- reads source files inside the scan target dir
	f, err := os.Open(modPath)
	if err != nil {
		return ""
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module"))
		}
	}
	return ""
}

func readPackageDecl(absPath string) string {
	// #nosec G304 -- reads source files inside the scan target dir
	f, err := os.Open(absPath)
	if err != nil {
		return ""
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "package ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "package"))
		}
		if line == "" {
			continue
		}
		if isCommentLine(line) {
			continue
		}
	}
	return ""
}

func extractPythonModule(filePath, repoPath string) string {
	// Convert file path to dotted module name
	rel := filePath
	if filepath.IsAbs(filePath) {
		var err error
		rel, err = filepath.Rel(repoPath, filePath)
		if err != nil {
			rel = filePath
		}
	}
	// Remove .py extension
	rel = strings.TrimSuffix(rel, ".py")
	rel = strings.TrimSuffix(rel, ".pyi")
	// Convert path separators to dots
	parts := strings.Split(filepath.ToSlash(rel), "/")
	// Remove __init__ if present
	if len(parts) > 0 && parts[len(parts)-1] == "__init__" {
		parts = parts[:len(parts)-1]
	}
	return strings.Join(parts, ".")
}

func extractJSModule(filePath, repoPath string) string {
	// Walk up to find nearest package.json
	dir := filepath.Dir(filePath)
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(repoPath, dir)
	}
	for {
		pkgPath := filepath.Join(dir, "package.json")
		if _, err := os.Stat(pkgPath); err == nil {
			// #nosec G304 -- reads source files inside the scan target dir
			// Read name field
			data, err := os.ReadFile(pkgPath)
			if err == nil {
				name := extractJSONField(string(data), "name")
				if name != "" {
					return name
				}
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	// Fallback to directory
	return filepath.Dir(filePath)
}

func extractJSONField(jsonStr, field string) string {
	// Simple extraction without full JSON parse
	key := `"` + field + `"`
	idx := strings.Index(jsonStr, key)
	if idx == -1 {
		return ""
	}
	rest := jsonStr[idx+len(key):]
	// Skip whitespace and colon
	rest = strings.TrimLeft(rest, " \t\n\r:")
	if len(rest) == 0 || rest[0] != '"' {
		return ""
	}
	rest = rest[1:]
	end := strings.Index(rest, `"`)
	if end == -1 {
		return ""
	}
	return rest[:end]
}

func extractJavaPackage(filePath string) string {
	// #nosec G304 -- reads source files inside the scan target dir
	absPath := filePath
	f, err := os.Open(absPath) // #nosec G304 -- path is validated upstream (scan-root / artifact-dir / configured input)
	if err != nil {
		return ""
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "package ") {
			pkg := strings.TrimPrefix(line, "package ")
			pkg = strings.TrimSuffix(pkg, ";")
			return strings.TrimSpace(pkg)
		}
		if line == "" {
			continue
		}
		if isCommentLine(line) {
			continue
		}
		if strings.HasPrefix(line, "*") {
			continue
		}
	}
	return ""
}

func extractRustCrate(filePath, repoPath string) string {
	// Walk up to find nearest Cargo.toml
	dir := filepath.Dir(filePath)
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(repoPath, dir)
	}
	for {
		// #nosec G304 -- reads source files inside the scan target dir
		cargoPath := filepath.Join(dir, "Cargo.toml")
		if _, err := os.Stat(cargoPath); err == nil {
			data, err := os.ReadFile(cargoPath) // #nosec G304 -- path is validated upstream (scan-root / artifact-dir / configured input)
			if err == nil {
				// Simple name extraction from [package] section
				lines := strings.Split(string(data), "\n")
				inPackage := false
				for _, line := range lines {
					line = strings.TrimSpace(line)
					if line == "[package]" {
						inPackage = true
						continue
					}
					if strings.HasPrefix(line, "[") {
						inPackage = false
						continue
					}
					if inPackage && strings.HasPrefix(line, "name") {
						parts := strings.SplitN(line, "=", 2)
						if len(parts) == 2 {
							name := strings.TrimSpace(parts[1])
							name = strings.Trim(name, `"'`)
							return name
						}
					}
				}
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}
