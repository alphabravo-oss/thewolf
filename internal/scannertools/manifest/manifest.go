package manifest

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/alphabravocompany/thewolf/scanners"
)

// ManifestEnvOverride lets operators point wolf at a custom tools manifest.
const ManifestEnvOverride = "WOLF_SCANNER_MANIFEST"

const (
	TierDefault  = "default"
	TierBucket   = "bucket"
	TierUpstream = "upstream"
)

// Manifest is the scanner metadata source of truth. The plugin registry owns
// executable behavior; this manifest owns version, image, and update metadata.
type Manifest struct {
	Tools map[string]Tool `yaml:"tools"`
}

type Tool struct {
	DisplayName     string       `yaml:"display_name"`
	Category        string       `yaml:"category"`
	ResourceClass   string       `yaml:"resource_class"`
	DefaultTimeout  string       `yaml:"default_timeout"`
	NetworkRequired bool         `yaml:"network_required,omitempty"`
	Exclusive       bool         `yaml:"exclusive,omitempty"`
	PluginPackage   string       `yaml:"plugin_package"`
	IntegrationTier string       `yaml:"integration_tier"`
	Bucket          string       `yaml:"bucket,omitempty"`
	PinnedVersion   string       `yaml:"pinned_version,omitempty"`
	VersionVariable string       `yaml:"version_variable,omitempty"`
	Image           Image        `yaml:"image,omitempty"`
	Install         Install      `yaml:"install,omitempty"`
	UpdateSource    UpdateSource `yaml:"update_source,omitempty"`
	Smoke           Smoke        `yaml:"smoke,omitempty"`
	Docs            Docs         `yaml:"docs,omitempty"`
}

type Image struct {
	Registry        string   `yaml:"registry,omitempty"`
	Repository      string   `yaml:"repository,omitempty"`
	TagTemplate     string   `yaml:"tag_template,omitempty"`
	PinnedReference string   `yaml:"pinned_reference,omitempty"`
	Entrypoint      string   `yaml:"entrypoint,omitempty"`
	Platforms       []string `yaml:"platforms,omitempty"`
}

type Install struct {
	Manager string `yaml:"manager,omitempty"`
	Package string `yaml:"package,omitempty"`
}

type UpdateSource struct {
	Type       string `yaml:"type,omitempty"`
	Repository string `yaml:"repository,omitempty"`
	Package    string `yaml:"package,omitempty"`
	Module     string `yaml:"module,omitempty"`
	Owner      string `yaml:"owner,omitempty"`
	Repo       string `yaml:"repo,omitempty"`
	Channel    string `yaml:"channel,omitempty"`
	TagPattern string `yaml:"tag_pattern,omitempty"`
}

type Smoke struct {
	Command         []string `yaml:"command,omitempty"`
	ExpectedPattern string   `yaml:"expected_pattern,omitempty"`
}

type Docs struct {
	Description string `yaml:"description,omitempty"`
	Homepage    string `yaml:"homepage,omitempty"`
	License     string `yaml:"license,omitempty"`
}

var toolNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

func LoadFile(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return LoadBytes(data, path)
}

// LoadBytes decodes and validates a manifest from raw YAML. source is used only
// in error messages (a file path or a label like "embedded").
func LoadBytes(data []byte, source string) (*Manifest, error) {
	var m Manifest
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("decode scanner manifest %s: %w", source, err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// LoadDefault resolves the scanner manifest from, in order:
//  1. WOLF_SCANNER_MANIFEST, if set (operator override);
//  2. scanners/tools.yaml in the repo checkout, if we're running inside one
//     (dev convenience — edits apply without recompiling the embedded copy);
//  3. the copy embedded in the binary (always present — this is what makes the
//     container image and `go install`-ed binaries work, where no repo exists).
func LoadDefault() (*Manifest, error) {
	if p := strings.TrimSpace(os.Getenv(ManifestEnvOverride)); p != "" {
		return LoadFile(p)
	}
	if root, err := FindRepoRoot(""); err == nil {
		if m, err := LoadFile(filepath.Join(root, "scanners", "tools.yaml")); err == nil {
			return m, nil
		}
	}
	return LoadBytes(scanners.ToolsYAML, "embedded scanners/tools.yaml")
}

func FindRepoRoot(start string) (string, error) {
	if start == "" {
		var err error
		start, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "scanners", "tools.yaml")); err == nil {
				return dir, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("repo root with scanners/tools.yaml not found from %s", start)
		}
		dir = parent
	}
}

