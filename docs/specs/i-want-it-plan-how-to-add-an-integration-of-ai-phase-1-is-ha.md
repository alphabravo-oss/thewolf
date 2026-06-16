# Specification: AI Integration — Finding Enrichment & Auto-Remediation Loop

*Finalized: 2026-05-19*

## Overview

Add AI capabilities to thewolf in two delivered phases plus one deferred:

- **Phase 1 — Enrichment:** an `enrich` command that writes AI-ready fix
  prompts into the findings JSON, selectable by a full filter expression.
- **Phase 2 — Auto-remediation loop:** an agentic scan → AI-triage → plan
  → fix → rescan loop that drives a configured AI tool until targeted
  findings clear, a severity threshold is met, or an iteration cap is hit.
- **Phase 3 (deferred) — Plugin system:** config-declared scanner-container
  plugins for paid SAST/DAST tools.

The MVP is **Phase 1 + the full Phase 2 loop**. The plugin system is
specified but explicitly deferred.

## Problem Statement

Today a user manually exports findings, hands the JSON to an AI agent
(e.g. Claude Code), asks it to analyze the codebase, write a granular
remediation plan, execute it, then re-scans with wolf by hand. This is
effective but entirely manual and unaudited. thewolf should perform this
loop itself — flexibly across AI tools — while keeping every run
recorded, reversible, and human-gated.

## Core Principles

**1. wolf owns findings.** wolf's scans are the *sole source* of
findings. The AI never creates, discovers, or adds findings to wolf's
record. wolf is the driving force behind every finding and every
validation. The AI has exactly two jobs:

- **Fix** — attempt to remediate a finding wolf reported.
- **Prove validity** — determine whether a finding is genuine or a
    false positive (e.g. sample data, a test fixture, a deliberate
    anti-pattern) and justify that call.

The AI may notice fix-relevant context along the way, but it is never a
scanner. Every iteration's finding set comes from a fresh wolf scan, and
the wolf rescan — not the AI's own claims — is the authoritative
validator of whether a finding actually cleared.

**2. AI is optional.** thewolf must remain fully functional with no AI
configured. The deterministic path (scan, deterministic enrichment
templates, normal reporting) is the guaranteed baseline; all AI features
are an enhancement layer that degrades cleanly to "unavailable".

## Existing Infrastructure (extend, do not rebuild)

- `internal/ai/` — `Provider` interface; Anthropic, OpenAI, CLI, noop
  providers; `Analyze`/`Score`/`Summarize` methods.
- `internal/fix/engine/` — `SubprocessEngine` interface; ClaudeCode,
  Codex, Custom, Auto engines; `NewEngine()`.
- `internal/fix/planner/` — existing remediation planner.
- `internal/loop/controller/` — scan→fix→rescan loop with
  `MaxIterations`, pause/resume/stop, `OnIteration*` callbacks.
- `internal/prompt/` — prompt templates; `ai_prompt_templates` table with
  collection → global resolution.
- `ai_logs` table + `AILog` model — per-call AI logging.
- Container runner (`internal/plugin/container/`) + SARIF/JSON
  normalization for scanners.
- Findings carry `Fingerprint`, `Category`, `Status` (incl.
  `false_positive`), and enrichment fields (`CodeSnippet`, `FunctionName`,
  `ModuleName`, `FilePurpose`, `DependentsJSON`, `CWEID`, `RuleID`).
- The Loops UI exists but is currently hidden; the backend engine is
  intact.

## Scope

### In Scope

- A deterministic finding-enrichment engine and `enrich` CLI command.
- Optional AI-generated remediation guidance layered on enrichment.
- A hybrid AI tool registry: config-driven CLI agent definitions +
  Go-coded raw-API engines.
- A dedicated, writable, networked per-loop AI runner container.
- The full auto-remediation loop: triage → plan → fix → rescan, with
  iteration memory, stop conditions, determinism manifest, cost ceilings.
- Reviving and updating the Loops UI; a `wolf loop` CLI command; loop
  REST API.
- Config-declared scanner-container plugins for paid tools (deferred
  phase, specified here).

### Out of Scope

- **AI as a finding source.** The AI never adds findings to wolf's
  record — wolf scans are the only source (see Core Principle 1).
