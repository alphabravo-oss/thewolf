// Package container provides a docker-backed replacement for the legacy
// plugin.CommandContext (host-exec) helper, used by every scanner plugin
// after the containerization migration.
//
// Design notes (also documented in PLAN.md §5.3):
//
//   - Plugins call CommandContext(ctx, cfg, opts, tool, args...). The returned
//     *exec.Cmd, when Run/Output is called, invokes `docker run --rm ...
//     wolf-scanners:<tag> <tool> <args>` with the repo bind-mounted read-only
//     at /scan and the runtime user pinned to host uid:gid.
//
//   - Context cancellation triggers `docker kill <container-name>` rather than
//     SIGKILL on the local docker CLI process. This is the only signal that
//     reliably stops the tool *inside* the container.
//
//   - Path translation has two flavors:
//     1. HOST→DAEMON: when wolf-slim runs in a container, the host paths
//     that docker daemon needs for -v aren't visible inside wolf-slim.
//     We translate InContainerReposRoot → HostReposRoot before -v.
//     2. CONTAINER→REPO-RELATIVE: paths returned in scanner findings are
//     "/scan/foo.py". translate.go normalizes those to "foo.py" so
//     fingerprints are portable across hosts.
package container

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// ToolImageSpec describes an upstream-official image for a single tool.
// Used in Config.UpstreamTools to route specific tools (trivy, semgrep,
// gitleaks, …) to maintainer-published images instead of the wolf-built
// scanner image.
//
// Example:
//
//	"trivy":   {Image: "aquasec/trivy:0.57.0", Entrypoint: ""}  // trivy is the image's default entrypoint
//	"semgrep": {Image: "semgrep/semgrep:1.92", Entrypoint: ""}
//	"gosec":   {Image: "securego/gosec:2.21.4", Entrypoint: "gosec"} // override needed if entrypoint differs
type ToolImageSpec struct {
	// Image is the fully-qualified upstream image reference.
	Image string
	// Entrypoint, when non-empty, is passed as `docker run --entrypoint <name>`
	// to override whatever ENTRYPOINT the image declares. Leave empty to
	// trust the image's own entrypoint.
	Entrypoint string
}

// PullPolicy controls how EnsureImage handles a missing scanners image.
type PullPolicy int

const (
	// PullIfNotPresent pulls the image only when it is not already present
	// locally. Default for production.
	PullIfNotPresent PullPolicy = iota
	// PullAlways pulls on every startup. Useful for floating dev tags.
	PullAlways
	// PullNever errors if the image is absent. Useful for air-gapped installs
	// where the image is loaded via `docker load`.
	PullNever
)

// String returns the canonical lowercase name. Matches the wolf.yaml format.
func (p PullPolicy) String() string {
	switch p {
	case PullAlways:
		return "Always"
	case PullNever:
		return "Never"
	default:
		return "IfNotPresent"
	}
}

// ParsePullPolicy is the inverse of String. Accepts mixed case for forgiveness.
func ParsePullPolicy(s string) (PullPolicy, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "ifnotpresent", "if-not-present":
		return PullIfNotPresent, nil
	case "always":
		return PullAlways, nil
	case "never":
		return PullNever, nil
	default:
		return PullIfNotPresent, fmt.Errorf("invalid pull policy %q (want IfNotPresent|Always|Never)", s)
	}
}

