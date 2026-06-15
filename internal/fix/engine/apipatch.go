package engine

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/ai"
	"github.com/alphabravocompany/thewolf/internal/models"
)

// APIPatchEngine is the raw-API fix engine. A plain LLM endpoint cannot
// autonomously edit a repo, so wolf drives it: prompt the model for a
// unified diff, apply it with `git apply`, and on rejection re-prompt
// with the apply error — retrying up to MaxAttempts before failing the
// batch. It implements SubprocessEngine so the registry treats it
// uniformly with the agentic CLI engines.
type APIPatchEngine struct {
	provider ai.Provider
	// MaxAttempts caps patch-apply retries per finding. Default 3.
	MaxAttempts int
}

// NewAPIPatchEngine builds a raw-API patch engine over the given provider.
func NewAPIPatchEngine(provider ai.Provider) *APIPatchEngine {
	return &APIPatchEngine{provider: provider, MaxAttempts: 3}
}

func (e *APIPatchEngine) Name() string { return "api-patch" }

// Available reports whether a usable provider is configured.
func (e *APIPatchEngine) Available() bool {
	return e.provider != nil && e.provider.Name() != "noop"
}

func (e *APIPatchEngine) Fix(ctx context.Context, req FixRequest) (*FixResult, error) {
	if !e.Available() {
		return &FixResult{Error: "no AI provider configured for api-patch engine"}, nil
	}
	timeout := req.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	attempts := e.MaxAttempts
	if attempts <= 0 {
		attempts = 3
	}

	base := buildPatchPrompt(req.Finding)
	var lastErr string
	var transcript strings.Builder

	for attempt := 1; attempt <= attempts; attempt++ {
		prompt := base
		if lastErr != "" {
			prompt = base + "\n\nYour previous patch failed to apply with this error:\n" +
				lastErr + "\nReturn a corrected unified diff."
		}

		raw, err := e.provider.Complete(ctx, prompt)
		if err != nil {
			return &FixResult{Output: transcript.String(), Error: "ai provider: " + err.Error()}, nil
		}
		diff := extractDiff(raw)
		fmt.Fprintf(&transcript, "--- attempt %d ---\n%s\n", attempt, raw)
		if diff == "" {
			lastErr = "no unified diff found in the response"
			continue
		}

		applyErr := gitApply(ctx, req.RepoPath, diff)
		if applyErr == nil {
			return &FixResult{
				Success:      true,
				Diff:         diff,
				FilesChanged: diffFiles(diff),
				Output:       transcript.String(),
				// APIPatchEngine applies the diff itself, so the worktree is
				// already mutated when it reports success.
				EditsInPlace: true,
			}, nil
		}
		lastErr = applyErr.Error()
	}

	return &FixResult{
		Success: false,
		Output:  transcript.String(),
		Error:   fmt.Sprintf("patch did not apply after %d attempts: %s", attempts, lastErr),
	}, nil
}

// buildPatchPrompt asks the model for a unified diff and nothing else.
func buildPatchPrompt(f models.Finding) string {
	var b strings.Builder
	b.WriteString("Fix the security/quality finding below by returning a ")
	b.WriteString("unified diff (git apply compatible) and nothing else.\n\n")
	fmt.Fprintf(&b, "Title: %s\n", f.Title)
	fmt.Fprintf(&b, "Severity: %s\n", f.Severity)
	fmt.Fprintf(&b, "File: %s:%d\n", f.FilePath, f.LineStart)
	if f.Description != "" {
		fmt.Fprintf(&b, "Description: %s\n", f.Description)
	}
	if f.CodeSnippet != "" {
		fmt.Fprintf(&b, "Code:\n%s\n", f.CodeSnippet)
	}
	b.WriteString("\nRules: change only what is needed to fix this finding; ")
	b.WriteString("use paths relative to the repo root with a/ and b/ prefixes; ")
	b.WriteString("do not include commentary outside the diff.")
	return b.String()
}

// gitApply applies a unified diff to the repo via `git apply`.
func gitApply(ctx context.Context, repoPath, diff string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", repoPath, "apply", "--whitespace=nowarn", "-")
	cmd.Stdin = strings.NewReader(diff)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}

// extractDiff pulls a unified diff out of an LLM response, tolerating
// ```diff fenced code blocks and surrounding prose.
func extractDiff(raw string) string {
	s := strings.TrimSpace(raw)
	// Prefer a fenced block.
	if i := strings.Index(s, "```"); i >= 0 {
		rest := s[i+3:]
		// drop an optional language tag on the fence line
		if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
			rest = rest[nl+1:]
		}
		if end := strings.Index(rest, "```"); end >= 0 {
			block := strings.TrimSpace(rest[:end])
			if looksLikeDiff(block) {
				return block + "\n"
			}
		}
	}
	if looksLikeDiff(s) {
		return s + "\n"
	}
	// Last resort: from the first diff marker to the end.
	for _, marker := range []string{"diff --git", "--- "} {
		if i := strings.Index(s, marker); i >= 0 {
			return strings.TrimSpace(s[i:]) + "\n"
		}
	}
	return ""
}

func looksLikeDiff(s string) bool {
	return strings.HasPrefix(s, "diff --git") ||
		strings.HasPrefix(s, "--- ") ||
		strings.HasPrefix(s, "Index: ")
}

// diffFiles lists files touched by a unified diff (best-effort).
func diffFiles(diff string) []string {
	var files []string
	seen := map[string]bool{}
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "+++ ") {
			p := strings.TrimSpace(strings.TrimPrefix(line, "+++ "))
			p = strings.TrimPrefix(p, "b/")
			if p != "" && p != "/dev/null" && !seen[p] {
				seen[p] = true
				files = append(files, p)
			}
		}
	}
	return files
}
