// Package ospackages owns the reproducible apt snapshot and direct-package
// pins used by Wolf-built scanner images.
package ospackages

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	PolicySchemaVersion   = "wolf.scanners/os-package-policy/v1"
	LockSchemaVersion     = "wolf.scanners/os-package-lock/v1"
	DefaultPolicyPath     = "scanners/os-packages.yaml"
	DefaultLockPath       = "scanners/os-packages.lock.yaml"
	DefaultOutputDir      = "scanners/os-packages"
	BootstrapPackagePath  = "scanners/os-packages/bootstrap/ca-certificates.deb"
	BootstrapChecksumPath = "scanners/os-packages/bootstrap/ca-certificates.sha256"

	RepositoryDebianSnapshot = "debian_snapshot"
	RepositoryAPTArtifact    = "apt_artifact"
)

var (
	digestPattern   = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	snapshotPattern = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}Z$`)
	namePattern     = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]*$`)
)

type Policy struct {
	SchemaVersion string                      `yaml:"schemaVersion"`
	Repositories  map[string]RepositoryPolicy `yaml:"repositories"`
	Variants      map[string]VariantPolicy    `yaml:"variants"`
}

type RepositoryPolicy struct {
	Type      string `yaml:"type"`
	URI       string `yaml:"uri"`
	Suite     string `yaml:"suite"`
	Component string `yaml:"component"`
}

type VariantPolicy struct {
	Dockerfile string              `yaml:"dockerfile"`
	Platforms  []string            `yaml:"platforms"`
	Packages   map[string][]string `yaml:"packages"`
}

type Lock struct {
	SchemaVersion string                    `yaml:"schemaVersion"`
	LockDigest    string                    `yaml:"lockDigest"`
	PolicyDigest  string                    `yaml:"policyDigest"`
	Snapshot      string                    `yaml:"snapshot"`
	Repositories  map[string]RepositoryLock `yaml:"repositories"`
	Variants      map[string]VariantLock    `yaml:"variants"`
}

type RepositoryLock struct {
	Type          string               `yaml:"type"`
	URI           string               `yaml:"uri"`
	Suite         string               `yaml:"suite"`
	Component     string               `yaml:"component"`
	ReleaseSHA256 string               `yaml:"releaseSHA256,omitempty"`
	Indexes       map[string]IndexLock `yaml:"indexes"`
}

type IndexLock struct {
	URL    string `yaml:"url"`
	SHA256 string `yaml:"sha256"`
}

type VariantLock struct {
	Dockerfile string                  `yaml:"dockerfile"`
	Platforms  map[string]PlatformLock `yaml:"platforms"`
}

type PlatformLock struct {
	Packages []PackageLock `yaml:"packages"`
}

type PackageLock struct {
	Name         string `yaml:"name"`
	Version      string `yaml:"version"`
	Architecture string `yaml:"architecture"`
	Source       string `yaml:"source"`
	Filename     string `yaml:"filename"`
	SHA256       string `yaml:"sha256"`
	URL          string `yaml:"url,omitempty"`
}

func LoadPolicy(path string) (*Policy, []byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	var policy Policy
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&policy); err != nil {
		return nil, nil, fmt.Errorf("decode OS package policy %s: %w", path, err)
	}
	if err := policy.Validate(); err != nil {
		return nil, nil, err
	}
	return &policy, data, nil
}

func LoadLock(path string) (*Lock, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseLock(data)
}

func ParseLock(data []byte) (*Lock, error) {
	var lock Lock
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&lock); err != nil {
		return nil, fmt.Errorf("decode OS package lock: %w", err)
	}
	if err := lock.Validate(); err != nil {
		return nil, err
	}
	return &lock, nil
}