// Config is the runtime configuration for the container backend. One instance
// is built at wolf-slim startup (from wolf.yaml + env), then passed through
// the runner into every plugin via models.ExecuteOpts.ContainerCfg.
type Config struct {
	// DockerPath and DockerEnvironment select the daemon endpoint used by the
	// trusted caller. DockerEnvironment is exact (not merged with the ambient
	// process environment), which lets managed release workers use a remote
	// mTLS engine without exposing those credentials inside scanner containers.
	DockerPath        string
	DockerEnvironment []string

	// Image is the DEFAULT fully-qualified scanners image reference, used
	// for any tool that does not have a per-tool override in ImageOverrides.
	// Example: "ghcr.io/alphabravocompany/wolf-scanners:1.0.0".
	Image string

	// ImageOverrides is the per-tool override map for our OWN bucket images
	// (wolf-scanners-jvm, -rust, -codeql). All images in this map are
	// expected to have wolf-tool-entry as their entrypoint, so the shim
	// invokes them as: `docker run image <tool> <args...>`.
	ImageOverrides map[string]string

	// UpstreamTools maps tool name → upstream-official image (e.g.
	// `aquasec/trivy:0.57.0`, `semgrep/semgrep:1.92`). Unlike ImageOverrides,
	// these images don't use wolf-tool-entry: the shim either trusts the
	// image's own ENTRYPOINT (when ToolImageSpec.Entrypoint is empty) or
	// overrides it explicitly (when set). The tool name is NOT passed as
	// the first arg.
	//
	// Lookup precedence in ImageFor / shim invocation:
	//   1. UpstreamTools[tool]     — upstream image, no wolf-tool-entry
	//   2. ImageOverrides[tool]    — our bucket image, wolf-tool-entry semantics
	//   3. Image                   — default wolf-scanners image
	UpstreamTools map[string]ToolImageSpec

	// PullPolicy controls EnsureImage behavior at startup.
	PullPolicy PullPolicy

	// Network is the docker --network value. "bridge" (default), "none"
	// (paranoid mode; some tools degrade), or "host" (discouraged; defeats
	// isolation).
	Network string

	// UID and GID are the host uid:gid passed via --user. Resolved once at
	// startup via os.Getuid()/os.Getgid(). Zero values are valid (means root,
	// which is *not* recommended).
	UID int
	GID int

	// HostReposRoot is the path on the docker daemon's filesystem that
	// corresponds to InContainerReposRoot. When wolf-slim runs in a container,
	// these differ — paths the wolf API sees ("/repos/myrepo") must be
	// translated to the daemon's view ("/Users/me/projects/myrepo") before
	// being passed to `-v ...:/scan`.
	//
	// If both are empty, no translation happens (dev mode: wolf-slim runs on
	// host, paths line up).
	HostReposRoot        string
	InContainerReposRoot string
	// HostWorkspaceRoot maps the worker-visible workspace used for one-shot
	// Git/SSH snapshots to the same directory on the Docker daemon host.
	HostWorkspaceRoot        string
	InContainerWorkspaceRoot string

	// Memory is the --memory value (e.g. "2g"). Empty disables the limit.
	Memory string
	// CPUs is the --cpus value (e.g. "1.5"). Empty disables the limit.
	CPUs string

	// DBVolume is the name of a Docker volume mounted at /var/lib/wolf-db
	// in every scanner container. Used for shared vuln-DB caches (trivy,
	// grype). Empty disables the volume mount.
	DBVolume string

	// RepoVolume mounts a Docker-managed volume at /scan instead of asking the
	// daemon to resolve a worker-local bind path. It is used by remote release
	// engines after the trusted adapter has materialized the canonical corpus.
	RepoVolume string

	// OnContainerScheduled observes the generated immutable container name.
	// Managed quality evidence uses it to sample peak memory from the engine.
	OnContainerScheduled func(string)

	// ExtraEnv is forwarded to every scanner container as -e flags. Useful
	// for tool-specific env (e.g. SEMGREP_APP_TOKEN). Keys must be uppercase.
	ExtraEnv map[string]string

	// Disabled is true when the container backend is intentionally off, e.g.
	// when running unit tests in pure CI. CheckAvailable returns true and
	// CommandContext returns a clearly-failing command. Callers should not
	// reach this path in normal operation.
	Disabled bool
}

// DefaultConfig returns a Config populated with sensible defaults for
// production use. Callers should override Image at minimum.
func DefaultConfig() *Config {
	return &Config{
		DockerPath:               "docker",
		Image:                    "wolf-scanners:dev",
		PullPolicy:               PullIfNotPresent,
		Network:                  "bridge",
		UID:                      os.Getuid(),
		GID:                      os.Getgid(),
		HostReposRoot:            "",
		InContainerReposRoot:     "",
		HostWorkspaceRoot:        "",
		InContainerWorkspaceRoot: "",
		Memory:                   "2g",
		CPUs:                     "1.5",
		DBVolume:                 "",
		RepoVolume:               "",
		ExtraEnv:                 nil,
	}
}

