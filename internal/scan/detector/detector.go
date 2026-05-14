// Package detector provides language, framework, and test-file detection
// by walking a repository's directory tree and classifying each file.
package detector

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/models"
)

// Framework represents a recognised software framework.
type Framework string

const (
	FrameworkDjango  Framework = "django"
	FrameworkFlask   Framework = "flask"
	FrameworkFastAPI Framework = "fastapi"
	FrameworkExpress Framework = "express"
	FrameworkNextJS  Framework = "nextjs"
	FrameworkReact   Framework = "react"
	FrameworkVue     Framework = "vue"
	FrameworkSpring  Framework = "spring"
	FrameworkRails   Framework = "rails"
)

// DetectionResult holds the aggregated output of a repository scan.
type DetectionResult struct {
	Languages   map[models.Language]int // language -> file count
	Frameworks  []string
	TestFiles   []string
	SourceFiles []string
	TotalFiles  int
}

// skipDirs is the set of directory names that should be skipped during a walk.
var skipDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"vendor":       true,
	"__pycache__":  true,
	".venv":        true,
	"venv":         true,
	"build":        true,
	"dist":         true,
	"target":       true,
}

// extToLang maps file extensions (without leading dot) to languages.
var extToLang = map[string]models.Language{
	"py":   models.LangPython,
	"pyw":  models.LangPython,
	"js":   models.LangJavaScript,
	"mjs":  models.LangJavaScript,
	"cjs":  models.LangJavaScript,
	"jsx":  models.LangJavaScript,
	"ts":   models.LangTypeScript,
	"tsx":  models.LangTypeScript,
	"mts":  models.LangTypeScript,
	"cts":  models.LangTypeScript,
	"go":   models.LangGo,
	"rs":   models.LangRust,
	"java": models.LangJava,
	"rb":   models.LangRuby,
	"php":  models.LangPHP,
	"c":    models.LangC,
	"h":    models.LangC,
	"cpp":  models.LangCPP,
	"cc":   models.LangCPP,
	"cxx":  models.LangCPP,
	"hpp":  models.LangCPP,
	"hxx":  models.LangCPP,
	"sh":   models.LangShell,
	"bash": models.LangShell,
	"zsh":  models.LangShell,
}

// Detect walks repoPath, classifies every file by language and test status,
// then detects frameworks. Directories in skipDirs are pruned.
func Detect(repoPath string) (*DetectionResult, error) {
	result := &DetectionResult{
		Languages:   make(map[models.Language]int),
		Frameworks:  []string{},
		TestFiles:   []string{},
		SourceFiles: []string{},
	}

	err := filepath.Walk(repoPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // best-effort: skip unreadable entries
		}

		if info.IsDir() {
			if skipDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}

		// Only count regular files.
		if !info.Mode().IsRegular() {
			return nil
		}

		result.TotalFiles++

		lang := DetectLanguage(path)
		if lang != "" {
			result.Languages[lang]++
		}

		rel, _ := filepath.Rel(repoPath, path)
		if rel == "" {
			rel = path
		}

		if IsTestFile(path) {
			result.TestFiles = append(result.TestFiles, rel)
		} else if lang != "" {
			result.SourceFiles = append(result.SourceFiles, rel)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	result.Frameworks = DetectFrameworks(repoPath, result.Languages)
	return result, nil
}

// DetectLanguage returns the language for filePath based on its extension.
// If the extension is not recognised and the file is executable, it peeks at
// the shebang line to identify shell scripts. Returns "" for unknown files.
func DetectLanguage(filePath string) models.Language {
	ext := strings.TrimPrefix(filepath.Ext(filePath), ".")
	if lang, ok := extToLang[ext]; ok {
		return lang
	}

	// No recognised extension — check for shebang.
	return detectFromShebang(filePath)
}

// detectFromShebang reads the first line of a file looking for a #! line
// that indicates a scripting language.
func detectFromShebang(filePath string) models.Language {
	// #nosec G304 -- reads files inside the scan target dir (the whole point of detection)
	f, err := os.Open(filePath)
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		return ""
	}
	line := scanner.Text()
	if !strings.HasPrefix(line, "#!") {
		return ""
	}

	lower := strings.ToLower(line)
	switch {
	case strings.Contains(lower, "python"):
		return models.LangPython
	case strings.Contains(lower, "node"):
		return models.LangJavaScript
	case strings.Contains(lower, "ruby"):
		return models.LangRuby
	case strings.Contains(lower, "php"):
		return models.LangPHP
	case strings.Contains(lower, "bash"),
		strings.Contains(lower, "/sh"),
		strings.Contains(lower, "zsh"):
		return models.LangShell
	}
	return ""
}

