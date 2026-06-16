# Autonomous Fix Engine — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development or superpowers:executing-plans. Steps use checkbox (`- [ ]`) syntax.

**Design:** [`docs/superpowers/specs/2026-06-15-autonomous-fix-engine-design.md`](../specs/2026-06-15-autonomous-fix-engine-design.md) — read it first for architecture and rationale.

**Goal:** Implement v1 of the autonomous fix engine — dry-run, per-finding, verified, branch-only — behind a master feature flag (`autofix_enabled`, default OFF). Server enqueues durable jobs; a separable `wolf fixer` worker claims and runs them; CLI-first / API-fallback engines; verify-by-result gate; branch + diff artifact for review.

**Feature flag (non-negotiable, woven through every phase):** a single setting `autofix_enabled` (default `false`). When off: the `/fixes` execute path, the worker, and the UI surface are all dark/disabled. Mirrors `fleet_mode` / `ai_enabled` (`internal/api/routes/fleet.go:14`). Every new API endpoint that *executes* a fix checks it and returns `403 autofix_disabled` when off.

**Tech stack:** Go 1.26 (chi, sqlx, os/exec, git, go:embed), SQLite + Postgres, existing SSE broker + scanner backend + scanner-image build subsystem, React 19 + Nocturne UI.

---

## Phasing

Eight phases (from design §12). Phases 1–6 are backend + worker; 7 is UI; 8 is the engine containers + auth flow + docs. Each phase is independently shippable behind the flag.

---

## Phase 1 — Feature flag + job model (foundation)

### Task 1.1: Migration + flag

**Files:** `internal/db/migrations/021_autofix.sql`, `internal/db/sqlite.go`, `internal/db/postgres.go`

- [ ] Create `021_autofix.sql`: `fix_jobs` + `fix_attempts` tables + `INSERT OR IGNORE INTO settings (key,value) VALUES ('autofix_enabled','false')`. Columns per design §5. Common SQL subset (TEXT/INTEGER/TIMESTAMP), `IF NOT EXISTS`.
- [ ] Wire `migration021SQL` into both `sqlite.go` and `postgres.go` Migrate() (and the Postgres `ON CONFLICT` seed for `autofix_enabled`).
- [ ] Verify: `go test ./internal/db/...` migrates cleanly on a fresh `:memory:` store.

### Task 1.2: Models

**Files:** `internal/models/fix_job.go`

- [ ] `FixJob` + `FixAttempt` structs with json/db tags (design §5). Status consts (`FixJobQueued|Claimed|Running|Succeeded|Failed|Cancelled`; attempt outcome `Kept|RolledBack|Unfixable`).

### Task 1.3: Store methods (SQLite + Postgres)

**Files:** `internal/db/store.go`, `internal/db/sqlite_fixjobs.go`, `internal/db/postgres_fixjobs.go`, `internal/db/sqlite_fixjobs_test.go`

- [ ] Interface + impls: `EnqueueFixJob`, `GetFixJobByID`, `ListFixJobs(repoID?)`, `ClaimNextFixJob(workerID)` (atomic `UPDATE … SET status='claimed', claimed_by=? WHERE id=(SELECT id FROM fix_jobs WHERE status='queued' ORDER BY created_at LIMIT 1) RETURNING …` / SQLite equivalent), `UpdateFixJob`, `ReclaimStaleJobs(olderThan)`, `CreateFixAttempt`, `ListFixAttempts(jobID)`.
- [ ] Test: enqueue → claim (returns it, marks claimed) → second claim returns nothing → update → list; attempt round-trip. Concurrency: two claims never return the same job.

### Task 1.4: Flag helper

**Files:** `internal/api/routes/autofix.go`

- [ ] `autofixEnabled(ctx, store) bool` (reads the setting, false on error/absent) — mirrors `fleetModeEnabled`.

Commit per task. `go build ./... && go vet ./... && go test ./internal/db/...` green.

---

## Phase 2 — Writability preflight

