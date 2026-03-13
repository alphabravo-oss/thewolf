package mapper

import (
	"context"
	"testing"

	"github.com/alphabravocompany/thewolf/internal/ai"
)

// ---------------------------------------------------------------------------
// Test: noop provider returns empty annotations
// ---------------------------------------------------------------------------

func TestAnnotateFiles_NoopProvider(t *testing.T) {
	provider := ai.NewNoopProvider()

	rm := &RepoMap{
		FileHashes: map[string]string{
			"cmd/main.go":            "abc123",
			"internal/service/svc.go": "def456",
		},
		Symbols: []Symbol{
			{Name: "main", Kind: "function", FilePath: "cmd/main.go", Line: 1, Language: "Go"},
			{Name: "Run", Kind: "function", FilePath: "internal/service/svc.go", Line: 10, Language: "Go"},
		},
		FileStats: map[string]FileStats{
			"cmd/main.go":            {Language: "Go", Code: 20},
			"internal/service/svc.go": {Language: "Go", Code: 150},
		},
	}

	annotations, err := AnnotateFiles(context.Background(), provider, rm, "/tmp/repo")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// Noop provider returns an error from Complete, so annotations should be empty.
	if len(annotations) != 0 {
		t.Errorf("expected 0 annotations from noop provider, got %d", len(annotations))
	}
}

// ---------------------------------------------------------------------------
// Test: nil provider returns nil gracefully
// ---------------------------------------------------------------------------

func TestAnnotateFiles_NilProvider(t *testing.T) {
	rm := &RepoMap{
		FileHashes: map[string]string{"main.go": "abc"},
	}

	annotations, err := AnnotateFiles(context.Background(), nil, rm, "/tmp/repo")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if annotations != nil {
		t.Errorf("expected nil annotations, got %v", annotations)
	}
}

// ---------------------------------------------------------------------------
// Test: prompt building
// ---------------------------------------------------------------------------

func TestBuildAnnotatePrompt(t *testing.T) {
	rm := &RepoMap{
		Symbols: []Symbol{
			{Name: "UserController", Kind: "class", FilePath: "app/controllers/user.go", Line: 5, Language: "Go"},
			{Name: "CreateUser", Kind: "function", FilePath: "app/controllers/user.go", Line: 10, Language: "Go"},
			{Name: "User", Kind: "class", FilePath: "app/models/user.go", Line: 3, Language: "Go"},
		},
		FileStats: map[string]FileStats{
			"app/controllers/user.go": {Language: "Go", Code: 100},
			"app/models/user.go":      {Language: "Go", Code: 50},
		},
	}

	files := []string{"app/controllers/user.go", "app/models/user.go"}
	prompt := buildAnnotatePrompt(files, rm)

	// Verify the prompt contains key elements.
	if !containsStr(prompt, "app/controllers/user.go") {
		t.Error("prompt should contain file path app/controllers/user.go")
	}
	if !containsStr(prompt, "app/models/user.go") {
		t.Error("prompt should contain file path app/models/user.go")
	}
	if !containsStr(prompt, "UserController") {
		t.Error("prompt should contain symbol UserController")
	}
	if !containsStr(prompt, "purpose") {
		t.Error("prompt should contain the word 'purpose'")
	}
	if !containsStr(prompt, "JSON") {
		t.Error("prompt should mention JSON response format")
	}
}

// ---------------------------------------------------------------------------
// Test: response parsing
// ---------------------------------------------------------------------------

