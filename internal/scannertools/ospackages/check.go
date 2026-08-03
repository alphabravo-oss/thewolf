package ospackages

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func (l Lock) ValidatePolicy(policy Policy, policyData []byte) error {
	var errs []string
	if l.PolicyDigest != PolicyDigest(policyData) {
		errs = append(errs, "policyDigest does not match scanners/os-packages.yaml")
	}
	if len(l.Repositories) != len(policy.Repositories) {
		errs = append(errs, "repository count does not match policy")
	}
	expectedArchitectures := policyArchitectures(policy)
	for name, expected := range policy.Repositories {
		actual, ok := l.Repositories[name]
		if !ok {
			errs = append(errs, "lock is missing repository "+name)
			continue
		}
		if actual.Type != expected.Type || actual.URI != expected.URI ||
			actual.Suite != expected.Suite || actual.Component != expected.Component {
			errs = append(errs, "lock repository "+name+" does not match policy")
		}
		for _, architecture := range expectedArchitectures {
			if _, ok := actual.Indexes[architecture]; !ok {
				errs = append(errs, "lock repository "+name+" is missing "+architecture+" index")
			}
		}
	}
	if len(l.Variants) != len(policy.Variants) {
		errs = append(errs, "variant count does not match policy")
	}
	for variantName, expected := range policy.Variants {
		actual, ok := l.Variants[variantName]
		if !ok {
			errs = append(errs, "lock is missing variant "+variantName)
			continue
		}
		if actual.Dockerfile != expected.Dockerfile {
			errs = append(errs, "lock variant "+variantName+" dockerfile does not match policy")
		}
		if len(actual.Platforms) != len(expected.Platforms) {
			errs = append(errs, "lock variant "+variantName+" platform count does not match policy")
		}
		expectedPackages := map[string]string{}
		for source, packages := range expected.Packages {
			for _, name := range packages {
				expectedPackages[name] = source
			}
		}
		for _, platform := range expected.Platforms {
			lockedPlatform, ok := actual.Platforms[platform]
			if !ok {
				errs = append(errs, "lock variant "+variantName+" is missing platform "+platform)
				continue
			}
			if len(lockedPlatform.Packages) != len(expectedPackages) {
				errs = append(errs, "lock variant "+variantName+" package count does not match policy for "+platform)
			}
			seen := map[string]struct{}{}
			for _, pkg := range lockedPlatform.Packages {
				declaredSource, expected := expectedPackages[pkg.Name]
				if !expected {
					errs = append(errs, "lock variant "+variantName+" has undeclared package "+pkg.Name)
					continue
				}
				seen[pkg.Name] = struct{}{}
				if policy.Repositories[declaredSource].Type == RepositoryAPTArtifact &&
					pkg.Source != declaredSource {
					errs = append(errs, "lock variant "+variantName+" external package "+pkg.Name+" changed source")
				}
				if policy.Repositories[declaredSource].Type == RepositoryDebianSnapshot &&
					policy.Repositories[pkg.Source].Type != RepositoryDebianSnapshot {
					errs = append(errs, "lock variant "+variantName+" Debian package "+pkg.Name+" changed source type")
				}
			}
			for name := range expectedPackages {
				if _, ok := seen[name]; !ok {
					errs = append(errs, "lock variant "+variantName+" is missing package "+name+" for "+platform)
				}
			}
		}
	}
	if len(errs) > 0 {
		sort.Strings(errs)
		return fmt.Errorf("OS package policy/lock parity failed:\n- %s", strings.Join(errs, "\n- "))
	}
	return nil
}

