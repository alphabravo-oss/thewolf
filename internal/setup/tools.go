// Package setup provides tool installation metadata and logic shared by
// both the CLI (wolf setup) and the API server (POST /config/plugins/:name/install).
package setup

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// InstallMethod describes one way to install a tool.
type InstallMethod struct {
	// Requires is a binary that must exist for this method to work (e.g. "brew", "pip3").
	// Empty string means no prerequisite (e.g. curl-based install script).
	Requires string `json:"requires"`
	// Cmd is the shell command to run.
	Cmd string `json:"cmd"`
}

// ToolDef defines a tool that Wolf can use, along with how to detect and install it.
type ToolDef struct {
	Name           string          `json:"name"`
	Description    string          `json:"description"`
	ProjectURL     string          `json:"project_url,omitempty"`
	CheckCmd       string          `json:"-"`
	CheckArgs      []string        `json:"-"`
	Category       string          `json:"category"`
	InstallMethods []InstallMethod `json:"install_methods"`
}

// Tools is the canonical list of all supported analysis tools.
var Tools = []ToolDef{
	{
		Name: "semgrep", Description: "Multi-language SAST scanner",
		ProjectURL: "https://github.com/semgrep/semgrep",
		CheckCmd: "semgrep", CheckArgs: []string{"--version"}, Category: "sast",
		InstallMethods: []InstallMethod{
			{Requires: "uv", Cmd: "uv tool install --upgrade semgrep"},
			{Requires: "brew", Cmd: "brew install semgrep"},
		},
	},
	{
		Name: "trivy", Description: "Vulnerability & misconfiguration scanner",
		ProjectURL: "https://github.com/aquasecurity/trivy",
		CheckCmd: "trivy", CheckArgs: []string{"--version"}, Category: "sca",
		InstallMethods: []InstallMethod{
			{Requires: "brew", Cmd: "brew install trivy"},
			{Cmd: "sh -c 'curl -sfL https://raw.githubusercontent.com/aquasecurity/trivy/main/contrib/install.sh | sh -s -- -b /usr/local/bin'"},
		},
	},
	{
		Name: "gitleaks", Description: "Secret & credential scanner",
		ProjectURL: "https://github.com/gitleaks/gitleaks",
		CheckCmd: "gitleaks", CheckArgs: []string{"version"}, Category: "secrets",
		InstallMethods: []InstallMethod{
			{Requires: "brew", Cmd: "brew install gitleaks"},
			{Requires: "go", Cmd: "go install github.com/gitleaks/gitleaks/v8@latest"},
		},
	},
	{
		Name: "bandit", Description: "Python security linter",
		ProjectURL: "https://github.com/PyCQA/bandit",
		CheckCmd: "bandit", CheckArgs: []string{"--version"}, Category: "sast",
		InstallMethods: []InstallMethod{
			{Requires: "uv", Cmd: "uv tool install --upgrade bandit"},
			{Requires: "brew", Cmd: "brew install bandit"},
		},
	},
	{
		Name: "ruff", Description: "Fast Python linter & formatter",
		ProjectURL: "https://github.com/astral-sh/ruff",
		CheckCmd: "ruff", CheckArgs: []string{"--version"}, Category: "quality",
		InstallMethods: []InstallMethod{
			{Requires: "uv", Cmd: "uv tool install --upgrade ruff"},
			{Requires: "brew", Cmd: "brew install ruff"},
		},
	},
	{
		Name: "eslint", Description: "JavaScript/TypeScript linter",
		ProjectURL: "https://github.com/eslint/eslint",
		CheckCmd: "eslint", CheckArgs: []string{"--version"}, Category: "quality",
		InstallMethods: []InstallMethod{
			{Requires: "npm", Cmd: "npm install -g eslint"},
		},
	},
	{
		Name: "gosec", Description: "Go security checker",
		ProjectURL: "https://github.com/securego/gosec",
		CheckCmd: "gosec", CheckArgs: []string{"-version"}, Category: "sast",
		InstallMethods: []InstallMethod{
			{Requires: "brew", Cmd: "brew install gosec"},
			{Requires: "go", Cmd: "go install github.com/securego/gosec/v2/cmd/gosec@latest"},
		},
	},
	{
		Name: "golangci-lint", Description: "Go meta-linter",
		ProjectURL: "https://github.com/golangci/golangci-lint",
		CheckCmd: "golangci-lint", CheckArgs: []string{"--version"}, Category: "quality",
		InstallMethods: []InstallMethod{
			{Requires: "brew", Cmd: "brew install golangci-lint"},
			{Requires: "go", Cmd: "go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"},
		},
	},
	{
		Name: "clippy", Description: "Rust linter (via rustup)",
		ProjectURL: "https://github.com/rust-lang/rust-clippy",
		CheckCmd: "cargo", CheckArgs: []string{"clippy", "--version"}, Category: "quality",
		InstallMethods: []InstallMethod{
			{Requires: "rustup", Cmd: "rustup component add clippy"},
		},
	},
	{
		Name: "hadolint", Description: "Dockerfile linter",
		ProjectURL: "https://github.com/hadolint/hadolint",
		CheckCmd: "hadolint", CheckArgs: []string{"--version"}, Category: "container",
		InstallMethods: []InstallMethod{
			{Requires: "brew", Cmd: "brew install hadolint"},
			{Cmd: "sh -c 'curl -sL -o /usr/local/bin/hadolint https://github.com/hadolint/hadolint/releases/latest/download/hadolint-$(uname -s)-$(uname -m) && chmod +x /usr/local/bin/hadolint'"},
		},
	},
	{
		Name: "syft", Description: "SBOM generator",
		ProjectURL: "https://github.com/anchore/syft",
		CheckCmd: "syft", CheckArgs: []string{"version"}, Category: "sbom",
		InstallMethods: []InstallMethod{
			{Requires: "brew", Cmd: "brew install syft"},
			{Requires: "go", Cmd: "go install github.com/anchore/syft/cmd/syft@latest"},
		},
	},
	// --- Additional tools below ---
	{
		Name: "mypy", Description: "Python static type checker",
		ProjectURL: "https://github.com/python/mypy",
		CheckCmd: "mypy", CheckArgs: []string{"--version"}, Category: "quality",
		InstallMethods: []InstallMethod{
			{Requires: "uv", Cmd: "uv tool install --upgrade mypy"},
		},
	},
	{
		Name: "radon", Description: "Python complexity & maintainability analyzer",
		ProjectURL: "https://github.com/rubik/radon",
		CheckCmd: "radon", CheckArgs: []string{"--version"}, Category: "quality",
		InstallMethods: []InstallMethod{
			{Requires: "uv", Cmd: "uv tool install --upgrade radon"},
		},
	},
	{
		Name: "vulture", Description: "Python dead code finder",
		ProjectURL: "https://github.com/jendrikseipp/vulture",
		CheckCmd: "vulture", CheckArgs: []string{"--version"}, Category: "quality",
		InstallMethods: []InstallMethod{
			{Requires: "uv", Cmd: "uv tool install --upgrade vulture"},
		},
	},
	{
		Name: "pip-audit", Description: "Python dependency vulnerability scanner",
		ProjectURL: "https://github.com/pypa/pip-audit",
		CheckCmd: "pip-audit", CheckArgs: []string{"--version"}, Category: "sca",
		InstallMethods: []InstallMethod{
			{Requires: "uv", Cmd: "uv tool install --upgrade pip-audit"},
		},
	},
	{
		Name: "shellcheck", Description: "Shell script static analysis",
		ProjectURL: "https://github.com/koalaman/shellcheck",
		CheckCmd: "shellcheck", CheckArgs: []string{"--version"}, Category: "quality",
		InstallMethods: []InstallMethod{
			{Requires: "brew", Cmd: "brew install shellcheck"},
			{Cmd: "sh -c 'curl -sL https://github.com/koalaman/shellcheck/releases/latest/download/shellcheck-latest.$(uname -s).$(uname -m).tar.xz | tar xJf - -C /usr/local/bin --strip-components=1 shellcheck-latest/shellcheck'"},
		},
	},
	{
		Name: "staticcheck", Description: "Go advanced static analysis",
		ProjectURL: "https://github.com/dominikh/go-tools",
		CheckCmd: "staticcheck", CheckArgs: []string{"-version"}, Category: "quality",
		InstallMethods: []InstallMethod{
			{Requires: "brew", Cmd: "brew install staticcheck"},
			{Requires: "go", Cmd: "go install honnef.co/go/tools/cmd/staticcheck@latest"},
		},
	},
	{
		Name: "checkov", Description: "Infrastructure-as-code scanner",
		ProjectURL: "https://github.com/bridgecrewio/checkov",
		CheckCmd: "checkov", CheckArgs: []string{"--version"}, Category: "sast",
		InstallMethods: []InstallMethod{
			{Requires: "uv", Cmd: "uv tool install --upgrade checkov"},
			{Requires: "brew", Cmd: "brew install checkov"},
		},
	},
	{
		Name: "trufflehog", Description: "Secret scanner (deep git history)",
		ProjectURL: "https://github.com/trufflesecurity/trufflehog",
		CheckCmd: "trufflehog", CheckArgs: []string{"--version"}, Category: "secrets",
		InstallMethods: []InstallMethod{
			{Requires: "brew", Cmd: "brew install trufflehog"},
			{Requires: "go", Cmd: "go install github.com/trufflesecurity/trufflehog/v3@latest"},
		},
	},
	{
		Name: "grype", Description: "Vulnerability scanner for containers & filesystems",
		ProjectURL: "https://github.com/anchore/grype",
		CheckCmd: "grype", CheckArgs: []string{"version"}, Category: "sca",
		InstallMethods: []InstallMethod{
			{Requires: "brew", Cmd: "brew install grype"},
			{Cmd: "sh -c 'curl -sSfL https://raw.githubusercontent.com/anchore/grype/main/install.sh | sh -s -- -b /usr/local/bin'"},
		},
	},
	{
		Name: "dockle", Description: "Container image linter",
		ProjectURL: "https://github.com/goodwithtech/dockle",
		CheckCmd: "dockle", CheckArgs: []string{"--version"}, Category: "container",
		InstallMethods: []InstallMethod{
			{Requires: "brew", Cmd: "brew install goodwithtech/r/dockle"},
			{Requires: "go", Cmd: "go install github.com/goodwithtech/dockle/cmd/dockle@latest"},
		},
	},
	{
		Name: "vale", Description: "Prose & documentation linter",
		ProjectURL: "https://github.com/errata-ai/vale",
		CheckCmd: "vale", CheckArgs: []string{"--version"}, Category: "docs",
		InstallMethods: []InstallMethod{
			{Requires: "brew", Cmd: "brew install vale"},
			{Requires: "go", Cmd: "go install github.com/errata-ai/vale/v3/cmd/vale@latest"},
		},
	},
	{
		Name: "spectral", Description: "OpenAPI & JSON/YAML linter",
		ProjectURL: "https://github.com/stoplightio/spectral",
		CheckCmd: "spectral", CheckArgs: []string{"--version"}, Category: "quality",
		InstallMethods: []InstallMethod{
			{Requires: "npm", Cmd: "npm install -g @stoplight/spectral-cli"},
		},
	},
	{
		Name: "codeql", Description: "GitHub semantic code analysis",
		ProjectURL: "https://github.com/github/codeql",
		CheckCmd: "codeql", CheckArgs: []string{"version"}, Category: "sast",
		InstallMethods: []InstallMethod{
			{Requires: "brew", Cmd: "brew install codeql"},
		},
	},
	{
		Name: "pmd", Description: "Java/Apex source code analyzer",
		ProjectURL: "https://github.com/pmd/pmd",
		CheckCmd: "pmd", CheckArgs: []string{"--version"}, Category: "sast",
		InstallMethods: []InstallMethod{
			{Requires: "brew", Cmd: "brew install pmd"},
		},
	},
	// --- Tier 1: Major language gaps ---
	{
		Name: "cppcheck", Description: "C/C++ static analysis",
		ProjectURL: "https://github.com/danmar/cppcheck",
		CheckCmd: "cppcheck", CheckArgs: []string{"--version"}, Category: "sast",
		InstallMethods: []InstallMethod{
			{Requires: "brew", Cmd: "brew install cppcheck"},
			{Cmd: "sh -c 'apt-get install -y cppcheck || yum install -y cppcheck'"},
		},
	},
	{
		Name: "phpstan", Description: "PHP static analysis",
		ProjectURL: "https://github.com/phpstan/phpstan",
		CheckCmd: "phpstan", CheckArgs: []string{"--version"}, Category: "sast",
		InstallMethods: []InstallMethod{
			{Requires: "brew", Cmd: "brew install phpstan"},
			{Cmd: "sh -c 'curl -sL https://github.com/phpstan/phpstan/releases/latest/download/phpstan.phar -o /usr/local/bin/phpstan && chmod +x /usr/local/bin/phpstan'"},
		},
	},
	{
		Name: "brakeman", Description: "Ruby on Rails security scanner",
		ProjectURL: "https://github.com/presidentbeef/brakeman",
		CheckCmd: "brakeman", CheckArgs: []string{"--version"}, Category: "sast",
		InstallMethods: []InstallMethod{
			{Requires: "rbenv", Cmd: "gem install brakeman"},
		},
	},
	{
		Name: "rubocop", Description: "Ruby static code analyzer & formatter",
		ProjectURL: "https://github.com/rubocop/rubocop",
		CheckCmd: "rubocop", CheckArgs: []string{"--version"}, Category: "quality",
		InstallMethods: []InstallMethod{
			{Requires: "rbenv", Cmd: "gem install rubocop"},
		},
	},
	{
		Name: "govulncheck", Description: "Go vulnerability scanner (official)",
		ProjectURL: "https://github.com/golang/vuln",
		CheckCmd: "govulncheck", CheckArgs: []string{"-version"}, Category: "sca",
		InstallMethods: []InstallMethod{
			{Requires: "go", Cmd: "go install golang.org/x/vuln/cmd/govulncheck@latest"},
		},
	},
	{
		Name: "swiftlint", Description: "Swift style & conventions linter (requires Xcode.app)",
		ProjectURL: "https://github.com/realm/SwiftLint",
		CheckCmd: "swiftlint", CheckArgs: []string{"version"}, Category: "quality",
		InstallMethods: []InstallMethod{}, // Requires full Xcode.app — cannot auto-install
	},
	{
		Name: "sqlfluff", Description: "SQL linter & auto-formatter (multi-dialect)",
		ProjectURL: "https://github.com/sqlfluff/sqlfluff",
		CheckCmd: "sqlfluff", CheckArgs: []string{"version"}, Category: "quality",
		InstallMethods: []InstallMethod{
			{Requires: "uv", Cmd: "uv tool install --upgrade sqlfluff"},
		},
	},
	{
		Name: "infer", Description: "C/C++/Java/ObjC deep static analysis (Meta)",
		ProjectURL: "https://github.com/facebook/infer",
		CheckCmd: "infer", CheckArgs: []string{"--version"}, Category: "sast",
		InstallMethods: []InstallMethod{}, // Manual install — see github.com/facebook/infer/releases
	},
	// --- Tier 2: Infrastructure & security ---
	{
		Name: "tflint", Description: "Terraform linter",
		ProjectURL: "https://github.com/terraform-linters/tflint",
		CheckCmd: "tflint", CheckArgs: []string{"--version"}, Category: "infra",
		InstallMethods: []InstallMethod{
			{Requires: "brew", Cmd: "brew install tflint"},
			{Cmd: "sh -c 'curl -s https://raw.githubusercontent.com/terraform-linters/tflint/master/install_linux.sh | bash'"},
		},
	},
	{
		Name: "kubescape", Description: "Kubernetes security scanner (CNCF)",
		ProjectURL: "https://github.com/kubescape/kubescape",
		CheckCmd: "kubescape", CheckArgs: []string{"version"}, Category: "infra",
		InstallMethods: []InstallMethod{
			{Requires: "brew", Cmd: "brew install kubescape"},
			{Cmd: "sh -c 'curl -s https://raw.githubusercontent.com/kubescape/kubescape/master/install.sh | /bin/bash'"},
		},
	},
	{
		Name: "kube-linter", Description: "Kubernetes manifest linter",
		ProjectURL: "https://github.com/stackrox/kube-linter",
		CheckCmd: "kube-linter", CheckArgs: []string{"version"}, Category: "infra",
		InstallMethods: []InstallMethod{
			{Requires: "brew", Cmd: "brew install kube-linter"},
			{Requires: "go", Cmd: "go install golang.stackrox.io/kube-linter/cmd/kube-linter@latest"},
		},
	},
	{
		Name: "nuclei", Description: "Template-based vulnerability scanner",
		ProjectURL: "https://github.com/projectdiscovery/nuclei",
		CheckCmd: "nuclei", CheckArgs: []string{"-version"}, Category: "dast",
		InstallMethods: []InstallMethod{
			{Requires: "brew", Cmd: "brew install nuclei"},
			{Requires: "go", Cmd: "go install -v github.com/projectdiscovery/nuclei/v3/cmd/nuclei@latest"},
		},
	},
	{
		Name: "osv-scanner", Description: "Multi-language dependency vulnerability scanner (Google)",
		ProjectURL: "https://github.com/google/osv-scanner",
		CheckCmd: "osv-scanner", CheckArgs: []string{"--version"}, Category: "sca",
		InstallMethods: []InstallMethod{
			{Requires: "brew", Cmd: "brew install osv-scanner"},
			{Requires: "go", Cmd: "go install github.com/google/osv-scanner/cmd/osv-scanner@latest"},
		},
	},
	{
		Name: "detect-secrets", Description: "Baseline secrets scanner (Yelp)",
		ProjectURL: "https://github.com/Yelp/detect-secrets",
		CheckCmd: "detect-secrets", CheckArgs: []string{"--version"}, Category: "secrets",
		InstallMethods: []InstallMethod{
			{Requires: "uv", Cmd: "uv tool install --upgrade detect-secrets"},
			{Requires: "brew", Cmd: "brew install detect-secrets"},
			{Requires: "pip3", Cmd: "pip3 install detect-secrets"},
		},
	},
	{
		Name: "npm-audit", Description: "Node.js dependency vulnerability checker",
		ProjectURL: "https://docs.npmjs.com/cli/commands/npm-audit",
		CheckCmd: "npm", CheckArgs: []string{"audit", "--version"}, Category: "sca",
		InstallMethods: []InstallMethod{
			// npm-audit is built into npm — if npm exists, it's available
			{Requires: "npm", Cmd: "echo 'npm audit is built into npm — already available'"},
		},
	},
}

