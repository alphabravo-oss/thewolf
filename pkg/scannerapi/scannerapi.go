// Package scannerapi is the public contract for scanners registered outside
// Community's internal tree, such as the Enterprise overlay's adapters for
// customer-licensed commercial engines. Registered scanners run through the
// same scheduler, container isolation, raw-output capture, normalization,
// corroboration, baselines and gates as first-party scanners.
//
// Types are aliases of Wolf's internal model, so there is exactly one model.
// See docs/scanner-plugins.md.
package scannerapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/alphabravocompany/thewolf/internal/finding/sarifio"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/plugin"
	"github.com/alphabravocompany/thewolf/internal/plugin/container"
	"github.com/alphabravocompany/thewolf/internal/scan/runner"
)

type (
	// Plugin is implemented by every scanner.
	Plugin = models.Plugin
	// ExecuteOpts is passed to Plugin.Execute. SecretEnv holds the secrets the
	// scanner declared, resolved for this run.
	ExecuteOpts = models.ExecuteOpts
	// Finding is one normalized result.
	Finding  = models.Finding
	Category = models.Category
	Severity = models.Severity
	Language = models.Language
)

const (
	CategorySAST      = models.CategorySAST
	CategorySCA       = models.CategorySCA
	CategorySecrets   = models.CategorySecrets
	CategoryQuality   = models.CategoryQuality
	CategoryContainer = models.CategoryContainer
	CategoryInfra     = models.CategoryInfra
	CategoryDAST      = models.CategoryDAST

	SeverityCritical = models.SeverityCritical
	SeverityHigh     = models.SeverityHigh
	SeverityMedium   = models.SeverityMedium
	SeverityLow      = models.SeverityLow
	SeverityInfo     = models.SeverityInfo
)

// Applicability is optional. A scanner that cannot run without some input (a
// DAST target URL, a language's files) returns NotApplicable so the run is
// recorded as skipped, not failed, and never counted as coverage.
type Applicability interface {
	Applicability(repoPath, target string) error
}

// Metadata describes how Wolf schedules and runs a registered scanner.
type Metadata struct {
	// Image is the scanner's container image. Customer-supplied for licensed
	// engines (never redistributed). Empty for scanners that only call a
	// vendor server API; Run then cannot be used.
	Image string
	// ResourceClass is "light", "medium" or "heavy" (heavy tools are limited by
	// heavy-tool concurrency). Empty derives it from the category.
	ResourceClass string
	// Timeout overrides the run's default per-tool timeout when set.
	Timeout time.Duration
	// NetworkRequired lets the container reach the network (vendor license
	// servers, DAST targets). Otherwise it runs with the default network policy.
	NetworkRequired bool
	// Secrets maps container environment variable names to the names of Wolf
	// custom secrets resolved from the scan owner at run time. A missing secret
	// fails the tool with an actionable message instead of running unlicensed.
	Secrets map[string]string
}

var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)

// Register adds a scanner. Names must be new: a registered scanner can never
// replace a first-party one. Call during process start, before serving.
func Register(p Plugin, meta Metadata) error {
	if p == nil {
		return errors.New("scannerapi: nil plugin")
	}
	name := p.Name()
	if !namePattern.MatchString(name) {
		return fmt.Errorf("scannerapi: invalid scanner name %q", name)
	}
	if _, err := plugin.Global.Get(name); err == nil {
		return fmt.Errorf("scannerapi: scanner %q is already registered", name)
	}
	switch meta.ResourceClass {
	case "", "light", "medium", "heavy":
	default:
		return fmt.Errorf("scannerapi: %s: resource class %q must be light, medium or heavy", name, meta.ResourceClass)
	}
	for env := range meta.Secrets {
		if env == "" || strings.ContainsAny(env, "= \t\n") {
			return fmt.Errorf("scannerapi: %s: invalid secret environment name %q", name, env)
		}
	}
	if meta.Image != "" {
		container.RegisterExternalImage(name, meta.Image)
	}
	if meta.ResourceClass != "" || meta.Timeout > 0 || meta.NetworkRequired {
		class := meta.ResourceClass
		if class == "" {
			class = "medium"
		}
		runner.RegisterExternalResources(name, runner.ResourceSpec{Class: class, Timeout: meta.Timeout, NetworkRequired: meta.NetworkRequired})
	}
	runner.RegisterExternalSecrets(name, meta.Secrets)
	plugin.Register(p)
	return nil
}

// NotApplicable marks a run as skipped because its input is absent.
func NotApplicable(reason string) error { return &plugin.NotApplicableError{Reason: reason} }

// Skipped marks a run as skipped for another reason (e.g. not configured).
func Skipped(reason string) error { return &plugin.SkippedError{Reason: reason} }

// Container describes one invocation of a registered scanner's image.
type Container struct {
	// Args are passed to the image entrypoint.
	Args []string
	// Env adds non-secret environment. Declared secrets are added automatically.
	Env map[string]string
	// NoRepoMount skips mounting the repository at /scan (e.g. DAST).
	NoRepoMount bool
	// WorkDir is the container working directory (default /scan).
	WorkDir string
}

// Result is a finished container invocation. Stdout is the tool's report when
// it writes one to standard output.
type Result struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// Run executes the scanner's registered image with the same isolation, resource
// limits and readiness checks as first-party scanners. A non-zero exit is not
// an error by itself (many scanners exit non-zero when they find issues);
// failure to start or a kill is returned as an error.
func Run(ctx context.Context, opts ExecuteOpts, tool string, c Container) (Result, error) {
	cfg := container.ConfigFromOpts(opts.ContainerCfg)
	env := make(map[string]string, len(c.Env)+len(opts.SecretEnv))
	for k, v := range c.Env {
		env[k] = v
	}
	for k, v := range opts.SecretEnv {
		env[k] = v
	}
	cmd := container.CommandContext(ctx, cfg, container.Options{
		RepoDir: opts.RepoPath, NoRepoMount: c.NoRepoMount, WorkDir: c.WorkDir, ExtraEnv: env,
	}, tool, c.Args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	result := Result{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		if result.ExitCode == 137 || result.ExitCode < 0 {
			return result, plugin.WrapExecError(tool, err)
		}
		return result, nil
	}
	if err != nil {
		return result, plugin.WrapExecError(tool, err)
	}
	return result, nil
}

// FindingsFromSARIF converts a SARIF 2.1.0 report into findings attributed to
// tool, using the same parser, limits and identity rules as SARIF import.
func FindingsFromSARIF(tool string, data []byte) ([]Finding, error) {
	parsed, err := sarifio.Import(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", tool, err)
	}
	for i := range parsed.Findings {
		parsed.Findings[i].ToolName = tool
	}
	return parsed.Findings, nil
}
