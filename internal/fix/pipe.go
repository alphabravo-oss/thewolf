package fix

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/alphabravocompany/thewolf/internal/models"
)

// ScanOutput represents the JSON output from `wolf scan --format json`.
type ScanOutput struct {
	ScanID   string           `json:"scan_id"`
	RepoID   string           `json:"repo_id"`
	RepoPath string           `json:"repo_path"`
	Branch   string           `json:"branch"`
	Findings []models.Finding `json:"findings"`
}

// IsPiped returns true if stdin is not a terminal (data is being piped in).
func IsPiped() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) == 0
}

// ReadPipedInput reads and parses scan JSON from the given reader.
func ReadPipedInput(r io.Reader) (*ScanOutput, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read stdin: %w", err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("empty input")
	}

	var output ScanOutput
	if err := json.Unmarshal(data, &output); err != nil {
		return nil, fmt.Errorf("parse scan JSON: %w", err)
	}

	if len(output.Findings) == 0 {
		return nil, fmt.Errorf("no findings in input")
	}

	return &output, nil
}
