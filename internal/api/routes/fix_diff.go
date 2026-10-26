package routes

import (
	"context"
	"path/filepath"
	"strings"

	gitpkg "github.com/alphabravocompany/thewolf/internal/fix/git"
)

// filterUnifiedDiff keeps only file sections whose path matches one of files.
func filterUnifiedDiff(diff string, files []string) string {
	if strings.TrimSpace(diff) == "" || len(files) == 0 {
		return diff
	}
	want := map[string]bool{}
	for _, f := range files {
		f = strings.TrimSpace(strings.TrimPrefix(f, "./"))
		if f != "" {
			want[f] = true
		}
	}
	var out []string
	var buf []string
	keep := false
	flush := func() {
		if keep && len(buf) > 0 {
			out = append(out, buf...)
		}
		buf = nil
		keep = false
	}
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			flush()
			keep = diffHeaderMatches(line, want)
			buf = []string{line}
			continue
		}
		if strings.HasPrefix(line, "+++ ") || strings.HasPrefix(line, "--- ") {
			if p := diffPath(line); p != "" && want[p] {
				keep = true
			}
		}
		if buf == nil && strings.HasPrefix(line, "@@") {
			// bare hunk without a header — keep only if we already decided
			buf = []string{line}
			continue
		}
		if buf != nil {
			buf = append(buf, line)
		}
	}
	flush()
	return strings.Join(out, "\n")
}

func diffHeaderMatches(header string, want map[string]bool) bool {
	// diff --git a/foo.go b/foo.go
	fields := strings.Fields(header)
	for _, f := range fields {
		p := strings.TrimPrefix(strings.TrimPrefix(f, "a/"), "b/")
		if want[p] {
			return true
		}
	}
	return false
}

func diffPath(line string) string {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "+++ ") && !strings.HasPrefix(line, "--- ") {
		return ""
	}
	p := strings.TrimSpace(line[4:])
	if p == "/dev/null" {
		return ""
	}
	if strings.HasPrefix(p, "a/") || strings.HasPrefix(p, "b/") {
		p = p[2:]
	}
	if i := strings.Index(p, "\t"); i >= 0 {
		p = p[:i]
	}
	return p
}

func liveWorkspaceDiff(repoPath string) string {
	if repoPath == "" {
		return ""
	}
	if !filepath.IsAbs(repoPath) {
		return ""
	}
	base := workspaceMergeBase(repoPath)
	if base == "" {
		base = "HEAD"
	}
	// Compare the full working tree to the base once, including staged edits.
	// Disable repository-configured diff/textconv and fsmonitor helpers.
	out, err := gitpkg.Command(context.Background(), nil, "-C", repoPath, "diff", "--no-ext-diff", "--no-textconv", "--find-renames", base, "--").Output() // #nosec G204
	if err != nil {
		return ""
	}
	return string(out)
}

func workspaceMergeBase(repoPath string) string {
	for _, ref := range []string{"main", "master", "origin/main", "origin/master"} {
		out, err := gitpkg.Command(context.Background(), nil, "-C", repoPath, "merge-base", "HEAD", ref).Output() // #nosec G204
		if err == nil {
			if s := strings.TrimSpace(string(out)); s != "" {
				return s
			}
		}
	}
	return ""
}

type fixCommit struct {
	SHA     string `json:"sha"`
	Subject string `json:"subject"`
	When    string `json:"when,omitempty"`
}

func listWorkspaceCommits(repoPath string) []fixCommit {
	if repoPath == "" || !filepath.IsAbs(repoPath) {
		return nil
	}
	out, err := gitpkg.Command(context.Background(), nil, "-C", repoPath, "log", "-50", "--format=%H\t%s\t%cI").Output() // #nosec G204
	if err != nil {
		return nil
	}
	var commits []fixCommit
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		c := fixCommit{SHA: parts[0]}
		if len(parts) > 1 {
			c.Subject = parts[1]
		}
		if len(parts) > 2 {
			c.When = parts[2]
		}
		commits = append(commits, c)
	}
	return commits
}