- **Bit-reproducible AI output.** Determinism means recorded and
  auditable runs, not identical re-runs.
- **Auto-merging fix branches.** wolf leaves a PR-ready branch; a human
  always merges.
- **HTTP-adapter DAST plugins.** Only container plugins this effort.
- **Egress allowlisting** for the AI runner container — future hardening.
- **Auto-fix on non-git repos.** The loop requires git; scan + enrich
  still work on non-git paths.

## Requirements

### Functional Requirements

- **FR-0:** wolf is the sole source of findings. No AI code path creates,
  discovers, or inserts a finding. The AI may only change a finding's
  `status` (via triage) or its remediation state — never add rows.
- **FR-1:** `enrich` builds a deterministic `ai_fix_prompt` per finding
  with fixed sections: Problem, Location + snippet, Repo context, Task,
  Acceptance criteria.
- **FR-2:** `enrich` accepts a filter expression: `--severity`,
  `--category`, `--tool`, `--exclude-path` (glob), `--ids`.
- **FR-3:** `enrich` writes the `ai_fix_prompt` field back into the
  scan's findings JSON artifact.
- **FR-4:** When an AI provider is configured and the user opts in
  (`--ai`), `enrich` may replace/augment the templated prompt with
  AI-generated guidance; with no provider, the deterministic template is
  always produced.
- **FR-5:** AI tools are registered via a hybrid model — config entries
  (settings table) for CLI agents (command, arg template, cwd, success
  rule); Go engines for raw OpenAI-compatible / Anthropic API endpoints.
- **FR-6:** Raw-API mode prompts the model for a unified diff, applies it
  via `git apply`, and on rejection re-prompts with the apply error,
  retrying up to the per-finding fix budget.
- **FR-7:** Agentic CLI tools receive the whole targeted findings set
  plus a meta-prompt instructing them to analyze, write a granular plan
  with acceptance criteria, and execute until done.
- **FR-8:** The loop runs in a dedicated writable container: repo mounted
  read-write, network enabled, git + AI CLIs installed, API keys injected
  as env.
- **FR-9:** The loop starts from an existing scan: `wolf loop --scan
  <id>`; that scan is iteration 0.
- **FR-10:** Each iteration: AI triage → plan artifact → fix → wolf
  rescan. The wolf rescan + fingerprint comparison is the authoritative
  validator of whether a finding cleared — never the AI's own claim.
- **FR-11:** AI-triaged false positives are set to status
  `false_positive` with the AI's reason and a `triaged_by=ai` tag,
  excluded from success counting, and surfaced for human review. Triage
  changes a finding's status only; it never removes the finding.
- **FR-12:** The meta-prompt has a built-in default, overridable globally
  or per-collection via `ai_prompt_templates`.
- **FR-13:** Iteration 2+ meta-prompts include a summary of prior
  iterations (attempted / fixed / regressed / still failing).
- **FR-14:** The agentic tool's plan is written to a known path and
  persisted as a per-iteration artifact.
- **FR-15:** If the agentic tool makes its own commits they are kept;
  wolf commits any uncommitted remainder. All on branch
  `wolf/fix-<scanid>`.
- **FR-16:** The loop stops on any of: max-iterations reached; zero
  targeted findings remain; a severity threshold met; no progress in an
  iteration; regression detected; configured cost/time ceiling crossed.
- **FR-17:** Regression detection runs a verify command (auto-detected
  from the detected language, overridable via repo config
  `verify_command`); non-zero exit, or increased finding count, is a
  regression — the loop stops and the offending commit(s) may be
  reverted.
- **FR-18:** Each finding has a per-finding fix budget (default 3
  attempts); exhausted findings are marked `ai_unfixable` and skipped.
- **FR-19:** Each iteration writes a run manifest: git SHA before/after,
  scanner image digests, tool set, finding fingerprints in/out, AI tool
  used, plan artifact reference.
- **FR-20:** Every AI invocation's prompt, model, params, raw response,
  and applied patch are persisted (extends `ai_logs` with `loop_id`,
  `iteration`).
- **FR-21:** Loop targets: max-iterations is mandatory; per-invocation
  timeout, total token/$ budget, and overall wall-clock cap are each
  optional and individually configurable; the loop stops at whichever
  triggers first.
