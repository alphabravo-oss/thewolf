package plugin

// DefaultExcludeDirs lists directories that should be skipped by SAST and
// secret-scanning tools. These are vendored dependencies, build artifacts,
// caches, and IDE config — scanning them produces noise and wastes CPU.
//
// NOTE: Do NOT use these for SCA/dependency tools (trivy, grype, osv-scanner).
// Those tools NEED to scan node_modules, vendor, etc. to find vulnerable deps.
var DefaultExcludeDirs = []string{
	// Vendored dependencies (skip for SAST/secrets, NOT for SCA)
	"node_modules",
	"vendor",
	"bower_components",

	// Version control
	".git",

	// Build artifacts / output
	"dist",
	".next",

	// Python
	"__pycache__",
	".tox",
	".venv",
	"venv",
	".mypy_cache",
	".pytest_cache",
	".ruff_cache",

	// Infrastructure
	".terraform",

	// Test coverage
	"coverage",
	".nyc_output",

	// IDE
	".idea",
	".vscode",
}

// ExcludeArgs builds command-line arguments for a given flag name repeated
// per directory. For example, ExcludeArgs("--exclude") returns
// ["--exclude", "node_modules", "--exclude", "vendor", ...].
func ExcludeArgs(flag string) []string {
	args := make([]string, 0, len(DefaultExcludeDirs)*2)
	for _, dir := range DefaultExcludeDirs {
		args = append(args, flag, dir)
	}
	return args
}