// Validate returns nil if the config is internally consistent, otherwise a
// descriptive error suitable for surfacing to operators.
func (c *Config) Validate() error {
	if c == nil {
		return fmt.Errorf("container config is nil")
	}
	if c.Disabled {
		return nil
	}
	if c.Image == "" {
		return fmt.Errorf("container image is empty (set scan.container.image or WOLF_SCANNERS_IMAGE)")
	}
	if c.DockerPath == "" {
		c.DockerPath = "docker"
	}
	for _, item := range c.DockerEnvironment {
		name, _, ok := strings.Cut(item, "=")
		if !ok || name == "" || strings.ContainsAny(name, "\x00") || strings.ContainsRune(item, '\x00') {
			return fmt.Errorf("container Docker environment contains an invalid assignment")
		}
	}
	if c.RepoVolume != "" && !dockerVolumeNamePattern.MatchString(c.RepoVolume) {
		return fmt.Errorf("container repository volume name is invalid")
	}
	if c.Network == "" {
		// We allow this — docker defaults to bridge — but it's worth flagging.
		c.Network = "bridge"
	}
	if (c.HostReposRoot == "") != (c.InContainerReposRoot == "") {
		return fmt.Errorf("HostReposRoot and InContainerReposRoot must both be set, or both empty (got host=%q, in-container=%q)",
			c.HostReposRoot, c.InContainerReposRoot)
	}
	if (c.HostWorkspaceRoot == "") != (c.InContainerWorkspaceRoot == "") {
		return fmt.Errorf("HostWorkspaceRoot and InContainerWorkspaceRoot must both be set, or both empty (got host=%q, in-container=%q)",
			c.HostWorkspaceRoot, c.InContainerWorkspaceRoot)
	}
	return nil
}

func (c *Config) dockerPath() string {
	if c == nil || strings.TrimSpace(c.DockerPath) == "" {
		return "docker"
	}
	return c.DockerPath
}

var dockerVolumeNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

// ImageFor returns the image reference for the named tool, walking the
// lookup precedence: UpstreamTools → ImageOverrides → default Image.
func (c *Config) ImageFor(tool string) string {
	if c == nil {
		return ""
	}
	if spec, ok := c.UpstreamTools[tool]; ok && spec.Image != "" {
		return spec.Image
	}
	if img, ok := c.ImageOverrides[tool]; ok && img != "" {
		return img
	}
	return c.Image
}

// HasDedicatedImage reports whether a tool will run inside a
// non-default image — either an upstream spec or a wolf-built bucket
// override. Returns false when the tool falls through to the default
// wolf-scanners image. Heavyweight tools (codeql, infer, pmd, …) that
// only exist inside bucket images can use this in CheckAvailable to
// skip cleanly when the operator hasn't built/wired the bucket.
func (c *Config) HasDedicatedImage(tool string) bool {
	if c == nil {
		return false
	}
	if spec, ok := c.UpstreamTools[tool]; ok && spec.Image != "" {
		return true
	}
	if img, ok := c.ImageOverrides[tool]; ok && img != "" {
		return true
	}
	return false
}

// UpstreamSpec returns the upstream image spec for the named tool, or
// (ToolImageSpec{}, false) if the tool is served by ImageOverrides or the
// default image. The shim uses this to decide whether to bypass
// wolf-tool-entry.
func (c *Config) UpstreamSpec(tool string) (ToolImageSpec, bool) {
	if c == nil {
		return ToolImageSpec{}, false
	}
	spec, ok := c.UpstreamTools[tool]
	if !ok || spec.Image == "" {
		return ToolImageSpec{}, false
	}
	return spec, true
}

// AllImages returns the de-duplicated set of image references the config can
// produce. Used by EnsureAllImages so wolf-slim startup pulls every needed
// image once (rather than lazily at first scan).
func (c *Config) AllImages() []string {
	if c == nil {
		return nil
	}
	seen := make(map[string]struct{}, 1+len(c.ImageOverrides)+len(c.UpstreamTools))
	out := make([]string, 0, 1+len(c.ImageOverrides)+len(c.UpstreamTools))
	add := func(img string) {
		if img == "" {
			return
		}
		if _, ok := seen[img]; ok {
			return
		}
		seen[img] = struct{}{}
		out = append(out, img)
	}
	add(c.Image)
	for _, v := range c.ImageOverrides {
		add(v)
	}
	for _, spec := range c.UpstreamTools {
		add(spec.Image)
	}
	return out
}

// --- process-wide default config (used by Plugin.CheckAvailable) -------------

var (
	defaultMu        sync.RWMutex
	defaultCfg       *Config
	runtimeAvailable bool
)