// GetTool looks up a tool definition by name.
func GetTool(name string) (ToolDef, bool) {
	for _, t := range Tools {
		if t.Name == name {
			return t, true
		}
	}
	return ToolDef{}, false
}

// Prereqs tracks which package managers/runtimes are available on the host.
type Prereqs struct {
	Brew   bool `json:"brew"`
	Uv     bool `json:"uv"`
	Pip3   bool `json:"pip3"`
	Npm    bool `json:"npm"`
	Go     bool `json:"go"`
	Rustup bool `json:"rustup"`
	Curl   bool `json:"curl"`
	Gem    bool `json:"gem"`
}

// DetectPrereqs checks which package managers and runtimes are in PATH.
func DetectPrereqs() Prereqs {
	has := func(name string) bool {
		_, err := exec.LookPath(name)
		return err == nil
	}
	return Prereqs{
		Brew:   has("brew"),
		Uv:     has("uv"),
		Pip3:   has("pip3"),
		Npm:    has("npm"),
		Go:     has("go"),
		Rustup: has("rustup"),
		Curl:   has("curl"),
		Gem:    has("gem"),
	}
}

// HasPrereq checks if a specific prerequisite is available.
func (p Prereqs) HasPrereq(requires string) bool {
	if requires == "" {
		return true
	}
	switch requires {
	case "brew":
		return p.Brew
	case "uv":
		return p.Uv
	case "pip3":
		return p.Pip3
	case "npm":
		return p.Npm
	case "go":
		return p.Go
	case "rustup":
		return p.Rustup
	case "curl":
		return p.Curl
	case "gem":
		return p.Gem
	default:
		_, err := exec.LookPath(requires)
		return err == nil
	}
}