func (p Policy) Validate() error {
	var errs []string
	if p.SchemaVersion != PolicySchemaVersion {
		errs = append(errs, fmt.Sprintf("schemaVersion=%q, want %q", p.SchemaVersion, PolicySchemaVersion))
	}
	if len(p.Repositories) == 0 {
		errs = append(errs, "repositories is empty")
	}
	for name, repository := range p.Repositories {
		if !namePattern.MatchString(name) {
			errs = append(errs, "repository has invalid name "+name)
		}
		switch repository.Type {
		case RepositoryDebianSnapshot, RepositoryAPTArtifact:
		default:
			errs = append(errs, "repository "+name+" has unsupported type "+repository.Type)
		}
		parsed, err := url.Parse(repository.URI)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" ||
			parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			errs = append(errs, "repository "+name+" must use an absolute credential-free HTTPS URI")
		}
		if repository.Suite == "" || repository.Component == "" {
			errs = append(errs, "repository "+name+" must declare suite and component")
		}
	}
	if len(p.Variants) == 0 {
		errs = append(errs, "variants is empty")
	}
	for name, variant := range p.Variants {
		if !namePattern.MatchString(name) {
			errs = append(errs, "variant has invalid name "+name)
		}
		if variant.Dockerfile == "" || filepath.IsAbs(variant.Dockerfile) ||
			strings.Contains(filepath.Clean(variant.Dockerfile), "..") {
			errs = append(errs, "variant "+name+" has invalid dockerfile")
		}
		if !sortedUnique(variant.Platforms) {
			errs = append(errs, "variant "+name+" platforms must be sorted and unique")
		}
		for _, platform := range variant.Platforms {
			if platform != "linux/amd64" && platform != "linux/arm64" {
				errs = append(errs, "variant "+name+" has unsupported platform "+platform)
			}
		}
		if len(variant.Packages) == 0 {
			errs = append(errs, "variant "+name+" packages is empty")
		}
		seen := map[string]string{}
		for source, packages := range variant.Packages {
			if _, ok := p.Repositories[source]; !ok {
				errs = append(errs, "variant "+name+" uses unknown repository "+source)
			}
			if !sortedUnique(packages) {
				errs = append(errs, "variant "+name+" packages for "+source+" must be sorted and unique")
			}
			for _, packageName := range packages {
				if !namePattern.MatchString(packageName) {
					errs = append(errs, "variant "+name+" has invalid package "+packageName)
				}
				if previous, duplicate := seen[packageName]; duplicate {
					errs = append(errs, "variant "+name+" package "+packageName+" appears in "+previous+" and "+source)
				}
				seen[packageName] = source
			}
		}
	}
	if len(errs) > 0 {
		sort.Strings(errs)
		return fmt.Errorf("OS package policy validation failed:\n- %s", strings.Join(errs, "\n- "))
	}
	return nil
}

