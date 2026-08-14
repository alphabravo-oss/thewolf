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

// Framework represents a recognised software framework. Detection is best-
// effort heuristic — we look for the conventional manifest entry, build
// file, or scaffolding file the framework's docs tell you to ship. We
// favor false-negatives over false-positives.
type Framework string

const (
	// --- Python ---
	FrameworkDjango    Framework = "django"
	FrameworkFlask     Framework = "flask"
	FrameworkFastAPI   Framework = "fastapi"
	FrameworkLitestar  Framework = "litestar"
	FrameworkStarlette Framework = "starlette"
	FrameworkTornado   Framework = "tornado"
	FrameworkPyramid   Framework = "pyramid"
	FrameworkSanic     Framework = "sanic"

	// --- JS / TS — HTTP / app ---
	FrameworkExpress Framework = "express"
	FrameworkNestJS  Framework = "nestjs"
	FrameworkKoa     Framework = "koa"
	FrameworkHapi    Framework = "hapi"
	FrameworkFastify Framework = "fastify"

	// --- JS / TS — UI / meta-frameworks ---
	FrameworkNextJS      Framework = "nextjs"
	FrameworkRemix       Framework = "remix"
	FrameworkAstro       Framework = "astro"
	FrameworkReact       Framework = "react"
	FrameworkReactNative Framework = "react-native"
	FrameworkVue         Framework = "vue"
	FrameworkNuxt        Framework = "nuxt"
	FrameworkSvelte      Framework = "svelte"
	FrameworkSvelteKit   Framework = "sveltekit"
	FrameworkAngular     Framework = "angular"
	FrameworkSolid       Framework = "solidjs"
	FrameworkQwik        Framework = "qwik"
	FrameworkElectron    Framework = "electron"

	// --- JS / TS — build tools / routers / state ---
	FrameworkVite           Framework = "vite"
	FrameworkWebpack        Framework = "webpack"
	FrameworkTurborepo      Framework = "turborepo"
	FrameworkTanStackRouter Framework = "tanstack-router"
	FrameworkReactRouter    Framework = "react-router"
	FrameworkRedux          Framework = "redux"
	FrameworkZustand        Framework = "zustand"
	FrameworkTailwind       Framework = "tailwindcss"

	// --- Go ---
	FrameworkGin     Framework = "gin"
	FrameworkChi     Framework = "chi"
	FrameworkEcho    Framework = "echo"
	FrameworkFiber   Framework = "fiber"
	FrameworkGorilla Framework = "gorilla-mux"
	FrameworkCobra   Framework = "cobra-cli"
	FrameworkGorm    Framework = "gorm"
	FrameworkSqlx    Framework = "sqlx"

	// --- Java / Kotlin ---
	FrameworkSpring     Framework = "spring"
	FrameworkQuarkus    Framework = "quarkus"
	FrameworkMicronaut  Framework = "micronaut"
	FrameworkDropwizard Framework = "dropwizard"
	FrameworkKtor       Framework = "ktor"

	// --- Ruby ---
	FrameworkRails   Framework = "rails"
	FrameworkSinatra Framework = "sinatra"
	FrameworkHanami  Framework = "hanami"

	// --- PHP ---
	FrameworkLaravel Framework = "laravel"
	FrameworkSymfony Framework = "symfony"
	FrameworkCakePHP Framework = "cakephp"

	// --- Rust ---
	FrameworkActix  Framework = "actix-web"
	FrameworkAxum   Framework = "axum"
	FrameworkRocket Framework = "rocket"
	FrameworkWarp   Framework = "warp"

	// --- .NET ---
	FrameworkASPNet Framework = "aspnet-core"
	FrameworkBlazor Framework = "blazor"

	// --- Mobile / cross-platform ---
	FrameworkFlutter Framework = "flutter"
	FrameworkSwiftUI Framework = "swiftui"

	// --- Infra / IaC ---
	FrameworkTerraform  Framework = "terraform"
	FrameworkPulumi     Framework = "pulumi"
	FrameworkHelm       Framework = "helm"
	FrameworkAnsible    Framework = "ansible"
	FrameworkDocker     Framework = "docker"
	FrameworkKubernetes Framework = "kubernetes"
)

