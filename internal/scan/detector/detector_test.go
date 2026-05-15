package detector

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/models"
)

// ---------------------------------------------------------------------------
// DetectLanguage
// ---------------------------------------------------------------------------

func TestDetectLanguage_Extensions(t *testing.T) {
	tests := []struct {
		file string
		want models.Language
	}{
		{"main.py", models.LangPython},
		{"app.pyw", models.LangPython},
		{"index.js", models.LangJavaScript},
		{"index.mjs", models.LangJavaScript},
		{"index.cjs", models.LangJavaScript},
		{"component.jsx", models.LangJavaScript},
		{"index.ts", models.LangTypeScript},
		{"component.tsx", models.LangTypeScript},
		{"main.go", models.LangGo},
		{"lib.rs", models.LangRust},
		{"App.java", models.LangJava},
		{"app.rb", models.LangRuby},
		{"index.php", models.LangPHP},
		{"main.c", models.LangC},
		{"header.h", models.LangC},
		{"main.cpp", models.LangCPP},
		{"main.cc", models.LangCPP},
		{"header.hpp", models.LangCPP},
		{"deploy.sh", models.LangShell},
		{"run.bash", models.LangShell},
		{"setup.zsh", models.LangShell},
		{"README.md", ""},
		{"data.json", ""},
		{"Makefile", ""},
	}

	for _, tc := range tests {
		t.Run(tc.file, func(t *testing.T) {
			got := DetectLanguage(tc.file)
			if got != tc.want {
				t.Errorf("DetectLanguage(%q) = %q, want %q", tc.file, got, tc.want)
			}
		})
	}
}

func TestDetectLanguage_Shebang(t *testing.T) {
	tmp := t.TempDir()

	tests := []struct {
		name    string
		shebang string
		want    models.Language
	}{
		{"python", "#!/usr/bin/env python3\n", models.LangPython},
		{"node", "#!/usr/bin/env node\n", models.LangJavaScript},
		{"bash", "#!/bin/bash\n", models.LangShell},
		{"sh", "#!/bin/sh\n", models.LangShell},
		{"ruby", "#!/usr/bin/env ruby\n", models.LangRuby},
		{"unknown", "#!/usr/bin/env something\n", ""},
		{"no_shebang", "hello world\n", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(tmp, "script_"+tc.name)
			if err := os.WriteFile(p, []byte(tc.shebang), 0o755); err != nil {
				t.Fatal(err)
			}
			got := DetectLanguage(p)
			if got != tc.want {
				t.Errorf("DetectLanguage(shebang %q) = %q, want %q", tc.shebang, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// IsTestFile
// ---------------------------------------------------------------------------

func TestIsTestFile(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		// Go
		{"handler_test.go", true},
		{"handler.go", false},
		// Python
		{"test_handler.py", true},
		{"handler_test.py", true},
		{"handler.py", false},
		// JS/TS
		{"app.test.js", true},
		{"app.spec.js", true},
		{"app.test.ts", true},
		{"app.spec.tsx", true},
		{"app.js", false},
		// Java
		{"UserServiceTest.java", true},
		{"UserServiceTests.java", true},
		{"UserService.java", false},
		// Ruby
		{"user_spec.rb", true},
		{"user_test.rb", true},
		{"user.rb", false},
		// PHP
		{"UserTest.php", true},
		{"User.php", false},
		// Directory-based
		{"__tests__/Button.js", true},
		{"tests/test_handler.py", true},
		{"spec/user_spec.rb", true},
		{"src/utils/helper.ts", false},
		// Deep nesting
		{"src/__tests__/components/Button.tsx", true},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			got := IsTestFile(tc.path)
			if got != tc.want {
				t.Errorf("IsTestFile(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// DetectFrameworks
// ---------------------------------------------------------------------------

func TestDetectFrameworks_Python(t *testing.T) {
	tmp := t.TempDir()

	// Create a requirements.txt with Django + FastAPI.
	writeFile(t, filepath.Join(tmp, "requirements.txt"),
		"Django>=4.2\nfastapi>=0.100\nuvicorn\n")

	// Create a .py file so the language map is populated.
	writeFile(t, filepath.Join(tmp, "app.py"), "print('hello')\n")

	langs := map[models.Language]int{models.LangPython: 1}
	fws := DetectFrameworks(tmp, langs)

	assertContains(t, fws, "django")
	assertContains(t, fws, "fastapi")
}

func TestDetectFrameworks_Flask(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, "requirements.txt"), "flask>=2.0\n")

	langs := map[models.Language]int{models.LangPython: 1}
	fws := DetectFrameworks(tmp, langs)
	assertContains(t, fws, "flask")
}

func TestDetectFrameworks_DjangoManagePy(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, "manage.py"),
		"#!/usr/bin/env python\nimport django\n")
	writeFile(t, filepath.Join(tmp, "app.py"), "")

	langs := map[models.Language]int{models.LangPython: 2}
	fws := DetectFrameworks(tmp, langs)
	assertContains(t, fws, "django")
}

func TestDetectFrameworks_Express(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, "package.json"),
		`{"dependencies": {"express": "^4.18"}}`)

	langs := map[models.Language]int{models.LangJavaScript: 1}
	fws := DetectFrameworks(tmp, langs)
	assertContains(t, fws, "express")
}

func TestDetectFrameworks_React(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, "package.json"),
		`{"dependencies": {"react": "^18", "react-dom": "^18"}}`)

	langs := map[models.Language]int{models.LangJavaScript: 1}
	fws := DetectFrameworks(tmp, langs)
	assertContains(t, fws, "react")
}

