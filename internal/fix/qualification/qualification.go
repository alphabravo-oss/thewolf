// Package qualification exercises the autonomous fixer runtime contract from
// inside a built image. It deliberately uses the production engine selector
// and API engine, but a deterministic in-process provider: release
// qualification must prove fallback semantics without sending credentials or
// prompts to an external service.
package qualification

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/alphabravocompany/thewolf/internal/ai"
	"github.com/alphabravocompany/thewolf/internal/fix/engine"
	"github.com/alphabravocompany/thewolf/internal/models"
)

const SchemaVersion = "wolf.fixer-runtime-qualification/v1"

// Report is the canonical, credential-free evidence emitted by a fixer image.
// The release adapter validates every field before accepting it.
type Report struct {
	SchemaVersion   string   `json:"schema_version"`
	Variant         string   `json:"variant"`
	AuthMode        string   `json:"auth_mode"`
	InstalledCLIs   []string `json:"installed_clis"`
	SelectedTiers   []string `json:"selected_tiers"`
	CompletedChecks []string `json:"completed_checks"`
}

type boundary struct {
	authMode string
	cli      string
}

var boundaries = map[string]boundary{
	"base":   {authMode: "none"},
	"api":    {authMode: "api-key"},
	"claude": {authMode: "interactive-session", cli: "claude"},
	"codex":  {authMode: "interactive-session", cli: "codex"},
	// opencode takes credentials per run through OPENCODE_AUTH_CONTENT rather
	// than a logged-in session on disk, so its boundary is "injected".
	"opencode": {authMode: "injected", cli: "opencode"},
}

// Run validates the packaged variant and executes success, fallback, and
// fail-closed engine cases. It assumes the caller has supplied an empty HOME
// and disabled container networking; an unexpectedly authenticated CLI is a
// qualification failure, never accepted as evidence.
func Run(ctx context.Context, expectedVariant, expectedAuthMode, scratch string) (Report, error) {
	expectedVariant = strings.TrimSpace(expectedVariant)
	expectedAuthMode = strings.TrimSpace(expectedAuthMode)
	expected, ok := boundaries[expectedVariant]
	if !ok {
		return Report{}, fmt.Errorf("unknown fixer variant %q", expectedVariant)
	}
	if expectedAuthMode != expected.authMode {
		return Report{}, fmt.Errorf("variant %q requires auth mode %q, got %q", expectedVariant, expected.authMode, expectedAuthMode)
	}
	if actual := strings.TrimSpace(os.Getenv("WOLF_FIXER_VARIANT")); actual != expectedVariant {
		return Report{}, fmt.Errorf("runtime variant is %q, expected %q", actual, expectedVariant)
	}
	if !filepath.IsAbs(scratch) {
		return Report{}, errors.New("fixer qualification requires an absolute executable scratch directory")
	}

	installed := make([]string, 0, 1)
	for _, command := range []string{"claude", "codex", "opencode"} {
		_, err := exec.LookPath(command)
		present := err == nil
		if command == expected.cli && !present {
			return Report{}, fmt.Errorf("variant %q is missing required CLI %q", expectedVariant, command)
		}
		if command != expected.cli && present {
			return Report{}, fmt.Errorf("variant %q unexpectedly contains CLI %q", expectedVariant, command)
		}
		if present {
			installed = append(installed, command)
		}
	}

	provider := deterministicProvider{}
	chain, err := engine.SelectEngine(ctx, engine.ChainConfig{Engine: "auto", Provider: provider})
	if err != nil {
		return Report{}, fmt.Errorf("select deterministic fallback chain: %w", err)
	}
	tiers := tierNames(chain)
	if !slices.Equal(tiers, []string{"api"}) {
		return Report{}, fmt.Errorf("empty-home offline engine chain is %v, expected API-only fallback", tiers)
	}

	if _, err := engine.SelectEngine(ctx, engine.ChainConfig{Engine: "auto", Provider: ai.NewNoopProvider()}); err == nil {
		return Report{}, errors.New("empty-home offline engine selection did not fail closed without an API provider")
	}
	if _, err := engine.SelectEngine(ctx, engine.ChainConfig{Engine: "api", Provider: ai.NewNoopProvider()}); err == nil {
		return Report{}, errors.New("explicit API engine did not reject a missing provider")
	}

	result, err := engine.NewAPIEngine(provider).Fix(ctx, engine.FixRequest{Finding: models.Finding{
		Title: "qualification", FilePath: "qualification.txt", LineStart: 1,
	}})
	if err != nil {
		return Report{}, fmt.Errorf("execute deterministic API engine: %w", err)
	}
	if result == nil || !result.Success || result.EditsInPlace ||
		result.Diff != qualificationDiff || !slices.Equal(result.FilesChanged, []string{"qualification.txt"}) {
		return Report{}, errors.New("API engine did not return the exact non-mutating unified-diff contract")
	}

	badResult, err := engine.NewAPIEngine(emptyProvider{}).Fix(ctx, engine.FixRequest{Finding: models.Finding{
		Title: "qualification", FilePath: "qualification.txt", LineStart: 1,
	}})
	if err != nil || badResult == nil || badResult.Success || strings.TrimSpace(badResult.Error) == "" {
		return Report{}, errors.New("API engine did not reject a provider response without a unified diff")
	}
	if err := exerciseCLIProtocols(ctx, scratch); err != nil {
		return Report{}, err
	}

	report := Report{
		SchemaVersion: SchemaVersion,
		Variant:       expectedVariant,
		AuthMode:      expectedAuthMode,
		InstalledCLIs: installed,
		SelectedTiers: tiers,
		CompletedChecks: []string{
			"api-diff-contract",
			"api-malformed-response-rejected",
			"cli-boundary",
			"cli-command-failure-rejected",
			"cli-malformed-output-rejected",
			"cli-protocol-success",
			"cli-timeout-rejected",
			"missing-provider-rejected",
			"unauthenticated-api-fallback",
		},
	}
	if err := ValidateReport(report, expectedVariant, expectedAuthMode); err != nil {
		return Report{}, err
	}
	return report, nil
}

