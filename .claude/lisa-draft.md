# Specification Draft: i want it plan how to add an integration of ai. phase 1 is having commands to enrich the json or individual findings (should be able to set by finding type... like maybe only critical and high) that tells teh user waht to pass to an ai agent to fix the issue. phase 2 is leveraging AI tools of varying kinds (so this needs to be flexible) wether its passing it to claude code or codex or antigravity/gemini or opencode or cursor agent, etc, and potentially some arbitrary other systems or directly to openai api compatible or anthropic endpoint and having the ai agents automatically work to remediate issues, scan again, and attempt to fix again. we should be able t to set the number of loops it should run on this auto loop. waht else would make this a deterministic scan, ai scan, ai fix loop. we may also want to support paid sast and dast and other types of tools via a plugin system. lets talk about this

*Interview in progress - Started: 2026-05-19*

## Overview
[To be filled during interview]

## Problem Statement
[To be filled during interview]

## Scope

### In Scope
<!-- Explicit list of what IS included in this implementation -->
- [To be filled during interview]

### Out of Scope
<!-- Explicit list of what is NOT included - future work, won't fix, etc. -->
- [To be filled during interview]

## User Stories

<!--
IMPORTANT: Each story must be small enough to complete in ONE focused coding session.
If a story is too large, break it into smaller stories.

Format each story with VERIFIABLE acceptance criteria:

### US-1: [Story Title]
**Description:** As a [user type], I want [action] so that [benefit].

**Acceptance Criteria:**
- [ ] [Specific, verifiable criterion - e.g., "API returns 200 for valid input"]
- [ ] [Another verifiable criterion - e.g., "Error message displayed for invalid email"]
- [ ] Typecheck/lint passes
- [ ] [If UI] Verify in browser

BAD criteria (too vague): "Works correctly", "Is fast", "Handles errors"
GOOD criteria: "Response time < 200ms", "Returns 404 for missing resource", "Form shows inline validation"
-->

[To be filled during interview]

## Technical Design

### Data Model
[To be filled during interview]

### API Endpoints
[To be filled during interview]

### Integration Points
[To be filled during interview]

## User Experience

### User Flows
[To be filled during interview]

### Edge Cases
[To be filled during interview]

## Requirements

### Functional Requirements
<!--
Use FR-IDs for each requirement:
- FR-1: [Requirement description]
- FR-2: [Requirement description]
-->
[To be filled during interview]

### Non-Functional Requirements
<!--
Performance, security, scalability requirements:
- NFR-1: [Requirement - e.g., "Response time < 500ms for 95th percentile"]
- NFR-2: [Requirement - e.g., "Support 100 concurrent users"]
-->
[To be filled during interview]

## Implementation Phases

<!-- Break work into 2-4 incremental milestones Ralph can complete one at a time -->

### Phase 1: [Foundation/Setup]
- [ ] [Task 1]
- [ ] [Task 2]
- **Verification:** `[command to verify phase 1]`

### Phase 2: [Core Implementation]
- [ ] [Task 1]
- [ ] [Task 2]
- **Verification:** `[command to verify phase 2]`

### Phase 3: [Integration/Polish]
- [ ] [Task 1]
- [ ] [Task 2]
- **Verification:** `[command to verify phase 3]`

<!-- Add Phase 4 if needed for complex features -->

## Definition of Done

This feature is complete when:
- [ ] All acceptance criteria in user stories pass
- [ ] All implementation phases verified
- [ ] Tests pass: `[verification command]`
- [ ] Types/lint check: `[verification command]`
- [ ] Build succeeds: `[verification command]`

## Ralph Loop Command

<!-- Generated at finalization with phases and escape hatch -->

