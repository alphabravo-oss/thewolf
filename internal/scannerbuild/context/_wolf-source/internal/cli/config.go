// Package cli implements the wolf command-line interface: a full HTTP client
// for the wolf API plus the local one-shot commands. Every API endpoint is
// reachable as a `wolf <resource> <verb>` subcommand.
package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// DefaultServer is used when no context, flag, or env var supplies one.
const DefaultServer = "http://localhost:8778"

// Context is one named server target — a URL and the credential to use.
type Context struct {
	Server string `yaml:"server"`
	Token  string `yaml:"token"`
}

// Config is the CLI's on-disk configuration: a set of named contexts (like
// kubeconfig) and a pointer to the active one.
type Config struct {
	CurrentContext string             `yaml:"current_context"`
	Contexts       map[string]Context `yaml:"contexts"`
}

// ConfigPath returns the CLI config file location, ~/.wolf/cli.yaml —
// co-located with the server's wolf.db and artifacts.
func ConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".wolf-cli.yaml"
	}
	return filepath.Join(home, ".wolf", "cli.yaml")
}

// LoadConfig reads the CLI config. A missing file is not an error — it
// yields an empty, usable config.
func LoadConfig() (*Config, error) {
	cfg := &Config{Contexts: map[string]Context{}}
	data, err := os.ReadFile(ConfigPath())
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, fmt.Errorf("read config: %w", err)
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", ConfigPath(), err)
	}
	if cfg.Contexts == nil {
		cfg.Contexts = map[string]Context{}
	}
	return cfg, nil
}

// Save writes the config to disk with 0600 permissions — it holds tokens.
func (c *Config) Save() error {
	path := ConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

// Active returns the context selected by name, or the current context when
// name is empty. The boolean reports whether a context was found.
func (c *Config) Active(name string) (Context, bool) {
	if name == "" {
		name = c.CurrentContext
	}
	if name == "" {
		return Context{}, false
	}
	ctx, ok := c.Contexts[name]
	return ctx, ok
}
