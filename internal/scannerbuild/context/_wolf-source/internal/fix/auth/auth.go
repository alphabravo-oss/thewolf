// Package auth resolves LLM credentials for the autonomous fixer: stored
// API keys (Anthropic / OpenAI) and/or an OAuth session on the worker host.
// Either is enough to run Claude, Codex, or OpenCode.
package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/alphabravocompany/thewolf/internal/db"
	"github.com/alphabravocompany/thewolf/internal/fix/profile"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/secrets"
)

// Credentials are the per-user API keys the worker injects into CLI and API
// engines. Empty strings mean "not configured".
type Credentials struct {
	AnthropicKey string
	OpenAIKey    string
	XAIKey       string
}

// HasAny reports whether at least one provider key is present.
func (c Credentials) HasAny() bool {
	return c.AnthropicKey != "" || c.OpenAIKey != "" || c.XAIKey != ""
}

// Env returns the environment assignments (KEY=value) to merge onto a CLI
// process so it can authenticate with a key instead of (or in addition to)
// an interactive OAuth session.
func (c Credentials) Env() []string {
	var env []string
	if c.AnthropicKey != "" {
		env = append(env, "ANTHROPIC_API_KEY="+c.AnthropicKey)
	}
	if c.OpenAIKey != "" {
		env = append(env, "OPENAI_API_KEY="+c.OpenAIKey)
	}
	if c.XAIKey != "" {
		env = append(env, "XAI_API_KEY="+c.XAIKey)
	}
	return env
}

// HasKeyFor reports whether the credentials can satisfy the named CLI.
func (c Credentials) HasKeyFor(command string) bool {
	switch command {
	case "claude":
		return c.AnthropicKey != ""
	case "codex":
		return c.OpenAIKey != ""
	case "opencode":
		return c.HasAny()
	case "grok", "xai":
		return c.XAIKey != ""
	default:
		return false
	}
}

// Resolve loads the user's stored Anthropic/OpenAI keys, falling back to
// process environment. First matching secret of each type wins.
func Resolve(ctx context.Context, store db.Store, userID string) Credentials {
	var creds Credentials
	if store != nil && userID != "" {
		if list, err := store.ListSecretsByUser(ctx, userID); err == nil {
			for _, s := range list {
				dec, derr := secrets.Decrypt(s.EncryptedValue)
				if derr != nil || dec == "" {
					continue
				}
				switch s.KeyType {
				case models.KeyTypeAnthropicKey:
					if creds.AnthropicKey == "" {
						creds.AnthropicKey = dec
					}
				case models.KeyTypeOpenAIKey:
					if creds.OpenAIKey == "" {
						creds.OpenAIKey = dec
					}
				case models.KeyTypeXAIKey:
					if creds.XAIKey == "" {
						creds.XAIKey = dec
					}
				}
			}
		}
	}
	if creds.AnthropicKey == "" {
		creds.AnthropicKey = os.Getenv("ANTHROPIC_API_KEY")
	}
	if creds.OpenAIKey == "" {
		creds.OpenAIKey = os.Getenv("OPENAI_API_KEY")
	}
	if creds.XAIKey == "" {
		creds.XAIKey = os.Getenv("XAI_API_KEY")
	}
	return creds
}

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

// StatusFileName is written under the artifacts root so the API can surface
// worker-side OAuth state (the API process does not have the CLIs).
const StatusFileName = "fixer-engines.json"

// ProbeAll reports local CLI + key readiness. creds may be empty.
func ProbeAll(ctx context.Context, creds Credentials) []EngineStatus {
	return []EngineStatus{
		probeCLI(ctx, "claude-code", "claude", creds),
		probeCLI(ctx, "codex", "codex", creds),
		probeCLI(ctx, "opencode", "opencode", creds),
		{
			Name:      "grok",
			Available: creds.XAIKey != "",
			Auth:      grokAuthLabel(creds),
			Detail:    grokDetail(creds),
		},
		{
			Name:      "api",
			Available: creds.HasAny(),
			Auth:      apiAuthLabel(creds),
			Detail:    apiDetail(creds),
		},
	}
}

func grokAuthLabel(c Credentials) string {
	if c.XAIKey != "" {
		return "api_key"
	}
	return "none"
}

func grokDetail(c Credentials) string {
	if c.XAIKey != "" {
		return "xAI API key configured"
	}
	return "add an xAI / Grok API key under Account → Secrets"
}

func apiAuthLabel(c Credentials) string {
	if c.HasAny() {
		return "api_key"
	}
	return "none"
}

func apiDetail(c Credentials) string {
	var parts []string
	if c.AnthropicKey != "" {
		parts = append(parts, "anthropic")
	}
	if c.OpenAIKey != "" {
		parts = append(parts, "openai")
	}
	if c.XAIKey != "" {
		parts = append(parts, "xai")
	}
	if len(parts) == 0 {
		return "no anthropic_key, openai_key, or xai_key configured"
	}
	return "keys: " + strings.Join(parts, ", ")
}