```bash
/ralph-loop "Implement i want it plan how to add an integration of ai. phase 1 is having commands to enrich the json or individual findings (should be able to set by finding type... like maybe only critical and high) that tells teh user waht to pass to an ai agent to fix the issue. phase 2 is leveraging AI tools of varying kinds (so this needs to be flexible) wether its passing it to claude code or codex or antigravity/gemini or opencode or cursor agent, etc, and potentially some arbitrary other systems or directly to openai api compatible or anthropic endpoint and having the ai agents automatically work to remediate issues, scan again, and attempt to fix again. we should be able t to set the number of loops it should run on this auto loop. waht else would make this a deterministic scan, ai scan, ai fix loop. we may also want to support paid sast and dast and other types of tools via a plugin system. lets talk about this per spec at docs/specs/i-want-it-plan-how-to-add-an-integration-of-ai-phase-1-is-ha.md

PHASES:
1. [Phase 1 name]: [tasks] - verify with [command]
2. [Phase 2 name]: [tasks] - verify with [command]
3. [Phase 3 name]: [tasks] - verify with [command]

VERIFICATION (run after each phase):
- [test command]
- [lint/typecheck command]
- [build command]

ESCAPE HATCH: After 20 iterations without progress:
- Document what's blocking in the spec file under 'Implementation Notes'
- List approaches attempted
- Stop and ask for human guidance

Output <promise>COMPLETE</promise> when all phases pass verification." --max-iterations 30 --completion-promise "COMPLETE"
```

## Open Questions
- (round 2) Phase-2 AI tool registration model; CLI-agent vs API-endpoint
  execution; loop stop conditions; determinism guarantees.
- (later) Plugin system shape for paid SAST/DAST.

## Implementation Notes
[To be filled during interview]

---
## INTERVIEW NOTES (accumulating)

### Existing infrastructure — DO NOT rebuild
- `internal/ai/`: Provider interface; Anthropic, OpenAI, CLI, noop providers.
- `internal/fix/engine/`: SubprocessEngine interface; ClaudeCode, Codex,
  Custom, Auto engines; NewEngine().
- `internal/loop/controller/`: scan→fix→rescan loop, MaxIterations,
  pause/resume/stop, OnIteration callbacks.
- `internal/prompt/`: prompt templates.
- Fixes/Loops UI hidden earlier this session; backend engine intact.

### Core principle
AI is OPTIONAL. wolf must remain fully functional with NO AI configured.
The deterministic path is the guaranteed baseline; AI is an enhancement.

### Round 1 answers
- **Enrichment generation:** deterministic template is the baseline (free,
  reproducible, works with no AI). AI-generated guidance is an optional
  layer when a provider is configured and the user opts in.
- **Enrich output:** writes a field (e.g. `ai_fix_prompt`) back into the
  findings JSON artifact.
- **Enrich scope:** full filter expression — `--severity`, `--category`,
  `--tool`, `--exclude-path` glob, `--ids`.

### Round 2 answers
- **AI tool registration:** HYBRID. Config-driven definitions for CLI
  agents (cursor-agent, opencode, antigravity, codex, claude code) —
  command, arg template, cwd, result interpretation. Go code for tools
  needing bespoke logic (raw OpenAI-compatible / Anthropic API).
- **API vs CLI execution:** support BOTH. Raw API endpoints: wolf prompts
  for a unified diff and applies it (git apply + validate, retry on
  failure). CLI agents edit repo files themselves. Loop treats them
  uniformly via a "did the repo change?" check.
- **Loop early-stop conditions (chosen):**
  - No progress: a fix+rescan cycle that reduces targeted findings by 0.
  - Regression guardrail: stop (and optionally revert) if an iteration
    increases findings or breaks the build/tests.
  - Per-finding fix budget: cap attempts per finding (e.g. 3); mark
    `ai_unfixable` and skip so the loop continues.
  - (implicit success exit: zero targeted findings remain.)

### Round 3 answers
- **Git strategy:** one commit per iteration on a `wolf/fix-<scanid>`
  branch; branch is PR-ready when the loop ends. Revert = drop commit.
- **Build/test verification:** auto-detect a verify command from the
  detected language (go build, npm run build, ...); repo config can
  override with an explicit `verify_command`. Non-zero exit = regression.
- **Determinism (all in scope):**
  - Per-iteration run manifest (git SHA before/after, scanner image
    digests, tool set, finding fingerprints in/out, AI tool used).
  - Pinned scanner image digests so finding deltas are attributable to
    AI edits, not scanner drift.
  - Full AI I/O capture — prompt, model, params, raw response, applied
    patch per iteration (extends ai_logs).
  - Stable finding fingerprints across rescans to track fixed/moved/new
    and drive the per-finding fix budget.