func (m *Manifest) Validate() error {
	if m == nil {
		return fmt.Errorf("scanner manifest is nil")
	}
	if len(m.Tools) == 0 {
		return fmt.Errorf("scanner manifest has no tools")
	}

	var errs []string
	for name, tool := range m.Tools {
		prefix := "tool " + name + ": "
		if !toolNameRE.MatchString(name) {
			errs = append(errs, prefix+"name must be lowercase kebab-case")
		}
		if strings.TrimSpace(tool.DisplayName) == "" {
			errs = append(errs, prefix+"display_name is required")
		}
		if strings.TrimSpace(tool.Category) == "" {
			errs = append(errs, prefix+"category is required")
		}
		switch tool.ResourceClass {
		case "light", "medium", "heavy", "network", "exclusive":
		default:
			errs = append(errs, prefix+"resource_class must be light, medium, heavy, network, or exclusive")
		}
		if strings.TrimSpace(tool.DefaultTimeout) == "" {
			errs = append(errs, prefix+"default_timeout is required")
		} else if _, err := time.ParseDuration(tool.DefaultTimeout); err != nil {
			errs = append(errs, prefix+"default_timeout is invalid: "+err.Error())
		}
		if tool.Exclusive && tool.ResourceClass != "exclusive" {
			errs = append(errs, prefix+"exclusive tools must use resource_class exclusive")
		}
		if tool.NetworkRequired && tool.ResourceClass == "light" {
			errs = append(errs, prefix+"network_required tools must not use resource_class light")
		}
		if strings.TrimSpace(tool.PluginPackage) == "" {
			errs = append(errs, prefix+"plugin_package is required")
		}
		switch tool.IntegrationTier {
		case TierDefault:
			if tool.Bucket != "" {
				errs = append(errs, prefix+"default tools must not declare bucket")
			}
			if tool.Image.PinnedReference != "" {
				errs = append(errs, prefix+"default tools must not declare image.pinned_reference")
			}
			if tool.Install.Manager == "" {
				errs = append(errs, prefix+"default tools must declare install.manager")
			}
		case TierBucket:
			if tool.Bucket != "jvm" && tool.Bucket != "rust" && tool.Bucket != "codeql" {
				errs = append(errs, prefix+"bucket tools must declare bucket as jvm, rust, or codeql")
			}
			if tool.Image.PinnedReference != "" {
				errs = append(errs, prefix+"bucket tools must not declare image.pinned_reference")
			}
		case TierUpstream:
			if tool.Bucket != "" {
				errs = append(errs, prefix+"upstream tools must not declare bucket")
			}
			if tool.Image.PinnedReference == "" {
				errs = append(errs, prefix+"upstream tools must declare image.pinned_reference")
			}
		default:
			errs = append(errs, prefix+"integration_tier must be default, bucket, or upstream")
		}
		if tool.VersionVariable != "" && tool.PinnedVersion == "" {
			errs = append(errs, prefix+"version_variable requires pinned_version")
		}
		if tool.PinnedVersion != "" && tool.VersionVariable == "" {
			errs = append(errs, prefix+"pinned_version requires version_variable")
		}
		if tool.UpdateSource.Type == "" {
			errs = append(errs, prefix+"update_source.type is required")
		}
		if tool.UpdateSource.TagPattern != "" {
			if _, err := regexp.Compile(tool.UpdateSource.TagPattern); err != nil {
				errs = append(errs, prefix+"update_source.tag_pattern is invalid: "+err.Error())
			}
		}
	}
	if len(errs) > 0 {
		sort.Strings(errs)
		return fmt.Errorf("invalid scanner manifest:\n- %s", strings.Join(errs, "\n- "))
	}
	return nil
}

func (m *Manifest) Names() []string {
	if m == nil {
		return nil
	}
	names := make([]string, 0, len(m.Tools))
	for name := range m.Tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (m *Manifest) TierCounts() map[string]int {
	counts := map[string]int{
		TierDefault:  0,
		TierBucket:   0,
		TierUpstream: 0,
	}
	if m == nil {
		return counts
	}
	for _, tool := range m.Tools {
		counts[tool.IntegrationTier]++
	}
	return counts
}

func ParseVersionsEnv(data []byte) (map[string]string, error) {
	out := make(map[string]string)
	scanner := bufio.NewScanner(bytes.NewReader(data))
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("versions.env:%d: expected KEY=VALUE", lineNo)
		}
		key = strings.TrimSpace(key)
		val = strings.Trim(strings.TrimSpace(val), `"'`)
		if key == "" || val == "" {
			return nil, fmt.Errorf("versions.env:%d: empty key or value", lineNo)
		}
		out[key] = val
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
