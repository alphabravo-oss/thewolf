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
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/plugin/container"
	"github.com/alphabravocompany/thewolf/internal/wolflog"
)

// execLookPath and execCommand are package vars so tests can stub the
// shell out — autoDiscoverBucketImages calls `docker image inspect`,
// which we don't want hitting the real daemon during unit tests.
var (
	execLookPath = exec.LookPath
	execCommand  = exec.Command
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
	if err := os.MkdirAll(dir, 0o750); err != nil {
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
	tag := ResolvedTag()
	return Config{
		Image:          envOr("WOLF_SCANNERS_IMAGE", "alphabravodevops/wolf-scanners:"+tag),
		ImageOverrides: envBucketOverrides(),
		PullPolicy:     envOr("WOLF_SCANNERS_PULL_POLICY", "IfNotPresent"),
		Network:        envOr("WOLF_SCANNERS_NETWORK", "bridge"),
		Memory:         envOr("WOLF_SCANNERS_MEMORY", "2g"),
		CPUs:           envOr("WOLF_SCANNERS_CPUS", "1.5"),
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

// DefaultScannersTag is the runtime default tag for the wolf-built scanner
// images. CI historically only published :latest and :dev; a build/push from
// the scanner-image feature is what cuts this versioned tag. Operators can
// override with WOLF_SCANNERS_TAG (e.g. WOLF_SCANNERS_TAG=latest) until a
// release publishes this tag.
const DefaultScannersTag = "2.0.0"

// ResolvedTag returns the scanner image tag the runtime resolves: the
// WOLF_SCANNERS_TAG override if set, else DefaultScannersTag.
func ResolvedTag() string {
	return envOr("WOLF_SCANNERS_TAG", DefaultScannersTag)
}

// bucketVariantEnv maps each bucket variant name to its dedicated
// WOLF_SCANNERS_IMAGE_* override env var.
var bucketVariantEnv = map[string]string{
	"jvm":    "WOLF_SCANNERS_IMAGE_JVM",
	"rust":   "WOLF_SCANNERS_IMAGE_RUST",
	"codeql": "WOLF_SCANNERS_IMAGE_CODEQL",
}

// bucketVariantSuffix maps each bucket variant name to the suffix appended to
// the default image repo (matching scannerbuild.Variants and the Makefile
// scanners-build-<variant> targets).
var bucketVariantSuffix = map[string]string{
	"jvm":    "-jvm",
	"rust":   "-rust",
	"codeql": "-codeql",
}

// ActiveImageRefs returns, per wolf-built variant, the fully-qualified
// "<ns>/<img>:<tag>" reference the runtime will resolve and pull. It honors
// the same env overrides the runtime config uses:
//
//   - WOLF_SCANNERS_IMAGE overrides the "default" ref outright (repo + tag).
//   - WOLF_SCANNERS_TAG sets the tag for refs derived from the default repo.
//   - WOLF_SCANNERS_IMAGE_{JVM,RUST,CODEQL} override a bucket variant outright.
//
// Bucket variants without an explicit override are derived from the default
// image's repo base by appending the variant suffix and the resolved tag, so
// the UI shows exactly what a scan would pull. Keys are the variant names
// "default", "jvm", "rust", "codeql".
func ActiveImageRefs() map[string]string {
	tag := ResolvedTag()
	defaultImage := envOr("WOLF_SCANNERS_IMAGE", "alphabravodevops/wolf-scanners:"+tag)

	refs := map[string]string{"default": defaultImage}

	// Derive bucket refs from the default image's repo base so a custom
	// WOLF_SCANNERS_IMAGE flows through to the buckets, while an explicit
	// per-bucket override still wins.
	base, _ := splitImageTag(defaultImage)
	for _, variant := range []string{"jvm", "rust", "codeql"} {
		if override := strings.TrimSpace(os.Getenv(bucketVariantEnv[variant])); override != "" {
			refs[variant] = override
			continue
		}
		if base == "" {
			continue
		}
		refs[variant] = base + bucketVariantSuffix[variant] + ":" + tag
	}
	return refs
}

// envBucketOverrides reads WOLF_SCANNERS_IMAGE_{JVM,RUST,CODEQL} and produces
// the per-tool override map. Tools not listed fall through to Config.Image.
func envBucketOverrides() map[string]string {
	out := map[string]string{}
	if v := os.Getenv("WOLF_SCANNERS_IMAGE_JVM"); v != "" {
		out["detekt"] = v
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
	warnFloatingLatestImages(cfg)
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
	// Auto-discover bucket images locally. The three-tier design keeps
	// heavyweight tools (codeql ~800MB, JVM ~600MB infer+pmd, rust
	// ~400MB clippy) in dedicated bucket images so the default wolf-
	// scanners stays lean. Rather than make operators set
	// WOLF_SCANNERS_IMAGE_{JVM,RUST,CODEQL} by hand, walk `docker image
	// ls` once at startup and wire the bucket-served tools into
	// ImageOverrides if the corresponding image exists. Explicit env
	// vars still take precedence (handled by ToContainerConfig before
	// this point).
	autoDiscoverBucketImages(cfg)

	container.SetDefault(cfg)
	if err := container.EnsureImage(ctx, cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// bucketImageTools is the per-bucket map of tools served by each bucket
// image. Keys match the SCANNERS_IMAGE-{name} convention used by the
// Makefile targets (scanners-build-jvm, etc.).
var bucketImageTools = map[string][]string{
	"jvm":    {"detekt", "infer", "pmd"},
	"rust":   {"clippy"},
	"codeql": {"codeql"},
}

func warnFloatingLatestImages(cfg *container.Config) {
	for _, image := range latestTaggedScannerImages(cfg) {
		wolflog.Warn().
			Str("image", image).
			Msg("scanner image uses floating :latest tag; pin a release tag or digest for reproducible scans")
	}
}

func latestTaggedScannerImages(cfg *container.Config) []string {
	if cfg == nil {
		return nil
	}
	seen := map[string]struct{}{}
	var out []string
	for _, image := range cfg.AllImages() {
		if !usesLatestTag(image) {
			continue
		}
		if _, ok := seen[image]; ok {
			continue
		}
		seen[image] = struct{}{}
		out = append(out, image)
	}
	return out
}

func usesLatestTag(ref string) bool {
	if ref == "" || strings.Contains(ref, "@sha256:") {
		return false
	}
	slash := strings.LastIndex(ref, "/")
	colon := strings.LastIndex(ref, ":")
	return colon > slash && ref[colon+1:] == "latest"
}

// autoDiscoverBucketImages probes docker for locally-available bucket
// images and populates cfg.ImageOverrides for the tools each bucket
// serves. Already-configured overrides (from WOLF_SCANNERS_IMAGE_*) win
// — we only fill in what the operator hasn't explicitly set.
//
// Defense: each candidate bucket image's ID is compared to the default
// image's ID. If they match, the bucket is a tag-alias (e.g. the
// operator ran `docker tag wolf-scanners:dev wolf-scanners-jvm:dev`
// instead of building the bucket for real) and the override is
// skipped with a warning. Wiring the override anyway would route
// bucket tools to an image that doesn't contain them, producing
// "tool not present in this image" failures at scan time.
func autoDiscoverBucketImages(cfg *container.Config) {
	if cfg == nil {
		return
	}
	// Derive the bucket image base name from the default image. With
	// the default "wolf-scanners:dev" we look for "wolf-scanners-jvm:dev"
	// etc. Anything more exotic gets skipped silently.
	base, tag := splitImageTag(cfg.Image)
	if base == "" {
		return
	}
	if cfg.ImageOverrides == nil {
		cfg.ImageOverrides = map[string]string{}
	}
	defaultID := dockerImageID(cfg.Image)
	for bucket, tools := range bucketImageTools {
		candidate := base + "-" + bucket + ":" + tag
		candidateID := dockerImageID(candidate)
		if candidateID == "" {
			continue
		}
		if defaultID != "" && candidateID == defaultID {
			wolflog.Warn().
				Str("bucket", candidate).
				Str("default", cfg.Image).
				Strs("tools", tools).
				Msg("bucket image is a tag-alias of default; skipping override (run `make scanners-build-" + bucket + "` to build for real)")
			continue
		}
		for _, tool := range tools {
			if _, alreadySet := cfg.ImageOverrides[tool]; alreadySet {
				continue
			}
			cfg.ImageOverrides[tool] = candidate
		}
	}
}

// splitImageTag parses "repo:tag" into ("repo", "tag"). When no tag is
// present, defaults the tag to "dev" so the bucket lookup uses the same
// convention as the Makefile targets.
func splitImageTag(s string) (repo, tag string) {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == ':' {
			// guard against registry-port colons by ensuring no '/' after
			if !containsAfter(s, '/', i) {
				return s[:i], s[i+1:]
			}
		}
		if s[i] == '/' {
			break
		}
	}
	if s == "" {
		return "", ""
	}
	return s, "dev"
}

func containsAfter(s string, c byte, idx int) bool {
	for i := idx + 1; i < len(s); i++ {
		if s[i] == c {
			return true
		}
	}
	return false
}

// dockerImageID returns the local image ID for ref (e.g. "sha256:abc…")
// or "" if docker is absent, the image isn't present, or inspect fails.
// The ID is the content-addressable manifest digest; two tags pointing
// to the same image share an ID, which is how we detect tag-aliases.
func dockerImageID(ref string) string {
	if _, err := execLookPath("docker"); err != nil {
		return ""
	}
	cmd := execCommand("docker", "image", "inspect", "--format", "{{.Id}}", ref)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
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