func TestParseAnnotateResponse(t *testing.T) {
	resp := `[
		{"file_path": "cmd/main.go", "purpose": "main", "importance": "critical", "description": "Application entry point"},
		{"file_path": "internal/service/svc.go", "purpose": "service", "importance": "high", "description": "Core business logic service"}
	]`

	requested := []string{"cmd/main.go", "internal/service/svc.go"}
	annotations, err := parseAnnotateResponse(resp, requested)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if len(annotations) != 2 {
		t.Fatalf("expected 2 annotations, got %d", len(annotations))
	}

	if annotations[0].FilePath != "cmd/main.go" {
		t.Errorf("expected file_path cmd/main.go, got %s", annotations[0].FilePath)
	}
	if annotations[0].Purpose != "main" {
		t.Errorf("expected purpose main, got %s", annotations[0].Purpose)
	}
	if annotations[0].Importance != "critical" {
		t.Errorf("expected importance critical, got %s", annotations[0].Importance)
	}

	if annotations[1].Purpose != "service" {
		t.Errorf("expected purpose service, got %s", annotations[1].Purpose)
	}
}

func TestParseAnnotateResponse_WithMarkdownFences(t *testing.T) {
	resp := "```json\n" + `[
		{"file_path": "main.go", "purpose": "main", "importance": "critical", "description": "Entry point"}
	]` + "\n```"

	annotations, err := parseAnnotateResponse(resp, []string{"main.go"})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if len(annotations) != 1 {
		t.Fatalf("expected 1 annotation, got %d", len(annotations))
	}
}

func TestParseAnnotateResponse_FiltersUnrequestedFiles(t *testing.T) {
	resp := `[
		{"file_path": "main.go", "purpose": "main", "importance": "critical", "description": "Entry point"},
		{"file_path": "extra.go", "purpose": "utility", "importance": "low", "description": "Extra file"}
	]`

	annotations, err := parseAnnotateResponse(resp, []string{"main.go"})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if len(annotations) != 1 {
		t.Fatalf("expected 1 annotation (filtered), got %d", len(annotations))
	}
	if annotations[0].FilePath != "main.go" {
		t.Errorf("expected main.go, got %s", annotations[0].FilePath)
	}
}

// ---------------------------------------------------------------------------
// Test: normalization helpers
// ---------------------------------------------------------------------------

func TestNormalizePurpose(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"controller", "controller"},
		{"Controller", "controller"},
		{"  SERVICE  ", "service"},
		{"unknown_type", "utility"},
		{"", "utility"},
	}
	for _, tt := range tests {
		got := normalizePurpose(tt.input)
		if got != tt.want {
			t.Errorf("normalizePurpose(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestNormalizeImportance(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"critical", "critical"},
		{"HIGH", "high"},
		{"  Normal  ", "normal"},
		{"low", "low"},
		{"unknown", "normal"},
		{"", "normal"},
	}
	for _, tt := range tests {
		got := normalizeImportance(tt.input)
		if got != tt.want {
			t.Errorf("normalizeImportance(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Test: skip logic
// ---------------------------------------------------------------------------

func TestShouldSkipAnnotation(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"vendor/github.com/foo/bar.go", true},
		{"node_modules/react/index.js", true},
		{".git/config", true},
		{"dist/bundle.js", true},
		{"styles/app.min.css", true},
		{"go.sum", true},
		{"main.go", false},
		{"internal/service/svc.go", false},
		{"README.md", false},
	}
	for _, tt := range tests {
		got := shouldSkipAnnotation(tt.path)
		if got != tt.want {
			t.Errorf("shouldSkipAnnotation(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Test: candidate file selection
// ---------------------------------------------------------------------------

func TestCandidateFiles(t *testing.T) {
	rm := &RepoMap{
		FileHashes: map[string]string{
			"main.go":                     "a",
			"internal/svc.go":             "b",
			"vendor/lib/lib.go":           "c",
			"node_modules/react/index.js": "d",
			"assets/logo.png":             "e",
		},
	}

	files := candidateFiles(rm)

	// Should include main.go and internal/svc.go, exclude the rest.
	if len(files) != 2 {
		t.Fatalf("expected 2 candidate files, got %d: %v", len(files), files)
	}

	expected := map[string]bool{"main.go": true, "internal/svc.go": true}
	for _, f := range files {
		if !expected[f] {
			t.Errorf("unexpected candidate file: %s", f)
		}
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
