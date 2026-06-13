package validate

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
	"github.com/alphabravocompany/thewolf/internal/scannertools/manifest"
	"gopkg.in/yaml.v3"
)

type Result struct {
	ToolCount       int
	DefaultCount    int
	BucketCount     int
	UpstreamCount   int
	VersionVarCount int
}

type toolchainManifest struct {
	BaseImages map[string]string                 `yaml:"base_images"`
	Toolchains map[string]toolchainManifestEntry `yaml:"toolchains"`
}

type toolchainManifestEntry struct {
	Version         string `yaml:"version,omitempty"`
	VersionVariable string `yaml:"version_variable,omitempty"`
	Major           string `yaml:"major,omitempty"`
	Package         string `yaml:"package,omitempty"`
	Source          string `yaml:"source,omitempty"`
	Dockerfile      string `yaml:"dockerfile,omitempty"`
	InstallScript   string `yaml:"install_script,omitempty"`
}

func Run(root string) (Result, error) {
	if root == "" {
		var err error
		root, err = manifest.FindRepoRoot("")
		if err != nil {
			return Result{}, err
		}
	}
	m, err := manifest.LoadFile(filepath.Join(root, "scanners", "tools.yaml"))
	if err != nil {
		return Result{}, err
	}
	var errs []string
	errs = append(errs, validatePlugins(m)...)
	errs = append(errs, validateVersions(root, m)...)
	errs = append(errs, validateRouting(m)...)
	errs = append(errs, validateSmoke(root, m)...)
	errs = append(errs, validateToolchains(root)...)
	if len(errs) > 0 {
		sort.Strings(errs)
		return Result{}, fmt.Errorf("scanner metadata validation failed:\n- %s", strings.Join(errs, "\n- "))
	}
	counts := m.TierCounts()
	versionVars := 0
	for _, tool := range m.Tools {
		if tool.VersionVariable != "" {
			versionVars++
		}
	}
	return Result{
		ToolCount:       len(m.Tools),
		DefaultCount:    counts[manifest.TierDefault],
		BucketCount:     counts[manifest.TierBucket],
		UpstreamCount:   counts[manifest.TierUpstream],
		VersionVarCount: versionVars,
	}, nil
}

func validatePlugins(m *manifest.Manifest) []string {
	registered := map[string]models.Plugin{}
	for _, p := range plugin.Global.GetAll() {
		registered[p.Name()] = p
	}
	var errs []string
	for name, p := range registered {
		tool, ok := m.Tools[name]
		if !ok {
			errs = append(errs, "missing manifest entry for registered plugin "+name)
			continue
		}
		if tool.Category != string(p.Category()) {
			errs = append(errs, fmt.Sprintf("%s category=%s want %s", name, tool.Category, p.Category()))
		}
	}
	for name := range m.Tools {
		if _, ok := registered[name]; !ok {
			errs = append(errs, "manifest entry has no registered plugin "+name)
		}
	}
	return errs
}

func validateVersions(root string, m *manifest.Manifest) []string {
	data, err := os.ReadFile(filepath.Join(root, "scanners", "versions.env"))
	if err != nil {
		return []string{err.Error()}
	}
	versions, err := manifest.ParseVersionsEnv(data)
	if err != nil {
		return []string{err.Error()}
	}
	var errs []string
	used := map[string]struct{}{}
	for name, tool := range m.Tools {
		if tool.VersionVariable == "" {
			continue
		}
		used[tool.VersionVariable] = struct{}{}
		got, ok := versions[tool.VersionVariable]
		if !ok {
			errs = append(errs, name+" missing "+tool.VersionVariable+" in versions.env")
			continue
		}
		if got != tool.PinnedVersion {
			errs = append(errs, fmt.Sprintf("%s %s=%s want %s", name, tool.VersionVariable, got, tool.PinnedVersion))
		}
	}
	for key := range versions {
		if _, ok := used[key]; !ok {
			errs = append(errs, "versions.env variable not referenced by manifest: "+key)
		}
	}
	return errs
}

func validateRouting(m *manifest.Manifest) []string {
	upstream := container.DefaultUpstreamTools()
	buckets := bucketAssignments()
	var errs []string
	for name, tool := range m.Tools {
		switch tool.IntegrationTier {
		case manifest.TierUpstream:
			spec, ok := upstream[name]
			if !ok {
				errs = append(errs, name+" is upstream in manifest but not DefaultUpstreamTools")
				continue
			}
			if spec.Image != tool.Image.PinnedReference {
				errs = append(errs, fmt.Sprintf("%s upstream image=%s want %s", name, spec.Image, tool.Image.PinnedReference))
			}
			if spec.Entrypoint != tool.Image.Entrypoint {
				errs = append(errs, fmt.Sprintf("%s upstream entrypoint=%s want %s", name, spec.Entrypoint, tool.Image.Entrypoint))
			}
		case manifest.TierBucket:
			if got := buckets[name]; got != tool.Bucket {
				errs = append(errs, fmt.Sprintf("%s bucket=%s want %s", name, got, tool.Bucket))
			}
		case manifest.TierDefault:
			if _, ok := upstream[name]; ok {
				errs = append(errs, name+" is default in manifest but present in DefaultUpstreamTools")
			}
			if _, ok := buckets[name]; ok {
				errs = append(errs, name+" is default in manifest but present in DefaultBucketImages")
			}
		}
	}
	for name := range upstream {
		if tool, ok := m.Tools[name]; !ok || tool.IntegrationTier != manifest.TierUpstream {
			errs = append(errs, name+" is in DefaultUpstreamTools but not upstream in manifest")
		}
	}
	for name := range buckets {
		if tool, ok := m.Tools[name]; !ok || tool.IntegrationTier != manifest.TierBucket {
			errs = append(errs, name+" is in DefaultBucketImages but not bucket in manifest")
		}
	}
	return errs
}

