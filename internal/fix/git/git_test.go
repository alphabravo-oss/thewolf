package git

import (
	"testing"
)

func TestBranchName(t *testing.T) {
	tests := []struct {
		scanID   string
		category string
		want     string
	}{
		{"abc123", "security", "wolf-fix/abc123/security"},
		{"def456", "Code Quality", "wolf-fix/def456/code-quality"},
		{"ghi789", "sast", "wolf-fix/ghi789/sast"},
		{"scan-1", "CONTAINER", "wolf-fix/scan-1/container"},
	}

	for _, tt := range tests {
		got := BranchName(tt.scanID, tt.category)
		if got != tt.want {
			t.Errorf("BranchName(%q, %q) = %q, want %q", tt.scanID, tt.category, got, tt.want)
		}
	}
}

func TestSanitizePath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"wolf-fix/abc/sast", "wolf-fix_abc_sast"},
		{"path with spaces", "path_with_spaces"},
		{"simple", "simple"},
	}

	for _, tt := range tests {
		got := sanitizePath(tt.input)
		if got != tt.want {
			t.Errorf("sanitizePath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
