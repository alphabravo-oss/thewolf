# Autonomous Fix & Remediation Engine — Design Spec

- **Date:** 2026-06-15
- **Status:** Design for review (not yet a task plan)
- **Author:** brainstorming session

> This is the validated **design**. Once approved, it expands into a phased
> task-by-task implementation plan (writing-plans) we can execute.

---

## 1. Goal

Reliably automate the loop you run by hand today — **scan → export → hand to an
interactive claude/codex session → fix → rescan** — as an unattended,
verifiable pipeline. Prefer **agent CLIs** (claude/codex/gh) because they bill
against a **subscription**, fall back to the **direct API** when the CLI path
isn't available or doesn't work, run the work on a **separate fixer worker**
(designed for Kubernetes later), and finish v1 by producing a **reviewable fix
branch — no auto-PR yet**.

## 2. The reliability problem (why this is hard)

Your interactive loop works because a human is steering. Headless, agents are
unreliable in specific, predictable ways:

- They claim success while deleting code, suppressing the rule, or breaking the build.
- Every headless mode emits different output (`claude -p --output-format json` ≠ `codex --approval-mode full-auto` ≠ custom) — parsing their self-reports to judge success is a trap.
- Given a whole repo + "fix these 40 findings," they go rogue and refactor half the codebase.
- Subscription CLIs use a **logged-in session** that must exist wherever the worker runs, and have **usage limits** — when exhausted you want the API.

## 3. Design principles (the load-bearing decisions)

1. **Judge by the diff, not the transcript.** Never use an agent's claims to decide success. Treat *every* engine — CLI or API — as an untrusted worker that "makes changes in this worktree," then verify by what landed on disk. This makes the system engine-agnostic and dissolves the "every headless mode differs" problem (their output is used only for logs and the PR body).
2. **Per-finding scope, isolated worktree.** One finding (or a tight same-site cluster) per attempt, in a dedicated git worktree on a fix branch, with a bounded prompt. Small scope → higher success, trivial rollback, reviewable output.
3. **CLI-first, API-fallback.** A provider chain: agent CLI preferred (subscription), direct API when the CLI is missing/unauthed/rate-limited/repeatedly-failing.
4. **Worker-based, queue-driven, k8s-ready.** The server enqueues durable jobs; a separable **fixer worker** claims and runs them. No in-process goroutines — so it survives restarts and scales horizontally.
5. **v1 is dry-run, branch-only, verified.** Produce a verified fix branch + diff for human review. Auto-PR and remote-source-fixing are fast-follows once the verification engine is proven.

## 4. Architecture

```
┌──────────────┐   enqueue job    ┌──────────────────┐   claim job    ┌────────────────────────┐
│  wolf server │ ───────────────▶ │  fix_jobs (DB)   │ ◀───────────── │  wolf fixer (worker)   │
│  API + UI    │                  │  durable queue   │                │  runs IN an engine     │
│  SSE relay   │ ◀─ status/logs ─ │                  │ ── status ───▶ │  container (authed CLI)│
└──────────────┘                  └──────────────────┘                └────────────────────────┘
       ▲                                                                         │
       │ proposed diff (artifact) + summary                                      │ worktree · engine · verify
       └─────────────────────────────────────────────────────────────────────────┘
```

- **wolf server** — owns the API, persists jobs, relays the worker's streamed logs to the UI over SSE, stores the proposed diff as an artifact. **Runs no agents.**
- **wolf fixer worker** (`wolf fixer` command) — a stateless process that atomically claims queued jobs, runs the orchestration, streams logs + status back, and exits. One or many; in k8s it's a Deployment (or Job-per-task) scaled to the queue. Runs **inside an engine container**.
- **Engine containers** — `wolf-fixer-claude`, `wolf-fixer-codex`, `wolf-fixer-api`, each with git + `gh`/`glab` + language build tools + the relevant agent CLI, **independently versioned and built/pushed through the scanner-image build subsystem we already have**. The CLI variants require a **one-time interactive auth** (`claude login` / `codex login`); the session is persisted on a mounted volume (a PVC in k8s) so pods/restarts stay "ready." The `api` variant needs no login — just a key from the secret store — so it's the zero-auth fallback for environments where you can't provision a CLI session.