// IsTestFile returns true when filePath matches common test-file naming
// conventions across supported languages.
func IsTestFile(filePath string) bool {
	base := filepath.Base(filePath)
	dir := filepath.Dir(filePath)

	// Directory-based patterns.
	parts := strings.Split(filepath.ToSlash(filePath), "/")
	for _, p := range parts {
		switch p {
		case "__tests__", "tests", "spec":
			// Any source file inside a test directory counts.
			ext := strings.TrimPrefix(filepath.Ext(base), ".")
			if _, ok := extToLang[ext]; ok {
				return true
			}
		}
	}

	// Go: *_test.go
	if strings.HasSuffix(base, "_test.go") {
		return true
	}

	// Python: test_*.py or *_test.py
	if strings.HasSuffix(base, ".py") {
		name := strings.TrimSuffix(base, ".py")
		if strings.HasPrefix(name, "test_") || strings.HasSuffix(name, "_test") {
			return true
		}
	}

	// JS/TS: *.test.{js,ts,jsx,tsx,mjs} or *.spec.{js,ts,jsx,tsx,mjs}
	jsExts := []string{".js", ".ts", ".jsx", ".tsx", ".mjs"}
	for _, ext := range jsExts {
		if strings.HasSuffix(base, ".test"+ext) || strings.HasSuffix(base, ".spec"+ext) {
			return true
		}
	}

	// Java: *Test.java or *Tests.java
	if strings.HasSuffix(base, "Test.java") || strings.HasSuffix(base, "Tests.java") {
		return true
	}

	// Ruby: *_spec.rb or *_test.rb
	if strings.HasSuffix(base, "_spec.rb") || strings.HasSuffix(base, "_test.rb") {
		return true
	}

	// Rust: files inside tests/ already caught above; also mod tests in-file
	// but we only do name-based detection here.

	// PHP: *Test.php
	if strings.HasSuffix(base, "Test.php") {
		return true
	}

	// Check parent dir name as a final heuristic.
	dirBase := filepath.Base(dir)
	if dirBase == "test" || dirBase == "tests" || dirBase == "spec" || dirBase == "__tests__" {
		ext := strings.TrimPrefix(filepath.Ext(base), ".")
		if _, ok := extToLang[ext]; ok {
			return true
		}
	}

	return false
}

// DetectFrameworks inspects well-known config/manifest files in repoPath to
// determine which frameworks are in use. The languages map is used to narrow
// the search space.
func DetectFrameworks(repoPath string, languages map[models.Language]int) []string {
	var frameworks []string
	seen := make(map[Framework]bool)

	add := func(fw Framework) {
		if !seen[fw] {
			seen[fw] = true
			frameworks = append(frameworks, string(fw))
		}
	}

	// --- Python frameworks ---
	if languages[models.LangPython] > 0 {
		detectPythonFrameworks(repoPath, add)
	}

	// --- JavaScript / TypeScript frameworks ---
	if languages[models.LangJavaScript] > 0 || languages[models.LangTypeScript] > 0 {
		detectJSFrameworks(repoPath, add)
	}

	// --- Java frameworks ---
	if languages[models.LangJava] > 0 {
		detectJavaFrameworks(repoPath, add)
	}

	// --- Ruby frameworks ---
	if languages[models.LangRuby] > 0 {
		detectRubyFrameworks(repoPath, add)
	}

	return frameworks
}

