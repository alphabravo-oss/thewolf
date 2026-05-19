package suppress

import (
	"bytes"
	"errors"
	"os/exec"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/models"
)

// gitignoreReason is the SuppressedReason value applied to findings hidden
// by ApplyGitignore. Distinct from the "default:" prefix so the UI can
// surface "hidden by .gitignore" separately if it ever wants to.
const gitignoreReason = "default:gitignored"

// IgnoredPaths returns the subset of `paths` (repo-relative file paths)
// that are matched by the repo's gitignore configuration — i.e. any
// `.gitignore` at any depth, `.git/info/exclude`, and the user's global
// excludes file. Uses `git check-ignore --stdin` so semantics match git
// exactly, including negations and root-anchored patterns.
//
// Returns nil (no entries ignored) on any error: missing `git` binary,
// the path not being a git repo, command failure, etc. Callers should
// treat "couldn't determine" the same as "nothing ignored" and continue
// — gitignore filtering is best-effort, not safety-critical.
func IgnoredPaths(repoPath string, paths []string) map[string]bool {
	if repoPath == "" || len(paths) == 0 {
		return nil
	}

	// git check-ignore is brittle when fed paths it considers invalid: a
	// single absolute-looking or "../" entry aborts the whole invocation
	// with exit 128, throwing away results for the well-formed paths that
	// preceded it. Filter to repo-relative paths only. Scanners
	// occasionally emit absolute paths (e.g. `/scan/foo.py` from a
	// container mount-root); those can't be checked against the repo's
	// gitignore anyway.
	safe := make([]string, 0, len(paths))
	for _, p := range paths {
		if p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, "..") {
			continue
		}
		safe = append(safe, p)
	}
	if len(safe) == 0 {
		return nil
	}

	// #nosec G204 -- repoPath comes from a trusted Repo.SourcePath that has
	// already passed the BrowseLocal allow-list check; `safe` is sanitized
	// above to repo-relative file paths.
	cmd := exec.Command("git", "-C", repoPath, "check-ignore", "--stdin")
	cmd.Stdin = strings.NewReader(strings.Join(safe, "\n"))

	out, err := cmd.Output()
	if err != nil {
		// git check-ignore exits 1 when no inputs match — that's a normal
		// "nothing ignored" result, not an error. Any other failure means
		// we can't trust the answer; fall through silently.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return nil
		}
		return nil
	}

	set := make(map[string]bool, bytes.Count(out, []byte("\n"))+1)
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if line != "" {
			set[line] = true
		}
	}
	return set
}

// ApplyGitignore marks findings whose FilePath is gitignored at repoPath.
// Skips findings already flagged as Suppressed so an earlier-applied
// reason (e.g. "default:testdata") wins — first-source semantics, same
// as Apply. Returns the count of newly-suppressed findings.
//
// Safe to call with repoPath="" or against a non-git directory; it
// simply returns 0 without modifying findings.
func ApplyGitignore(findings []models.Finding, repoPath string) int {
	if repoPath == "" || len(findings) == 0 {
		return 0
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
		return 0
	}

	ignored := IgnoredPaths(repoPath, paths)
	if len(ignored) == 0 {
		return 0
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
	return n
}