// ValidateReport rejects any partial or embellished qualification report. It
// is shared with the trusted release adapter so stdout alone is never treated
// as proof without validating the exact contract.
func ValidateReport(report Report, expectedVariant, expectedAuthMode string) error {
	expected, ok := boundaries[expectedVariant]
	if !ok || expected.authMode != expectedAuthMode {
		return errors.New("invalid expected fixer boundary")
	}
	expectedCLIs := []string{}
	if expected.cli != "" {
		expectedCLIs = []string{expected.cli}
	}
	expectedChecks := []string{
		"api-diff-contract",
		"api-malformed-response-rejected",
		"cli-boundary",
		"cli-command-failure-rejected",
		"cli-malformed-output-rejected",
		"cli-protocol-success",
		"cli-timeout-rejected",
		"missing-provider-rejected",
		"unauthenticated-api-fallback",
	}
	if report.SchemaVersion != SchemaVersion || report.Variant != expectedVariant ||
		report.AuthMode != expectedAuthMode || !slices.Equal(report.InstalledCLIs, expectedCLIs) ||
		!slices.Equal(report.SelectedTiers, []string{"api"}) ||
		!slices.Equal(report.CompletedChecks, expectedChecks) {
		return errors.New("fixer qualification report does not match the exact runtime contract")
	}
	return nil
}