func validateSmoke(root string, m *manifest.Manifest) []string {
	data, err := os.ReadFile(filepath.Join(root, "scanners", "smoke-test.sh"))
	if err != nil {
		return []string{err.Error()}
	}
	script := string(data)
	var errs []string
	for name, tool := range m.Tools {
		if tool.IntegrationTier == manifest.TierUpstream {
			continue
		}
		if len(tool.Smoke.Command) == 0 {
			errs = append(errs, name+" missing smoke.command in manifest")
			continue
		}
		bin := tool.Smoke.Command[0]
		if bin == "" {
			errs = append(errs, name+" smoke.command starts with empty binary")
			continue
		}
		if !strings.Contains(script, bin) {
			errs = append(errs, name+" smoke command "+bin+" is not referenced by scanners/smoke-test.sh")
		}
		if tool.Smoke.ExpectedPattern == tool.PinnedVersion && tool.VersionVariable != "" {
			if !strings.Contains(script, "$"+tool.VersionVariable) {
				errs = append(errs, name+" smoke expected_pattern uses pinned version but smoke-test.sh does not reference $"+tool.VersionVariable)
			}
		}
	}
	return errs
}

func validateToolchains(root string) []string {
	path := filepath.Join(root, "scanners", "toolchains.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return []string{err.Error()}
	}
	var tm toolchainManifest
	if err := yaml.Unmarshal(data, &tm); err != nil {
		return []string{fmt.Sprintf("decode %s: %v", path, err)}
	}
	var errs []string
	for variant, image := range tm.BaseImages {
		dockerfile := "Dockerfile"
		if variant != "default" {
			dockerfile = "Dockerfile." + variant
		}
		body, err := os.ReadFile(filepath.Join(root, "scanners", dockerfile))
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		if !strings.Contains(string(body), "FROM "+image) && !strings.Contains(string(body), "FROM --platform=linux/amd64 "+image) {
			errs = append(errs, fmt.Sprintf("%s base image=%s is not referenced by scanners/%s", variant, image, dockerfile))
		}
	}
	if goTC := tm.Toolchains["go"]; goTC.Version == "" {
		errs = append(errs, "toolchains.go.version is required")
	} else if !fileContains(root, "scanners/install/go-tools.sh", "GOTC_VERSION="+goTC.Version) {
		errs = append(errs, "toolchains.go.version does not match GOTC_VERSION in scanners/install/go-tools.sh")
	}
	if node := tm.Toolchains["nodejs"]; node.Major == "" {
		errs = append(errs, "toolchains.nodejs.major is required")
	} else if !fileContains(root, "scanners/Dockerfile", "setup_"+node.Major+".x") {
		errs = append(errs, "toolchains.nodejs.major does not match NodeSource setup script in scanners/Dockerfile")
	}
	if jdk := tm.Toolchains["jdk"]; jdk.Package == "" {
		errs = append(errs, "toolchains.jdk.package is required")
	} else if !fileContains(root, "scanners/Dockerfile.jvm", jdk.Package) {
		errs = append(errs, "toolchains.jdk.package does not match scanners/Dockerfile.jvm")
	}
	if rust := tm.Toolchains["rust"]; rust.VersionVariable == "" {
		errs = append(errs, "toolchains.rust.version_variable is required")
	} else if !fileContains(root, "scanners/versions.env", rust.VersionVariable+"=") || !fileContains(root, "scanners/install/rust.sh", rust.VersionVariable) {
		errs = append(errs, "toolchains.rust.version_variable must be defined in versions.env and consumed by install/rust.sh")
	}
	for _, name := range []string{"python", "ruby", "php"} {
		tc := tm.Toolchains[name]
		if tc.Package == "" || tc.Dockerfile == "" {
			errs = append(errs, "toolchains."+name+" must declare package and dockerfile")
			continue
		}
		if !fileContains(root, tc.Dockerfile, tc.Package) {
			errs = append(errs, fmt.Sprintf("toolchains.%s package %s is not referenced by %s", name, tc.Package, tc.Dockerfile))
		}
	}
	return errs
}

func fileContains(root, rel, needle string) bool {
	data, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		return false
	}
	return strings.Contains(string(data), needle)
}

func bucketAssignments() map[string]string {
	out := make(map[string]string)
	for tool, image := range container.DefaultBucketImages("wolf-scanners", "test") {
		switch {
		case strings.Contains(image, "-jvm:"):
			out[tool] = "jvm"
		case strings.Contains(image, "-rust:"):
			out[tool] = "rust"
		case strings.Contains(image, "-codeql:"):
			out[tool] = "codeql"
		default:
			out[tool] = "unknown"
		}
	}
	return out
}