**Files:** `internal/fix/writability/writability.go` + `_test.go`; `internal/models/repo.go` (add derived `Fixable`); a `GET /repos/{id}/fixable` route.

- [ ] `Check(ctx, repo, store) (Result{Writable bool, Reason string})`: local (`unix.Access` W_OK + `.git`), GitHub (token secret present + GitHub API push-permission probe + not archived), SSH (node reachable + `git push --dry-run` over the node). Each returns a clear `reason`.
- [ ] `GET /repos/{id}/fixable` (read:repos) returns the result; surfaced on the repo in the UI.
- [ ] Tests with stubbed git/GitHub/SSH probes.

---

## Phase 3 — Engine chain (CLI-first, API-fallback)

**Files:** `internal/fix/engine/api.go`, `internal/fix/engine/chain.go`, `internal/fix/engine/*_test.go`

- [ ] `APIEngine` (Anthropic/OpenAI via `internal/ai`): prompt asks for a unified diff in a fenced block; returns `FixResult{Diff}` for wolf to `git apply` (no in-place edits). Reuses the existing provider abstraction + a `dockerhub_token`-style key from secrets.
- [ ] Auth/availability detection: `claude`/`codex` on PATH **and** a fast auth probe (`claude -p "ok"` with a tiny timeout, or a documented `--check`); cache the result per-process.
- [ ] `Chain`/`SelectEngine(cfg)`: order `auto` → [available CLI] → API. On a verification failure the orchestrator (Phase 5) asks the chain for the next tier.
- [ ] Tests (no real agents): selection logic, diff extraction from a fenced block, that the API path never edits files.

---

## Phase 4 — Worktree + verification gate

**Files:** `internal/fix/workspace/workspace.go` (+test), `internal/fix/verify/verify.go` (+test)

- [ ] `workspace`: prepare a writable working tree on a new fix branch — local → `git worktree add`; github → clone-for-write with the token. `Rollback(file)`, `ChangedFiles()`, `Diff()`, `Cleanup()`. (SSH write-clone deferred to v1.1; preflight already gates it.)
- [ ] `verify.Gate(ctx, ws, finding, scanner)`: (1) files changed, (2) language-aware build (`go build ./...`, `tsc --noEmit`, …; parse-only minimum), (3) **targeted rescan** — re-run only the finding's scanner/rule against the changed file via the scanner backend and confirm the finding is gone, (4) regression diff (no new findings), (5) optional test command. Returns a structured `VerifyResult`.
- [ ] Tests: build-fail → gate fails; finding-still-present → fails; clean → passes (scanner backend stubbed).

---

## Phase 5 — Orchestration

**Files:** `internal/fix/orchestrator/orchestrator.go` (+test)

- [ ] `Run(ctx, job, deps)`: preflight writability (fail fast) → prepare workspace → for each finding (≥ severity floor, not triaged-away): bounded prompt → `engine.Fix` → `verify.Gate` → keep | rollback+escalate(next engine / more context, up to `max_attempts`) | `unfixable`; record a `FixAttempt` each time. Assemble the branch, persist the diff as an artifact, write a summary. **v1: stop — no push/PR.**
- [ ] Budget enforcement (reuse `internal/loop/budget`): iterations, per-fix timeout, wall-clock, cost (API tier).
- [ ] Tests: a 2-finding job with a stubbed engine+verifier — one fix kept, one escalated-then-unfixable; asserts attempts recorded, branch assembled, no push.

---

## Phase 6 — Worker + API + SSE

**Files:** `cmd/wolf/fixer.go` (new `wolf fixer` command), `internal/api/routes/fixes.go` (rework), `internal/api/routes/fix_jobs.go`, `server.go`, `openapi/spec.go`

