package mapper

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// testdataDir returns the absolute path to the top-level testdata directory.
func testdataDir(t *testing.T) string {
	t.Helper()
	// mapper_test.go lives in internal/scan/mapper; testdata is at repo root.
	dir, err := filepath.Abs(filepath.Join("..", "..", "..", "testdata"))
	if err != nil {
		t.Fatalf("resolve testdata dir: %v", err)
	}
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Skipf("testdata directory not found at %s", dir)
	}
	return dir
}

// ---------------------------------------------------------------------------
// TestBuildMap
// ---------------------------------------------------------------------------

func TestBuildMap(t *testing.T) {
	td := testdataDir(t)

	rm, err := BuildMap(MapConfig{RepoPath: td})
	if err != nil {
		t.Fatalf("BuildMap returned error: %v", err)
	}

	t.Run("returns non-nil RepoMap", func(t *testing.T) {
		if rm == nil {
			t.Fatal("expected non-nil RepoMap")
		}
	})

	t.Run("file hashes are populated", func(t *testing.T) {
		if len(rm.FileHashes) == 0 {
			t.Error("expected at least one file hash")
		}
	})

	t.Run("total files matches hash count", func(t *testing.T) {
		if rm.TotalFiles != len(rm.FileHashes) {
			t.Errorf("TotalFiles=%d but len(FileHashes)=%d", rm.TotalFiles, len(rm.FileHashes))
		}
	})

	t.Run("known testdata files present in hashes", func(t *testing.T) {
		expected := []string{
			filepath.Join("python", "app.py"),
			filepath.Join("javascript", "server.js"),
			filepath.Join("go", "main.go"),
			"Dockerfile",
		}
		for _, f := range expected {
			if _, ok := rm.FileHashes[f]; !ok {
				t.Errorf("expected file hash for %s", f)
			}
		}
	})

	t.Run("file stats or LOC summary populated", func(t *testing.T) {
		// At minimum the fallback LOC should produce stats.
		if len(rm.FileStats) == 0 && len(rm.LOCSummary) == 0 {
			t.Error("expected file stats or LOC summary to be non-empty")
		}
	})
}

// ---------------------------------------------------------------------------
// TestComputeFileHashes
// ---------------------------------------------------------------------------

