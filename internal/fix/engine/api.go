package engine

import (
	"context"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/ai"
)

// APIEngine is the direct-API fix engine. A plain LLM endpoint cannot touch
// the filesystem, so wolf prompts it for a unified diff inside a fenced block
// and returns that diff to the caller — it does NOT edit files in place and it
// does NOT run `git apply` itself. The orchestrator owns applying the diff to
// the worktree and then running the verification gate, keeping the
// "judge by the diff on disk, never the engine's self-report" principle intact.
//
// This is the zero-auth fallback tier of the engine chain: it needs only an
// AI provider (an Anthropic/OpenAI key from the secret store), no logged-in
// CLI session. It differs from APIPatchEngine, which applies the diff itself;
// APIEngine deliberately leaves application to the caller so the orchestrator
// can verify before committing to the change.
type APIEngine struct {
	provider ai.Provider
}

// NewAPIEngine builds a diff-returning API engine over the given provider.
func NewAPIEngine(provider ai.Provider) *APIEngine {
	return &APIEngine{provider: provider}
}

func (e *APIEngine) Name() string { return "api" }

// Available reports whether a usable provider is configured.
func (e *APIEngine) Available() bool {
	return e.provider != nil && e.provider.Name() != "noop"
}

// Fix asks the provider for a unified diff and returns it for the caller to
// apply. It NEVER writes to req.RepoPath. The returned FixResult has
// EditsInPlace=false to signal that Diff must be applied by the caller.
func (e *APIEngine) Fix(ctx context.Context, req FixRequest) (*FixResult, error) {
	if !e.Available() {
		return &FixResult{Error: "no AI provider configured for api engine"}, nil
	}
	timeout := req.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	prompt := buildPatchPrompt(req.Finding)
	raw, err := e.provider.Complete(ctx, prompt)
	if err != nil {
		return &FixResult{Output: raw, Error: "ai provider: " + err.Error()}, nil
	}

	diff := extractDiff(raw)
	if strings.TrimSpace(diff) == "" {
		return &FixResult{
			Success:      false,
			Output:       raw,
			Error:        "no unified diff found in the API response",
			EditsInPlace: false,
		}, nil
	}

	return &FixResult{
		Success:      true,
		Diff:         diff,
		FilesChanged: diffFiles(diff),
		Output:       raw,
		// The filesystem is untouched: the caller must apply Diff.
		EditsInPlace: false,
	}, nil
}
