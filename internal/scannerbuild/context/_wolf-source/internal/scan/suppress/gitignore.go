package suppress

import (
	"context"
	gitcmd "github.com/alphabravocompany/thewolf/internal/gitops"
	"github.com/alphabravocompany/thewolf/internal/models"
)

// gitignoreReason is the SuppressedReason value applied to findings hidden
// by ApplyGitignore. Distinct from the "default:" prefix so the UI can
// surface "hidden by .gitignore" separately if it ever wants to.
const gitignoreReason = "default:gitignored"

// ApplyGitignore marks findings whose FilePath is gitignored at repoPath.
// Skips findings already flagged as Suppressed so an earlier-applied
// reason (e.g. "default:testdata") wins — first-source semantics, same
// as Apply. Returns the count of newly-suppressed findings or an evaluation error.
//
// Safe to call with repoPath="" or against a non-git directory; it
// simply returns 0 without modifying findings.
func ApplyGitignore(ctx context.Context, findings []models.Finding, repoPath string) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if repoPath == "" || len(findings) == 0 {
		return 0, nil
	}

	// Build the candidate list — only paths from not-yet-suppressed
	// findings, deduped to keep the git invocation small on scans with
	// thousands of findings against the same files.
	seen := make(map[string]struct{}, len(findings))
	var paths []string
	for _, f := range findings {
		if f.Suppressed || f.FilePath == "" {
			continue
		}
		if _, dup := seen[f.FilePath]; dup {
			continue
		}
		seen[f.FilePath] = struct{}{}
		paths = append(paths, f.FilePath)
	}
	if len(paths) == 0 {
		return 0, nil
	}

	ignored, err := gitcmd.IgnoredPaths(ctx, repoPath, paths)
	if err != nil {
		return 0, err
	}
	if len(ignored) == 0 {
		return 0, nil
	}

	n := 0
	for i := range findings {
		if findings[i].Suppressed {
			continue
		}
		if ignored[findings[i].FilePath] {
			findings[i].Suppressed = true
			findings[i].SuppressedReason = gitignoreReason
			n++
		}
	}
	return n, nil
}