- **FR-22:** Surfaces: a `wolf loop` CLI command, a loop REST API
  (`POST /api/loops`, `GET /api/loops/{id}/stream`), and the revived
  Loops UI.
- **FR-23:** Partial failure within an iteration keeps successful
  commits, records failed batches with their error in the manifest,
  rescans, and continues; failed findings retry in later iterations.
- **FR-24 (deferred phase):** Paid scanners are added as config-declared
  container plugins — image, invocation, secret refs, SARIF/JSON output —
  consumed by the existing container runner + normalizer.

### Non-Functional Requirements

- **NFR-1:** With no AI provider configured, all non-AI commands and the
  deterministic enrichment template behave identically to today.
- **NFR-2:** The loop runs in the foreground attached to its initiating
  caller (CLI process or API stream); no background-job queue. A caller
  disconnect stops the loop. Because commits are per-batch/per-tool, a
  disconnect leaves committed work intact and discards at most an
  uncommitted in-progress batch.
- **NFR-3:** Concurrency is unlimited by default; an operator-set maximum
  is configurable via a UI setting (mirrors `scan_concurrency`).
- **NFR-4:** Scanner image digests are pinned for the loop so finding
  deltas are attributable to AI edits, not scanner drift.
- **NFR-5:** The AI runner container is isolated from the host; the host
  is never directly exposed to the agent.
- **NFR-6:** AI cost is always recorded in `ai_logs.cost_usd` regardless
  of whether cost ceilings are configured.

## User Stories

### US-1: Deterministic enrichment template engine

**Description:** As a security engineer, I want each finding turned into a
structured fix prompt assembled from data wolf already has, so I get a
usable AI-ready prompt with no AI call and no cost.

**Acceptance Criteria:**

- [ ] A function builds an `ai_fix_prompt` string with fixed sections:
  Problem, Location + snippet, Repo context, Task, Acceptance criteria.
- [ ] Repo context includes function/module name, file purpose,
  dependents, CWE, and rule remediation text when present on the finding.
- [ ] Output is deterministic — same finding in, same prompt out.
- [ ] Unit test: a finding with all enrichment fields produces every
  section; a finding with missing fields omits those gracefully.
- [ ] `go build ./...` and `go test ./...` pass.

### US-2: `enrich` command with filter expression

**Description:** As a user, I want `wolf enrich` to write fix prompts into
a scan's findings JSON for the findings I select.

**Acceptance Criteria:**

- [ ] `wolf enrich --scan <id>` populates `ai_fix_prompt` on findings in
  the scan's findings JSON artifact.
- [ ] Filters `--severity`, `--category`, `--tool`, `--exclude-path`
  (glob), `--ids` all work and combine (AND semantics).
- [ ] Findings outside the filter are left untouched.
- [ ] Re-running `enrich` is idempotent (overwrites the field cleanly).
- [ ] Test: enriching `--severity critical,high` touches only those rows.
- [ ] `go build ./...` and `go test ./...` pass.

### US-3: Optional AI-generated guidance layer

**Description:** As a user with an AI provider configured, I want
`enrich --ai` to produce richer, model-authored remediation guidance.

**Acceptance Criteria:**

- [ ] `enrich --ai` calls the configured provider per selected finding
  and stores model-authored guidance in `ai_fix_prompt`.
- [ ] With no provider configured, `--ai` fails with a clear message and
  the deterministic template path is unaffected.
- [ ] AI calls are recorded in `ai_logs`.
- [ ] Test: with the noop provider, `--ai` degrades to the template.
- [ ] `go build ./...` and `go test ./...` pass.

### US-4: Hybrid AI tool registry

**Description:** As an operator, I want to add a CLI AI agent by config
without recompiling, while raw-API engines stay in Go code.

**Acceptance Criteria:**

- [ ] CLI tool definitions (name, command, arg template, cwd, success
  rule) are read from the settings store.
- [ ] An unknown/misconfigured tool yields a clear error, not a panic.
- [ ] The existing ClaudeCode/Codex/Custom engines still resolve.
- [ ] Raw OpenAI-compatible and Anthropic API engines are selectable.
- [ ] Test: a config-defined CLI tool is discoverable via the registry.
- [ ] `go build ./...` and `go test ./...` pass.

### US-5: AI runner container

