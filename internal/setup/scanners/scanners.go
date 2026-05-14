// Package scanners wires up the container scanner backend at wolf-slim
// startup. It is intentionally small: load config (yaml + env), populate the
// process-wide *container.Config, and EnsureImage for the default image.
//
// The actual cmd/wolf main package is responsible for calling LoadAndInstall
// once at startup. Subcommands `wolf doctor` and `wolf pull scanners` are
// implemented here as Doctor() and Pull() so the CLI is a thin wrapper.
package scanners

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/plugin/container"
)

// defaultDBVolume returns a host bind-mount path under the user's home for
// persistent scanner-DB caching. A host path is preferred over a Docker named
// volume because the scanner containers run as the host user (UID/GID) and a
// named volume's default root-owned perms would block writes.
func defaultDBVolume() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	dir := filepath.Join(home, ".wolf", "scanner-cache")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	return dir
}

// Config is the wolf.yaml `scan.container` shape, with env overrides applied.
// Field tags use yaml.v3 conventions, but we don't depend on yaml.v3 here —
// the config loader in cmd/wolf/main.go is expected to populate this struct
// from whichever yaml lib it uses.
type Config struct {
	Image                string            `yaml:"image"`
	ImageOverrides       map[string]string `yaml:"image_overrides"`
	PullPolicy           string            `yaml:"pull_policy"`
	Network              string            `yaml:"network"`
	Memory               string            `yaml:"memory"`
	CPUs                 string            `yaml:"cpus"`
	DBVolume             string            `yaml:"db_volume"`
	DBRefresh            string            `yaml:"db_refresh"`
	HostReposRoot        string            `yaml:"host_repos_root"`
	InContainerReposRoot string            `yaml:"in_container_repos_root"`
	ExtraEnv             map[string]string `yaml:"extra_env"`
}

// EnvDefaults builds a Config from the WOLF_SCANNERS_* environment variables,
// applying sensible defaults for any unset value. Callers can either use the
// result directly or merge it with a yaml-loaded Config.
func EnvDefaults() Config {
	return Config{
		Image:                envOr("WOLF_SCANNERS_IMAGE", "ghcr.io/alphabravocompany/wolf-scanners:dev"),
		ImageOverrides:       envBucketOverrides(),
		PullPolicy:           envOr("WOLF_SCANNERS_PULL_POLICY", "IfNotPresent"),
		Network:              envOr("WOLF_SCANNERS_NETWORK", "bridge"),
		Memory:               envOr("WOLF_SCANNERS_MEMORY", "2g"),
		CPUs:                 envOr("WOLF_SCANNERS_CPUS", "1.5"),
		// Default to a host bind-mount under ~/.wolf/scanner-cache so
		// vulnerability DBs (grype, trivy, etc.) persist across scan
		// runs without permission issues (the path inherits the host
		// user's UID/GID, matching the scanner container user). A
		// Docker named volume would land root-owned and block writes
		// from the non-root scanner user. Operators who want strict
		// ephemerality can set WOLF_SCANNERS_DB_VOLUME="" explicitly.
		DBVolume:             envOr("WOLF_SCANNERS_DB_VOLUME", defaultDBVolume()),
		DBRefresh:            envOr("WOLF_SCANNERS_DB_REFRESH", "never"),
		HostReposRoot:        envOr("WOLF_HOST_REPOS_ROOT", ""),
		InContainerReposRoot: envOr("WOLF_IN_CONTAINER_REPOS_ROOT", ""),
	}
}