- [ ] `wolf fixer` command: loop — `ClaimNextFixJob(workerID)` → run the orchestrator → stream logs (write to artifact store + a status channel the server relays) → `UpdateFixJob`. Heartbeat + `ReclaimStaleJobs`. Single binary, runs inside an engine container; `--once` mode for k8s Job-per-task.
- [ ] Rework `POST /fixes`: **enqueue a job** (not the old no-op record) — gated by `autofixEnabled` (403 `autofix_disabled` when off) and `write:fixes`. `mode` defaults to `dry_run`.
- [ ] `GET /fixes/{id}` → job + attempts; `GET /fixes/{id}/stream` → SSE relay of the worker's logs; `GET /fixes/{id}/diff` → the proposed diff artifact; `DELETE /fixes/{id}` → cancel (sets cancelled; worker checks).
- [ ] Catalog in OpenAPI; tests: flag-off → 403; enqueue → job row; claim/stream/diff happy paths with a stubbed orchestrator.

---

## Phase 7 — UI

**Files:** `ui/src/routes/_authed.fixes.*.tsx`, `ui/src/components/fixes/*`, `ui/src/lib/fixes.ts`

- [ ] Gate the Fixes surface on `autofix_enabled` (hide/disable with a hint when off).
- [ ] Repo + finding: a **fixable** indicator (from `/repos/{id}/fixable`) and a **"Fix this finding (dry-run)"** action (disabled with reason when not fixable).
- [ ] Fixes page: job list + status, per-attempt detail (engine used, verify outcomes), the **proposed-diff viewer**, and a **live log console** (reuse the SSE `BuildConsole` from the scanner-image work).
- [ ] Settings → General: an `autofix_enabled` toggle (the master switch), default off.
- [ ] `pnpm typecheck` + `build` green.

---

## Phase 8 — Engine containers + auth flow + docs

**Files:** `fixer/Dockerfile.claude`, `fixer/Dockerfile.codex`, `fixer/Dockerfile.api`, `internal/scannerbuild` (extend variant table or a parallel `fixerbuild`), `README.md`, design doc cross-link

- [ ] Three Dockerfiles: a shared base (reuse the scanner Debian base) + git + `gh`/`glab` + language build tools; `claude`/`codex` CLI in the respective variants; `api` variant CLI-free. Built/pushed via the existing scanner-image build subsystem (add the fixer variants to its table or mirror it).
- [ ] Document the **auth-then-ready** flow (`docker exec -it … claude login`, session on a mounted volume / PVC) and the k8s shape (Deployment + PVC, or Job-per-task with `wolf fixer --once`).
- [ ] README: a "Autonomous remediation" section — flag, dry-run-first, the verify gate, the worker, the containers.

---

## Definition of Done

1. `autofix_enabled` defaults **off**; with it off, `POST /fixes` execute returns `403 autofix_disabled`, the worker processes nothing, and the UI surface is disabled. Flipping it on (Settings or `wolf settings set`) enables the path.
2. A `fix_jobs`/`fix_attempts` durable queue with atomic claim that two workers never double-claim.
3. `wolf fixer` claims a queued job, runs the orchestrator, and updates status; `--once` exits after one job (k8s Job-friendly).
4. Engine chain selects CLI when available+authed, falls back to API otherwise; the API path returns a diff wolf applies; **no engine's self-report is used to decide success.**
5. The verification gate rolls back any fix that doesn't build, doesn't clear the finding (targeted rescan), or introduces regressions.
6. A dry-run job over a real local repo with a seeded finding produces a **fix branch + diff artifact + summary**, opens **no PR**, and leaves the user's working tree untouched.
7. Writability preflight returns an accurate `fixable` verdict for local + GitHub (+ an SSH check), surfaced in the UI.
8. `go vet ./...`, `go test ./...`, `pnpm typecheck/build/test` all green.

---

## Risks (from design §13)

Agent rogue-fix → per-finding scope + verify gate + rollback. CLI auth in worker → versioned containers + persisted session + API fallback. Queue correctness → atomic claim + heartbeat + stale-reclaim. Build/verify cost → targeted single-rule rescans + budgets. **All of it dark until `autofix_enabled` is on.**