func TestDetectFrameworks_NextJS(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, "package.json"),
		`{"dependencies": {"next": "^14", "react": "^18"}}`)

	langs := map[models.Language]int{models.LangTypeScript: 1}
	fws := DetectFrameworks(tmp, langs)
	assertContains(t, fws, "nextjs")
	assertContains(t, fws, "react")
}

func TestDetectFrameworks_NextJSConfig(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, "package.json"), `{}`)
	writeFile(t, filepath.Join(tmp, "next.config.mjs"), "export default {}\n")

	langs := map[models.Language]int{models.LangTypeScript: 1}
	fws := DetectFrameworks(tmp, langs)
	assertContains(t, fws, "nextjs")
}

func TestDetectFrameworks_Vue(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, "package.json"),
		`{"dependencies": {"vue": "^3"}}`)

	langs := map[models.Language]int{models.LangJavaScript: 1}
	fws := DetectFrameworks(tmp, langs)
	assertContains(t, fws, "vue")
}

func TestDetectFrameworks_Spring(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, "pom.xml"),
		`<project><dependency>spring-boot-starter</dependency></project>`)

	langs := map[models.Language]int{models.LangJava: 3}
	fws := DetectFrameworks(tmp, langs)
	assertContains(t, fws, "spring")
}

func TestDetectFrameworks_SpringGradle(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, "build.gradle"),
		`plugins { id 'org.springframework.boot' }`)

	langs := map[models.Language]int{models.LangJava: 1}
	fws := DetectFrameworks(tmp, langs)
	assertContains(t, fws, "spring")
}

func TestDetectFrameworks_Rails(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, "Gemfile"), "gem 'rails', '~> 7.0'\n")

	langs := map[models.Language]int{models.LangRuby: 5}
	fws := DetectFrameworks(tmp, langs)
	assertContains(t, fws, "rails")
}

func TestDetectFrameworks_RailsRoutes(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, "Gemfile"), "gem 'puma'\n")
	if err := os.MkdirAll(filepath.Join(tmp, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(tmp, "config", "routes.rb"), "Rails.application.routes.draw do\nend\n")

	langs := map[models.Language]int{models.LangRuby: 2}
	fws := DetectFrameworks(tmp, langs)
	assertContains(t, fws, "rails")
}