// DetectionResult holds the aggregated output of a repository scan.
type DetectionResult struct {
	Languages   map[models.Language]int // language -> file count
	Frameworks  []string
	TestFiles   []string
	SourceFiles []string
	TotalFiles  int
}

// skipDirs is the set of directory names that should be skipped during a
// walk. Matches the suppression defaults' directory list so framework
// detection doesn't fingerprint a scanner-fixture package.json (testdata/
// javascript/package.json carries an `"express"` entry that would otherwise
// surface as Express in every wolf scan against this repo).
var skipDirs = map[string]bool{
	".git":          true,
	"node_modules":  true,
	"vendor":        true,
	"__pycache__":   true,
	".venv":         true,
	"venv":          true,
	"build":         true,
	"dist":          true,
	"target":        true,
	"testdata":      true,
	"test-fixtures": true,
	"test_fixtures": true,
	"testFixtures":  true,
	"fixtures":      true,
	"__fixtures__":  true,
	"__snapshots__": true,
	"__mocks__":     true,
	"mocks":         true,
	"examples":      true,
	"example":       true,
	"demo":          true,
	"samples":       true,
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
	defer func() { _ = f.Close() }()

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

// DetectFrameworks walks the repo (skipping the conventional ignore-dirs)
// and inspects every manifest file it finds. Unlike the previous root-only
// implementation, this catches monorepos where the JS app lives under
// /ui/, /web/, /frontend/, /apps/foo/ etc. — the manifest of a sub-app
// is just as authoritative as one at the root.
//
// The `languages` map is used as a coarse filter to skip whole language
// branches when no source files for that language exist; it's an
// optimisation, not a correctness requirement (some frameworks emit
// .yml / .toml only and wouldn't show up as a language).
func DetectFrameworks(repoPath string, languages map[models.Language]int) []string {
	var frameworks []string
	seen := make(map[Framework]bool)
	add := func(fw Framework) {
		if !seen[fw] {
			seen[fw] = true
			frameworks = append(frameworks, string(fw))
		}
	}

	// Find every manifest file in the tree once, then dispatch each to
	// the right language detector. Cheaper than walking the tree per
	// language.
	manifests := findManifests(repoPath)

	if languages[models.LangPython] > 0 {
		for _, p := range manifests.python {
			detectPythonFrameworks(p, add)
		}
		if fileExists(filepath.Join(repoPath, "manage.py")) {
			// classic Django scaffolding marker
			add(FrameworkDjango)
		}
	}

	if languages[models.LangJavaScript] > 0 || languages[models.LangTypeScript] > 0 {
		for _, p := range manifests.npm {
			detectJSFrameworks(p, add)
		}
		// Config files at any depth.
		for _, p := range manifests.jsConfigs {
			detectJSConfigMarkers(p, add)
		}
	}

	if languages[models.LangGo] > 0 {
		for _, p := range manifests.goMods {
			detectGoFrameworks(p, add)
		}
	}

	if languages[models.LangJava] > 0 {
		for _, p := range manifests.javaBuilds {
			detectJavaFrameworks(p, add)
		}
	}

	if languages[models.LangRuby] > 0 {
		for _, p := range manifests.gemfiles {
			detectRubyFrameworks(p, add)
		}
		if fileExists(filepath.Join(repoPath, "config", "routes.rb")) {
			add(FrameworkRails)
		}
	}

	if languages[models.LangPHP] > 0 {
		for _, p := range manifests.composer {
			detectPHPFrameworks(p, add)
		}
	}

	if languages[models.LangRust] > 0 {
		for _, p := range manifests.cargo {
			detectRustFrameworks(p, add)
		}
	}

	// .NET — *.csproj — language detection doesn't track C# right now,
	// so always probe.
	for _, p := range manifests.csproj {
		detectDotNetFrameworks(p, add)
	}

	// IaC / infra signals — independent of any source language. A repo
	// with a Dockerfile + helm chart + terraform module is the kind of
	// thing operators DO want surfaced.
	detectInfraFrameworks(manifests, add)

	return frameworks
}

// manifestSet collects every file path of interest grouped by what
// detector it feeds. Populated once via a single walk.
type manifestSet struct {
	python     []string
	npm        []string // package.json
	jsConfigs  []string // next.config.*, vite.config.*, vue.config.*, nuxt.config.*, svelte.config.*, astro.config.*, angular.json, ...
	goMods     []string
	javaBuilds []string // pom.xml + build.gradle*
	gemfiles   []string
	composer   []string // composer.json
	cargo      []string // Cargo.toml
	csproj     []string // *.csproj
	infra      infraSignals
}

type infraSignals struct {
	terraformFiles bool
	pulumiFiles    bool
	helmChartYAML  bool
	ansibleFiles   bool
	dockerfiles    bool
	k8sManifests   bool
}

// findManifests walks the repo once, collecting every interesting
// manifest path. Honors skipDirs so we don't descend into
// node_modules/, vendor/, etc. Files-only matches are quick stat
// fall-through; we only open the few we actually parse.
func findManifests(repoPath string) manifestSet {
	var m manifestSet
	_ = filepath.WalkDir(repoPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		base := d.Name()
		switch base {
		case "package.json":
			m.npm = append(m.npm, path)
		case "go.mod":
			m.goMods = append(m.goMods, path)
		case "pom.xml", "build.gradle", "build.gradle.kts":
			m.javaBuilds = append(m.javaBuilds, path)
		case "Gemfile":
			m.gemfiles = append(m.gemfiles, path)
		case "composer.json":
			m.composer = append(m.composer, path)
		case "Cargo.toml":
			m.cargo = append(m.cargo, path)
		case "requirements.txt", "Pipfile", "pyproject.toml", "setup.cfg", "setup.py":
			m.python = append(m.python, path)
		case "Dockerfile":
			m.infra.dockerfiles = true
		case "Chart.yaml":
			m.infra.helmChartYAML = true
		case "angular.json":
			m.jsConfigs = append(m.jsConfigs, path)
		}
		// suffix-based matches
		switch {
		case strings.HasSuffix(base, ".csproj"):
			m.csproj = append(m.csproj, path)
		case strings.HasPrefix(base, "Dockerfile."):
			m.infra.dockerfiles = true
		case strings.HasSuffix(base, ".tf"), strings.HasSuffix(base, ".tf.json"):
			m.infra.terraformFiles = true
		case strings.HasSuffix(base, ".tfvars"):
			m.infra.terraformFiles = true
		case base == "Pulumi.yaml", base == "Pulumi.yml":
			m.infra.pulumiFiles = true
		case base == "ansible.cfg", base == "playbook.yml", base == "playbook.yaml":
			m.infra.ansibleFiles = true
		}
		// JS framework config files (any prefix matching the framework's
		// conventional name, any of .js/.ts/.mjs/.cjs).
		for _, prefix := range []string{
			"next.config", "vite.config", "vue.config",
			"nuxt.config", "svelte.config", "astro.config",
			"remix.config", "qwik.config", "solid.config",
		} {
			for _, ext := range []string{".js", ".ts", ".mjs", ".cjs"} {
				if base == prefix+ext {
					m.jsConfigs = append(m.jsConfigs, path)
				}
			}
		}
		return nil
	})
	return m
}