func (l Lock) Validate() error {
	var errs []string
	if l.SchemaVersion != LockSchemaVersion {
		errs = append(errs, fmt.Sprintf("schemaVersion=%q, want %q", l.SchemaVersion, LockSchemaVersion))
	}
	if !digestPattern.MatchString(l.PolicyDigest) {
		errs = append(errs, "policyDigest must be sha256")
	}
	if !snapshotPattern.MatchString(l.Snapshot) {
		errs = append(errs, "snapshot must use YYYYMMDDTHHMMSSZ")
	} else if _, err := time.Parse("20060102T150405Z", l.Snapshot); err != nil {
		errs = append(errs, "snapshot is not a valid UTC timestamp")
	}
	if !digestPattern.MatchString(l.LockDigest) {
		errs = append(errs, "lockDigest must be sha256")
	} else if calculated, err := l.CanonicalDigest(); err != nil {
		errs = append(errs, "calculate lock digest: "+err.Error())
	} else if calculated != l.LockDigest {
		errs = append(errs, fmt.Sprintf("lockDigest=%s, calculated %s", l.LockDigest, calculated))
	}
	if len(l.Repositories) == 0 || len(l.Variants) == 0 {
		errs = append(errs, "repositories and variants must be non-empty")
	}
	for name, repository := range l.Repositories {
		if !namePattern.MatchString(name) {
			errs = append(errs, "repository has invalid name "+name)
		}
		switch repository.Type {
		case RepositoryDebianSnapshot:
			if !digestPattern.MatchString(repository.ReleaseSHA256) {
				errs = append(errs, "repository "+name+" releaseSHA256 must be sha256")
			}
		case RepositoryAPTArtifact:
		default:
			errs = append(errs, "repository "+name+" has unsupported type "+repository.Type)
		}
		repositoryURL, repositoryURLOK := credentialFreeHTTPS(repository.URI)
		if !repositoryURLOK || repository.Suite == "" || repository.Component == "" {
			errs = append(errs, "repository "+name+" is incomplete")
		}
		if len(repository.Indexes) == 0 {
			errs = append(errs, "repository "+name+" indexes is empty")
		}
		for architecture, index := range repository.Indexes {
			if architecture != "amd64" && architecture != "arm64" {
				errs = append(errs, "repository "+name+" has invalid architecture "+architecture)
			}
			if !digestPattern.MatchString(index.SHA256) {
				errs = append(errs, "repository "+name+" index "+architecture+" has invalid digest")
			}
			parsed, valid := credentialFreeHTTPS(index.URL)
			if !valid || !repositoryURLOK ||
				!strings.EqualFold(parsed.Scheme, repositoryURL.Scheme) ||
				!strings.EqualFold(parsed.Host, repositoryURL.Host) ||
				!strings.HasPrefix(parsed.Path, strings.TrimSuffix(repositoryURL.Path, "/")+"/") {
				errs = append(errs, "repository "+name+" index "+architecture+" has invalid URL")
			}
		}
	}
	for variantName, variant := range l.Variants {
		if !namePattern.MatchString(variantName) ||
			variant.Dockerfile == "" || filepath.IsAbs(variant.Dockerfile) ||
			strings.Contains(filepath.Clean(variant.Dockerfile), "..") ||
			len(variant.Platforms) == 0 {
			errs = append(errs, "variant "+variantName+" is incomplete")
		}
		for platform, lockedPlatform := range variant.Platforms {
			architecture, ok := platformArchitecture(platform)
			if !ok {
				errs = append(errs, "variant "+variantName+" has invalid platform "+platform)
			}
			previous := ""
			for _, pkg := range lockedPlatform.Packages {
				if pkg.Name <= previous {
					errs = append(errs, "variant "+variantName+" packages for "+platform+" must be sorted and unique")
				}
				previous = pkg.Name
				if !namePattern.MatchString(pkg.Name) || pkg.Version == "" ||
					(pkg.Architecture != architecture && pkg.Architecture != "all") ||
					!digestPattern.MatchString(pkg.SHA256) || !validPackagePath(pkg.Filename) {
					errs = append(errs, "variant "+variantName+" package "+pkg.Name+" for "+platform+" is incomplete")
				}
				repository, ok := l.Repositories[pkg.Source]
				if !ok {
					errs = append(errs, "variant "+variantName+" package "+pkg.Name+" uses unknown source "+pkg.Source)
				} else if repository.Type == RepositoryAPTArtifact {
					expectedURL := strings.TrimSuffix(repository.URI, "/") + "/" +
						strings.TrimPrefix(pkg.Filename, "/")
					if parsed, valid := credentialFreeHTTPS(pkg.URL); !valid ||
						parsed.String() != expectedURL {
						errs = append(errs, "variant "+variantName+" external package "+pkg.Name+" has invalid URL")
					}
				} else if pkg.URL != "" {
					errs = append(errs, "variant "+variantName+" snapshot package "+pkg.Name+" must not declare URL")
				}
				if _, exists := repository.Indexes[architecture]; ok && !exists {
					errs = append(errs, "variant "+variantName+" package "+pkg.Name+
						" has no repository index for "+architecture)
				}
			}
		}
	}
	if len(errs) > 0 {
		sort.Strings(errs)
		return fmt.Errorf("OS package lock validation failed:\n- %s", strings.Join(errs, "\n- "))
	}
	return nil
}

func credentialFreeHTTPS(raw string) (*url.URL, bool) {
	parsed, err := url.Parse(raw)
	return parsed, err == nil && parsed.Scheme == "https" && parsed.Host != "" &&
		parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == ""
}

func validPackagePath(filename string) bool {
	return filename != "" && !strings.HasPrefix(filename, "/") &&
		path.Clean(filename) == filename && !strings.HasPrefix(filename, "../")
}

func (l *Lock) SetDigest() error {
	digest, err := l.CanonicalDigest()
	if err != nil {
		return err
	}
	l.LockDigest = digest
	return nil
}

func (l Lock) CanonicalDigest() (string, error) {
	l.LockDigest = ""
	data, err := json.Marshal(l)
	if err != nil {
		return "", err
	}
	return sha256Value(data), nil
}

func (l Lock) MarshalYAML() ([]byte, error) {
	if err := l.Validate(); err != nil {
		return nil, err
	}
	data, err := yaml.Marshal(l)
	if err != nil {
		return nil, err
	}
	header := "# Code generated by `go run ./cmd/scannertools os-packages --refresh`; DO NOT EDIT.\n"
	return append([]byte(header), data...), nil
}

func PolicyDigest(data []byte) string {
	return sha256Value(data)
}

func sha256Value(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func sortedUnique(values []string) bool {
	if len(values) == 0 {
		return false
	}
	for index, value := range values {
		if value == "" || (index > 0 && values[index-1] >= value) {
			return false
		}
	}
	return true
}

func platformArchitecture(platform string) (string, bool) {
	switch platform {
	case "linux/amd64":
		return "amd64", true
	case "linux/arm64":
		return "arm64", true
	default:
		return "", false
	}
}