// detectPythonFrameworks looks for Django, Flask, and FastAPI markers.
func detectPythonFrameworks(repoPath string, add func(Framework)) {
	// Check requirements.txt, Pipfile, pyproject.toml, setup.cfg, setup.py
	manifestFiles := []string{
		"requirements.txt",
		"Pipfile",
		"pyproject.toml",
		"setup.cfg",
		"setup.py",
	}

	for _, name := range manifestFiles {
		content := readFileContent(filepath.Join(repoPath, name))
		if content == "" {
			continue
		}
		lower := strings.ToLower(content)
		if strings.Contains(lower, "django") {
			add(FrameworkDjango)
		}
		if strings.Contains(lower, "flask") {
			add(FrameworkFlask)
		}
		if strings.Contains(lower, "fastapi") {
			add(FrameworkFastAPI)
		}
	}

	// Also check for manage.py (Django) or app.py imports.
	if fileExists(filepath.Join(repoPath, "manage.py")) {
		content := readFileContent(filepath.Join(repoPath, "manage.py"))
		if strings.Contains(content, "django") {
			add(FrameworkDjango)
		}
	}
}

// detectJSFrameworks looks for Express, Next.js, React, and Vue markers.
func detectJSFrameworks(repoPath string, add func(Framework)) {
	pkgContent := readFileContent(filepath.Join(repoPath, "package.json"))
	if pkgContent == "" {
		return
	}
	lower := strings.ToLower(pkgContent)

	if strings.Contains(lower, "\"express\"") {
		add(FrameworkExpress)
	}
	if strings.Contains(lower, "\"next\"") || strings.Contains(lower, "\"next/") {
		add(FrameworkNextJS)
	}
	if strings.Contains(lower, "\"react\"") || strings.Contains(lower, "\"react-dom\"") {
		add(FrameworkReact)
	}
	if strings.Contains(lower, "\"vue\"") || strings.Contains(lower, "\"@vue/") {
		add(FrameworkVue)
	}

	// next.config.{js,ts,mjs}
	for _, ext := range []string{".js", ".ts", ".mjs"} {
		if fileExists(filepath.Join(repoPath, "next.config"+ext)) {
			add(FrameworkNextJS)
			break
		}
	}

	// nuxt.config / vue.config
	if fileExists(filepath.Join(repoPath, "nuxt.config.js")) || fileExists(filepath.Join(repoPath, "nuxt.config.ts")) {
		add(FrameworkVue)
	}
	if fileExists(filepath.Join(repoPath, "vue.config.js")) {
		add(FrameworkVue)
	}
}

// detectJavaFrameworks looks for Spring markers.
func detectJavaFrameworks(repoPath string, add func(Framework)) {
	// pom.xml
	content := readFileContent(filepath.Join(repoPath, "pom.xml"))
	if strings.Contains(content, "spring-boot") || strings.Contains(content, "springframework") {
		add(FrameworkSpring)
	}
	// build.gradle / build.gradle.kts
	for _, name := range []string{"build.gradle", "build.gradle.kts"} {
		gc := readFileContent(filepath.Join(repoPath, name))
		if strings.Contains(gc, "spring-boot") || strings.Contains(gc, "springframework") {
			add(FrameworkSpring)
		}
	}
}

// detectRubyFrameworks looks for Rails markers.
func detectRubyFrameworks(repoPath string, add func(Framework)) {
	gemfile := readFileContent(filepath.Join(repoPath, "Gemfile"))
	if strings.Contains(gemfile, "rails") {
		add(FrameworkRails)
	}
	if fileExists(filepath.Join(repoPath, "config", "routes.rb")) {
		add(FrameworkRails)
	}
}

// readFileContent returns the full text of a file, or "" on error.
func readFileContent(path string) string {
	// #nosec G304 -- reads files inside the scan target dir (the whole point of detection)
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

// fileExists returns true when path exists and is a regular file.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}
