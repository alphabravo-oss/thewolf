package driver

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/remediate/plan"
)

// findingIDsTrailer is the git trailer key executePrompt asks the agent to
// add to every fix commit, and the key commitFindingIDs reads back out. It
// is how a Patch's FindingIDs gets populated without re-deriving attribution
// from diffs, which agent edits routinely invalidate (a fix can touch a file
// no finding pointed at, or leave untouched a line a finding did).
const findingIDsTrailer = "Finding-IDs"

// triagePrompt instructs the agent to triage the finding set and print
// nothing but a plan.Plan on the last line of its output.
//
// plan.Parse rejects unknown fields, and the exec driver hands it only the
// LAST stdout line (execDriver.stream keeps a rolling `last`, not the whole
// transcript), so any prose around the JSON — reasoning before it, a remark
// after it, a markdown fence around it — breaks the parse. The instructions
// say so explicitly rather than trusting the model to infer it.
func triagePrompt(findings []models.Finding) string {
	var b strings.Builder
	b.WriteString("You are triaging findings from an automated security scan, before any code is changed.\n")
	b.WriteString("You have read-only tools (read, glob, grep, list) and read-only bash (git log/diff/show, grep, cat, ls, find) to inspect the repository. You cannot edit files in this run.\n\n")
	b.WriteString("Findings:\n")
	writeFindings(&b, findings)
	b.WriteString("\nFor every finding above, decide \"fix\" or \"skip\" and give one line of rationale each. If several findings share a root cause, say so in the rationale, but still emit one item per finding_id.\n\n")
	b.WriteString("When you are done, print ONLY this JSON object as the LAST line of your output — nothing after it, no markdown fence around it. Earlier lines may hold your reasoning; only the last line is parsed:\n")
	b.WriteString(`{"summary":"<one sentence>","items":[{"finding_id":"<id>","action":"fix|skip","rationale":"<why>","files":["<path>"]}]}`)
	b.WriteString("\nInclude exactly one item per finding_id listed above. \"files\" is optional; every other field is required. Do not add fields beyond these four — unknown fields fail validation.\n")
	return b.String()
}

// executePrompt instructs the agent to carry out an approved plan and land
// each fix as its own commit, tagged with the finding(s) it addresses so
// collectPatches can attribute commits without re-deriving it from diffs.
func executePrompt(p *plan.Plan, findings []models.Finding) string {
	byID := make(map[string]models.Finding, len(findings))
	for _, f := range findings {
		byID[f.ID] = f
	}

	var b strings.Builder
	b.WriteString("You are executing an approved remediation plan against the findings below. ")
	b.WriteString("The plan was produced by a prior triage run and already reviewed — do not re-triage or second-guess its \"skip\" decisions.\n\n")

	b.WriteString("Fix (in any order):\n")
	for _, item := range p.Items {
		if item.Action != plan.ActionFix {
			continue
		}
		fmt.Fprintf(&b, "- %s: %s", item.FindingID, item.Rationale)
		if f, ok := byID[item.FindingID]; ok {
			fmt.Fprintf(&b, " [%s/%s %s:%d-%d] %s", f.Severity, f.Category, f.FilePath, f.LineStart, f.LineEnd, f.Title)
		}
		if len(item.Files) > 0 {
			fmt.Fprintf(&b, " (files: %s)", strings.Join(item.Files, ", "))
		}
		b.WriteString("\n")
	}

	var skipped []string
	for _, item := range p.Items {
		if item.Action == plan.ActionSkip {
			skipped = append(skipped, item.FindingID)
		}
	}
	if len(skipped) > 0 {
		fmt.Fprintf(&b, "\nDo not touch (plan marked skip): %s\n", strings.Join(skipped, ", "))
	}

	b.WriteString("\nFor each fix, edit only the files it needs, then `git add` them and `git commit` as its own commit — one commit per fix, never a single commit spanning multiple findings. ")
	fmt.Fprintf(&b, "End every commit message with a trailer line of the exact form `%s: <id>[, <id>...]` naming every finding_id that commit addresses. ", findingIDsTrailer)
	b.WriteString("Wolf reads this trailer to attribute commits to findings, so it must be present, exact, and correct on every fix commit. ")
	b.WriteString("If the repository has a build or test command, run it after each fix to confirm nothing broke before moving to the next one.\n")
	return b.String()
}

// writeFindings renders one compact block per finding — enough for the agent
// to prioritize and act without reading the whole file first. It omits
// CodeSnippet: the agent has read/glob/grep tools, and the file on disk is a
// better source than a possibly-stale snippet baked into the scan record.
func writeFindings(b *strings.Builder, findings []models.Finding) {
	for _, f := range findings {
		fmt.Fprintf(b, "- id=%s severity=%s category=%s file=%s:%d-%d", f.ID, f.Severity, f.Category, f.FilePath, f.LineStart, f.LineEnd)
		if f.RuleID != "" {
			fmt.Fprintf(b, " rule=%s", f.RuleID)
		}
		if f.CWEID != "" {
			fmt.Fprintf(b, " cwe=%s", f.CWEID)
		}
		fmt.Fprintf(b, "\n  %s: %s\n", f.Title, truncate(collapse(f.Description), 300))
	}
}