func TestDetectFrameworks_NoMatch(t *testing.T) {
	tmp := t.TempDir()
	writeFile(t, filepath.Join(tmp, "main.go"), "package main\n")

	langs := map[models.Language]int{models.LangGo: 1}
	fws := DetectFrameworks(tmp, langs)
	if len(fws) != 0 {
		t.Errorf("expected no frameworks, got %v", fws)
	}
}

// ---------------------------------------------------------------------------
// Detect (integration)
// ---------------------------------------------------------------------------

func TestDetect_Integration(t *testing.T) {
	tmp := t.TempDir()

	// Source files.
	writeFile(t, filepath.Join(tmp, "main.go"), "package main\n")
	writeFile(t, filepath.Join(tmp, "handler.go"), "package main\n")
	writeFile(t, filepath.Join(tmp, "handler_test.go"), "package main\n")
	writeFile(t, filepath.Join(tmp, "app.py"), "import flask\n")
	writeFile(t, filepath.Join(tmp, "test_app.py"), "import pytest\n")
	writeFile(t, filepath.Join(tmp, "requirements.txt"), "flask>=2\n")

	// node_modules should be skipped.
	nmDir := filepath.Join(tmp, "node_modules")
	if err := os.MkdirAll(nmDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(nmDir, "package.json"), "{}")

	result, err := Detect(tmp)
	if err != nil {
		t.Fatalf("Detect() error: %v", err)
	}

	if result.Languages[models.LangGo] != 3 {
		t.Errorf("Go files: got %d, want 3", result.Languages[models.LangGo])
	}
	if result.Languages[models.LangPython] != 2 {
		t.Errorf("Python files: got %d, want 2", result.Languages[models.LangPython])
	}

	// Test files.
	sort.Strings(result.TestFiles)
	wantTests := []string{"handler_test.go", "test_app.py"}
	sort.Strings(wantTests)
	if !strSliceEqual(result.TestFiles, wantTests) {
		t.Errorf("TestFiles = %v, want %v", result.TestFiles, wantTests)
	}

	// Framework detection.
	assertContains(t, result.Frameworks, "flask")

	// node_modules should not contribute to TotalFiles.
	// Total = main.go + handler.go + handler_test.go + app.py + test_app.py + requirements.txt = 6
	if result.TotalFiles != 6 {
		t.Errorf("TotalFiles = %d, want 6", result.TotalFiles)
	}
}

func TestDetect_SkipsDirs(t *testing.T) {
	tmp := t.TempDir()

	for _, dir := range []string{".git", "node_modules", "vendor", "__pycache__", ".venv", "build", "dist", "target"} {
		d := filepath.Join(tmp, dir)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(d, "file.go"), "package x\n")
	}

	writeFile(t, filepath.Join(tmp, "real.go"), "package main\n")

	result, err := Detect(tmp)
	if err != nil {
		t.Fatalf("Detect() error: %v", err)
	}

	if result.TotalFiles != 1 {
		t.Errorf("TotalFiles = %d, want 1 (skip dirs not honoured)", result.TotalFiles)
	}
	if result.Languages[models.LangGo] != 1 {
		t.Errorf("Go files: got %d, want 1", result.Languages[models.LangGo])
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertContains(t *testing.T, slice []string, want string) {
	t.Helper()
	for _, s := range slice {
		if s == want {
			return
		}
	}
	t.Errorf("slice %v does not contain %q", slice, want)
}

func strSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestDetectFrameworks_NestedManifest verifies the detector walks into
// subdirectories — a JS app under ui-next/ should still surface its
// frameworks even though the repo root has no package.json.
func TestDetectFrameworks_NestedManifest(t *testing.T) {
	repo := t.TempDir()
	sub := filepath.Join(repo, "ui-next")
	if err := os.MkdirAll(sub, 0o750); err != nil {
		t.Fatal(err)
	}
	pkg := `{"dependencies":{"react":"^19","vite":"^6","@tanstack/react-router":"^1"}}`
	if err := os.WriteFile(filepath.Join(sub, "package.json"), []byte(pkg), 0o600); err != nil {
		t.Fatal(err)
	}
	frameworks := DetectFrameworks(repo, map[models.Language]int{models.LangTypeScript: 1})
	want := map[string]bool{"react": true, "vite": true, "tanstack-router": true}
	for _, fw := range frameworks {
		delete(want, fw)
	}
	if len(want) > 0 {
		t.Errorf("missing nested-manifest frameworks: %v (got %v)", want, frameworks)
	}
}

// TestDetectFrameworks_Go covers Go-side frameworks via go.mod.
func TestDetectFrameworks_Go(t *testing.T) {
	repo := t.TempDir()
	gomod := `module example.com/x
go 1.22
require (
  github.com/go-chi/chi/v5 v5.0.0
  github.com/spf13/cobra v1.7.0
  github.com/gin-gonic/gin v1.9.0
)`
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte(gomod), 0o600); err != nil {
		t.Fatal(err)
	}
	frameworks := DetectFrameworks(repo, map[models.Language]int{models.LangGo: 1})
	want := map[string]bool{"chi": true, "cobra-cli": true, "gin": true}
	for _, fw := range frameworks {
		delete(want, fw)
	}
	if len(want) > 0 {
		t.Errorf("missing Go frameworks: %v (got %v)", want, frameworks)
	}
}