// detectPythonFrameworks reads a requirements/pyproject/etc. manifest
// and matches against the Python framework keywords.
func detectPythonFrameworks(manifestPath string, add func(Framework)) {
	lower := strings.ToLower(readFileContent(manifestPath))
	if lower == "" {
		return
	}
	if strings.Contains(lower, "django") {
		add(FrameworkDjango)
	}
	if strings.Contains(lower, "flask") {
		add(FrameworkFlask)
	}
	if strings.Contains(lower, "fastapi") {
		add(FrameworkFastAPI)
	}
	if strings.Contains(lower, "litestar") {
		add(FrameworkLitestar)
	}
	if strings.Contains(lower, "starlette") {
		add(FrameworkStarlette)
	}
	if strings.Contains(lower, "tornado") {
		add(FrameworkTornado)
	}
	if strings.Contains(lower, "pyramid") {
		add(FrameworkPyramid)
	}
	if strings.Contains(lower, "sanic") {
		add(FrameworkSanic)
	}
}

// detectJSFrameworks reads a package.json and matches against the dep
// list. Substring match on quoted package names is good enough for our
// purpose and dodges the brittle full-JSON parse.
func detectJSFrameworks(manifestPath string, add func(Framework)) {
	content := readFileContent(manifestPath)
	if content == "" {
		return
	}
	lower := strings.ToLower(content)

	check := func(needle string, fw Framework) {
		if strings.Contains(lower, needle) {
			add(fw)
		}
	}
	// HTTP / app frameworks
	check(`"express"`, FrameworkExpress)
	check(`"@nestjs/`, FrameworkNestJS)
	check(`"koa"`, FrameworkKoa)
	check(`"@hapi/`, FrameworkHapi)
	check(`"fastify"`, FrameworkFastify)
	// UI / meta-frameworks
	check(`"next"`, FrameworkNextJS)
	check(`"@remix-run/`, FrameworkRemix)
	check(`"astro"`, FrameworkAstro)
	check(`"react"`, FrameworkReact)
	check(`"react-native"`, FrameworkReactNative)
	check(`"vue"`, FrameworkVue)
	check(`"@vue/`, FrameworkVue)
	check(`"nuxt"`, FrameworkNuxt)
	check(`"svelte"`, FrameworkSvelte)
	check(`"@sveltejs/kit"`, FrameworkSvelteKit)
	check(`"@angular/`, FrameworkAngular)
	check(`"solid-js"`, FrameworkSolid)
	check(`"@builder.io/qwik"`, FrameworkQwik)
	check(`"electron"`, FrameworkElectron)
	// Build / tooling
	check(`"vite"`, FrameworkVite)
	check(`"webpack"`, FrameworkWebpack)
	check(`"turbo"`, FrameworkTurborepo)
	// Routing / state
	check(`"@tanstack/react-router"`, FrameworkTanStackRouter)
	check(`"react-router"`, FrameworkReactRouter)
	check(`"react-router-dom"`, FrameworkReactRouter)
	check(`"redux"`, FrameworkRedux)
	check(`"@reduxjs/toolkit"`, FrameworkRedux)
	check(`"zustand"`, FrameworkZustand)
	check(`"tailwindcss"`, FrameworkTailwind)
}

