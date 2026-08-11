package validate

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
	scannerlock "github.com/alphabravocompany/thewolf/internal/scannertools/lock"
	"github.com/alphabravocompany/thewolf/internal/scannertools/manifest"
	"github.com/alphabravocompany/thewolf/internal/scannertools/ospackages"
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
	Version                  string `yaml:"version,omitempty"`
	VersionVariable          string `yaml:"version_variable,omitempty"`
	BootstrapVersion         string `yaml:"bootstrap_version,omitempty"`
	BootstrapVersionVariable string `yaml:"bootstrap_version_variable,omitempty"`
	LinuxAMD64SHA256         string `yaml:"linux_amd64_sha256,omitempty"`
	LinuxAMD64SHA256Variable string `yaml:"linux_amd64_sha256_variable,omitempty"`
	LinuxARM64SHA256         string `yaml:"linux_arm64_sha256,omitempty"`
	LinuxARM64SHA256Variable string `yaml:"linux_arm64_sha256_variable,omitempty"`
	Major                    string `yaml:"major,omitempty"`
	Package                  string `yaml:"package,omitempty"`
	Source                   string `yaml:"source,omitempty"`
	Dockerfile               string `yaml:"dockerfile,omitempty"`
	InstallScript            string `yaml:"install_script,omitempty"`
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
	errs = append(errs, validateDirectArtifactIntegrity(root, m)...)
	errs = append(errs, validateRouting(m)...)
	errs = append(errs, validateSmoke(root, m)...)
	errs = append(errs, validateToolchains(root)...)
	errs = append(errs, validateScannerBaseImages(root)...)
	if err := ospackages.Check(root); err != nil {
		errs = append(errs, err.Error())
	}
	errs = append(errs, validateParserFixtures(root, m)...)
	errs = append(errs, validateLock(root, m)...)
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

func validateDirectArtifactIntegrity(root string, m *manifest.Manifest) []string {
	requirements := map[string]string{
		"phpstan": "scanners/install/php.sh",
	}
	var errs []string
	for toolName, script := range requirements {
		tool, ok := m.Tools[toolName]
		if !ok {
			errs = append(errs, "direct-download integrity tool is absent: "+toolName)
			continue
		}
		integrity := tool.SourceIntegrity
		if integrity.URL == "" || integrity.SHA256 == "" || integrity.SHA256Variable == "" {
			errs = append(errs, toolName+" must declare URL, SHA-256, and checksum variable")
			continue
		}
		if tool.PinnedVersion == "" || !strings.Contains(integrity.URL, tool.PinnedVersion) {
			errs = append(errs, toolName+" integrity URL must bind the exact pinned version")
		}
		if !fileContains(root, script, integrity.SHA256Variable) ||
			!fileContains(root, script, "sha256sum --check --strict") {
			errs = append(errs, fmt.Sprintf(
				"%s installer %s does not fail closed on %s", toolName, script, integrity.SHA256Variable,
			))
		}
	}
	return errs
}

var pinnedDockerBasePattern = regexp.MustCompile(`^[^@\s]+@sha256:[0-9a-f]{64}$`)

func validateScannerBaseImages(root string) []string {
	files, err := filepath.Glob(filepath.Join(root, "scanners", "Dockerfile*"))
	if err != nil {
		return []string{"list scanner Dockerfiles: " + err.Error()}
	}
	var errs []string
	for _, file := range files {
		input, readErr := os.Open(file)
		if readErr != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", filepath.Base(file), readErr))
			continue
		}
		scanner := bufio.NewScanner(input)
		lineNumber := 0
		for scanner.Scan() {
			lineNumber++
			fields := strings.Fields(scanner.Text())
			if len(fields) < 2 || !strings.EqualFold(fields[0], "FROM") {
				continue
			}
			index := 1
			if strings.HasPrefix(fields[index], "--platform=") {
				index++
			}
			if index >= len(fields) {
				errs = append(errs, fmt.Sprintf("%s:%d has an invalid FROM instruction", filepath.Base(file), lineNumber))
				continue
			}
			base := fields[index]
			if base == "scratch" || strings.HasPrefix(base, "$") {
				continue
			}
			if !pinnedDockerBasePattern.MatchString(base) {
				errs = append(errs, fmt.Sprintf(
					"%s:%d base image %q is not pinned by sha256 digest",
					filepath.Base(file), lineNumber, base,
				))
			}
		}
		if scanErr := scanner.Err(); scanErr != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", filepath.Base(file), scanErr))
		}
		_ = input.Close()
	}
	return errs
}