**Description:** As the loop, I need a writable, networked container with
git and the AI CLIs so an agent can edit the repo.

**Acceptance Criteria:**

- [ ] A runner image (Dockerfile) ships git + supported AI CLIs.
- [ ] The loop launches it with the repo bind-mounted read-write, network
  enabled, and API keys injected as env from the secrets store.
- [ ] The container is removed after the loop ends.
- [ ] Verification: a loop invocation can write a file in the repo
  mount and the change is visible on the host path.
- [ ] `go build ./...` passes.

### US-6: Raw-API patch mode

**Description:** As a user pointing the loop at a plain LLM endpoint, I
want wolf to obtain and apply a patch itself.

**Acceptance Criteria:**

- [ ] wolf prompts the model for a unified diff and applies it via
  `git apply`.
- [ ] On `git apply` rejection, wolf re-prompts with the apply error and
  retries up to the per-finding fix budget.
- [ ] Budget exhausted → the batch is marked failed; the loop continues.
- [ ] Test: a deliberately malformed patch triggers a retry, then a
  failed-batch outcome.
- [ ] `go build ./...` and `go test ./...` pass.

### US-7: Loop iteration — triage, plan, fix, rescan

**Description:** As a user, I want one loop iteration to triage findings,
make the AI plan and fix, then rescan with wolf to validate.

**Acceptance Criteria:**

- [ ] An iteration: AI triage → plan artifact written → agentic fix or
  raw-API patch → wolf rescan.
- [ ] The agentic tool receives the whole targeted findings set + the
  meta-prompt.
- [ ] Fingerprint comparison across the wolf rescan classifies each
  finding as fixed / still-present / new — the AI's own claims are not
  trusted for this.
- [ ] Agentic tool commits are preserved; uncommitted remainder is
  committed by wolf on `wolf/fix-<scanid>`.
- [ ] Test: a stubbed AI engine that edits a file yields a committed
  iteration and a rescan delta.
- [ ] `go build ./...` and `go test ./...` pass.

### US-8: Loop stop conditions

**Description:** As a user, I want the loop to end correctly — success,
exhaustion, or guardrail.

**Acceptance Criteria:**

- [ ] Loop stops when targeted findings reach 0 (success).
- [ ] Loop stops when a configured severity threshold is met.
- [ ] Loop stops when an iteration reduces targeted findings by 0
  (no progress).
- [ ] Loop stops (and may revert) on regression — verify command
  non-zero exit, or increased finding count.
- [ ] Verify command auto-detects from language; repo `verify_command`
  overrides it.
- [ ] Per-finding fix budget exhaustion marks a finding `ai_unfixable`.
- [ ] Test: each stop condition is independently exercised with a stub.
- [ ] `go build ./...` and `go test ./...` pass.

### US-9: AI triage of false positives

**Description:** As a user, I want the AI's false-positive calls applied
but reviewable — the AI proving a finding invalid, never deleting it.

**Acceptance Criteria:**

- [ ] An AI-triaged false positive gets status `false_positive`, the AI's
  reason, and a `triaged_by=ai` tag; the finding row is retained.
- [ ] Such findings are excluded from loop success counting.
- [ ] They are listed for human review at loop end.
- [ ] Test: a triaged finding does not block a "reached 0" success exit.
- [ ] `go build ./...` and `go test ./...` pass.

### US-10: Determinism manifest & AI I/O capture

**Description:** As an auditor, I want every loop run fully reconstructable
from records.

**Acceptance Criteria:**

- [ ] Each iteration writes a manifest: git SHA before/after, scanner
  image digests, tool set, fingerprints in/out, AI tool, plan reference.
- [ ] Scanner image digests are pinned across the loop.
- [ ] `ai_logs` is extended with `loop_id` and `iteration`; prompt,
  model, params, raw response, and applied patch are persisted.
- [ ] Iteration 2+ meta-prompts include a prior-iteration summary.
- [ ] Test: a two-iteration loop produces two manifests and linked logs.
- [ ] `go build ./...` and `go test ./...` pass.

### US-11: Cost & time ceilings

**Description:** As an operator, I want optional spend/time caps.

**Acceptance Criteria:**

- [ ] `--max-iterations` is mandatory and enforced.
- [ ] Optional per-invocation timeout kills a hung batch and marks it
  failed.