## 5. Data model

```
fix_jobs
  id, type (fix|loop), repo_id, branch (target), source_kind, mode (dry_run)
  engine (auto|claude-code|codex|api|custom), finding_ids[] | scan_id
  severity_floor, max_attempts, budget (iterations/timeout/cost)
  status (queued|claimed|running|succeeded|failed|cancelled)
  claimed_by (worker id), claimed_at, started_at, finished_at
  result_branch, diff_artifact_id, summary_json, error

fix_attempts                       -- one per finding-attempt, the audit trail
  id, job_id, finding_id, attempt_no, engine_used (cli|api), model
  verify (built, finding_cleared, new_findings, tests), outcome (kept|rolled_back|unfixable)
  files_changed[], diff_excerpt, duration_ms, cost_usd, created_at
```

Repos gain a derived **fixable** status: `{ writable: bool, reason: string }`.

## 6. The engine chain

Extend the existing `internal/fix/engine` (`SubprocessEngine`: ClaudeCode / Codex / Custom — they edit files in place) with:

- **`APIEngine`** (Anthropic/OpenAI) — the API can't touch the filesystem, so it is prompted to return a **unified diff in a fenced block**, which *wolf* applies (`git apply`). Different mechanics, identical contract: both produce "a set of file changes wolf then verifies."
- **Auth/availability detection** — `claude`/`codex` present **and** a fast auth probe; otherwise skip to the next tier.
- **`auto` selector** — CLI-first; fall back to API on unavailability or after repeated verification failures. Per-job overridable.

## 7. The verification gate (the heart)

After every attempt, the worker independently checks and **rolls back on any failure**:

1. **Files changed?** non-empty git diff.
2. **Still builds?** language-aware (`go build`, `tsc --noEmit`, etc.); at minimum a parse check.
3. **Finding actually gone?** re-run *only that scanner/rule against that file* (targeted rescan — reuses the scanner backend), not the whole suite.
4. **No new findings?** rescan diff (the regression guard the tracker already does).
5. **Tests** (optional, if a test command is configured).

Pass → keep on the branch. Fail → roll back that finding, then **escalate**: retry with more context / a stronger model / the other engine, up to `max_attempts`, then mark `unfixable`. This attempt→verify→keep-or-rollback→escalate cycle, per finding, is what makes a flaky agent dependable.

## 8. Writability preflight

