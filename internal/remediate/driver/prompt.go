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
// last non-empty line of the agent's own final "text" event content — not
// the raw NDJSON stream, whose true last line is normally a step_finish
// accounting event, never prose (see execDriver.stream). So any prose
// around the JSON — reasoning before it, a remark after it, a markdown
// fence around it — breaks the parse. The instructions say so explicitly
// rather than trusting the model to infer it.
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

// collectPatches walks the commits an execute run produced, from base
// (exclusive) to HEAD (inclusive), and turns each into a Patch. base is the
// worktree's HEAD as of immediately before the run started — execDriver.
// Execute captures it with a plain `git rev-parse HEAD` right before
// invoking the agent, because the worktree carries the FULL repository
// history (workspace.Prepare does `git worktree add <path> -b <branch>` off
// a local checkout, or a full, non-shallow `git clone` for GitHub, either
// way on top of everything the repo already had), so an unbounded `git log`
// from HEAD would return the whole project's history, not just this run's
// commits. An earlier version of this function derived base from the
// branch's own reflog "Created from" entry instead; that broke under
// core.logAllRefUpdates=false (reflog show exits 0 with empty output, no
// error) and under a mid-run `git checkout <sha>` (permitted by the execute
// permission document's bash allowlist), silently discarding an otherwise-
// successful run in both cases. Capturing base directly has neither failure
// mode.
//
// findings is the set Wolf handed the agent this run; it validates the
// Finding-IDs trailer each commit carries (see commitFindingIDs) so a
// malformed or hallucinated ID cannot silently corrupt attribution. A
// commit that ends up with none is kept, not dropped — see Patch.
// Unattributed — because one missed trailer must not discard every other,
// correctly attributed commit in the same run. Only a run where NO commit
// carries any attribution at all is rejected outright, since that is the
// one shape that plausibly means the agent ignored the trailer contract
// entirely rather than missing it once.
//
// Cleanliness (did the agent leave uncommitted work behind) is deliberately
// NOT checked here — see checkClean's doc comment for why that is the
// caller's call, not this function's.
func collectPatches(ctx context.Context, worktreePath, base string, findings []models.Finding) (*PatchSeries, error) {
	validIDs := make(map[string]struct{}, len(findings))
	for _, f := range findings {
		validIDs[f.ID] = struct{}{}
	}

	out, err := runGit(ctx, worktreePath, "log", "--format=%H", "--reverse", base+"..HEAD")
	if err != nil {
		return nil, fmt.Errorf("collect patches: list commits: %w", err)
	}
	shas := strings.Fields(out)
	patches := make([]Patch, 0, len(shas))
	attributed := 0
	for _, sha := range shas {
		p, err := commitPatch(ctx, worktreePath, sha, validIDs)
		if err != nil {
			return nil, fmt.Errorf("collect patches: %s: %w", sha, err)
		}
		p.Unattributed = len(p.FindingIDs) == 0
		if !p.Unattributed {
			attributed++
		}
		patches = append(patches, p)
	}
	if len(patches) > 0 && attributed == 0 {
		return nil, fmt.Errorf("collect patches: no commit in this run carries an attributable %s trailer (%d commit(s))", findingIDsTrailer, len(patches))
	}

	return &PatchSeries{Patches: patches}, nil
}

// strippedAgentConfigPaths are the repository-level OpenCode config paths a
// separate, not-yet-landed task (StripAgentConfig) removes from the
// worktree before any driver call, on every run against a repo that ships
// one — that is the entire point of stripping them, so their removal
// shows up in every such run's `git status`, not as an edge case. The list
// is duplicated here defensively rather than imported: this package cannot
// depend on a sibling package that does not exist yet, and must not assume
// it lands with this exact shape.
var strippedAgentConfigPaths = []string{"opencode.json", "opencode.jsonc", ".opencode"}

func isStrippedAgentConfigPath(path string) bool {
	for _, p := range strippedAgentConfigPaths {
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}

// checkClean fails if the agent left uncommitted work behind — a tracked
// file edited and never committed, which `git log` alone would silently
// drop since nothing else inspects the working tree. It is NOT part of
// collectPatches: budget exhaustion kills the container mid-tool-call, so a
// half-written file is the EXPECTED state on that path, not evidence a fix
// was lost, and execDriver.Execute skips this check entirely when the run
// was exhausted rather than let it discard commits the agent already
// landed.
//
// Two categories of "dirty" are deliberately not failures:
//   - Untracked files (--untracked-files=no): a stray build artifact, or a
//     directory OpenCode itself creates in the project dir, is not agent
//     work left uncommitted.
//   - strippedAgentConfigPaths: removed from the worktree before this run
//     even started, by a step outside this package's control. Their
//     deletion is expected on every run against a repo that ships one, not
//     a signal of anything the agent did.
func checkClean(ctx context.Context, worktreePath string) error {
	out, err := runGit(ctx, worktreePath, "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return fmt.Errorf("collect patches: check worktree status: %w", err)
	}
	var dirty []string
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		if isStrippedAgentConfigPath(statusPath(line)) {
			continue
		}
		dirty = append(dirty, line)
	}
	if len(dirty) > 0 {
		return fmt.Errorf("collect patches: worktree has uncommitted changes:\n%s", strings.Join(dirty, "\n"))
	}
	return nil
}

// statusPath extracts the path from one `git status --porcelain` line:
// 2 status characters, a space, then either a plain path or, for a rename,
// "old -> new" — the latter names what's actually present in the tree now.
func statusPath(line string) string {
	if len(line) < 4 {
		return ""
	}
	path := line[3:]
	if idx := strings.Index(path, " -> "); idx >= 0 {
		path = path[idx+4:]
	}
	return path
}

// commitPatch turns one commit into a Patch: `git show --name-only` carries
// the subject and changed files; a second, body-only `git show` carries the
// Finding-IDs trailer executePrompt asked the agent to add.
func commitPatch(ctx context.Context, worktreePath, sha string, validIDs map[string]struct{}) (Patch, error) {
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
	patch.FindingIDs = commitFindingIDs(body, validIDs)
	return patch, nil
}

// commitFindingIDs extracts the Finding-IDs trailer executePrompt instructed
// the agent to add to every fix commit. It scans for the trailer's own exact
// format rather than shelling out to `git interpret-trailers`: the format is
// one this package controls end to end (it wrote the instruction), so a
// direct scan is simpler and has no git-version-dependent trailer parsing to
// account for.
//
// validIDs is the finding set Wolf actually handed the agent this run — the
// only IDs a genuine attribution can name — so an ID that isn't in it is
// dropped rather than trusted. This also catches malformed trailers as a
// side effect: "f-1 f-2" (space, not comma) parses as one bogus id
// "f-1 f-2", which matches nothing in validIDs and is dropped; "[f-1, f-2]"
// splits into "[f-1" and "f-2]", neither of which matches either. A commit
// that ends up with zero IDs this way is kept by collectPatches with
// Patch.Unattributed set, not rejected — see collectPatches's doc comment.
func commitFindingIDs(body string, validIDs map[string]struct{}) []string {
	var ids []string
	for _, line := range strings.Split(body, "\n") {
		key, val, ok := strings.Cut(line, ":")
		if !ok || !strings.EqualFold(strings.TrimSpace(key), findingIDsTrailer) {
			continue
		}
		for _, id := range strings.Split(val, ",") {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			if _, known := validIDs[id]; !known {
				continue
			}
			ids = append(ids, id)
		}
	}
	return ids
}