- [ ] Optional total token/$ budget stops the loop when crossed.
- [ ] Optional overall wall-clock cap stops the loop when crossed.
- [ ] The loop stops at whichever ceiling triggers first; the manifest
  records the stop reason.
- [ ] Test: a tiny $ budget stops a loop early with the correct reason.
- [ ] `go build ./...` and `go test ./...` pass.

### US-12: `wolf loop` CLI + loop REST API

**Description:** As a user, I want to start and watch a loop from the CLI
and API.

**Acceptance Criteria:**

- [ ] `wolf loop --scan <id> [--max-iterations N] [--ai-tool X]
  [--min-severity S] [budget/timeout flags]` runs a loop and streams
  progress.
- [ ] `POST /api/loops` starts a loop; `GET /api/loops/{id}/stream`
  streams iteration events; `GET /api/loops/{id}` returns status.
- [ ] A loop against a non-git repo fails fast with an actionable error.
- [ ] Test: API + CLI both start a loop and receive iteration events.
- [ ] `go build ./...` and `go test ./...` pass.

### US-13: Revive & update the Loops UI

**Description:** As a user, I want to configure, run, and watch a loop in
the browser.

**Acceptance Criteria:**

- [ ] The Loops nav entry, route, and command-palette item are un-hidden.
- [ ] A start-loop form: pick scan, AI tool, max iterations, severity
  target, optional ceilings.
- [ ] A live view shows per-iteration progress, the plan artifact, the
  per-iteration diff, the manifest, and a cost meter.
- [ ] TypeScript typecheck and build pass.
- [ ] Verify in browser: start a loop, watch iterations advance.

### US-14 (deferred): Container-plugin system for paid scanners

**Description:** As an operator, I want to add a paid SAST/DAST scanner by
config — extending wolf's own scanning, still wolf-owned findings.

**Acceptance Criteria:**

- [ ] A plugin is a config entry: name, image, invocation, secret refs,
  output format (SARIF or wolf JSON).
- [ ] The existing container runner executes it; output is normalized
  into the findings table.
- [ ] Secrets are injected from the encrypted secrets store.
- [ ] A missing/invalid plugin config yields a clear error.
- [ ] Test: a stub plugin emitting fixed SARIF produces normalized
  findings.
- [ ] `go build ./...` and `go test ./...` pass.

## Technical Design

### Data Model

- **`ai_logs`** — add `loop_id TEXT`, `iteration INT`, `applied_patch
  TEXT`. Existing `cost_usd` reused for budget accounting.
- **`findings`** — `ai_fix_prompt` field (enrichment output); a
  `triaged_by` marker (`""` | `ai` | `human`) alongside `status`; an
  `ai_unfixable` flag (or a reserved status/marker). No new finding rows
  are ever created by AI code paths.
- **`loops`** / loop-iteration records — extend the existing loop model
  with: iteration manifests, plan-artifact references, stop reason,
  scanner image digests, cost totals.
- **`settings`** — `ai_tools` (CLI tool definitions array), loop defaults
  (max iterations, optional ceilings), `loop_concurrency` max.
- **Secrets store** — provider API keys / scanner tokens (existing,
  encrypted).
- **`ai_prompt_templates`** — reused for the meta-prompt override
  (collection → global).

### API Endpoints

- `POST /api/findings/enrich` (or scan-scoped) — run enrichment with a
  filter payload.
- `POST /api/loops` — start a loop from a scan ID.
- `GET /api/loops/{id}` — loop status + iteration summaries.
- `GET /api/loops/{id}/stream` — SSE iteration events.
- Existing config endpoints manage `ai_tools` and loop defaults.

### Integration Points

- `internal/loop/controller/` — extended with the triage → plan → fix →
  rescan iteration shape, stop conditions, manifests.
- `internal/fix/engine/` — extended with the config-driven CLI registry
  and raw-API patch engine; runs inside the AI runner container.
- `internal/fix/planner/` — reused/extended for the plan artifact.
- `internal/ai/` — providers reused for enrichment guidance and raw-API
  patch generation.
- `internal/plugin/container/` — reused for the AI runner container and
  (deferred) scanner plugins.
- Scanner pinning reuses existing image-digest handling.

### User Flows