// envBucketOverrides reads WOLF_SCANNERS_IMAGE_{JVM,RUST,CODEQL} and produces
// the per-tool override map. Tools not listed fall through to Config.Image.
func envBucketOverrides() map[string]string {
	out := map[string]string{}
	if v := os.Getenv("WOLF_SCANNERS_IMAGE_JVM"); v != "" {
		out["infer"] = v
		out["pmd"] = v
	}
	if v := os.Getenv("WOLF_SCANNERS_IMAGE_RUST"); v != "" {
		out["clippy"] = v
	}
	if v := os.Getenv("WOLF_SCANNERS_IMAGE_CODEQL"); v != "" {
		out["codeql"] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ToContainerConfig translates a Config into a *container.Config suitable for
// container.SetDefault. Returns an error if values can't be parsed.
//
// The default upstream-tools map (container.DefaultUpstreamTools) is
// merged in automatically — set WOLF_SCANNERS_DISABLE_UPSTREAM=1 to
// opt out (forcing everything through wolf-built bucket images).
func (c Config) ToContainerConfig() (*container.Config, error) {
	pp, err := container.ParsePullPolicy(c.PullPolicy)
	if err != nil {
		return nil, err
	}
	var upstream map[string]container.ToolImageSpec
	if os.Getenv("WOLF_SCANNERS_DISABLE_UPSTREAM") != "1" {
		upstream = container.DefaultUpstreamTools()
	}
	cfg := &container.Config{
		Image:                c.Image,
		ImageOverrides:       c.ImageOverrides,
		UpstreamTools:        upstream,
		PullPolicy:           pp,
		Network:              c.Network,
		UID:                  os.Getuid(),
		GID:                  os.Getgid(),
		HostReposRoot:        c.HostReposRoot,
		InContainerReposRoot: c.InContainerReposRoot,
		Memory:               c.Memory,
		CPUs:                 c.CPUs,
		DBVolume:             c.DBVolume,
		ExtraEnv:             c.ExtraEnv,
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// LoadAndInstall builds a *container.Config from envs (caller may overlay
// yaml values onto the returned Config before calling SetDefault by hand),
// installs it as the process-wide default, and ensures the default image is
// locally available per the pull policy.
//
// This is the single entry point cmd/wolf/main.go should call at startup.
func LoadAndInstall(ctx context.Context) (*container.Config, error) {
	cfg, err := EnvDefaults().ToContainerConfig()
	if err != nil {
		return nil, fmt.Errorf("scanners: invalid container config: %w", err)
	}
	container.SetDefault(cfg)
	if err := container.EnsureImage(ctx, cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// Doctor runs a series of diagnostics and writes a human-readable report to w.
// Returns nil if every check passed; otherwise an aggregate error. Intended
// as the body of `wolf doctor`.
func Doctor(ctx context.Context, w io.Writer) error {
	cfg := container.Default()
	if cfg == nil {
		fmt.Fprintln(w, "FAIL  Container config not loaded (was scanners.LoadAndInstall called at startup?)")
		return errors.New("container config not loaded")
	}

	var firstErr error
	step := func(label string, fn func() error) {
		err := fn()
		if err != nil {
			fmt.Fprintf(w, "FAIL  %-30s %s\n", label, err)
			if firstErr == nil {
				firstErr = err
			}
		} else {
			fmt.Fprintf(w, "OK    %-30s\n", label)
		}
	}

	step("docker reachable", func() error { return container.DockerAvailable(ctx) })
	step("scanners image present", func() error {
		if !container.ImageReady(cfg) {
			return fmt.Errorf("image %q not ready", cfg.Image)
		}
		return nil
	})
	step("uid/gid mapped", func() error {
		if cfg.UID == 0 {
			return errors.New("running as root inside scanners is discouraged; set --user")
		}
		return nil
	})
	step("repos-root pairing", func() error {
		if (cfg.HostReposRoot == "") != (cfg.InContainerReposRoot == "") {
			return errors.New("HostReposRoot and InContainerReposRoot must both be set or both empty")
		}
		return nil
	})
	step("override images known", func() error {
		for tool, img := range cfg.ImageOverrides {
			if img == "" {
				return fmt.Errorf("override for %q is empty", tool)
			}
		}
		return nil
	})

	fmt.Fprintln(w)
	if firstErr != nil {
		fmt.Fprintln(w, "doctor: one or more checks failed. See above.")
		return firstErr
	}
	fmt.Fprintln(w, "doctor: all checks passed.")
	return nil
}

// Pull ensures every image in cfg (default + overrides) is locally available
// per the pull policy. Intended as the body of `wolf pull scanners`.
func Pull(ctx context.Context) error {
	cfg := container.Default()
	if cfg == nil {
		return errors.New("container config not loaded")
	}

	imgs := cfg.AllImages()
	for _, img := range imgs {
		sub := *cfg
		sub.Image = img
		sub.ImageOverrides = nil
		if err := container.EnsureImage(ctx, &sub); err != nil {
			return fmt.Errorf("pull %q: %w", img, err)
		}
	}
	return nil
}

// --- helpers ---

func envOr(key, fallback string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	return v
}

// envInt parses an env var as int, returning fallback if unset or unparseable.
func envInt(key string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

var _ = envInt // reserved for future use (concurrency override, etc.)
