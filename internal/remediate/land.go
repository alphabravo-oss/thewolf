package remediate

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/alphabravocompany/thewolf/internal/fix/pr"
	"github.com/alphabravocompany/thewolf/internal/models"
	"github.com/alphabravocompany/thewolf/internal/wolflog"
)

// credentialPattern matches token shapes that must never reach a PR body.
//
// The prefix|suffix alternatives (docker/GitHub PAT, GitHub fine-grained
// tokens gho_/ghp_/ghu_/ghs_/ghr_, OpenAI-shaped sk-, Slack xox[baprs]-) are
// anchored on \b: without it, "sk-" also matches mid-word inside ordinary
// English ("Disk-space", "Risk-based"), redacting real prose that happens to
// contain the substring — a redactor eating real text is its own failure
// mode, as bad as missing a real credential. AKIA/AIza/eyJ/PEM are fixed,
// self-contained shapes (AWS access key ID, Google API key, JWT header
// segment, PEM private key block) rather than prefix+generic-suffix, so they
// are matched as complete patterns instead of being forced into that shape.
var credentialPattern = regexp.MustCompile(`(?i)` +
	`\b(?:dckr_pat|github_pat|gh[opusr]_|sk-|xox[baprs]-)[A-Za-z0-9_\-]+` +
	`|\bAKIA[0-9A-Z]{16}\b` +
	`|\bAIza[0-9A-Za-z\-_]{35}\b` +
	`|\beyJ[A-Za-z0-9_\-]+\.` +
	`|-----BEGIN [A-Z ]*PRIVATE KEY-----`)

// redactCredentials replaces credential-shaped substrings. Finding titles are
// derived from scanned source and can contain real secrets.
func redactCredentials(s string) string {
	return credentialPattern.ReplaceAllString(s, "[REDACTED]")
}

// DeltaTable renders the scan delta as the PR body. Built now even though PR
// creation itself is deferred (see Runner.land's doc comment): it is the
// exact text a later call to pr.CreateGitHubPR / pr.CreateGitLabMR will send
// as the request body, so there is nothing left to design when that call is
// added back.
func DeltaTable(d Delta) string {
	var b strings.Builder
	b.WriteString("## Wolf remediation results\n\n")
	b.WriteString("| Outcome | Count |\n|---|---|\n")
	fmt.Fprintf(&b, "| Fixed | %d |\n", len(d.Fixed))
	fmt.Fprintf(&b, "| Remaining | %d |\n", len(d.Remaining))
	fmt.Fprintf(&b, "| New | %d |\n", len(d.New))

	if d.Regressed() {
		b.WriteString("\n> **Regression:** this branch has more findings than the baseline. Do not merge without review.\n")
	}
	if len(d.Fixed) > 0 {
		b.WriteString("\n### Fixed\n")
		for _, x := range d.Fixed {
			fmt.Fprintf(&b, "- %s\n", redactCredentials(x.Title))
		}
	}
	if len(d.New) > 0 {
		b.WriteString("\n### Newly introduced\n")
		for _, x := range d.New {
			fmt.Fprintf(&b, "- %s\n", redactCredentials(x.Title))
		}
	}
	return b.String()
}