// collapse folds a multi-line description onto one line so it cannot break
// the one-finding-per-line format writeFindings relies on.
func collapse(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// truncate caps a prompt fragment at n runes so one verbose finding cannot
// balloon a prompt sent on every run.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}

// runGit shells out to git in dir and returns its combined output, trimming
// nothing so callers can choose. It mirrors the package-var pattern in
// internal/fix/workspace (a var, not a func, so a future test can stub it),
// duplicated rather than imported because that package is a hard
// no-modification boundary and its runGit is unexported.
var runGit = func(ctx context.Context, dir string, args ...string) (string, error) {
	// #nosec G204 -- binary is fixed ("git"); args are branch names and SHAs
	// this package derived from git's own output, not external input.
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), strings.TrimSpace(string(out)), err)
	}
	return string(out), nil
}

// collectPatches walks the commits an execute run produced and turns each
// into a Patch.
//
// The worktree carries the FULL repository history — workspace.Prepare does
// `git worktree add <path> -b <branch>` off a local checkout, or a full (not
// shallow) `git clone` for GitHub, either way on top of everything the repo
// already had. So a bare `git log` from HEAD returns the whole project's
// history, not just this run's commits. A freshly created branch's own
// reflog records exactly where it forked ("branch: Created from HEAD"),
// which bounds the walk to commits the agent actually made — confirmed
// against git's real behavior for both `worktree add -b` and `clone` +
// `checkout -b` before relying on it here.
func collectPatches(ctx context.Context, worktreePath string) (*PatchSeries, error) {
	branchOut, err := runGit(ctx, worktreePath, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("collect patches: resolve branch: %w", err)
	}
	branch := strings.TrimSpace(branchOut)

	base, err := forkPoint(ctx, worktreePath, branch)
	if err != nil {
		return nil, fmt.Errorf("collect patches: resolve fork point: %w", err)
	}

	out, err := runGit(ctx, worktreePath, "log", "--format=%H", "--reverse", base+"..HEAD")
	if err != nil {
		return nil, fmt.Errorf("collect patches: list commits: %w", err)
	}
	shas := strings.Fields(out)
	patches := make([]Patch, 0, len(shas))
	for _, sha := range shas {
		p, err := commitPatch(ctx, worktreePath, sha)
		if err != nil {
			return nil, fmt.Errorf("collect patches: %s: %w", sha, err)
		}
		patches = append(patches, p)
	}
	return &PatchSeries{Patches: patches}, nil
}

// forkPoint returns the commit branch was created from, read from its own
// reflog's "branch: Created from" entry — the one record of the branch's
// starting point that survives without the caller having to know the name of
// whatever branch it forked from.
func forkPoint(ctx context.Context, worktreePath, branch string) (string, error) {
	out, err := runGit(ctx, worktreePath, "reflog", "show", branch)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "branch: Created from") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		return fields[0], nil
	}
	return "", fmt.Errorf("no %q entry in %s's reflog", "branch: Created from", branch)
}

// commitPatch turns one commit into a Patch: `git show --name-only` carries
// the subject and changed files; a second, body-only `git show` carries the
// Finding-IDs trailer executePrompt asked the agent to add.
func commitPatch(ctx context.Context, worktreePath, sha string) (Patch, error) {
	out, err := runGit(ctx, worktreePath, "show", "--format=%H%n%s", "--name-only", sha)
	if err != nil {
		return Patch{}, err
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 2 {
		return Patch{}, fmt.Errorf("unexpected `git show` output for %s", sha)
	}
	patch := Patch{CommitSHA: lines[0], Message: lines[1]}
	for _, f := range lines[2:] {
		if f = strings.TrimSpace(f); f != "" {
			patch.FilesChanged = append(patch.FilesChanged, f)
		}
	}

	body, err := runGit(ctx, worktreePath, "show", "-s", "--format=%B", sha)
	if err != nil {
		return Patch{}, err
	}
	patch.FindingIDs = commitFindingIDs(body)
	return patch, nil
}

// commitFindingIDs extracts the Finding-IDs trailer executePrompt instructed
// the agent to add to every fix commit. It scans for the trailer's own exact
// format rather than shelling out to `git interpret-trailers`: the format is
// one this package controls end to end (it wrote the instruction), so a
// direct scan is simpler and has no git-version-dependent trailer parsing to
// account for.
func commitFindingIDs(body string) []string {
	var ids []string
	for _, line := range strings.Split(body, "\n") {
		key, val, ok := strings.Cut(line, ":")
		if !ok || !strings.EqualFold(strings.TrimSpace(key), findingIDsTrailer) {
			continue
		}
		for _, id := range strings.Split(val, ",") {
			if id = strings.TrimSpace(id); id != "" {
				ids = append(ids, id)
			}
		}
	}
	return ids
}