Before a job runs (and surfaced as the repo's **fixable** indicator in the UI):

- **Local** — `os.Access(path, W_OK)` + the path is a git work tree.
- **GitHub** — a `github_token` secret exists, the repo isn't archived, and the token can push (probe the GitHub API for push permission; note branch protection on the target).
- **SSH** — the node is reachable, the remote path is writable, and a push to the remote succeeds (dry-run `git push --dry-run`).

Surface as `fixable: yes | no (reason)` so the UI can disable the Fix action with a clear hint instead of failing mid-job.

## 9. Orchestration (per job)

```
preflight writability → fail fast with reason if not fixable
prepare workspace:
  local  → git worktree on a new fix branch
  github → clone-for-write with the token, new branch
  ssh    → (v1.1) writable clone on the node, new branch
for each finding (severity ≥ floor, not triaged-away):
  bounded prompt (finding + file + surrounding code + fix-strategy hint)
  engine.Fix  → CLI edits in place, or API returns a diff wolf applies
  verify gate → built? finding cleared? no regressions?
  keep on branch  OR  rollback + escalate(max_attempts)  OR  mark unfixable
assemble branch → persist the diff as an artifact + a summary
v1: STOP here (branch-only, no push/PR). Emit the branch + diff for review.
```

## 10. Product surfaces

- **One-shot fix** — `POST /fixes` finally *executes*: fix one finding (or a selection), verify, produce a branch. The missing executor, delivered.
- **Loop** — iterate scan → fix-batch → rescan until **clean, budget-exhausted, or the quality gate passes** (exit tied to the gate, not just zero findings).
- **Priority gates** — only spend fixes on findings ≥ a severity floor or those failing a gate.
- **Dry-run / propose** (the v1 default) — branch + diff, no push/PR — your current manual flow, automated and *verified*.
- **PR (v1.1)** — `gh`/`glab` push + PR, body summarizing what was fixed **and the verification evidence** (built ✓, finding gone ✓, no regressions ✓).

## 11. v1 scope (this milestone)

**In:** durable job queue; the `wolf fixer` worker; writability preflight (local/GitHub/SSH check); engine chain (CLI-first claude/codex + API-fallback, auth detection); per-finding isolated worktree; the full verification gate; orchestration with escalation; **branch-only, dry-run** output + diff artifact; API enqueue + SSE log relay; a Fixes UI showing fixable status, a "Fix this finding (dry-run)" action, job/attempt status, the proposed diff, and live logs; the three engine-container Dockerfiles + the auth-then-ready flow + docs.

**Writable sources in v1:** **local + GitHub** (clone-for-write is well-trodden). The writability *check* covers SSH too, but SSH *fixing* (write-clone + push back over the node) is **v1.1**.

**Out (fast-follows):** auto-PR, SSH-source fixing, multi-runner autoscaling tuning, fix-budget cost dashboards.

## 12. Phasing (becomes the task plan)

1. **Job model + worker skeleton** — `fix_jobs` / `fix_attempts` tables (SQLite+Postgres), atomic claim, `wolf fixer` command that claims → no-op → completes. Proves the queue + dispatch seam.
2. **Writability preflight** — `internal/fix/writability` for local/GitHub/SSH + a repo `fixable` field surfaced via the API/UI.
3. **Engine chain** — `APIEngine` (diff-returning + `git apply`), auth/availability detection, `auto` CLI→API selector. Unit-tested with no real agents.
4. **Worktree + verification gate** — worktree/clone-for-write, language-aware build check, targeted single-rule rescan, regression diff, rollback.
5. **Orchestration** — per-finding attempt→verify→keep/rollback→escalate; assemble branch; persist diff artifact + summary (branch-only).
6. **API + dispatch + SSE** — `/fixes` (and a gated `/loops`) enqueue jobs; worker claims; server relays streamed logs; status endpoints.
7. **UI** — Fixes page: fixable indicator, dry-run Fix action, job/attempt status, proposed-diff viewer, live build console (reuse the SSE console from the scanner-image work).
8. **Engine containers + auth flow + docs** — `wolf-fixer-{claude,codex,api}` Dockerfiles built/pushed via the existing scanner-image subsystem; document the `claude login` → "ready" flow and the k8s shape (Deployment + PVC for the session, or Job-per-task).

## 13. Risks & mitigations

- **Agent goes rogue / bad fix.** → per-finding scope + the verification gate + rollback. The gate is the whole point.
- **CLI auth in a worker/k8s.** → bake CLIs into versioned containers; persist the session on a volume/PVC; provide the `api` engine as the zero-auth fallback.
- **Writable-repo assumptions.** → explicit preflight + a `fixable` indicator; never start a job that can't push.
- **Build/verify cost & time.** → targeted single-rule rescans (not full suites) + budgets (timeout, iterations, cost); concurrency-capped.
- **Queue correctness in k8s.** → atomic `UPDATE … WHERE status='queued'` claim + heartbeats + reclaim of stale `claimed` jobs.
- **Engine output drift.** → we don't parse it for success; only logs/PR body. Insulated by design.

## 14. Open questions for review

1. **Queue transport** — a DB-polling `fix_jobs` table (simplest, works on SQLite + Postgres, k8s-friendly enough) vs. a real broker (NATS/Redis) later? Recommend **DB table for v1**, broker only if scale demands.
2. **One PR/branch per job, or per finding?** v1 is branch-only; when PRs land (v1.1), default to **one branch per job** with per-finding commits (reviewable units in one PR).
3. **Loop exit condition** — "no findings" vs "**quality gate passes**." Recommend gate-driven as the headline; "clean" as a special case.
4. **Engine container base** — extend the existing scanner Debian base, or a leaner dedicated base? Recommend reuse for consistency + the build/push tooling we already have.