func probeCLI(ctx context.Context, name, command string, creds Credentials) EngineStatus {
	st := EngineStatus{Name: name, Command: command, Auth: "none"}
	if spec := loginSpec(command); spec != nil {
		st.Login = spec.Args
		st.SessionPaths = spec.Paths
		st.Persisted = sessionFilesPresent(spec.Paths)
	}
	st.Installable = true
	if _, err := exec.LookPath(command); err != nil {
		st.Detail = command + " is not installed on the fixer worker"
		if creds.HasKeyFor(command) {
			st.Auth = "api_key"
			st.Detail = command + " is not installed (API key is present — install the CLI to use OAuth)"
		}
		return st
	}
	st.Installed = true
	st.Available = true
	if acc, ok := oauthAccount(ctx, command); ok {
		st.Auth = "oauth"
		st.Account = stripANSI(acc)
		st.Persisted = true
		st.Detail = "OAuth session on this host"
		st.Usage = stripANSI(usageHint(ctx, command))
		if command == "opencode" {
			st.Models = listOpenCodeModels(ctx)
		}
		return st
	}
	if creds.HasKeyFor(command) {
		st.Auth = "api_key"
		st.Detail = "API key available"
		return st
	}
	st.Available = false
	st.Detail = "not logged in — run: wolf fixer login " + command
	return st
}

type loginCmd struct {
	Args  []string
	Paths []string
}

func loginSpec(command string) *loginCmd {
	home, _ := os.UserHomeDir()
	expand := func(p string) string {
		if strings.HasPrefix(p, "~/") && home != "" {
			return filepath.Join(home, strings.TrimPrefix(p, "~/"))
		}
		return p
	}
	var spec loginCmd
	switch command {
	case "claude":
		spec = loginCmd{Args: []string{"claude", "auth", "login"}, Paths: []string{"~/.claude.json", "~/.config/claude", "~/.claude"}}
	case "codex":
		spec = loginCmd{Args: []string{"codex", "login"}, Paths: []string{"~/.codex"}}
	case "opencode":
		spec = loginCmd{Args: []string{"opencode", "auth", "login"}, Paths: []string{"~/.local/share/opencode/auth.json", "~/.config/opencode"}}
	default:
		return nil
	}
	for i, p := range spec.Paths {
		spec.Paths[i] = expand(p)
	}
	return &spec
}

func sessionFilesPresent(paths []string) bool {
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

func oauthAccount(ctx context.Context, command string) (string, bool) {
	probeCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	switch command {
	case "claude":
		cmd := exec.CommandContext(probeCtx, "claude", "auth", "status", "--text") // #nosec G204
		out, err := cmd.CombinedOutput()
		if err == nil && len(bytesTrim(out)) > 0 {
			return firstLine(string(out)), true
		}
		// Older CLIs: a session file plus `claude --version` is enough.
		if spec := loginSpec("claude"); spec != nil && sessionFilesPresent(spec.Paths) {
			return "claude session", true
		}
		return "", false
	case "codex":
		if spec := loginSpec("codex"); spec != nil && sessionFilesPresent(spec.Paths) {
			return "codex session", true
		}
		return "", false
	case "opencode":
		cmd := exec.CommandContext(probeCtx, "opencode", "auth", "list") // #nosec G204
		cmd.Stdin = nil
		out, err := cmd.CombinedOutput()
		if err == nil && len(bytesTrim(out)) > 0 && !strings.Contains(strings.ToLower(string(out)), "no provider") {
			return firstLine(string(out)), true
		}
		if spec := loginSpec("opencode"); spec != nil && sessionFilesPresent(spec.Paths) {
			return "opencode session", true
		}
		return "", false
	default:
		return "", false
	}
}

func usageHint(ctx context.Context, command string) string {
	probeCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	switch command {
	case "opencode":
		cmd := exec.CommandContext(probeCtx, "opencode", "stats", "--days", "7") // #nosec G204
		cmd.Stdin = nil
		out, err := cmd.CombinedOutput()
		if err == nil {
			return strings.TrimSpace(firstLine(string(out)))
		}
	}
	return ""
}

func firstLine(s string) string {
	s = stripANSI(s)
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

func bytesTrim(b []byte) []byte {
	return []byte(strings.TrimSpace(string(b)))
}

// Login runs the harness OAuth flow on this host (same HOME the worker uses).
// The session files persist until logout. stdin/stdout must be a TTY.
func Login(ctx context.Context, engine string) error {
	var command string
	switch engine {
	case "claude-code", "claude":
		command = "claude"
	case "codex":
		command = "codex"
	case "opencode":
		command = "opencode"
	default:
		return fmt.Errorf("unknown engine %q (use claude, codex, or opencode)", engine)
	}
	spec := loginSpec(command)
	if spec == nil {
		return fmt.Errorf("no login command for %s", command)
	}
	if _, err := exec.LookPath(spec.Args[0]); err != nil {
		return fmt.Errorf("%s is not installed on this host: %w", spec.Args[0], err)
	}
	cmd := exec.CommandContext(ctx, spec.Args[0], spec.Args[1:]...) // #nosec G204
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// WriteStatus persists ProbeAll output for the API to read.
func WriteStatus(artifactsRoot string, engines []EngineStatus) error {
	if artifactsRoot == "" {
		return nil
	}
	if err := os.MkdirAll(artifactsRoot, 0o750); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(map[string]any{
		"engines":    engines,
		"updated_at": time.Now().UTC().Format(time.RFC3339),
	}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(artifactsRoot, StatusFileName), payload, 0o600)
}

// ReadStatus loads the worker-written engine file. Missing file is (nil, nil).
func ReadStatus(artifactsRoot string) ([]EngineStatus, error) {
	if artifactsRoot == "" {
		return nil, nil
	}
	data, err := os.ReadFile(filepath.Join(artifactsRoot, StatusFileName)) // #nosec G304
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var payload struct {
		Engines []EngineStatus `json:"engines"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	return payload.Engines, nil
}