func exerciseCLIProtocols(ctx context.Context, scratch string) error {
	if err := os.MkdirAll(scratch, 0o700); err != nil {
		return fmt.Errorf("create fixer qualification scratch: %w", err)
	}
	originalPath := os.Getenv("PATH")
	if err := os.Setenv("PATH", scratch+string(os.PathListSeparator)+originalPath); err != nil {
		return err
	}
	defer os.Setenv("PATH", originalPath) //nolint:errcheck -- best-effort process-local restoration at command exit.

	claudeCapture := filepath.Join(scratch, "claude.args")
	codexCapture := filepath.Join(scratch, "codex.args")
	if err := writeCLIShim(filepath.Join(scratch, "claude"), claudeCapture, `printf '%s\n' '{"result":"ok","files_changed":["qualification.txt"]}'`); err != nil {
		return err
	}
	if err := writeCLIShim(filepath.Join(scratch, "codex"), codexCapture, `printf '%s\n' 'codex-ok'`); err != nil {
		return err
	}
	request := engine.FixRequest{
		Finding:  models.Finding{Title: "qualification", FilePath: "qualification.txt", LineStart: 1},
		RepoPath: scratch, Timeout: 5 * time.Second,
	}
	claudeResult, err := (&engine.ClaudeCode{Model: "qualification-model"}).Fix(ctx, request)
	if err != nil || claudeResult == nil || !claudeResult.Success || !claudeResult.EditsInPlace {
		return errors.New("Claude protocol success case failed")
	}
	claudeArgs, err := os.ReadFile(claudeCapture)
	if err != nil || !containsLines(string(claudeArgs), "--dangerously-skip-permissions", "--output-format", "json", "--model", "qualification-model", "-p") {
		return errors.New("Claude production invocation arguments do not match the fixer contract")
	}
	codexResult, err := (&engine.Codex{}).Fix(ctx, request)
	if err != nil || codexResult == nil || !codexResult.Success || !codexResult.EditsInPlace {
		return errors.New("Codex protocol success case failed")
	}
	codexArgs, err := os.ReadFile(codexCapture)
	if err != nil || !containsLines(string(codexArgs), "--approval-mode", "full-auto", "--quiet") {
		return errors.New("Codex production invocation arguments do not match the fixer contract")
	}

	if err := writeCLIShim(filepath.Join(scratch, "claude"), claudeCapture, `printf '%s\n' 'malformed'`); err != nil {
		return err
	}
	malformed, err := (&engine.ClaudeCode{}).Fix(ctx, request)
	if err != nil || malformed == nil || malformed.Success || strings.TrimSpace(malformed.Error) == "" {
		return errors.New("Claude malformed output was not rejected")
	}
	if err := writeCLIShim(filepath.Join(scratch, "claude"), claudeCapture, `exit 17`); err != nil {
		return err
	}
	failed, err := (&engine.ClaudeCode{}).Fix(ctx, request)
	if err != nil || failed == nil || failed.Success || strings.TrimSpace(failed.Error) == "" {
		return errors.New("Claude command failure was not rejected")
	}
	if err := writeCLIShim(filepath.Join(scratch, "claude"), claudeCapture, `while :; do :; done`); err != nil {
		return err
	}
	timeoutRequest := request
	timeoutRequest.Timeout = 25 * time.Millisecond
	timedOut, err := (&engine.ClaudeCode{}).Fix(ctx, timeoutRequest)
	if err != nil || timedOut == nil || timedOut.Success || strings.TrimSpace(timedOut.Error) == "" {
		return errors.New("Claude timeout was not rejected")
	}
	if err := writeCLIShim(filepath.Join(scratch, "codex"), codexCapture, `exit 23`); err != nil {
		return err
	}
	failed, err = (&engine.Codex{}).Fix(ctx, request)
	if err != nil || failed == nil || failed.Success || strings.TrimSpace(failed.Error) == "" {
		return errors.New("Codex command failure was not rejected")
	}
	if err := writeCLIShim(filepath.Join(scratch, "codex"), codexCapture, `while :; do :; done`); err != nil {
		return err
	}
	timedOut, err = (&engine.Codex{}).Fix(ctx, timeoutRequest)
	if err != nil || timedOut == nil || timedOut.Success || strings.TrimSpace(timedOut.Error) == "" {
		return errors.New("Codex timeout was not rejected")
	}
	return nil
}

func writeCLIShim(path, capture, behavior string) error {
	value := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + shellSingleQuote(capture) + "\n" + behavior + "\n"
	return os.WriteFile(path, []byte(value), 0o700)
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func containsLines(value string, expected ...string) bool {
	lines := strings.Split(strings.TrimSpace(value), "\n")
	for _, item := range expected {
		if !slices.Contains(lines, item) {
			return false
		}
	}
	return true
}

func tierNames(chain *engine.Chain) []string {
	values := make([]string, 0, chain.Len())
	for _, tier := range chain.Tiers() {
		values = append(values, tier.Name())
	}
	return values
}

const qualificationDiff = "diff --git a/qualification.txt b/qualification.txt\n" +
	"--- a/qualification.txt\n" +
	"+++ b/qualification.txt\n" +
	"@@ -1 +1 @@\n" +
	"-unsafe\n" +
	"+safe\n"

type deterministicProvider struct{}

func (deterministicProvider) Name() string { return "qualification" }
func (deterministicProvider) Analyze(context.Context, ai.AnalyzeRequest) (*ai.AnalyzeResponse, error) {
	return nil, errors.New("qualification provider supports Complete only")
}
func (deterministicProvider) Score(context.Context, ai.ScoreRequest) (*ai.ScoreResponse, error) {
	return nil, errors.New("qualification provider supports Complete only")
}
func (deterministicProvider) Summarize(context.Context, ai.SummarizeRequest) (string, error) {
	return "", errors.New("qualification provider supports Complete only")
}
func (deterministicProvider) Complete(context.Context, string) (string, error) {
	return "```diff\n" + qualificationDiff + "```", nil
}

type emptyProvider struct{ deterministicProvider }

func (emptyProvider) Complete(context.Context, string) (string, error) { return "no patch", nil }