// RenderFiles returns every generated Docker-consumable file relative to the
// repository root.
func RenderFiles(lock Lock) (map[string][]byte, error) {
	if err := lock.Validate(); err != nil {
		return nil, err
	}
	files := map[string][]byte{}
	var sources strings.Builder
	sources.WriteString("# Code generated from scanners/os-packages.lock.yaml; DO NOT EDIT.\n")
	for _, name := range sortedLockedRepositoryNames(lock.Repositories) {
		repository := lock.Repositories[name]
		if repository.Type != RepositoryDebianSnapshot {
			continue
		}
		fmt.Fprintf(
			&sources,
			"\nTypes: deb\nURIs: %s/%s/\nSuites: %s\nComponents: %s\n"+
				"Signed-By: /usr/share/keyrings/debian-archive-keyring.pgp\nCheck-Valid-Until: no\n",
			strings.TrimSuffix(repository.URI, "/"),
			lock.Snapshot,
			repository.Suite,
			repository.Component,
		)
	}
	files[filepath.ToSlash(filepath.Join(DefaultOutputDir, "snapshot.sources"))] = []byte(sources.String())
	bootstrapPackage, err := bootstrapCAPackage(lock)
	if err != nil {
		return nil, err
	}
	files[BootstrapChecksumPath] = []byte(
		strings.TrimPrefix(bootstrapPackage.SHA256, "sha256:") +
			"  ca-certificates.deb\n",
	)

	for _, variantName := range sortedLockedVariantNames(lock.Variants) {
		variant := lock.Variants[variantName]
		platforms := make([]string, 0, len(variant.Platforms))
		for platform := range variant.Platforms {
			platforms = append(platforms, platform)
		}
		sort.Strings(platforms)
		for _, platform := range platforms {
			architecture, _ := platformArchitecture(platform)
			var pins, artifacts strings.Builder
			pins.WriteString("# Code generated from scanners/os-packages.lock.yaml; DO NOT EDIT.\n")
			artifacts.WriteString("# name<TAB>url<TAB>sha256<TAB>filename; generated, DO NOT EDIT.\n")
			for _, pkg := range variant.Platforms[platform].Packages {
				repository := lock.Repositories[pkg.Source]
				if repository.Type == RepositoryAPTArtifact {
					fmt.Fprintf(
						&artifacts,
						"%s\t%s\t%s\t%s\n",
						pkg.Name,
						pkg.URL,
						strings.TrimPrefix(pkg.SHA256, "sha256:"),
						filepath.Base(pkg.Filename),
					)
					continue
				}
				if pkg.Architecture == "all" {
					fmt.Fprintf(&pins, "%s=%s\n", pkg.Name, pkg.Version)
				} else {
					fmt.Fprintf(&pins, "%s:%s=%s\n", pkg.Name, pkg.Architecture, pkg.Version)
				}
			}
			base := variantName + "-" + architecture
			files[filepath.ToSlash(filepath.Join(DefaultOutputDir, "pins", base+".txt"))] = []byte(pins.String())
			files[filepath.ToSlash(filepath.Join(DefaultOutputDir, "artifacts", base+".txt"))] = []byte(artifacts.String())
		}
	}
	return files, nil
}

func Check(root string) error {
	policyPath := filepath.Join(root, filepath.FromSlash(DefaultPolicyPath))
	lockPath := filepath.Join(root, filepath.FromSlash(DefaultLockPath))
	policy, policyData, err := LoadPolicy(policyPath)
	if err != nil {
		return err
	}
	lockData, err := os.ReadFile(lockPath)
	if err != nil {
		return err
	}
	lock, err := ParseLock(lockData)
	if err != nil {
		return err
	}
	if err := lock.ValidatePolicy(*policy, policyData); err != nil {
		return err
	}
	canonical, err := lock.MarshalYAML()
	if err != nil {
		return err
	}
	if !bytes.Equal(lockData, canonical) {
		return fmt.Errorf("%s is not in canonical generated form", DefaultLockPath)
	}
	expected, err := RenderFiles(*lock)
	if err != nil {
		return err
	}
	for relative, contents := range expected {
		current, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			return fmt.Errorf("%s: %w", relative, err)
		}
		if !bytes.Equal(current, contents) {
			return fmt.Errorf("%s is stale; refresh the OS package lock", relative)
		}
	}
	if err := verifyBootstrapPackage(root, *lock); err != nil {
		return err
	}
	if err := validateGeneratedFileSet(root, expected); err != nil {
		return err
	}
	return validateDockerfileCoverage(root, *policy)
}

func validateGeneratedFileSet(root string, expected map[string][]byte) error {
	base := filepath.Join(root, filepath.FromSlash(DefaultOutputDir))
	var unexpected []string
	err := filepath.WalkDir(base, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if _, ok := expected[relative]; !ok && relative != BootstrapPackagePath {
			unexpected = append(unexpected, relative)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(unexpected) > 0 {
		sort.Strings(unexpected)
		return fmt.Errorf("unexpected generated OS package files: %s", strings.Join(unexpected, ", "))
	}
	return nil
}

func verifyBootstrapPackage(root string, lock Lock) error {
	expected, err := bootstrapCAPackage(lock)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(BootstrapPackagePath)))
	if err != nil {
		return fmt.Errorf("%s: %w", BootstrapPackagePath, err)
	}
	if actual := sha256Value(data); actual != expected.SHA256 {
		return fmt.Errorf("%s digest=%s, want %s", BootstrapPackagePath, actual, expected.SHA256)
	}
	return nil
}