// TestDetectFrameworks_TestdataIgnored verifies that scanner test
// fixtures (testdata/javascript/package.json etc.) DON'T leak into
// the framework list — they're isolated fixtures with deliberately-
// vulnerable dep names, not real dependencies of the parent repo.
func TestDetectFrameworks_TestdataIgnored(t *testing.T) {
	repo := t.TempDir()
	sub := filepath.Join(repo, "testdata", "javascript")
	if err := os.MkdirAll(sub, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "package.json"), []byte(`{"dependencies":{"express":"4.17.1"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	frameworks := DetectFrameworks(repo, map[models.Language]int{models.LangJavaScript: 1})
	for _, fw := range frameworks {
		if fw == "express" {
			t.Errorf("testdata/ fixture leaked 'express' into detected frameworks")
		}
	}
}

// TestDetectFrameworks_Polyglot exercises a real-world-ish layout:
// Go backend + nested JS UI + Dockerfile. Should pick up everything.
func TestDetectFrameworks_Polyglot(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte(`require github.com/go-chi/chi v5
require github.com/spf13/cobra v1`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "Dockerfile"), []byte("FROM alpine"), 0o600); err != nil {
		t.Fatal(err)
	}
	ui := filepath.Join(repo, "web")
	if err := os.MkdirAll(ui, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ui, "package.json"), []byte(`{"dependencies":{"react":"19","tailwindcss":"3","vite":"6"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ui, "vite.config.ts"), []byte("export default {}"), 0o600); err != nil {
		t.Fatal(err)
	}
	frameworks := DetectFrameworks(repo, map[models.Language]int{
		models.LangGo:         1,
		models.LangTypeScript: 1,
	})
	want := map[string]bool{
		"chi": true, "cobra-cli": true, "react": true,
		"vite": true, "tailwindcss": true, "docker": true,
	}
	for _, fw := range frameworks {
		delete(want, fw)
	}
	if len(want) > 0 {
		t.Errorf("polyglot detection missing: %v (got %v)", want, frameworks)
	}
}

// TestDetectFrameworks_Terraform verifies infra-only signals work when
// no source language is present — a docs/IaC repo should still get
// terraform/helm/docker surfaced.
func TestDetectFrameworks_Terraform(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "main.tf"), []byte(`resource "aws_s3_bucket" "x" {}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "Chart.yaml"), []byte(`apiVersion: v2`), 0o600); err != nil {
		t.Fatal(err)
	}
	frameworks := DetectFrameworks(repo, map[models.Language]int{})
	want := map[string]bool{"terraform": true, "helm": true}
	for _, fw := range frameworks {
		delete(want, fw)
	}
	if len(want) > 0 {
		t.Errorf("infra-only detection missing: %v (got %v)", want, frameworks)
	}
}