func TestComputeFileHashes(t *testing.T) {
	td := testdataDir(t)

	t.Run("returns hashes for testdata files", func(t *testing.T) {
		hashes, err := ComputeFileHashes(td, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(hashes) == 0 {
			t.Fatal("expected non-empty hash map")
		}
	})

	t.Run("hashes are deterministic", func(t *testing.T) {
		h1, err := ComputeFileHashes(td, nil)
		if err != nil {
			t.Fatalf("first call: %v", err)
		}
		h2, err := ComputeFileHashes(td, nil)
		if err != nil {
			t.Fatalf("second call: %v", err)
		}
		for k, v := range h1 {
			if h2[k] != v {
				t.Errorf("hash mismatch for %s: %s vs %s", k, v, h2[k])
			}
		}
	})

	t.Run("hashes are valid SHA-256 hex", func(t *testing.T) {
		hashes, err := ComputeFileHashes(td, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for path, h := range hashes {
			decoded, err := hex.DecodeString(h)
			if err != nil {
				t.Errorf("hash for %s is not valid hex: %s", path, h)
			}
			if len(decoded) != sha256.Size {
				t.Errorf("hash for %s has wrong length: %d bytes", path, len(decoded))
			}
		}
	})

	t.Run("respects exclude paths", func(t *testing.T) {
		hashes, err := ComputeFileHashes(td, []string{"python"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for k := range hashes {
			if filepath.Dir(k) == "python" || k == "python" {
				t.Errorf("expected python dir to be excluded, found %s", k)
			}
		}
	})

	t.Run("temp file content matches hash", func(t *testing.T) {
		dir := t.TempDir()
		content := []byte("hello wolf")
		fpath := filepath.Join(dir, "test.txt")
		if err := os.WriteFile(fpath, content, 0644); err != nil {
			t.Fatal(err)
		}

		hashes, err := ComputeFileHashes(dir, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		sum := sha256.Sum256(content)
		expected := hex.EncodeToString(sum[:])
		if got := hashes["test.txt"]; got != expected {
			t.Errorf("got hash %s, want %s", got, expected)
		}
	})
}

// ---------------------------------------------------------------------------
// TestChangedFiles
// ---------------------------------------------------------------------------

func TestChangedFiles(t *testing.T) {
	tests := []struct {
		name     string
		old      map[string]string
		new      map[string]string
		expected []string
	}{
		{
			name:     "nil old means all new files are changed",
			old:      nil,
			new:      map[string]string{"a.go": "abc", "b.go": "def"},
			expected: []string{"a.go", "b.go"},
		},
		{
			name:     "identical hashes produce no changes",
			old:      map[string]string{"a.go": "abc"},
			new:      map[string]string{"a.go": "abc"},
			expected: nil,
		},
		{
			name:     "modified file detected",
			old:      map[string]string{"a.go": "abc"},
			new:      map[string]string{"a.go": "xyz"},
			expected: []string{"a.go"},
		},
		{
			name:     "new file detected",
			old:      map[string]string{"a.go": "abc"},
			new:      map[string]string{"a.go": "abc", "b.go": "def"},
			expected: []string{"b.go"},
		},
		{
			name:     "deleted file detected",
			old:      map[string]string{"a.go": "abc", "b.go": "def"},
			new:      map[string]string{"a.go": "abc"},
			expected: []string{"b.go"},
		},
		{
			name:     "mixed changes",
			old:      map[string]string{"a.go": "aaa", "b.go": "bbb", "c.go": "ccc"},
			new:      map[string]string{"a.go": "aaa", "b.go": "BBB", "d.go": "ddd"},
			expected: []string{"b.go", "c.go", "d.go"},
		},
		{
			name:     "both empty",
			old:      map[string]string{},
			new:      map[string]string{},
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ChangedFiles(tt.old, tt.new)
			sort.Strings(got)
			sort.Strings(tt.expected)
			if len(got) != len(tt.expected) {
				t.Fatalf("got %v, want %v", got, tt.expected)
			}
			for i := range got {
				if got[i] != tt.expected[i] {
					t.Errorf("got[%d]=%s, want %s", i, got[i], tt.expected[i])
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestGuessLanguage
// ---------------------------------------------------------------------------

func TestGuessLanguage(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"main.go", "Go"},
		{"app.py", "Python"},
		{"server.js", "JavaScript"},
		{"index.ts", "TypeScript"},
		{"component.tsx", "TypeScript"},
		{"app.jsx", "JavaScript"},
		{"lib.rs", "Rust"},
		{"Main.java", "Java"},
		{"hello.c", "C"},
		{"hello.h", "C Header"},
		{"algo.cpp", "C++"},
		{"algo.cc", "C++"},
		{"header.hpp", "C++ Header"},
		{"app.cs", "C#"},
		{"app.rb", "Ruby"},
		{"index.php", "PHP"},
		{"app.swift", "Swift"},
		{"app.kt", "Kotlin"},
		{"build.kts", "Kotlin"},
		{"app.scala", "Scala"},
		{"script.sh", "Shell"},
		{"script.bash", "Shell"},
		{"config.yaml", "YAML"},
		{"config.yml", "YAML"},
		{"data.json", "JSON"},
		{"config.toml", "TOML"},
		{"layout.xml", "XML"},
		{"index.html", "HTML"},
		{"index.htm", "HTML"},
		{"style.css", "CSS"},
		{"style.scss", "SCSS"},
		{"query.sql", "SQL"},
		{"README.md", "Markdown"},
		{"api.proto", "Protocol Buffers"},
		{"script.lua", "Lua"},
		{"main.zig", "Zig"},
		{"app.ex", "Elixir"},
		{"test.exs", "Elixir"},
		{"server.erl", "Erlang"},
		{"main.hs", "Haskell"},
		{"parser.ml", "OCaml"},
		{"parser.mli", "OCaml"},
		{"analysis.r", "R"},
		{"app.dart", "Dart"},
		{"App.vue", "Vue"},
		{"App.svelte", "Svelte"},
		// Unknown extension returns empty string.
		{"Makefile", ""},
		{"noext", ""},
		{"photo.png", ""},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := guessLanguage(tt.path)
			if got != tt.want {
				t.Errorf("guessLanguage(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestShouldExclude
// ---------------------------------------------------------------------------

func TestShouldExclude(t *testing.T) {
	tests := []struct {
		name     string
		rel      string
		patterns []string
		want     bool
	}{
		{
			name:     "prefix match on directory",
			rel:      "vendor/pkg/foo.go",
			patterns: []string{"vendor"},
			want:     true,
		},
		{
			name:     "no match",
			rel:      "src/main.go",
			patterns: []string{"vendor", "node_modules"},
			want:     false,
		},
		{
			name:     "glob match on extension",
			rel:      "build/output.o",
			patterns: []string{"*.o"},
			want:     true,
		},
		{
			name:     "glob match on base name",
			rel:      "deep/nested/file.log",
			patterns: []string{"*.log"},
			want:     true,
		},
		{
			name:     "node_modules prefix",
			rel:      "node_modules/express/index.js",
			patterns: []string{"node_modules"},
			want:     true,
		},
		{
			name:     "empty patterns never excludes",
			rel:      "anything.go",
			patterns: nil,
			want:     false,
		},
		{
			name:     ".git prefix",
			rel:      ".git/config",
			patterns: []string{".git"},
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldExclude(tt.rel, tt.patterns)
			if got != tt.want {
				t.Errorf("shouldExclude(%q, %v) = %v, want %v", tt.rel, tt.patterns, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestDeduplicateSymbols
// ---------------------------------------------------------------------------

func TestDeduplicateSymbols(t *testing.T) {
	t.Run("removes exact duplicates", func(t *testing.T) {
		symbols := []Symbol{
			{Name: "main", FilePath: "main.go", Line: 1, Kind: "function", Language: "Go"},
			{Name: "main", FilePath: "main.go", Line: 1, Kind: "function", Language: "Go"},
			{Name: "main", FilePath: "main.go", Line: 1, Kind: "function", Language: "Go"},
		}
		got := deduplicateSymbols(symbols)
		if len(got) != 1 {
			t.Errorf("expected 1 symbol, got %d", len(got))
		}
	})

	t.Run("keeps symbols with different lines", func(t *testing.T) {
		symbols := []Symbol{
			{Name: "handler", FilePath: "api.go", Line: 10, Kind: "function"},
			{Name: "handler", FilePath: "api.go", Line: 50, Kind: "function"},
		}
		got := deduplicateSymbols(symbols)
		if len(got) != 2 {
			t.Errorf("expected 2 symbols, got %d", len(got))
		}
	})

	t.Run("keeps symbols with different files", func(t *testing.T) {
		symbols := []Symbol{
			{Name: "init", FilePath: "a.go", Line: 1, Kind: "function"},
			{Name: "init", FilePath: "b.go", Line: 1, Kind: "function"},
		}
		got := deduplicateSymbols(symbols)
		if len(got) != 2 {
			t.Errorf("expected 2 symbols, got %d", len(got))
		}
	})

	t.Run("keeps symbols with different names", func(t *testing.T) {
		symbols := []Symbol{
			{Name: "foo", FilePath: "main.go", Line: 1, Kind: "function"},
			{Name: "bar", FilePath: "main.go", Line: 1, Kind: "function"},
		}
		got := deduplicateSymbols(symbols)
		if len(got) != 2 {
			t.Errorf("expected 2 symbols, got %d", len(got))
		}
	})

	t.Run("nil input returns nil", func(t *testing.T) {
		got := deduplicateSymbols(nil)
		if got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})

	t.Run("empty input returns empty", func(t *testing.T) {
		got := deduplicateSymbols([]Symbol{})
		if len(got) != 0 {
			t.Errorf("expected empty slice, got %d items", len(got))
		}
	})
}