// land pushes the remediation branch and records the scan delta on the
// session's audit trail, and deliberately stops there. PR creation
// (pr.CreateGitHubPR / pr.CreateGitLabMR, with DeltaTable(d) as the body and
// the result stored on sess.PRURL) is out of scope for now — human-directed:
// branch push only, PR creation deferred — so this function is written to
// end at "branch pushed, delta recorded" with that call as the obvious next
// line to add, not a rewrite of this one. A completed session with an empty
// PRURL is therefore the correct, normal outcome of this scope; nothing
// downstream may treat it as an error.
//
// The push itself always targets sess.WorktreePath/sess.BranchName — exactly
// the worktree the driver edited and exactly the branch prepareWorkspace
// created for this session (BranchName(sess.ID)) — via pr.PushBranch, which
// is itself fixed to `git push -u origin <branch>`: never a force push,
// never any remote but origin, never any branch but this session's own.
//
// For a local-source session (the only kind that reaches here today — see
// prepareWorkspace's doc comment on the still-open GitHub gap), that
// worktree sits on the scratch clone cloneLocalForRemediation built, whose
// `origin` remote is the SOURCE repository it cloned from — i.e. the user's
// real, actual repository (see cloneLocalForRemediation's doc comment for
// why `git clone --local` always records that). So this push deliberately
// lands the remediation branch in the user's real repository; that is how
// the branch is meant to reach them, not an accident of the clone's origin
// remote pointing somewhere unexpected.
func (r *Runner) land(ctx context.Context, sess *models.RemediationSession, d Delta) error {
	if err := pr.PushBranch(ctx, sess.WorktreePath, sess.BranchName); err != nil {
		return fmt.Errorf("push branch: %w", err)
	}
	r.recordDelta(ctx, sess.ID, d)
	return nil
}

// landingDelta approximates the scan delta from data already on hand: the
// baseline scan's findings (the same ListFindingsByScan call runPlanPhase
// and runExecutePhase already make), and which of them the approved patches
// claim to address (RemediationPatch.FindingIDs, written by savePatches).
//
// This is not a real rescan of the pushed branch — nothing in this
// subsystem re-runs the scanners yet, and building that is out of scope
// here — so New is always empty on this path: without a live rescan there is
// no way to detect a finding the fix itself introduced, and reporting the
// Fixed/Remaining split from what IS known beats reporting nothing. Because
// New is always empty here, Regressed() (New > Fixed) can never fire from
// this computation — the safe default when the data a real regression check
// would need is not being collected yet.
func (r *Runner) landingDelta(ctx context.Context, sess *models.RemediationSession) (Delta, error) {
	before, ferr := r.store.ListFindingsByScan(ctx, sess.ScanID)
	if ferr != nil {
		return Delta{}, fmt.Errorf("load findings: %w", ferr)
	}
	patches, perr := r.store.ListRemediationPatches(ctx, sess.ID)
	if perr != nil {
		return Delta{}, fmt.Errorf("load patches: %w", perr)
	}

	fixedIDs := make(map[string]struct{})
	for _, p := range patches {
		var ids []string
		// A malformed FindingIDs cell (shouldn't happen — savePatches is the
		// only writer, and it always JSON-encodes) is treated as "addresses
		// nothing" rather than failing a branch that already pushed
		// successfully over a bookkeeping glitch.
		if err := json.Unmarshal([]byte(p.FindingIDs), &ids); err != nil {
			continue
		}
		for _, id := range ids {
			fixedIDs[id] = struct{}{}
		}
	}

	after := make([]models.Finding, 0, len(before))
	for _, finding := range before {
		if _, fixed := fixedIDs[finding.ID]; !fixed {
			after = append(after, finding)
		}
	}
	return ComputeDelta(before, after), nil
}

// recordDelta appends the scan delta to the session's audit trail as a
// landing.delta event. Only counts and the regression verdict are recorded,
// never finding content: Finding.Title is derived from scanned source and
// can carry a real credential (the same hazard DeltaTable's own
// redactCredentials exists for), and an audit event's payload has no
// redaction layer of its own — the simplest way to keep a secret out of it
// is to never put finding text in it at all.
func (r *Runner) recordDelta(ctx context.Context, sessionID string, d Delta) {
	seq := r.lastEventSeq(ctx, sessionID) + 1
	payload, merr := json.Marshal(struct {
		Fixed     int  `json:"fixed"`
		Remaining int  `json:"remaining"`
		New       int  `json:"new"`
		Regressed bool `json:"regressed"`
	}{len(d.Fixed), len(d.Remaining), len(d.New), d.Regressed()})
	if merr != nil {
		wolflog.L().Error().Err(merr).Str("session", sessionID).Int("seq", seq).
			Msg("marshal remediation landing delta payload")
	}
	r.appendEvent(ctx, sessionID, seq, "landing.delta", payload)
}
