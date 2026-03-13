package coverage

import (
	"os"
	"path/filepath"
	"strings"
)

// skipDirs is the set of directory names that should be pruned during a walk.
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
	".next":        true,
	".nuxt":        true,
	".cache":       true,
}

// langConfig defines how to identify test files and map them back to source
// files for each supported language.
type langConfig struct {
	name       string
	extensions map[string]bool
	isTest     func(rel string, base string) bool
	// testToSource maps a test file path (relative) to a candidate source file
	// path (relative). Returns "" if no mapping can be derived.
	testToSource func(rel string) string
}

var languages = []langConfig{
	goLang(),
	jsLang(),
	pythonLang(),
	rubyLang(),
	rustLang(),
	javaLang(),
	swiftLang(),
	phpLang(),
	cLang(),
}

// Analyze walks repoPath, classifies files by language, identifies test files,
// maps tests to source files, and returns a CoverageReport.
func Analyze(repoPath string) (*CoverageReport, error) {
	type fileInfo struct {
		rel  string
		lang string
	}

	var sourceFiles []fileInfo
	var testFiles []fileInfo

	err := filepath.Walk(repoPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // best-effort
		}
		if info.IsDir() {
			if skipDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}

		rel, _ := filepath.Rel(repoPath, path)
		if rel == "" {
			rel = path
		}
		// Normalise separators for consistent matching.
		rel = filepath.ToSlash(rel)
		base := filepath.Base(rel)

		for _, lc := range languages {
			ext := strings.TrimPrefix(filepath.Ext(base), ".")
			// For multi-dot extensions like .test.ts, also check the double ext.
			if !lc.extensions[ext] {
				// Check double extension (e.g., "test.ts" -> check if "ts" is valid).
				if !hasLangExtension(base, lc.extensions) {
					continue
				}
			}

			if lc.isTest(rel, base) {
				testFiles = append(testFiles, fileInfo{rel: rel, lang: lc.name})
			} else {
				sourceFiles = append(sourceFiles, fileInfo{rel: rel, lang: lc.name})
			}
			break // first matching language wins
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	// Build a set of all source file paths for quick lookup.
	sourceSet := make(map[string]bool, len(sourceFiles))
	for _, sf := range sourceFiles {
		sourceSet[sf.rel] = true
	}

	// For each test file, find the corresponding source file.
	coveredSources := make(map[string]bool)
	for _, tf := range testFiles {
		for _, lc := range languages {
			if lc.name != tf.lang {
				continue
			}
			if lc.testToSource == nil {
				break
			}
			candidate := lc.testToSource(tf.rel)
			if candidate != "" && sourceSet[candidate] {
				coveredSources[candidate] = true
			}
			break
		}
	}

	// Build per-language stats.
	langSourceFiles := make(map[string][]string)
	langTestCount := make(map[string]int)
	for _, sf := range sourceFiles {
		langSourceFiles[sf.lang] = append(langSourceFiles[sf.lang], sf.rel)
	}
	for _, tf := range testFiles {
		langTestCount[tf.lang]++
	}

	report := &CoverageReport{
		TotalSourceFiles: len(sourceFiles),
		TestFiles:        len(testFiles),
		UncoveredFiles:   []string{},
		ByLanguage:       make(map[string]LanguageCoverage),
	}

	for lang, srcs := range langSourceFiles {
		lc := LanguageCoverage{
			Language:    lang,
			SourceFiles: len(srcs),
			TestFiles:   langTestCount[lang],
		}
		for _, src := range srcs {
			if coveredSources[src] {
				lc.FilesWithTests++
			} else {
				lc.UncoveredFiles = append(lc.UncoveredFiles, src)
			}
		}
		if lc.SourceFiles > 0 {
			lc.CoveragePercent = float64(lc.FilesWithTests) / float64(lc.SourceFiles) * 100
		}
		report.ByLanguage[lang] = lc
		report.FilesWithTests += lc.FilesWithTests
		report.UncoveredFiles = append(report.UncoveredFiles, lc.UncoveredFiles...)
	}

	report.FilesWithoutTests = report.TotalSourceFiles - report.FilesWithTests
	if report.TotalSourceFiles > 0 {
		report.CoveragePercent = float64(report.FilesWithTests) / float64(report.TotalSourceFiles) * 100
	}

	return report, nil
}

// hasLangExtension checks if the base filename has any extension from the set,
// accounting for multi-dot names like foo.test.ts.
func hasLangExtension(base string, exts map[string]bool) bool {
	ext := strings.TrimPrefix(filepath.Ext(base), ".")
	return exts[ext]
}

// --- Language configurations ---

func goLang() langConfig {
	return langConfig{
		name:       "go",
		extensions: map[string]bool{"go": true},
		isTest: func(rel, base string) bool {
			return strings.HasSuffix(base, "_test.go")
		},
		testToSource: func(rel string) string {
			base := filepath.Base(rel)
			dir := filepath.Dir(rel)
			src := strings.TrimSuffix(base, "_test.go") + ".go"
			return filepath.ToSlash(filepath.Join(dir, src))
		},
	}
}

func jsLang() langConfig {
	exts := map[string]bool{"js": true, "ts": true, "jsx": true, "tsx": true, "mjs": true, "mts": true}
	testSuffixes := []string{".test.", ".spec."}
	return langConfig{
		name:       "javascript",
		extensions: exts,
		isTest: func(rel, base string) bool {
			lower := strings.ToLower(base)
			for _, suf := range testSuffixes {
				if strings.Contains(lower, suf) {
					return true
				}
			}
			// Files in __tests__/ directories.
			return strings.Contains(rel, "__tests__/")
		},
		testToSource: func(rel string) string {
			base := filepath.Base(rel)
			dir := filepath.Dir(rel)

			// Handle __tests__/foo.ts -> ../foo.ts
			if strings.Contains(rel, "__tests__/") {
				dir = strings.Replace(dir, "__tests__", "", 1)
				dir = strings.TrimSuffix(dir, "/")
				if dir == "" {
					dir = "."
				}
				return filepath.ToSlash(filepath.Join(dir, base))
			}

			// foo.test.ts -> foo.ts, foo.spec.tsx -> foo.tsx
			for _, suf := range testSuffixes {
				idx := strings.LastIndex(base, suf)
				if idx >= 0 {
					ext := base[idx+len(suf)-1:] // includes the dot
					src := base[:idx] + ext
					return filepath.ToSlash(filepath.Join(dir, src))
				}
			}
			return ""
		},
	}
}

func pythonLang() langConfig {
	return langConfig{
		name:       "python",
		extensions: map[string]bool{"py": true},
		isTest: func(rel, base string) bool {
			name := strings.TrimSuffix(base, ".py")
			if strings.HasPrefix(name, "test_") || strings.HasSuffix(name, "_test") {
				return true
			}
			return strings.Contains(rel, "tests/")
		},
		testToSource: func(rel string) string {
			base := filepath.Base(rel)
			dir := filepath.Dir(rel)
			name := strings.TrimSuffix(base, ".py")

			// test_foo.py -> foo.py
			if strings.HasPrefix(name, "test_") {
				src := strings.TrimPrefix(name, "test_") + ".py"
				// Look in same dir and parent dir.
				candidate := filepath.ToSlash(filepath.Join(dir, src))
				return candidate
			}
			// foo_test.py -> foo.py
			if strings.HasSuffix(name, "_test") {
				src := strings.TrimSuffix(name, "_test") + ".py"
				return filepath.ToSlash(filepath.Join(dir, src))
			}
			// tests/foo.py -> foo.py (in parent)
			if strings.Contains(rel, "tests/") {
				parentDir := strings.Replace(dir, "tests", "", 1)
				parentDir = strings.TrimSuffix(parentDir, "/")
				if parentDir == "" {
					parentDir = "."
				}
				return filepath.ToSlash(filepath.Join(parentDir, base))
			}
			return ""
		},
	}
}

func rubyLang() langConfig {
	return langConfig{
		name:       "ruby",
		extensions: map[string]bool{"rb": true},
		isTest: func(rel, base string) bool {
			if strings.HasSuffix(base, "_spec.rb") || strings.HasSuffix(base, "_test.rb") {
				return true
			}
			return strings.Contains(rel, "spec/") || strings.Contains(rel, "test/")
		},
		testToSource: func(rel string) string {
			base := filepath.Base(rel)
			dir := filepath.Dir(rel)

			if strings.HasSuffix(base, "_spec.rb") {
				src := strings.TrimSuffix(base, "_spec.rb") + ".rb"
				// spec/ -> lib/ is common in Ruby.
				srcDir := strings.Replace(dir, "spec", "lib", 1)
				return filepath.ToSlash(filepath.Join(srcDir, src))
			}
			if strings.HasSuffix(base, "_test.rb") {
				src := strings.TrimSuffix(base, "_test.rb") + ".rb"
				srcDir := strings.Replace(dir, "test", "lib", 1)
				return filepath.ToSlash(filepath.Join(srcDir, src))
			}
			return ""
		},
	}
}

func rustLang() langConfig {
	return langConfig{
		name:       "rust",
		extensions: map[string]bool{"rs": true},
		isTest: func(rel, base string) bool {
			if strings.Contains(rel, "tests/") {
				return true
			}
			return strings.HasSuffix(base, "_test.rs") || strings.HasPrefix(base, "test_")
		},
		testToSource: func(rel string) string {
			base := filepath.Base(rel)
			dir := filepath.Dir(rel)

			if strings.HasSuffix(base, "_test.rs") {
				src := strings.TrimSuffix(base, "_test.rs") + ".rs"
				return filepath.ToSlash(filepath.Join(dir, src))
			}
			// tests/foo.rs -> src/foo.rs
			if strings.Contains(rel, "tests/") {
				srcDir := strings.Replace(dir, "tests", "src", 1)
				return filepath.ToSlash(filepath.Join(srcDir, base))
			}
			return ""
		},
	}
}

func javaLang() langConfig {
	return langConfig{
		name:       "java",
		extensions: map[string]bool{"java": true, "kt": true},
		isTest: func(rel, base string) bool {
			if strings.Contains(rel, "src/test/") {
				return true
			}
			return strings.HasSuffix(base, "Test.java") || strings.HasSuffix(base, "Tests.java") ||
				strings.HasSuffix(base, "Test.kt") || strings.HasSuffix(base, "Tests.kt")
		},
		testToSource: func(rel string) string {
			base := filepath.Base(rel)
			dir := filepath.Dir(rel)

			// src/test/java/com/foo/BarTest.java -> src/main/java/com/foo/Bar.java
			if strings.Contains(rel, "src/test/") {
				srcDir := strings.Replace(dir, "src/test/", "src/main/", 1)
				srcBase := base
				for _, suf := range []string{"Tests.java", "Test.java", "Tests.kt", "Test.kt"} {
					if strings.HasSuffix(base, suf) {
						ext := filepath.Ext(suf) // .java or .kt
						srcBase = strings.TrimSuffix(base, suf) + ext
						break
					}
				}
				return filepath.ToSlash(filepath.Join(srcDir, srcBase))
			}
			// FooTest.java -> Foo.java
			for _, suf := range []string{"Tests.java", "Test.java", "Tests.kt", "Test.kt"} {
				if strings.HasSuffix(base, suf) {
					ext := filepath.Ext(suf)
					src := strings.TrimSuffix(base, suf) + ext
					return filepath.ToSlash(filepath.Join(dir, src))
				}
			}
			return ""
		},
	}
}

func swiftLang() langConfig {
	return langConfig{
		name:       "swift",
		extensions: map[string]bool{"swift": true},
		isTest: func(rel, base string) bool {
			if strings.HasSuffix(base, "Tests.swift") || strings.HasSuffix(base, "Test.swift") {
				return true
			}
			return strings.Contains(rel, "Tests/")
		},
		testToSource: func(rel string) string {
			base := filepath.Base(rel)
			dir := filepath.Dir(rel)

			if strings.HasSuffix(base, "Tests.swift") {
				src := strings.TrimSuffix(base, "Tests.swift") + ".swift"
				srcDir := strings.Replace(dir, "Tests", "Sources", 1)
				return filepath.ToSlash(filepath.Join(srcDir, src))
			}
			if strings.HasSuffix(base, "Test.swift") {
				src := strings.TrimSuffix(base, "Test.swift") + ".swift"
				srcDir := strings.Replace(dir, "Tests", "Sources", 1)
				return filepath.ToSlash(filepath.Join(srcDir, src))
			}
			return ""
		},
	}
}

func phpLang() langConfig {
	return langConfig{
		name:       "php",
		extensions: map[string]bool{"php": true},
		isTest: func(rel, base string) bool {
			if strings.HasSuffix(base, "Test.php") {
				return true
			}
			return strings.Contains(rel, "tests/") || strings.Contains(rel, "test/")
		},
		testToSource: func(rel string) string {
			base := filepath.Base(rel)
			dir := filepath.Dir(rel)

			if strings.HasSuffix(base, "Test.php") {
				src := strings.TrimSuffix(base, "Test.php") + ".php"
				srcDir := strings.Replace(dir, "tests", "src", 1)
				srcDir = strings.Replace(srcDir, "test", "src", 1)
				return filepath.ToSlash(filepath.Join(srcDir, src))
			}
			return ""
		},
	}
}

func cLang() langConfig {
	return langConfig{
		name:       "c/cpp",
		extensions: map[string]bool{"c": true, "cpp": true, "cc": true, "cxx": true, "h": true, "hpp": true, "hxx": true},
		isTest: func(rel, base string) bool {
			name := strings.TrimSuffix(base, filepath.Ext(base))
			if strings.HasSuffix(name, "_test") || strings.HasPrefix(name, "test_") {
				return true
			}
			return strings.Contains(rel, "test/") || strings.Contains(rel, "tests/")
		},
		testToSource: func(rel string) string {
			base := filepath.Base(rel)
			dir := filepath.Dir(rel)
			ext := filepath.Ext(base)
			name := strings.TrimSuffix(base, ext)

			if strings.HasSuffix(name, "_test") {
				src := strings.TrimSuffix(name, "_test") + ext
				return filepath.ToSlash(filepath.Join(dir, src))
			}
			if strings.HasPrefix(name, "test_") {
				src := strings.TrimPrefix(name, "test_") + ext
				return filepath.ToSlash(filepath.Join(dir, src))
			}
			// tests/foo.c -> src/foo.c or ../foo.c
			if strings.Contains(rel, "tests/") || strings.Contains(rel, "test/") {
				srcDir := strings.Replace(dir, "tests", "src", 1)
				srcDir = strings.Replace(srcDir, "test", "src", 1)
				return filepath.ToSlash(filepath.Join(srcDir, base))
			}
			return ""
		},
	}
}