// BestMethod returns the first install method whose prerequisite is available.
func BestMethod(t ToolDef, p Prereqs) (InstallMethod, bool) {
	for _, m := range t.InstallMethods {
		if p.HasPrereq(m.Requires) {
			return m, true
		}
	}
	return InstallMethod{}, false
}

// extraPaths returns common tool binary directories that may not be in the
// server process's PATH (e.g. go install, uv tool install, cargo, gem --user-install).
func extraPaths() []string {
	home, _ := os.UserHomeDir()
	if home == "" {
		return nil
	}
	paths := []string{
		home + "/go/bin",           // go install default
		home + "/.local/bin",       // uv tool install, pip --user
		home + "/.cargo/bin",       // cargo / rustup
		"/opt/homebrew/bin",        // macOS ARM brew
		"/usr/local/bin",           // macOS Intel brew / manual installs
	}

	// Add Ruby gem --user-install bin directories (versioned paths).
	gemDir := home + "/.gem/ruby"
	if entries, err := os.ReadDir(gemDir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				paths = append(paths, gemDir+"/"+e.Name()+"/bin")
			}
		}
	}

	// Also check GOPATH/bin and GOBIN if set.
	if gobin := os.Getenv("GOBIN"); gobin != "" {
		paths = append(paths, gobin)
	} else if gopath := os.Getenv("GOPATH"); gopath != "" {
		paths = append(paths, gopath+"/bin")
	}

	return paths
}