// SetDefault installs a *Config as the process-wide default. Called once by
// wolf-slim startup after loading wolf.yaml. Idempotent; later calls replace.
func SetDefault(c *Config) {
	defaultMu.Lock()
	defer defaultMu.Unlock()
	defaultCfg = c
}

// SetRuntimeAvailable marks a non-Docker runtime as ready. Image presence is
// then delegated to that runtime instead of Docker's local image cache.
func SetRuntimeAvailable(available bool) {
	defaultMu.Lock()
	defer defaultMu.Unlock()
	runtimeAvailable = available
}

// Default returns the process-wide default config (or nil before SetDefault).
// Plugins use this from CheckAvailable, where they don't have an ExecuteOpts
// to read ContainerCfg from.
func Default() *Config {
	defaultMu.RLock()
	defer defaultMu.RUnlock()
	return defaultCfg
}

// ConfigFromOpts extracts a *Config from a models.ExecuteOpts.ContainerCfg
// field (typed `any` to avoid an import cycle). Falls back to Default() when
// the opts value is nil or wrong-type.
//
// Plugins use this in Execute:
//
//	cfg := container.ConfigFromOpts(opts.ContainerCfg)
//	cmd := container.CommandContext(ctx, cfg, container.Options{RepoDir: opts.RepoPath},
//	    "bandit", "-r", "/scan", "-f", "json")
func ConfigFromOpts(v any) *Config {
	if cfg, ok := v.(*Config); ok && cfg != nil {
		return cfg
	}
	return Default()
}

// IsScannersReady is a convenience used by every plugin's CheckAvailable. It
// asks the process-wide default config whether its image (or all of them, if
// ImageOverrides is set) is locally ready to run.
//
// Returns false when:
//   - Default() returns nil (wolf-slim startup did not call SetDefault)
//   - The default image is not present locally (EnsureImage was not called or failed)
//   - The config is Disabled
func IsScannersReady() bool {
	cfg := Default()
	if cfg == nil || cfg.Disabled {
		return false
	}
	defaultMu.RLock()
	available := runtimeAvailable
	defaultMu.RUnlock()
	if available {
		return true
	}
	// We only require the default image to be ready here. Per-tool override
	// images, if present, are checked lazily by ImageReady when the plugin
	// actually runs.
	return ImageReady(cfg)
}

// TranslateRepoPath converts a repo path as seen by wolf-slim into the host
// path that the docker daemon resolves bind mounts against. See the doc on
// Config.HostReposRoot.
//
// If HostReposRoot/InContainerReposRoot are empty (dev mode), returns a clean
// version of p unchanged.
//
// In production translation mode, p must live under InContainerReposRoot or
// InContainerWorkspaceRoot. Unrelated absolute paths and traversal attempts
// are rejected rather than silently bind-mounted into scanner containers.
func (c *Config) TranslateRepoPath(p string) (string, error) {
	if strings.TrimSpace(p) == "" {
		return "", fmt.Errorf("repo path is empty")
	}
	if c == nil || (c.HostReposRoot == "" && c.HostWorkspaceRoot == "") {
		return filepath.Clean(p), nil
	}
	candidate := filepath.Clean(p)
	mappings := [][2]string{
		{c.InContainerReposRoot, c.HostReposRoot},
		{c.InContainerWorkspaceRoot, c.HostWorkspaceRoot},
	}
	for _, mapping := range mappings {
		if mapping[0] == "" || mapping[1] == "" {
			continue
		}
		if translated, ok := translateMappedPath(candidate, mapping[0], mapping[1]); ok {
			return translated, nil
		}
	}
	return "", fmt.Errorf("repo path %q is outside configured repo and workspace roots", p)
}

func translateMappedPath(candidate, inContainerRoot, hostRootValue string) (string, bool) {
	root := filepath.Clean(inContainerRoot)
	hostRoot := filepath.Clean(hostRootValue)
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return "", false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	if rel == "." {
		return hostRoot, true
	}
	hostPath := filepath.Join(hostRoot, rel)
	hostRel, err := filepath.Rel(hostRoot, hostPath)
	if err != nil || hostRel == ".." || strings.HasPrefix(hostRel, ".."+string(filepath.Separator)) || filepath.IsAbs(hostRel) {
		return "", false
	}
	return hostPath, true
}