func bootstrapCAPackage(lock Lock) (PackageLock, error) {
	var selected PackageLock
	for _, variant := range lock.Variants {
		for _, platform := range variant.Platforms {
			for _, pkg := range platform.Packages {
				if pkg.Name != "ca-certificates" {
					continue
				}
				repository := lock.Repositories[pkg.Source]
				if pkg.Architecture != "all" || repository.Type != RepositoryDebianSnapshot {
					return PackageLock{}, fmt.Errorf("ca-certificates bootstrap must be an architecture-independent Debian snapshot package")
				}
				if selected.Name == "" {
					selected = pkg
					continue
				}
				if selected.Version != pkg.Version || selected.Architecture != pkg.Architecture ||
					selected.Source != pkg.Source || selected.Filename != pkg.Filename ||
					selected.SHA256 != pkg.SHA256 {
					return PackageLock{}, fmt.Errorf("ca-certificates bootstrap package differs across variants or platforms")
				}
			}
		}
	}
	if selected.Name == "" {
		return PackageLock{}, fmt.Errorf("OS package lock does not contain ca-certificates for TLS bootstrap")
	}
	return selected, nil
}

func validateDockerfileCoverage(root string, policy Policy) error {
	scannerFiles, err := filepath.Glob(filepath.Join(root, "scanners", "Dockerfile*"))
	if err != nil {
		return err
	}
	expectedDockerfiles := map[string]string{}
	for variant, definition := range policy.Variants {
		expectedDockerfiles[filepath.ToSlash(definition.Dockerfile)] = variant
	}
	files := append([]string(nil), scannerFiles...)
	seenFiles := make(map[string]struct{}, len(files))
	for _, file := range files {
		relative, relErr := filepath.Rel(root, file)
		if relErr != nil {
			return relErr
		}
		seenFiles[filepath.ToSlash(relative)] = struct{}{}
	}
	// Scanner Dockerfiles remain exhaustive by convention. Additional
	// Wolf-owned release artifacts (for example fixer/Dockerfile.base) may
	// opt into the same immutable OS package policy explicitly.
	for relative := range expectedDockerfiles {
		if _, exists := seenFiles[relative]; exists {
			continue
		}
		files = append(files, filepath.Join(root, filepath.FromSlash(relative)))
		seenFiles[relative] = struct{}{}
	}
	var errs []string
	for _, file := range files {
		relative, err := filepath.Rel(root, file)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		variant, ok := expectedDockerfiles[relative]
		if !ok {
			errs = append(errs, "OS package policy does not cover "+relative)
			continue
		}
		data, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		text := string(data)
		if !strings.Contains(text, "/usr/local/bin/install-os-packages "+variant) {
			errs = append(errs, relative+" does not invoke its locked OS package variant")
		}
		if containsAPTInstall(text) {
			errs = append(errs, relative+" contains a direct apt install outside install-os-packages")
		}
	}
	if len(files) != len(expectedDockerfiles) {
		errs = append(errs, "OS package policy Dockerfile count does not match scanners/Dockerfile*")
	}
	installFiles, err := filepath.Glob(filepath.Join(root, "scanners", "install", "*.sh"))
	if err != nil {
		return err
	}
	for _, file := range installFiles {
		if filepath.Base(file) == "os-packages.sh" {
			continue
		}
		data, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		if containsAPTInstall(string(data)) || strings.Contains(string(data), "apt-get update") {
			errs = append(errs, filepath.ToSlash(strings.TrimPrefix(file, root+string(filepath.Separator)))+
				" bypasses the locked OS package installer")
		}
	}
	installerPath := filepath.Join(root, "scanners", "install", "os-packages.sh")
	installer, err := os.ReadFile(installerPath)
	if err != nil {
		errs = append(errs, "read scanners/install/os-packages.sh: "+err.Error())
	} else {
		installerText := string(installer)
		for _, required := range []string{
			"--proto '=https'",
			"--proto-redir '=https'",
			`Acquire::https::CaInfo "/etc/ssl/certs/ca-certificates.crt"`,
		} {
			if !strings.Contains(installerText, required) {
				errs = append(errs, "scanners/install/os-packages.sh is missing HTTPS control "+required)
			}
		}
		for _, forbidden := range []string{
			`Acquire::https::Verify-Peer "false"`,
			"http://snapshot.debian.org",
		} {
			if strings.Contains(installerText, forbidden) {
				errs = append(errs, "scanners/install/os-packages.sh contains forbidden TLS downgrade "+forbidden)
			}
		}
	}
	if len(errs) > 0 {
		sort.Strings(errs)
		return fmt.Errorf("OS package Dockerfile policy failed:\n- %s", strings.Join(errs, "\n- "))
	}
	return nil
}

func containsAPTInstall(text string) bool {
	normalized := strings.NewReplacer("\\\n", " ", "\t", " ", "\r", " ").Replace(text)
	for _, marker := range []string{"apt-get install", "apt install"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func sortedLockedRepositoryNames(repositories map[string]RepositoryLock) []string {
	out := make([]string, 0, len(repositories))
	for name := range repositories {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func sortedLockedVariantNames(variants map[string]VariantLock) []string {
	out := make([]string, 0, len(variants))
	for name := range variants {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