// ensureExtendedPATH adds common tool directories to PATH if not already present.
// Called once at init so all exec.LookPath / exec.Command calls find tools.
func init() {
	current := os.Getenv("PATH")
	var missing []string
	for _, p := range extraPaths() {
		if !strings.Contains(current, p) {
			if _, err := os.Stat(p); err == nil {
				missing = append(missing, p)
			}
		}
	}
	if len(missing) > 0 {
		os.Setenv("PATH", current+":"+strings.Join(missing, ":"))
	}
}

// GetVersion checks if a tool is installed and returns its version string.
func GetVersion(t ToolDef) (string, bool) {
	out, err := exec.Command(t.CheckCmd, t.CheckArgs...).CombinedOutput()
	if err != nil {
		return "", false
	}
	ver := strings.TrimSpace(strings.Split(string(out), "\n")[0])
	return ver, true
}

// Platform describes the detected OS environment.
type Platform struct {
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	Distro string `json:"distro,omitempty"`
}

// DetectPlatform identifies the host OS and Linux distribution.
func DetectPlatform() Platform {
	p := Platform{OS: runtime.GOOS, Arch: runtime.GOARCH}
	if p.OS != "linux" {
		return p
	}
	data, err := os.ReadFile("/etc/os-release")
	if err != nil {
		p.Distro = "unknown"
		return p
	}
	content := strings.ToLower(string(data))
	switch {
	case strings.Contains(content, "id=debian") || strings.Contains(content, "id=ubuntu") || strings.Contains(content, "id_like=debian"):
		p.Distro = "debian"
	case strings.Contains(content, "id=fedora") || strings.Contains(content, "id=rhel") || strings.Contains(content, "id=centos") || strings.Contains(content, "id_like=fedora"):
		p.Distro = "fedora"
	case strings.Contains(content, "id=alpine"):
		p.Distro = "alpine"
	case strings.Contains(content, "id=arch") || strings.Contains(content, "id_like=arch"):
		p.Distro = "arch"
	default:
		p.Distro = "unknown"
	}
	return p
}