func validateLock(root string, m *manifest.Manifest) []string {
	path := filepath.Join(root, scannerlock.DefaultLockPath)
	current, err := os.ReadFile(path)
	if err != nil {
		return []string{fmt.Sprintf("scanner lock: %v", err)}
	}
	parsed, err := scannerlock.Parse(current)
	if err != nil {
		return []string{err.Error()}
	}
	if err := parsed.ValidateManifestCoverage(m); err != nil {
		return []string{err.Error()}
	}
	generated, err := scannerlock.Generate(context.Background(), root, scannerlock.GenerateOptions{ExistingLock: parsed})
	if err != nil {
		return []string{"generate scanner lock: " + err.Error()}
	}
	expected, err := generated.MarshalYAML()
	if err != nil {
		return []string{"marshal scanner lock: " + err.Error()}
	}
	if !bytes.Equal(current, expected) {
		return []string{"scanners/scanner-lock.yaml is stale; run `go run ./cmd/scannertools lock`"}
	}
	return nil
}

func validateParserFixtures(root string, m *manifest.Manifest) []string {
	var errs []string
	for name, tool := range m.Tools {
		for _, fixture := range tool.ParserContract.Fixtures {
			info, err := os.Stat(filepath.Join(root, filepath.FromSlash(fixture)))
			if err != nil {
				errs = append(errs, fmt.Sprintf("%s parser fixture %s: %v", name, fixture, err))
			} else if info.IsDir() {
				errs = append(errs, fmt.Sprintf("%s parser fixture %s is a directory", name, fixture))
			}
		}
	}
	return errs
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
		if tool.VersionVariable != "" {
			used[tool.VersionVariable] = struct{}{}
			got, ok := versions[tool.VersionVariable]
			if !ok {
				errs = append(errs, name+" missing "+tool.VersionVariable+" in versions.env")
			} else if got != tool.PinnedVersion {
				errs = append(errs, fmt.Sprintf("%s %s=%s want %s", name, tool.VersionVariable, got, tool.PinnedVersion))
			}
		}
		if variable := tool.SourceIntegrity.SHA256Variable; variable != "" {
			used[variable] = struct{}{}
			got, ok := versions[variable]
			expected := strings.TrimPrefix(tool.SourceIntegrity.SHA256, "sha256:")
			if !ok {
				errs = append(errs, name+" missing "+variable+" in versions.env")
			} else if got != expected {
				errs = append(errs, fmt.Sprintf("%s %s=%s want %s", name, variable, got, expected))
			}
		}
	}
	toolchainPath := filepath.Join(root, "scanners", "toolchains.yaml")
	toolchainData, err := os.ReadFile(toolchainPath)
	if err != nil {
		return []string{err.Error()}
	}
	var toolchains toolchainManifest
	if err := yaml.Unmarshal(toolchainData, &toolchains); err != nil {
		return []string{fmt.Sprintf("decode %s: %v", toolchainPath, err)}
	}
	if rust, ok := toolchains.Toolchains["rust"]; ok {
		for _, pin := range []struct {
			name, value, variable string
		}{
			{"bootstrap version", rust.BootstrapVersion, rust.BootstrapVersionVariable},
			{"linux/amd64 bootstrap SHA-256", rust.LinuxAMD64SHA256, rust.LinuxAMD64SHA256Variable},
			{"linux/arm64 bootstrap SHA-256", rust.LinuxARM64SHA256, rust.LinuxARM64SHA256Variable},
		} {
			if pin.value == "" || pin.variable == "" {
				errs = append(errs, "toolchains.rust "+pin.name+" pin is incomplete")
				continue
			}
			used[pin.variable] = struct{}{}
			if got := versions[pin.variable]; got != pin.value {
				errs = append(errs, fmt.Sprintf(
					"toolchains.rust %s=%s want %s", pin.variable, got, pin.value,
				))
			}
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
	upstream := container.UpstreamToolsFromManifest(m)
	buckets := bucketAssignments(m)
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
	osPolicy, _, err := ospackages.LoadPolicy(filepath.Join(root, ospackages.DefaultPolicyPath))
	if err != nil {
		return []string{err.Error()}
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
	} else if source, ok := osPackageSource(*osPolicy, "default", "nodejs"); !ok ||
		osPolicy.Repositories[source].Type != ospackages.RepositoryAPTArtifact ||
		!strings.Contains(osPolicy.Repositories[source].URI, "node_"+node.Major+".x") {
		errs = append(errs, "toolchains.nodejs.major does not match the locked NodeSource repository")
	}
	if jdk := tm.Toolchains["jdk"]; jdk.Package == "" {
		errs = append(errs, "toolchains.jdk.package is required")
	} else if !osPolicyHasPackage(*osPolicy, "jvm", jdk.Package) {
		errs = append(errs, "toolchains.jdk.package does not match the jvm OS package policy")
	}
	if rust := tm.Toolchains["rust"]; rust.VersionVariable == "" {
		errs = append(errs, "toolchains.rust.version_variable is required")
	} else if !fileContains(root, "scanners/versions.env", rust.VersionVariable+"=") || !fileContains(root, "scanners/install/rust.sh", rust.VersionVariable) {
		errs = append(errs, "toolchains.rust.version_variable must be defined in versions.env and consumed by install/rust.sh")
	}
	rust := tm.Toolchains["rust"]
	for _, variable := range []string{
		rust.BootstrapVersionVariable,
		rust.LinuxAMD64SHA256Variable,
		rust.LinuxARM64SHA256Variable,
	} {
		if variable == "" || !fileContains(root, "scanners/install/rust.sh", variable) {
			errs = append(errs, "toolchains.rust bootstrap version/checksum variables must be consumed by install/rust.sh")
			break
		}
	}
	for _, name := range []string{"python", "ruby", "php"} {
		tc := tm.Toolchains[name]
		if tc.Package == "" || tc.Dockerfile == "" {
			errs = append(errs, "toolchains."+name+" must declare package and dockerfile")
			continue
		}
		if !osPolicyDockerfileHasPackage(*osPolicy, tc.Dockerfile, tc.Package) {
			errs = append(errs, fmt.Sprintf("toolchains.%s package %s is not referenced by the OS package policy for %s", name, tc.Package, tc.Dockerfile))
		}
	}
	return errs
}

func osPackageSource(policy ospackages.Policy, variantName, packageName string) (string, bool) {
	variant, ok := policy.Variants[variantName]
	if !ok {
		return "", false
	}
	for source, packages := range variant.Packages {
		for _, candidate := range packages {
			if candidate == packageName {
				return source, true
			}
		}
	}
	return "", false
}

func osPolicyHasPackage(policy ospackages.Policy, variantName, packageName string) bool {
	_, ok := osPackageSource(policy, variantName, packageName)
	return ok
}

func osPolicyDockerfileHasPackage(policy ospackages.Policy, dockerfile, packageName string) bool {
	for variantName, variant := range policy.Variants {
		if variant.Dockerfile == dockerfile {
			return osPolicyHasPackage(policy, variantName, packageName)
		}
	}
	return false
}

func fileContains(root, rel, needle string) bool {
	data, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		return false
	}
	return strings.Contains(string(data), needle)
}

func bucketAssignments(definition *manifest.Manifest) map[string]string {
	out := make(map[string]string)
	for tool, image := range container.BucketImagesFromManifest(definition, "wolf-scanners", "test") {
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