### Round 4 answers
- **Plugin contract:** BOTH, phased. MVP = config-declared scanner
  containers emitting SARIF/JSON (reuse existing container runner +
  normalizer). Later = HTTP adapter shape for API-only DAST services.
- **Loop surface:** CLI command + REST API + revive the (currently
  hidden) Loops UI — start loop, watch iterations, view per-iteration
  diff + manifest.
- **AI config storage:** reuse existing stores — encrypted secrets store
  for API keys/tokens, settings table for CLI tool definitions and loop
  defaults. No new storage. Managed via existing config endpoints.

### Round 5 answers
- **MVP line:** first release = Phase 1 enrichment + the FULL Phase-2
  auto-fix loop. Paid-tool plugin system is the only deferred piece.
- **Out of scope (explicitly not built):**
  - Bit-reproducible AI output (determinism = recorded/auditable, not
    identical re-runs).
  - Auto-merging fix branches (wolf leaves a PR-ready branch; a human
    always merges).
  - HTTP-adapter DAST plugins (only container plugins this effort).
- **Enrichment prompt context:** rich by default — includes code snippet,
  function/module, file purpose, dependents graph, CWE, rule remediation
  docs (reuses wolf's existing finding enrichment data).

### Round 6 answers
- **Non-git repos:** the auto-fix loop REQUIRES git and fails fast with an
  actionable precondition error ("not a git repo — run git init / add as
  a git source"). Scan + enrich still work on non-git local paths.
- **Fix granularity:** batched per file (default) or per severity — one AI
  invocation per batch, one commit per batch. An iteration contains
  multiple batch-commits. Reconciles the round-3 git answer: fix branch
  `wolf/fix-<scanid>`, one commit PER BATCH; revert granularity is
  per-batch-commit; per-iteration manifest still bounds the set.

### Round 7 answers
- **Loop execution model:** FOREGROUND, attached to the initiating caller
  (CLI process or API stream). No background-job queue / no resumability.
  If the caller disconnects, the loop stops. Mitigation: because commits
  are per-batch, a disconnect leaves completed batch-commits intact and
  at worst discards an uncommitted in-progress batch — no half-applied
  commit. The Loops UI watches via a streaming (SSE) connection that is
  itself the loop's lifeline.
- **Concurrency:** unlimited by default; operator can set a max via a UI
  setting (mirrors the existing scan_concurrency setting).
- **Cost/time ceilings:** all are SUPPORTED and individually configurable
  (optional). max-iterations is the only mandatory bound. Optional,
  default-unset: per-invocation wall-clock timeout (kills a hung batch,
  marks it failed); total token/dollar budget per loop (from ai_logs
  cost_usd); overall loop wall-clock cap. Loop stops at whichever
  ceiling triggers first. Costs are always recorded regardless.

### Round 8 answers
- **AI-tool success signal:** the RESCAN is the authoritative validator.
  The AI tool does its own work (and may run its own tests); wolf does
  NOT trust the tool's own success claim. Flow: a fix invocation produces
  a repo diff = "an attempt was made" (exit code advisory only); the
  subsequent wolf rescan + finding-fingerprint comparison determines
  whether the finding actually went away. Fixed = fingerprint gone after
  rescan.
- **Malformed patch (raw-API mode):** on `git apply` rejection, re-prompt
  the model with the apply error for a corrected patch, retrying up to
  the per-finding fix budget; budget exhausted → batch marked failed.
- **Partial failure within an iteration:** keep successful batch-commits,
  record failed batches (with error) in the manifest, rescan, and
  continue. Failed findings are retried in subsequent iterations until
  their per-finding budget is spent, then marked `ai_unfixable`.

### Round 9 — canonical loop flow (user clarification)
The authoritative end-to-end flow:

1. `wolf scan` → findings JSON.
2. AI reads the findings JSON and **triages**: identifies which findings
   are valid vs false positives. Invalid ones are marked `false_positive`
   (status already in the model) with a reason and do NOT block success.
3. AI **writes a remediation plan** (note: `internal/fix/planner` already
   exists — reuse/extend it).
4. AI **fixes according to the plan** and does its own validation (may
   run its own tests).
5. Back to `wolf scan` (rescan) — wolf's rescan is the authoritative
   validator of whether findings actually cleared.
6. AI takes the new findings and tries again.
7. **Loop exit:** reaches 0 targeted findings, OR a user-set severity
   threshold is met (e.g. "no critical/high remaining"), OR N iterations
   are exhausted — followed by a final `wolf scan`.
8. The user reviews the result and **decides what to do** (review the
   PR-ready branch, merge, discard). wolf never auto-merges.

Implications folded into the design:
- Triage is a first-class loop step; AI may set finding status to
  `false_positive`. Success counting ignores AI-triaged false positives.
- Plan step is explicit and persisted per iteration (auditable).
- "Targeted findings" filter doubles as the success threshold — target
  critical+high and reaching 0 of those is success even if lows remain.

### Round 10 — the AI step models the user's current manual workflow
User's exact current workflow (what the built-in AI should replicate):
"Take the findings JSON, give it to Claude Code, tell it to analyze the
codebase, look at the findings, write a detailed task-driven granular
plan with acceptance criteria, then execute that plan until done. Then
rescan with wolf."

This REFRAMES the per-iteration AI step and reconciles earlier rounds:
- **Agentic CLI tools (primary mode):** wolf hands the tool the WHOLE
  targeted findings set plus a meta-prompt instructing it to analyze,
  write a granular plan with acceptance criteria, and execute until done.
  The AI tool does its own task breakdown / batching / self-validation.
  wolf does NOT pre-batch for agentic tools. One handoff per iteration.
- **Raw-API mode (fallback):** a plain LLM endpoint cannot autonomously
  plan-and-execute, so wolf drives it — THIS is where the round-6
  "batched per file/severity + patch-apply + retry" mechanics apply.
- So "batched per file/severity" (round 6) is specifically the raw-API
  execution strategy; agentic tools self-manage.
- The AI's plan should be captured/persisted per iteration as an
  artifact (auditable; part of the determinism manifest).

### Round 11 answers
- **Agentic commit handling:** if the agentic tool makes its own commits,
  wolf keeps them (agent commit history preserved); wolf commits any
  remaining uncommitted edits as a remainder commit. All on the
  `wolf/fix-<scanid>` branch.
- **Plan artifact:** REQUIRED. The meta-prompt instructs the agent to
  write its plan (granular tasks + acceptance criteria) to a known path
  (e.g. `.wolf/plan-iterN.md`); wolf persists it with the iteration
  manifest and shows it in the Loops UI.
- **AI false-positive triage:** trusted but flagged. AI-triaged FPs →
  status `false_positive`, with the AI's reason and a `triaged_by=ai`
  tag; excluded from loop success counting; surfaced for human
  review/override at the end.

### Round 12 answers
- **Meta-prompt:** wolf ships a strong built-in default; overridable
  globally or per-collection via the existing `ai_prompt_templates`
  resolution (collection → global), same model as scan prompts.
- **Phase-1 enrichment template:** FIXED, deterministic structure. Fixed
  section layout: Problem / Location + snippet / Repo context / Task /
  Acceptance criteria. No per-config template management for enrichment.
- **Iteration memory:** iteration 2+ meta-prompts include a summary of
  prior iterations — what was attempted, fixed, regressed, still failing
  — so the AI avoids repeating dead ends. (Pairs with full AI I/O capture
  and the per-iteration plan artifacts.)

### Round 13 answers
- **AI execution environment:** a dedicated WRITABLE container per loop.
  Repo bind-mounted read-write, network enabled, git + the AI CLI tools
  installed, API keys injected as env. wolf builds/maintains this runner
  image — separate from (and more privileged than) the locked-down
  read-only scanner containers. The host is never directly exposed to
  the agent.
- **Loop start:** `wolf loop --scan <id>` — a loop operates on an
  existing completed scan as iteration 0, then fix → rescan from there.
  (User scans first, reviews, then decides to loop.)
- **Network for the AI runner:** unrestricted — the agent can reach LLM
  APIs and package registries (npm/pypi/crates) so fixes that need
  dependency changes work. Egress allowlisting is explicitly a future
  hardening item, NOT this effort.

---
*Interview notes will be accumulated below as the interview progresses*
---