// ToolStatus represents the full status of a tool for API responses.
type ToolStatus struct {
	Name           string          `json:"name"`
	Description    string          `json:"description"`
	ProjectURL     string          `json:"project_url,omitempty"`
	Category       string          `json:"category"`
	Installed      bool            `json:"installed"`
	Version        string          `json:"version,omitempty"`
	Installable    bool            `json:"installable"`
	InstallVia     string          `json:"install_via,omitempty"`
	InstallMethods []InstallMethod `json:"install_methods"`
}

// AllToolStatus returns the status of every tool.
func AllToolStatus() []ToolStatus {
	prereqs := DetectPrereqs()
	result := make([]ToolStatus, 0, len(Tools))

	for _, t := range Tools {
		ts := ToolStatus{
			Name:           t.Name,
			Description:    t.Description,
			ProjectURL:     t.ProjectURL,
			Category:       t.Category,
			InstallMethods: t.InstallMethods,
		}

		if ver, ok := GetVersion(t); ok {
			ts.Installed = true
			ts.Version = ver
		}

		if method, ok := BestMethod(t, prereqs); ok {
			ts.Installable = true
			via := method.Requires
			if via == "" {
				via = "script"
			}
			ts.InstallVia = via
		}

		result = append(result, ts)
	}

	return result
}