// detectJSConfigMarkers infers frameworks from the presence of conventional
// config files (vite.config.ts, next.config.js, …). Some projects don't
// list the framework as a top-level dep but have its config — pulling
// the manifest names from above lets us catch them.
func detectJSConfigMarkers(path string, add func(Framework)) {
	base := filepath.Base(path)
	switch {
	case strings.HasPrefix(base, "vite.config"):
		add(FrameworkVite)
	case strings.HasPrefix(base, "next.config"):
		add(FrameworkNextJS)
	case strings.HasPrefix(base, "nuxt.config"):
		add(FrameworkNuxt)
	case strings.HasPrefix(base, "vue.config"):
		add(FrameworkVue)
	case strings.HasPrefix(base, "svelte.config"):
		add(FrameworkSvelte)
	case strings.HasPrefix(base, "astro.config"):
		add(FrameworkAstro)
	case strings.HasPrefix(base, "remix.config"):
		add(FrameworkRemix)
	case strings.HasPrefix(base, "qwik.config"):
		add(FrameworkQwik)
	case strings.HasPrefix(base, "solid.config"):
		add(FrameworkSolid)
	case base == "angular.json":
		add(FrameworkAngular)
	}
}

// detectGoFrameworks reads go.mod and looks for known module paths.
// Substring is safe here because module paths are URL-shaped and
// don't collide with prose.
func detectGoFrameworks(manifestPath string, add func(Framework)) {
	content := readFileContent(manifestPath)
	if content == "" {
		return
	}
	check := func(needle string, fw Framework) {
		if strings.Contains(content, needle) {
			add(fw)
		}
	}
	check("github.com/gin-gonic/gin", FrameworkGin)
	check("github.com/go-chi/chi", FrameworkChi)
	check("github.com/labstack/echo", FrameworkEcho)
	check("github.com/gofiber/fiber", FrameworkFiber)
	check("github.com/gorilla/mux", FrameworkGorilla)
	check("github.com/spf13/cobra", FrameworkCobra)
	check("gorm.io/gorm", FrameworkGorm)
	check("github.com/jmoiron/sqlx", FrameworkSqlx)
}

