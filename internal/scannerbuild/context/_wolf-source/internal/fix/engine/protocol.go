package engine

import (
	"context"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/pkg/fixengine"
)

// Adapt exposes a harness through the public Fix Engine Protocol without
// moving implementations out of this package.
func Adapt(e SubprocessEngine) fixengine.Engine {
	if e == nil {
		return nil
	}
	return protocolAdapter{inner: e}
}

type protocolAdapter struct {
	inner SubprocessEngine
}

func (a protocolAdapter) Name() string    { return a.inner.Name() }
func (a protocolAdapter) Available() bool { return a.inner.Available() }

func (a protocolAdapter) Fix(ctx context.Context, req fixengine.Request) (*fixengine.Result, error) {
	in := FixRequest{
		RepoPath:     req.RepoPath,
		Timeout:      req.Timeout,
		Model:        req.Model,
		Effort:       req.Effort,
		Instructions: req.Instructions,
		Phase:        req.Phase,
	}
	for _, f := range req.Findings {
		in.Findings = append(in.Findings, models.Finding{
			ID: f.ID, ToolName: f.Tool, Title: f.Title, FilePath: f.FilePath,
			RuleID: f.RuleID, LineStart: f.Line, Severity: models.Severity(f.Severity),
		})
	}
	out, err := a.inner.Fix(ctx, in)
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, nil
	}
	return &fixengine.Result{
		Success:      out.Success,
		FilesChanged: out.FilesChanged,
		Diff:         out.Diff,
		Output:       out.Output,
		Error:        out.Error,
		EditsInPlace: out.EditsInPlace,
		Skipped:      out.Skipped,
		SkipReason:   out.SkipReason,
	}, nil
}

var (
	_ fixengine.Engine = protocolAdapter{}
	_ SubprocessEngine = (*ClaudeCode)(nil)
	_ SubprocessEngine = (*Codex)(nil)
	_ SubprocessEngine = (*OpenCode)(nil)
	_ SubprocessEngine = (*APIEngine)(nil)
	_ SubprocessEngine = (*APIPatchEngine)(nil)
	_ SubprocessEngine = (*ConfigEngine)(nil)
	_ SubprocessEngine = (*AutoEngine)(nil)
)