**Enrichment:** `wolf scan` → `wolf enrich --scan <id> --severity
critical,high` → findings JSON gains `ai_fix_prompt` → user hands prompts
to any AI agent.

**Auto-fix loop:** `wolf scan` (wolf produces all findings) → review →
`wolf loop --scan <id> --max-iterations 5 --ai-tool claude-code` → per
iteration: AI triages validity → writes a granular plan with acceptance
criteria → executes the plan in the runner container → commits on
`wolf/fix-<scanid>` → **wolf rescan** re-derives findings → repeat until
0 / threshold / cap / guardrail → user reviews the PR-ready branch and
decides. wolf drives every scan; the AI only fixes and proves validity.

### Edge Cases

- No AI provider/tool configured → enrichment still works (template);
  loop refuses with a clear message.
- Non-git repo → scan + enrich work; loop fails fast.
- Agentic tool exits non-zero but made real edits → wolf rescan decides;
  edits are committed and validated.
- Malformed raw-API patch → retry-with-error up to budget, then failed
  batch.
- Caller disconnect mid-loop → loop stops; committed work is intact.
- Regression (broken build or more findings) → loop stops, commits may be
  reverted.
- AI marks a real issue as a false positive → recorded as `triaged_by=ai`
  and surfaced for human override; the finding row is never lost.

## Implementation Phases

### Phase 1: Enrichment foundation

- [ ] US-1 deterministic enrichment template engine
- [ ] US-2 `enrich` command + filter expression + JSON write-back
- [ ] US-3 optional AI-generated guidance layer
- **Verification:** `go build ./... && go test ./internal/...`

### Phase 2: AI tool abstraction & execution environment

- [ ] US-4 hybrid AI tool registry
- [ ] US-5 AI runner container
- [ ] US-6 raw-API patch mode
- **Verification:** `go build ./... && go test ./internal/...`

### Phase 3: The auto-remediation loop

- [ ] US-7 loop iteration (triage, plan, fix, rescan)
- [ ] US-8 loop stop conditions
- [ ] US-9 AI false-positive triage
- [ ] US-10 determinism manifest & AI I/O capture
- [ ] US-11 cost & time ceilings
- **Verification:** `go build ./... && go test ./internal/...`

### Phase 4: Surfaces

- [ ] US-12 `wolf loop` CLI + loop REST API
- [ ] US-13 revive & update the Loops UI
- **Verification:** `go build ./...`, `go test ./internal/...`,
  `cd ui-next && npx tsc --noEmit && npm run build`

### Phase 5 (deferred): Plugin system

- [ ] US-14 container-plugin system for paid scanners
- **Verification:** `go build ./... && go test ./internal/...`

## Definition of Done

This feature is complete (MVP = Phases 1–4) when:

- [ ] All acceptance criteria in US-1 … US-13 pass.
- [ ] Phases 1–4 verified by their verification commands.
- [ ] Tests pass: `go test ./...`
- [ ] Types/lint: `go vet ./internal/...` and
  `cd ui-next && npx tsc --noEmit`
- [ ] Build: `go build ./...` and `cd ui-next && npm run build`
- [ ] With no AI configured, all non-AI behavior is unchanged (NFR-1).
- [ ] No AI code path creates a finding row (FR-0).
- [ ] A loop run produces a PR-ready `wolf/fix-<scanid>` branch, complete
  per-iteration manifests, plan artifacts, and `ai_logs` entries.
- [ ] Loops UI verified in a browser: start a loop, watch iterations.

Phase 5 (plugins) is tracked separately and not required for MVP done.

## Open Questions

- None blocking. Future hardening: egress allowlisting for the AI runner
  container; HTTP-adapter DAST plugins; background-job loop execution if
  foreground/attached proves too limiting.

## Implementation Notes

- Reuse, don't rebuild: `internal/ai`, `internal/fix/*`,
  `internal/loop/controller`, `internal/prompt`, the container runner.
- The Loops UI was hidden earlier; un-hiding is part of US-13.
- "Batched per file/severity + patch-apply" applies specifically to
  raw-API mode; agentic CLI tools self-manage batching and planning.
- Core Principle 1 is load-bearing: any reviewer should confirm no AI
  path inserts into `findings`. AI mutates `status`/`triaged_by` only.
