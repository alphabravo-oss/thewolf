package coverage

import (
	"os"
	"path/filepath"
	"testing"
)

// helper to create files in a temp directory.
func createFile(t *testing.T, dir, rel string) {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("// placeholder"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAnalyze_GoRepo(t *testing.T) {
	dir := t.TempDir()
	createFile(t, dir, "main.go")
	createFile(t, dir, "server.go")
	createFile(t, dir, "server_test.go")
	createFile(t, dir, "handler.go")
	createFile(t, dir, "handler_test.go")
	createFile(t, dir, "utils.go")

	report, err := Analyze(dir)
	if err != nil {
		t.Fatal(err)
	}

	if report.TotalSourceFiles != 4 {
		t.Errorf("TotalSourceFiles: got %d, want 4", report.TotalSourceFiles)
	}
	if report.TestFiles != 2 {
		t.Errorf("TestFiles: got %d, want 2", report.TestFiles)
	}
	// Go uses package-level testing: any test file in a directory covers all
	// source files in that directory, so all 4 source files are covered.
	if report.FilesWithTests != 4 {
		t.Errorf("FilesWithTests: got %d, want 4", report.FilesWithTests)
	}
	if report.FilesWithoutTests != 0 {
		t.Errorf("FilesWithoutTests: got %d, want 0", report.FilesWithoutTests)
	}

	// coverage should be 100%
	if report.CoveragePercent != 100 {
		t.Errorf("CoveragePercent: got %f, want 100", report.CoveragePercent)
	}

	langGo, ok := report.ByLanguage["go"]
	if !ok {
		t.Fatal("expected 'go' language in ByLanguage")
	}
	if langGo.SourceFiles != 4 {
		t.Errorf("go SourceFiles: got %d, want 4", langGo.SourceFiles)
	}
	if langGo.TestFiles != 2 {
		t.Errorf("go TestFiles: got %d, want 2", langGo.TestFiles)
	}
}

func TestAnalyze_JSRepo(t *testing.T) {
	dir := t.TempDir()
	createFile(t, dir, "src/app.ts")
	createFile(t, dir, "src/app.test.ts")
	createFile(t, dir, "src/utils.ts")
	createFile(t, dir, "src/utils.spec.ts")
	createFile(t, dir, "src/config.ts")

	report, err := Analyze(dir)
	if err != nil {
		t.Fatal(err)
	}

	if report.TotalSourceFiles != 3 {
		t.Errorf("TotalSourceFiles: got %d, want 3", report.TotalSourceFiles)
	}
	if report.TestFiles != 2 {
		t.Errorf("TestFiles: got %d, want 2", report.TestFiles)
	}
	if report.FilesWithTests != 2 {
		t.Errorf("FilesWithTests: got %d, want 2", report.FilesWithTests)
	}
}

func TestAnalyze_PythonRepo(t *testing.T) {
	dir := t.TempDir()
	createFile(t, dir, "app.py")
	createFile(t, dir, "test_app.py")
	createFile(t, dir, "models.py")
	createFile(t, dir, "models_test.py")
	createFile(t, dir, "views.py")

	report, err := Analyze(dir)
	if err != nil {
		t.Fatal(err)
	}

	if report.TotalSourceFiles != 3 {
		t.Errorf("TotalSourceFiles: got %d, want 3", report.TotalSourceFiles)
	}
	if report.TestFiles != 2 {
		t.Errorf("TestFiles: got %d, want 2", report.TestFiles)
	}
	if report.FilesWithTests != 2 {
		t.Errorf("FilesWithTests: got %d, want 2", report.FilesWithTests)
	}
}

func TestAnalyze_SkipsDirs(t *testing.T) {
	dir := t.TempDir()
	createFile(t, dir, "main.go")
	createFile(t, dir, "vendor/lib.go")
	createFile(t, dir, "node_modules/pkg.js")

	report, err := Analyze(dir)
	if err != nil {
		t.Fatal(err)
	}

	// vendor/ and node_modules/ should be skipped.
	if report.TotalSourceFiles != 1 {
		t.Errorf("TotalSourceFiles: got %d, want 1", report.TotalSourceFiles)
	}
}

func TestAnalyze_EmptyRepo(t *testing.T) {
	dir := t.TempDir()

	report, err := Analyze(dir)
	if err != nil {
		t.Fatal(err)
	}

	if report.TotalSourceFiles != 0 {
		t.Errorf("TotalSourceFiles: got %d, want 0", report.TotalSourceFiles)
	}
	if report.CoveragePercent != 0 {
		t.Errorf("CoveragePercent: got %f, want 0", report.CoveragePercent)
	}
}

func TestAnalyze_MultiLang(t *testing.T) {
	dir := t.TempDir()
	createFile(t, dir, "main.go")
	createFile(t, dir, "main_test.go")
	createFile(t, dir, "src/app.ts")
	createFile(t, dir, "src/app.test.ts")
	createFile(t, dir, "lib/helper.py")

	report, err := Analyze(dir)
	if err != nil {
		t.Fatal(err)
	}

	if len(report.ByLanguage) != 3 {
		t.Errorf("expected 3 languages, got %d", len(report.ByLanguage))
	}
	if report.TotalSourceFiles != 3 {
		t.Errorf("TotalSourceFiles: got %d, want 3", report.TotalSourceFiles)
	}
	if report.FilesWithTests != 2 {
		t.Errorf("FilesWithTests: got %d, want 2", report.FilesWithTests)
	}
}

func TestAnalyze_JavaRepo(t *testing.T) {
	dir := t.TempDir()
	createFile(t, dir, "src/main/java/com/example/App.java")
	createFile(t, dir, "src/test/java/com/example/AppTest.java")
	createFile(t, dir, "src/main/java/com/example/Service.java")
	createFile(t, dir, "src/test/java/com/example/ServiceIT.java")

	report, err := Analyze(dir)
	if err != nil {
		t.Fatal(err)
	}

	if report.TotalSourceFiles != 2 {
		t.Errorf("TotalSourceFiles: got %d, want 2", report.TotalSourceFiles)
	}
	// Both AppTest.java and ServiceIT.java (integration test) should map
	if report.FilesWithTests != 2 {
		t.Errorf("FilesWithTests: got %d, want 2", report.FilesWithTests)
	}
}

func TestAnalyze_CSharpRepo(t *testing.T) {
	dir := t.TempDir()
	createFile(t, dir, "MyProject/Foo.cs")
	createFile(t, dir, "MyProject/Bar.cs")
	createFile(t, dir, "MyProject.Tests/FooTests.cs")

	report, err := Analyze(dir)
	if err != nil {
		t.Fatal(err)
	}

	if report.TotalSourceFiles != 2 {
		t.Errorf("TotalSourceFiles: got %d, want 2", report.TotalSourceFiles)
	}
	if report.TestFiles != 1 {
		t.Errorf("TestFiles: got %d, want 1", report.TestFiles)
	}
	if report.FilesWithTests != 1 {
		t.Errorf("FilesWithTests: got %d, want 1", report.FilesWithTests)
	}
}

func TestAnalyze_ScalaRepo(t *testing.T) {
	dir := t.TempDir()
	createFile(t, dir, "src/main/scala/com/example/App.scala")
	createFile(t, dir, "src/test/scala/com/example/AppSpec.scala")
	createFile(t, dir, "src/main/scala/com/example/Service.scala")

	report, err := Analyze(dir)
	if err != nil {
		t.Fatal(err)
	}

	if report.TotalSourceFiles != 2 {
		t.Errorf("TotalSourceFiles: got %d, want 2", report.TotalSourceFiles)
	}
	if report.FilesWithTests != 1 {
		t.Errorf("FilesWithTests: got %d, want 1", report.FilesWithTests)
	}
	if _, ok := report.ByLanguage["scala"]; !ok {
		t.Fatal("expected 'scala' language in ByLanguage")
	}
}

func TestAnalyze_DartRepo(t *testing.T) {
	dir := t.TempDir()
	createFile(t, dir, "lib/app.dart")
	createFile(t, dir, "lib/utils.dart")
	createFile(t, dir, "test/app_test.dart")

	report, err := Analyze(dir)
	if err != nil {
		t.Fatal(err)
	}

	if report.TotalSourceFiles != 2 {
		t.Errorf("TotalSourceFiles: got %d, want 2", report.TotalSourceFiles)
	}
	if report.TestFiles != 1 {
		t.Errorf("TestFiles: got %d, want 1", report.TestFiles)
	}
	// test/app_test.dart -> lib/app.dart
	if report.FilesWithTests != 1 {
		t.Errorf("FilesWithTests: got %d, want 1", report.FilesWithTests)
	}
}

func TestAnalyze_ElixirRepo(t *testing.T) {
	dir := t.TempDir()
	createFile(t, dir, "lib/app.ex")
	createFile(t, dir, "lib/worker.ex")
	createFile(t, dir, "test/app_test.exs")

	report, err := Analyze(dir)
	if err != nil {
		t.Fatal(err)
	}

	if report.TotalSourceFiles != 2 {
		t.Errorf("TotalSourceFiles: got %d, want 2", report.TotalSourceFiles)
	}
	if report.TestFiles != 1 {
		t.Errorf("TestFiles: got %d, want 1", report.TestFiles)
	}
	// test/app_test.exs -> lib/app.ex
	if report.FilesWithTests != 1 {
		t.Errorf("FilesWithTests: got %d, want 1", report.FilesWithTests)
	}
}

func TestAnalyze_GoPackageLevel(t *testing.T) {
	dir := t.TempDir()
	// Simulate a Go package where one test file covers all source files
	createFile(t, dir, "plugins/infra/kubelinter.go")
	createFile(t, dir, "plugins/infra/kubescape.go")
	createFile(t, dir, "plugins/infra/tflint.go")
	createFile(t, dir, "plugins/infra/infra_test.go")

	report, err := Analyze(dir)
	if err != nil {
		t.Fatal(err)
	}

	if report.TotalSourceFiles != 3 {
		t.Errorf("TotalSourceFiles: got %d, want 3", report.TotalSourceFiles)
	}
	// All 3 source files should be covered via package-level detection
	if report.FilesWithTests != 3 {
		t.Errorf("FilesWithTests: got %d, want 3", report.FilesWithTests)
	}
	if report.CoveragePercent != 100 {
		t.Errorf("CoveragePercent: got %f, want 100", report.CoveragePercent)
	}
}
