package fix

import (
	"strings"
	"testing"
)

func TestReadPipedInput_Valid(t *testing.T) {
	input := `{
		"scan_id": "scan-123",
		"repo_id": "repo-456",
		"repo_path": "/tmp/myrepo",
		"branch": "main",
		"findings": [
			{
				"id": "f1",
				"scan_id": "scan-123",
				"repo_id": "repo-456",
				"tool_name": "semgrep",
				"category": "sast",
				"severity": "high",
				"title": "SQL injection",
				"description": "User input used in SQL query",
				"file_path": "app.py",
				"line_start": 42,
				"composite_score": 85.0,
				"status": "open"
			}
		]
	}`

	output, err := ReadPipedInput(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output.ScanID != "scan-123" {
		t.Errorf("expected scan_id scan-123, got %s", output.ScanID)
	}
	if len(output.Findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(output.Findings))
	}
	if output.Findings[0].Title != "SQL injection" {
		t.Errorf("expected title 'SQL injection', got %s", output.Findings[0].Title)
	}
}

func TestReadPipedInput_Empty(t *testing.T) {
	_, err := ReadPipedInput(strings.NewReader(""))
	if err == nil {
		t.Fatal("expected error for empty input")
	}
}

func TestReadPipedInput_InvalidJSON(t *testing.T) {
	_, err := ReadPipedInput(strings.NewReader("not json"))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestReadPipedInput_NoFindings(t *testing.T) {
	input := `{"scan_id": "scan-123", "findings": []}`
	_, err := ReadPipedInput(strings.NewReader(input))
	if err == nil {
		t.Fatal("expected error for no findings")
	}
}