// InstallTool installs a tool, streaming output to the provided writer.
// Returns the version string on success.
func InstallTool(name string, output io.Writer) (string, error) {
	t, ok := GetTool(name)
	if !ok {
		return "", fmt.Errorf("unknown tool %q", name)
	}

	// Check if already installed
	if ver, ok := GetVersion(t); ok {
		fmt.Fprintf(output, "%s is already installed (version: %s)\n", name, ver)
		return ver, nil
	}

	prereqs := DetectPrereqs()
	method, ok := BestMethod(t, prereqs)
	if !ok {
		return "", fmt.Errorf("no install method available for %q — missing prerequisites", name)
	}

	via := method.Requires
	if via == "" {
		via = "script"
	}
	fmt.Fprintf(output, "Installing %s via %s...\n", name, via)
	fmt.Fprintf(output, "$ %s\n", method.Cmd)

	// Run the install command, streaming output
	cmd := exec.Command("sh", "-c", method.Cmd)
	cmd.Stdout = output
	cmd.Stderr = output

	if err := cmd.Run(); err != nil {
		// Try fallback
		fallback := findFallback(t, prereqs, method.Cmd)
		if fallback == nil {
			return "", fmt.Errorf("install failed: %w", err)
		}

		fallbackVia := fallback.Requires
		if fallbackVia == "" {
			fallbackVia = "script"
		}
		fmt.Fprintf(output, "\nPrimary method failed. Retrying via %s...\n", fallbackVia)
		fmt.Fprintf(output, "$ %s\n", fallback.Cmd)

		cmd2 := exec.Command("sh", "-c", fallback.Cmd)
		cmd2.Stdout = output
		cmd2.Stderr = output
		if err := cmd2.Run(); err != nil {
			return "", fmt.Errorf("install failed (fallback): %w", err)
		}
	}

	// Verify
	ver, ok := GetVersion(t)
	if !ok {
		return "", fmt.Errorf("install appeared to succeed but %s is not in PATH", name)
	}

	fmt.Fprintf(output, "\nInstalled %s (version: %s)\n", name, ver)
	return ver, nil
}

func findFallback(t ToolDef, p Prereqs, failedCmd string) *InstallMethod {
	pastFailed := false
	for _, m := range t.InstallMethods {
		if m.Cmd == failedCmd {
			pastFailed = true
			continue
		}
		if pastFailed && p.HasPrereq(m.Requires) {
			return &m
		}
	}
	return nil
}
