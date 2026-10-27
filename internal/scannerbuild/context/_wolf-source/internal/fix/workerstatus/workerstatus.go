// Package workerstatus is the file contract between the fixer worker and the
// API: the worker publishes engine readiness and its default prompts; the API
// reads them. The API never links the fixer engines or their prompts.
package workerstatus

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/alphabravocompany/thewolf/internal/fix/profile"
)

// FileName is written under the artifacts root.
const FileName = "fixer-engines.json"

// EngineStatus is the worker-local view of one fix engine's readiness.
type EngineStatus struct {
	Name         string          `json:"name"`
	Command      string          `json:"command,omitempty"`
	Available    bool            `json:"available"`
	Installed    bool            `json:"installed"`
	Installable  bool            `json:"installable,omitempty"`
	Auth         string          `json:"auth"` // oauth | api_key | none
	Detail       string          `json:"detail,omitempty"`
	Account      string          `json:"account,omitempty"`
	Login        []string        `json:"login,omitempty"`
	SessionPaths []string        `json:"session_paths,omitempty"`
	Persisted    bool            `json:"persisted"`
	Usage        string          `json:"usage,omitempty"`
	Models       []profile.Model `json:"models,omitempty"`
}

// Status is the whole published file.
type Status struct {
	Engines []EngineStatus `json:"engines"`
	// Prompts maps a prompt setting key to the worker's shipped default.
	Prompts map[string]string `json:"prompts,omitempty"`
	// Placeholder is the token operators use for the findings file path.
	Placeholder string `json:"placeholder,omitempty"`
	UpdatedAt   string `json:"updated_at,omitempty"`
}

// Write publishes s for the API. An empty root is a no-op.
func Write(root string, s Status) error {
	if root == "" {
		return nil
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return err
	}
	s.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	payload, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, FileName), payload, 0o600)
}

// Read loads the worker-published file. A missing file is an empty Status.
func Read(root string) (Status, error) {
	var s Status
	if root == "" {
		return s, nil
	}
	data, err := os.ReadFile(filepath.Join(root, FileName)) // #nosec G304 -- fixed name under the artifacts root
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	return s, json.Unmarshal(data, &s)
}