// detectJavaFrameworks reads pom.xml or build.gradle{,kts} and matches
// against well-known group/artifact substrings.
func detectJavaFrameworks(manifestPath string, add func(Framework)) {
	content := readFileContent(manifestPath)
	if content == "" {
		return
	}
	if strings.Contains(content, "spring-boot") || strings.Contains(content, "springframework") {
		add(FrameworkSpring)
	}
	if strings.Contains(content, "io.quarkus") {
		add(FrameworkQuarkus)
	}
	if strings.Contains(content, "io.micronaut") {
		add(FrameworkMicronaut)
	}
	if strings.Contains(content, "io.dropwizard") {
		add(FrameworkDropwizard)
	}
	if strings.Contains(content, "io.ktor") {
		add(FrameworkKtor)
	}
}

// detectRubyFrameworks reads Gemfile and matches against the Ruby
// framework gem names.
func detectRubyFrameworks(manifestPath string, add func(Framework)) {
	gemfile := readFileContent(manifestPath)
	if gemfile == "" {
		return
	}
	lower := strings.ToLower(gemfile)
	if strings.Contains(lower, "rails") {
		add(FrameworkRails)
	}
	if strings.Contains(lower, "sinatra") {
		add(FrameworkSinatra)
	}
	if strings.Contains(lower, "hanami") {
		add(FrameworkHanami)
	}
}

// detectPHPFrameworks reads composer.json for the major PHP frameworks.
func detectPHPFrameworks(manifestPath string, add func(Framework)) {
	content := readFileContent(manifestPath)
	if content == "" {
		return
	}
	lower := strings.ToLower(content)
	if strings.Contains(lower, `"laravel/framework"`) || strings.Contains(lower, `"laravel/laravel"`) {
		add(FrameworkLaravel)
	}
	if strings.Contains(lower, `"symfony/`) {
		add(FrameworkSymfony)
	}
	if strings.Contains(lower, `"cakephp/`) {
		add(FrameworkCakePHP)
	}
}

// detectRustFrameworks reads Cargo.toml for the major web frameworks.
func detectRustFrameworks(manifestPath string, add func(Framework)) {
	content := readFileContent(manifestPath)
	if content == "" {
		return
	}
	lower := strings.ToLower(content)
	// Substring match on the crate name in deps. Avoids the toml parse.
	if strings.Contains(lower, "actix-web") {
		add(FrameworkActix)
	}
	if strings.Contains(lower, "\naxum") || strings.Contains(lower, "axum =") {
		add(FrameworkAxum)
	}
	if strings.Contains(lower, "rocket") {
		add(FrameworkRocket)
	}
	if strings.Contains(lower, "warp") {
		add(FrameworkWarp)
	}
}

// detectDotNetFrameworks reads a *.csproj XML for ASP.NET Core / Blazor
// SDK refs.
func detectDotNetFrameworks(manifestPath string, add func(Framework)) {
	content := readFileContent(manifestPath)
	if content == "" {
		return
	}
	lower := strings.ToLower(content)
	if strings.Contains(lower, "microsoft.aspnetcore") || strings.Contains(lower, `sdk="microsoft.net.sdk.web"`) {
		add(FrameworkASPNet)
	}
	if strings.Contains(lower, "blazor") {
		add(FrameworkBlazor)
	}
}

// detectInfraFrameworks adds IaC frameworks based on the file-presence
// signals collected during the walk. Independent of any source-language
// fingerprint — a docs-only repo with a single helm chart still gets
// 'helm' surfaced.
func detectInfraFrameworks(m manifestSet, add func(Framework)) {
	if m.infra.terraformFiles {
		add(FrameworkTerraform)
	}
	if m.infra.pulumiFiles {
		add(FrameworkPulumi)
	}
	if m.infra.helmChartYAML {
		add(FrameworkHelm)
	}
	if m.infra.ansibleFiles {
		add(FrameworkAnsible)
	}
	if m.infra.dockerfiles {
		add(FrameworkDocker)
	}
	// Kubernetes manifests are harder to distinguish from generic YAML
	// without parsing. Skip for now — operators usually know they have
	// k8s manifests and the docker/helm signals overlap enough.
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
