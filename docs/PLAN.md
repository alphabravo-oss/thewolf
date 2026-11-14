# Wolf — current plan

Updated: 2026-11-14. Status: executing. This is the **only active plan** for the Wolf workspace (`thewolf/`, `wolf-enterprise/`, `wolf-site/`). Everything still to do is in this file; everything else is history.

The [PRD](../../wolf-update.md) is the requirements and edition-boundary authority. The commercial boundary and Enterprise roadmap live in the private `wolf-enterprise/docs/EDITION_BOUNDARY.md`. Superseded plans are archived and are not instructions:

- `docs/archive/2026-11-14/` — the 2026-10-24 plan (`PLAN-2026-10-24.md`, with the acceptance criteria and evidence log for every task ID below), the UI design plan, and the AI-integration drafts.
- `docs/archive/2026-10-23/` — the May–September plans.

## 1. How to work this plan

- Take the earliest unblocked item in **§3 Next up**, then the workstreams in §4. Add a new defect or task under the workstream it belongs to, with reproduction and acceptance.
- Task IDs are stable. `wolf-site/src/content/claims.yaml` cites some of them (F06, F07, F15, O02–O04, X05–X07), so do not renumber. IDs from the old plan keep their old acceptance text in the archive.
- Check a task only when its acceptance is met. Put one line of evidence on the task: date, command or PR, result. Implementation, deployment and production qualification are separate tasks.
- **Definition of done for Community** is the PRD §9.16 ease journey plus the daily journeys, passing as live Playwright e2e against a real `wolf serve`, the real scanner image and PostgreSQL (`scripts/e2e-live.sh`). Mocked specs are fast regressions, not acceptance. Verify per batch; run the full uncached race suite once before landing a release.
- Follow `AGENTS.md`: reuse before writing, fix the shared cause, no new dependency without need, one runnable check for non-trivial logic.
- Merges, deploys and deletes of shared state need the owner's explicit approval.

## 2. Where we are (2026-11-14)

- **Live** (owner's host, Compose): Wolf Enterprise `d91f2b1` on Community core `2b2056c`, with `postgres`, `wolf`, `scan-worker` and `fixer` healthy. PostgreSQL only. Open findings at last check: 12 critical, 212 high.
- **Community** is Apache-2.0 with no evaluation caps; Enterprise differs by capability and operating support. The newest tag and GitHub release is **v0.2.2 (2026-08-11)**, so none of the work below is in a release yet.
- **Shipped since the 10-24 plan:** Home dashboard with seven drill-down charts; Reports v1 with weekly/monthly schedules and Slack delivery (Enterprise, sealed credentials); PostgreSQL-only runtime with `wolf db migrate-from-sqlite`; tuned bundled PostgreSQL; dashboard queries 10–30× faster with identical results; Helm chart with CloudNativePG and Barman backups (Enterprise); design-system and settings consolidation; fast-path journey e2e green; gosec 29 → 0; scanner set published to GHCR.
- **Backups on the host:** `/root/wolf-backups/20261028-nosqlite` (newest `pg_dump`), `20261028-perf`, and `20261028-pgcutover` (the final SQLite snapshot). Rollback is a `pg_dump` restore plus image tags `ceeec82`; SQLite itself is gone.

## 3. Next up

Do these in order. Items marked **owner** need a decision, credential or approval.

- [ ] N01 **Keep `main` green.** Three things are red or about to be. (a) The Go workflow failed on the `main` merge of #49: the custom-build worker tests had a 100 ms test lease and, in the worker, a store call that hit the build deadline was returned as a persistence error instead of a timeout. Fixed in PR #50; reproduced under CPU load (fails within 12 runs before, 15/15 pass after). (b) The scheduled scanner release factory has failed daily since the Trivy DB locks went stale (8-day validity). (c) `trivy-db-refresh.yml` pushes a branch every day but `gh pr create` is refused ("GitHub Actions is not permitted to create or approve pull requests") and the step ends in `|| true`, so it reports success. Acceptance: merge the refresh PR; **owner** enables "Allow GitHub Actions to create and approve pull requests" (or supplies a PAT secret); the workflow fails loudly when it cannot open the PR; the Go workflow and the scanner factory are green on `main` for three consecutive days.
- [ ] N02 **Cut Community v0.3.0.** Breaking change: PostgreSQL only. Also carries Apache-2.0, no caps, Reports, the Home dashboard and the performance work. Steps: move `[Unreleased]` in `CHANGELOG.md` under 0.3.0; bump `AppVersion` in `internal/api/routes/system.go` and `ui/package.json` together (`system_test.go` compares them); promote the `:stable` scanner tag (today's default is `scanner-set-2026.36.1`); tag, publish the GitHub release; then revalidate the site claim `community-limits`. Acceptance: tag and release exist, a fresh Compose install from the tag completes the §9.16 journey, and the upgrade note covers `wolf db migrate-from-sqlite`.
- [ ] N03 **Enterprise follows the tag.** After N02, point `PINNED_CORE` (and `internal/bootstrap/bootstrap.go`, `internal/packaging/packaging.go`, `packaging_test.go`) at the tagged core commit, rebuild the overlay and fixer images by tag, deploy, and update the live-state line in §2.
- [ ] N04 **PostgreSQL operator workflow (O07).** `wolf backup` and `wolf restore` now print a pointer to `pg_dump` and CNPG Barman. Provide the real workflow: a documented `pg_dump` and restore for Compose, a scripted restore into an isolated instance with row-count and relationship checks, and a measured RPO/RTO. Acceptance: a rehearsed restore of the live database into a scratch instance, with the numbers recorded.
- [ ] N05 **Short-lived cache for `/findings/trends` and `/fleet/report`.** Both take about 1 s because the largest scan has 55k rows, and the first Home chart load is visibly slow. Cache per user and filter for a short TTL, invalidate on scan completion, keep results identical (the equivalence test against the old implementation stays). Acceptance: warm p95 under 200 ms, cold unchanged, no stale data after a scan finishes.
- [ ] N06 **Qualify the Helm CNPG path on a real cluster (O02/O03).** The chart is rendered and unit-tested but has never run on Kubernetes. Run CNPG with Barman to a real object store: install, backup, point-in-time restore, failover. Replace the CNPG NetworkPolicy's allow-all egress with the object-store endpoint, the Kubernetes API and the cluster peers (with a values override for custom endpoints). Acceptance: install, restore and failover pass on a cluster, and the policy blocks everything else.
- [ ] N07 **Finish removing SQLite after the N02 release.** Delete `wolf db migrate-from-sqlite`, `internal/db/sqlite_to_postgres.go`, its fixture, `go-sqlite3`, CGO and `sqlite-dev` from the Dockerfile, and make the build static. Acceptance: `CGO_ENABLED=0 go build ./...` passes and the image shrinks.
- [ ] N08 **Repository and host hygiene** (**owner** approval for every delete).
  - Remote branches in `thewolf` that are not merged and not current: `console-pty-race`, `elite-product`, `feat/opencode-remediation`, `feature/wolf-pack-mode` (174–466 commits behind, dated March–September), and 71 `chore/trivy-db-refresh-*` branches (the N01 workflow left them with no PR). Confirm nothing in them is still wanted before deleting.
  - Local scratch: `/root/wt-*` worktrees (all merged), the `/root/thewolf` symlink used for Enterprise builds, old `wolf-enterprise*` and fixer images, and `/root/wolf-backups/20261028-pgcutover` once the owner is sure.
  - Turn on "Automatically delete head branches" for both repos.
- [ ] N09 **Inputs only the owner can give.** A GitHub token with Pull requests: Read and write (the fixer's push-and-PR path, F15). A GitLab test project and token (A06, X06). The `wolf-site` path: it has local git only and no remote, so choose a repo, the Cloudflare worker name and origin, and replace the `wolf-site` and `wolf.example` placeholders before any deploy. Revalidate `wolf-site/src/content/claims.yaml` after N02.

## 3a. What "everything is on `main`" means today

`thewolf` and `wolf-enterprise` have no open pull requests and no uncommitted or unpushed work; local `main` equals `origin/main` in both. `wolf-site` has no remote, so it cannot be pushed until N09 is answered. Re-check with `git status -sb` and `gh pr list` in each repo before any release.

## 4. Open work by workstream

Every unchecked task from the 2026-10-24 plan is listed once below, with its original ID. Acceptance criteria and evidence for each ID live in the archived plan under the same section letter. A line here is a pointer, not a rewrite; read the archived task before starting it.

### P00 — baseline and landing

Reconcile the October change set and land it on `main`.

- [ ] P00.3 Reconcile the large existing change set into independently reviewable units, recording dependencies and generated files. **Now:** Landed through PRs #6–#49; confirm evidence, then check off.
- [ ] P00.6 Land reviewed Community units through the existing protected-branch process; update Enterprise's pin only to a verified merged core revision and run overlay compatibility checks. **Now:** Community PRs merged through #49 and the Enterprise pin follows core (`d91f2b1` → `2b2056c`); confirm, then check off.

### R — release blockers and scanner reliability

Scanner correctness, honest partial results, fail-closed gates.

- [ ] R02 Build the corrected scanner artifact, run its executable check under the supported runtime UID/network/cache conditions, record digest and architecture, then qualify its use in a real Wolf scan. **Now:** Scanner release `scanner-candidate-manual-d52e9a6da042` and live e2e are green (2026-10-26); record digest and check off.
- [ ] R02.2 Qualify and roll out the matching API/worker and scanner artifacts through the existing deployment process. **Now:** Same release; add the rollback identity and rollout evidence.
- [ ] R03 Triage all 43 October gosec alerts against current code: record rule, file, source/sink, trust boundary, exploitability, disposition, reviewer, and regression check. **Now:** gosec 29 → 0 on 2026-10-26 with 27 narrow `#nosec` notes; needs an independent review before it closes.
- [ ] R03.3 Complete Git subprocess confinement: prove safety against rename/replacement and external worktree gitdirs.
- [ ] R05 Validate every scanner using invocation + representative positive/negative fixtures and normalized parser output, not version presence alone.
- [ ] R06 Preserve findings on partial analysis while separately reporting complete/partial/failed/skipped execution, reason, exit code, retryability, and retained count.
- [ ] R07 Verify runtime prerequisites for Go module/build caches, CodeQL precompiled queries and permissions, Mypy scratch space, Semgrep rule network access, detect-secrets archive inputs, and stable finding identity.
- [ ] R08 Recheck fail-closed gates and CI exits after scanner changes: worker loss, cancellation, required-tool failure, partial coverage, missing test evidence, malformed/unknown gate responses, and pending state cannot produce a successful required gate.

### D — findings truth, evidence, quality policy

Identity, baselines, suppressions, policy, artifacts, SARIF.

- [ ] D02 Verify final persisted clusters reconcile early per-tool rows without losing evidence, foreign references, or stable user-visible IDs.
- [ ] D04 Verify persisted new/existing/fixed/resurfaced state, first/last seen, and selected baseline survive restart/retry and drive identical UI/gate/artifact decisions.
- [ ] D14 Verify fresh Compose setup with absolute repository roots, host-path translation, Docker socket group guidance, `/ready`, and PostgreSQL guidance for concurrent workers. **Now:** Compose now ships PostgreSQL by default; verify the fresh-install path.
- [ ] D15 Verify scanner `ExtraEnv`, source credentials, clone URLs, tool commands/output, audit events, reports, and AI logs redact secrets.
- [ ] D19 Qualify semantic fingerprints across moved lines, repeated scans, rule/version changes, and imported SARIF. Preserve versioned fallback identities for older evidence; backfill must be resumable and idempotent.
- [ ] D20 Qualify durable suppressions with reason, author, scope, creation/expiry/revocation, audit, visible explanation, and policy control.
- [ ] D21 Qualify last-successful/default-branch/named/explicit-scan baselines, diff artifacts, baseline ownership, and first successful Standard/Release auto-baseline.
- [ ] D22 Qualify policy precedence and explain output: global/collection/repository overrides, severity/confidence/category/CWE/tool/new-only/always-fail classes, warn/enforce modes, and audited overrides.
- [ ] D23 Qualify plan-only output and persisted run/skip reasons, profile/tool/category/path filters, language detection, estimated work, cancellation, and resource limits.
- [ ] D24 Qualify manifest/raw/findings/diff/gate/SARIF/SBOM/fix-document artifacts with schema versions, source SHA, image/tool/rules/DB identities, checksums, secret handling, retention, download authorization, and reproducible links.
- [ ] D25 Qualify SARIF import/export round trips, multiple runs, malformed/untrusted paths, quotas, provenance, duplicate imports, and compatibility with existing IDs/suppressions/baselines.

### S — setup, downloads, first useful scan

Readiness, guided first run, durable image preparation, headless parity.

- [ ] S01 Trace first-run, New scan, repository Scan, collection Scan, CLI, and MCP from intent through readiness and submission. **Now:** Community caps are gone, so there are no limits to carry over.
- [ ] S02 Establish one readiness result covering API/database/artifact storage, compatible workers, executor, source access, host mounts, disk capacity, required images, and credentials.
- [ ] S03 Build a guided first-run flow from existing setup status/sample repository/doctor/preflight pieces: connect source, choose repo/branch and sensible profile, inspect plan, prepare dependencies, start, view results.
- [ ] S04 Unify image preparation into authorized durable operations with immutable requested inputs, idempotency, per-image state, persisted progress/events, bounded concurrency, cancellation, timeout, retry, and restart recovery.
- [ ] S05 Show download/preparation status by image: queued/resolving/downloading/verifying/ready/failed/cancelled, bytes/total when available, elapsed time, cached work reused, and precise errors.
- [ ] S06 Verify expected digest/signature/policy before declaring an image ready; detect incompatible architecture, revoked releases, insufficient disk, proxy/TLS/registry-auth/rate-limit/network errors.
- [ ] S07 Retry failed preparation safely without redownloading completed content unnecessarily; explain runtime-supported resume limitations.
- [ ] S08 Apply least-required preparation from the selected scan plan; avoid pulling all heavy variants for a basic scan.
- [ ] S11 Preserve Docker local build without publishing credentials; make push an explicit action using registered secret references.
- [ ] S12 Complete headless setup parity: existing `wolf setup`, doctor and pull commands, plus PRD `wolf init` as an alias/orchestrated entry only if needed.
- [ ] S13 Provide an authoritative downloads/install entry for released CLI/application artifacts: supported OS/architecture, version/core/overlay identity, checksums/signatures, install/upgrade commands, and verified links.
- [ ] S14 Verify fresh Docker-only installation and default scanner preparation on a clean host.
- [ ] S15 Test the real first-hour journey, plus registry outage, denied credentials, interrupted pull, low disk, worker loss, browser refresh, repeated submit, partial preparation, and cancellation.
- [ ] S16 Run the complete PRD §9.16 ease journey using only in-product guidance: Compose start → sample Fast/Standard scan → one-line verdict → real corroboration → auto-baseline and new-only interpretation → copied CI fails on new issues/secrets → failed-tool retry.

### U — navigation and everyday workflows

One coherent console; honest states; accessibility.

- [ ] U01 Inventory all routes, tabs, buttons, equivalent actions, roles, and empty/error states.
- [ ] U02 Consolidate primary navigation around repositories, issues, scan activity, and remediation; clarify the place of collections, canonical vulnerabilities, raw findings, agents, and scanner coverage.
- [ ] U04 Clarify personal Account versus system Administration, including personal versus all-user tokens/secrets/nodes.
- [ ] U07 Preserve repository uniqueness and source identity; duplicate add/import must take the user to the existing repository, not silently create another.
- [ ] U07a Verify shared repository-add/import forms, framework/language parsing and chips, collection membership/context, default scan-on-add, duration formatting, and real source/branch errors.
- [ ] U08 Use validated URL state for search/filter/sort/page/grouping/branch; preserve values through refresh, back/forward, palette navigation, and cross-page links.
- [ ] U09 Separate loading, permission denied, not found, error, genuine zero results, and incomplete evidence.
- [ ] U10 Home prioritizes actionable new/resurfaced security risk, affected repositories, owners, evidence, and next actions.
- [ ] U11 Findings detail exposes persisted corroboration/confidence/category/score/baseline/timeline/strategy and suppression preview only after D01–D08 pass.
- [ ] U12 Make bulk triage/assignment/suppression safe and scope-aware, with clear selection count and partial failures.
- [ ] U13 Scan view shows prominent unfiltered findings count/severity breakdown, current activity, coverage, worker state, terminal verdict, and reason.
- [ ] U14 Remediation defaults to a sensible configured engine and safe branch behavior; put model/harness/effort controls behind advanced settings.
- [ ] U15 Global search carries the entered query into the palette, searches real permission-filtered data, and navigates with consumed route parameters.
- [ ] U16 Notifications distinguish scan/gate/new-critical/fix/update events; provide read state, relevant links, and permission filtering.
- [ ] U17 Consolidate code/patch/log presentation with safe rendering, copy/download actions, filenames/line links, and large-content bounds.
- [ ] U18 Qualify WCAG 2.2 AA primary journeys, keyboard/focus/error announcement, touch/narrow layouts, light/dark/system themes, and meaningful read-only states.
- [ ] U19 Remove or clearly label unfinished Enterprise catalog/tenancy/compliance/residency/customer-key UI. Record CRUD is not proof of enforcement; retain working bookmarked routes where compatible.

### P — performance and fleet scale

Concurrency, caches, bounded streams and retention.

- [ ] P07 Qualify scan heavy/network/tool concurrency, CPU/memory/scratch quotas, backpressure, deadlines, fair scheduling, cancellation cleanup, and capacity fallbacks.
- [ ] P08 Verify BuildKit/download cache isolation: unrelated tool/version/release-label changes do not rebuild every language layer, while relevant source/checksum/lock changes invalidate correctly.
- [ ] P09 Bound SSE/event replay, logs, release history, search, exports, and large coverage imports.

### A — API, CLI, MCP, sources, schedules, CI

Interface parity, queue/event semantics, schedules, webhooks.

- [ ] A01 Recheck REST route/OpenAPI coverage and request/response/error schemas against actual routes, including database settings, test coverage, setup, artifacts, and all scanner operations.
- [ ] A02 Publish one interface-parity matrix for UI/REST/CLI/MCP/webhook/pipeline, with intentional omissions and scopes.
- [ ] A03 Verify scoped API keys (hash-only, expiration, revoke), browser sessions, access/refresh purpose separation, rotation/replay/logout, MFA, and identical scope/ownership decisions across interfaces.
- [ ] A04 Qualify MCP official-SDK transport/lifecycle, negotiation, notification handling, tool/resource schemas, stdio bridge, read/scan execution, cancellation, errors, and a real external client; retain default-off and governed actions.
- [ ] A05 Complete CLI automation inputs (request files, source selectors, filters, refs, idempotency), stable JSON/output streams, wait/reconnect/cancel behavior, and documented terminal exit codes.
- [ ] A06 Qualify generated CI workflow from a clean runner: install verified CLI, configure secret environment, select exact PR/commit, submit once, capture ID, wait, inspect execution, evaluate gate, upload coverage/SARIF, retain artifacts.
- [ ] A07 Preserve source transports: registered repositories, GitHub, HTTPS/SSH Git, local paths, and SSH working trees.
- [ ] A08 Qualify remote-node onboarding: encrypted keys/passwords, strict known-host verification with explicit trust, allowed roots, canonical/symlink confinement, no shell interpolation, timeouts, denied paths, proxy/SSRF/host-binding checks, and connection doctor.
- [ ] A09 Qualify remote browsing, Git metadata/branches, source uniqueness, archive snapshot/extraction limits, safe filenames, partial/disconnected transport, cancellation/cleanup, and SSH discovery/GitHub import UI. Keep owner filtering throughout.
- [ ] A10 Qualify durable queue semantics with multiple API/worker replicas: atomic claims, random leases, heartbeats, bounded retry, stale recovery, lease-guarded writes/finalization, sticky cancellation, and idempotent same-key/conflicting-payload behavior.
- [ ] A11 Qualify replayable monotonic events and Last-Event-ID across worker/API restart, lease loss, duplicate delivery, expired history, and terminal reconnect.
- [ ] A12 Qualify repo/collection schedules, profile/ref, quiet hours, timezone/DST, SHA coalescing, overlap prevention, single catch-up after downtime, and authorization.
- [ ] A13 Qualify signed GitHub push/PR events, replay/deduplication, exact revision selection, branch filters, and untrusted fork isolation.
- [ ] A14 Complete per-tool/whole-scan retry with immutable source/policy/release intent, explicit evidence lineage, coverage semantics, and safe resumed execution; retain compact results and scanner-run/worker endpoints.
- [ ] A15 Finish reusable executor/artifact boundaries where they eliminate current global coupling.
- [ ] A16 Preserve API-only serving, normal embedded SPA, all existing CLI commands, and backward-compatible stream/error contracts through rolling upgrades.

### T — measured test coverage

Coverage parsers, provenance, thresholds, real CI evidence.

- [ ] T01 Verify LCOV, Cobertura, JaCoCo, Clover, OpenCover, Go profiles, Istanbul JSON, SimpleCov JSON, and Sonar generic XML parsers, with real exporter fixtures across supported language ecosystems.
- [ ] T02 Preserve line/statement/branch/function metric distinctions and weighted totals; test absent/zero denominators, exclusions, generated files, source roots, malformed/truncated/oversized input, path confinement, and overlapping report rejection/merge policy.
- [ ] T03 Require repository/scan/exact commit provenance and preserve existing evidence when an import fails.
- [ ] T04 Verify overall and changed-code thresholds, including diff-cover import, global/collection/repository precedence, per-source-root policy, and pass/fail/incomplete outcomes.
- [ ] T05 Verify API/CLI/MCP import/read/policy authorization and Tests UI pagination/sort/filter/uncovered lines.
- [ ] T06 Wire real CI reports for a representative polyglot repository and record the resulting gate through all interfaces.

### F — verified remediation (Enterprise fixer)

Verification, budgets, isolation, real finding → PR journey.

- [ ] F03 Preserve all CLI/API/custom harnesses behind existing engine interfaces; complete `pkg/fixengine` conformance where needed.
- [ ] F04 Verify deterministic enrichment prompts with finding evidence, source context, strategy, exact scope, and bounded payloads; filter/idempotent enrich behavior and optional AI guidance/logging reuse current services.
- [ ] F05 Verify batch/file-based prompts for Codex/Claude/OpenCode, safe prompt-file paths, empty-success handling, malformed/rejected API diff retries, and no shell/path escape in patch application.
- [ ] F06 Qualify independent verification: actual changes, build/parse, targeted scanner/rule recheck, no regression, optional test command, and structured evidence.
- [ ] F07 Qualify bounded iterations, per-finding attempts, severity floor, no-progress/regression/target-reached stops, wall-clock/token/cost caps, rollback, unfixable outcomes, preserved agent commits, and restart-safe attempt history.
- [ ] F08 Preserve immutable scan/image/policy inputs through a fix loop; persist before/after SHAs, plans, prompts/responses with redaction, verification outputs, diffs, engine versions, and iteration linkage.
- [ ] F09 Verify durable fixer claims/leases/heartbeat/recovery/cancellation, streamed logs, `--once`, resource cleanup, and concurrent-job isolation.
- [ ] F12 Revalidate OpenCode spike findings against the pinned version: root deny, tool permissions, correct config variable, untrusted repo config override, closed stdin, event/turn accounting, and timeout/cancellation.
- [ ] F13 Qualify malicious repository agent instructions, symlink/patch escape, secrets/network permissions, subprocess isolation, and independent verifier boundaries across every harness.
- [ ] F14 Qualify fixer base + engine image dependencies, login/session persistence and expiry, API fallback, native architecture CLI invocation, and user-facing setup/diagnostics without exposing tokens.
- [ ] F15 Run a real remote finding → dry-run diff → verification → authorized branch push → PR → rescan/gate journey and retain evidence.

### L — scanner factory, updates, publication, rollback

Discovery, build matrix, signing, publication, canary, rollback.

- [ ] L01 Verify `scanners/tools.yaml` as catalog authority: tool/category/language/source/version/platform/invocation/parser/network/timeout/resource/update/license metadata, generated docs/config, image routing, manual-update exceptions, and no floating defaults.
- [ ] L02 Verify exact versions/checksums/digests for tools, bases, OS snapshots, toolchains, downloaded artifacts, vulnerability databases, builder/parser/policy revisions, build arguments, and all supported platforms in the canonical lock.
- [ ] L03 Qualify discovery across GitHub, registries, package indexes, native sources, and advisories: bounded concurrency, cache/rate limits/authentication, partial failures, stale indicators, risk classification, selected versus complete checks, and no automatic unreviewed pin changes.
- [ ] L04 Qualify daily discovery (02:00 organization time, deterministic jitter up to 20 minutes), weekly Sunday 03:00 complete-set candidates, on-demand/security rebuilds, seven-day default maximum stable-image age, and configurable maintenance windows.
- [ ] L05 Qualify reviewable update proposals/PRs with source integrity and dependency/checksum refresh.
- [ ] L06 Verify persisted candidate/build/step/event/artifact/approval/publication/rollout states, idempotency, fencing/lease loss, retry/cancel/recovery at every state boundary, pagination, and database migration parity.
- [ ] L07 Build the four owned scanner variants (default/JVM/Rust/CodeQL) and four owned fixer images with explicit supported architecture matrix.
- [ ] L07a Reconcile every shipped Dockerfile/runner against the release matrix: application/API, scan worker, release adapter/worker, default/min/JVM/Rust/CodeQL scanners, fixer base/API/Claude/Codex/OpenCode, combined engines worker, and legacy AI runner.
- [ ] L08 Require real executable smoke, parser fixtures, finding-diff/collapse detection, performance/resource baselines, vulnerability/secret/license checks, SPDX SBOM, provenance, signatures, and runtime checks on every required tuple.
- [ ] L09 Qualify signing/key-provider integration and independent verification of the release manifest, image digests, attestations, SBOMs, and publication receipts; no private signing material in public source, logs, layers, or archives.
- [ ] L10 Publish qualified managed artifacts to GHCR, reconcile the Docker Hub mirror, and prove digest/signature/provenance parity.
- [ ] L11 Qualify customer registries, credentials, trust roots, build isolation, signer/KMS integration, and reproducibility comparisons from the same lock.
- [ ] L12 Qualify role separation for viewers/operators/approvers/admins/auditors, approval expiry and policy snapshots, explicit emergency exceptions, mandatory hard gates, and configurable low-risk automatic promotion.
- [ ] L13 Pin the selected release/digest set on every scan; active scans must not change when tags/channels move.
- [ ] L14 Qualify canary traffic, observation windows, staged cohorts, worker draining, capacity limits, maintenance windows, pause/resume, deadline, and automatic rollback on signature/pull/crash/parser/finding-collapse/performance regression.
- [ ] L15 Prove rollback uses prior verified digests, stops new candidate assignment, preserves active scans unless revocation policy requires cancellation, and records trigger/evidence/affected scans/timing.
- [ ] L16 Qualify revocation: block new assignment, identify queued/active scans, apply explicit finish/cancel/operator policy, choose safe rollback, and emit high-severity audit/notifications.
- [ ] L17 Preserve inventory/status/doctor/pull/local-build/custom-build troubleshooting through factory changes.
- [ ] L18 Qualify advanced UI/API/CLI update/candidate/release/rollout/policy/registry operations, event/log recovery, permissions, read-only states, responsive accessibility, and rolling API/UI compatibility.
- [ ] L19 Verify custom build allowlists and isolated execution, no arbitrary Dockerfile/script injection, archive import limits/path confinement, credential redaction, and updater/builder/signing/registry/rollout security review.
- [ ] L20 Run full Compose and Kubernetes release lifecycle with externally published exact-digest images, genuine scanner Jobs, staged canaries, failure injection, and measured rollback.
- [ ] L21 Exercise notification delivery/dead letters, audit retention, metrics/alerts, backup/restore, and disaster recovery for the release database and evidence.
- [ ] L22 Enable stable control only after the complete supported environment/artifact matrix and independent security/UX review pass; list every exception with owner and expiry.
- [ ] L23 DEFERRED — qualify signed offline bundle export/import, complete image transfer/readback, trust/compatibility/replay/revocation checks, no-network operation, and customer delivery.

### O — deployment, recovery, observability

Compose/Helm/Kubernetes qualification, backup/restore, SLOs, drills.

- [ ] O01 Qualify Docker Compose and hardened Compose from fresh install and an existing database; document bind roots, socket permissions, secret files, localhost exposure, health/readiness, worker/fixer resources, and upgrade/rollback.
- [ ] O02 Qualify Helm bundled PostgreSQL, CNPG, and external PostgreSQL modes; explicit secrets/TLS, resources, probes, persistence, ingress/pull secrets, and dependency readiness. **Now:** Helm moved to Enterprise: CNPG + Barman is the default (wolf-enterprise #18). Real-cluster qualification still open.
- [ ] O03 Verify default-deny NetworkPolicies allow only required API/worker/fixer/build-controller database traffic. **Now:** CNPG pods have a NetworkPolicy that allows all egress; tighten it (see N06).
- [ ] O04 Qualify native scan Jobs, result capture, cancellation/reconciliation/cleanup, RWX/shared artifact storage, RBAC, security contexts, and no API-pod Docker socket.
- [ ] O05 Verify isolation profiles: non-root/read-only source, writable bounded scratch, dropped capabilities, seccomp, CPU/memory/PID limits, network denied unless declared/authorized, and malicious build/project inputs.
- [ ] O06 Qualify fresh/repeated/concurrent PostgreSQL migrations (SQLite runtime support was removed 2026-10-28; `wolf db migrate-from-sqlite` is the upgrade path for one more release) and upgrades from real supported snapshots, including coverage/evidence changes. **Now:** PostgreSQL only since 2026-10-28; fresh/repeated/concurrent migrations and real-snapshot upgrades still to qualify.
- [ ] O07 Complete backup/restore operator workflow and CLI parity for databases (`wolf backup`/`wolf restore` were SQLite-only and now point to `pg_dump`/CNPG Barman; the PostgreSQL workflow is still to do), secrets/key references, artifacts, and release state. **Now:** Owner: N04.
- [ ] O08 Verify workspace/artifact/image retention, TTL reap, disk reporting, low-disk prevention, and cleanup errors.
- [ ] O09 Add request → durable job → scanner/fixer/release trace correlation and OTLP integration using existing logging/metrics boundaries.
- [ ] O10 Define and measure service objectives for API latency, queue delay, scan completeness, artifact availability, discovery freshness, rollout/rollback, backup, and restore.
- [ ] O11 Verify classified audit events (`event_type`, category, severity, actor, trusted client IP, user agent, resource, result), including failed/successful login, MFA, role changes, tokens, policy/suppression, export, approvals, and raw-evidence access.
- [ ] O12 Perform worker/API/database/registry/storage/network failure and restart drills under load; prove no lost findings, duplicate finalization, false-green gates, leaked credentials, or orphan workloads.
- [ ] O13 Deploy a traceable candidate canary, retain a tested rollback path, run real authenticated journeys, then promote only after acceptance.

### E — Enterprise edition, identity, compatibility

Boundary, entitlements, IdP, RBAC/ABAC, packaging.

- [ ] E02 Verify public core/private overlay boundaries, extension contracts and conformance, unique private migrations, pinned-core builds, private CI/registry access, accidental-publication procedure, and no private source/secrets in public builds.
- [ ] E03 Verify edition/capabilities (Community caps removed 2026-10-27) and core+overlay versions consistently through UI/API/CLI/MCP/audit/metrics; backend enforcement must not rely on hidden UI controls. **Now:** Community caps removed 2026-10-27; verify the rest.
- [ ] E04 Complete and qualify signed offline entitlement lifecycle where required by the PRD: issuer/trust roots, customer/scope/expiry/limits, verification, rotation/revocation, audit and safe failure.
- [ ] E05 Qualify OIDC/SAML/LDAP/AD/SCIM flows with genuine IdP fixtures, JIT, provision/deprovision, group mapping, logout/session revocation, audit, and local break-glass.
- [ ] E06 Verify mapped roles are validated; most-privileged valid mapped role wins where specified; no groups claim must not silently demote a break-glass admin.
- [ ] E07 Complete genuine resource-scoped RBAC/ABAC, custom roles, delegated administration, service/workload identity, and tenant isolation before representing labels/records as enforced tenancy.
- [ ] E08 Qualify supported Enterprise integrations, diagnostics/support bundle redaction, private packaging/distribution, and the overlay against the merged Community candidate.
- [ ] E09 Restore/enable private release automation only after its checks and publication credentials are qualified; preserve explicit dispatch-only controls until then.

### G — technology-guide adoption

Typed contracts, composition roots, key lifecycle, toolchain policy.

- [ ] G01 Introduce typed Connect/protobuf contracts at existing operation boundaries, generated clients, and parity/error/auth tests while retaining REST/CLI/MCP compatibility.
- [ ] G02 Plan/test sqlc/goose adoption against the existing migration history and PostgreSQL/SQLite requirements. **Now:** SQLite requirement dropped 2026-10-28; PostgreSQL only.
- [ ] G03 Replace mutable handler/auth/secret globals at composition roots where needed; prove two instances have isolated configuration, stores, auth, and encryption state.
- [ ] G04 Complete rotatable asymmetric JWT signing keys and versioned encrypted-secret/key lifecycle aligned with the adopted guide, with backward decryption, rotation recovery, revocation, and no plaintext migration dumps.
- [ ] G05 Unify toolchain/version policy across local setup, CI, Docker, generated metadata, scanner/fixer builds, and release docs; preserve security-maintained transitive pins and real runtime compatibility checks.
- [ ] G06 Reconcile Cppcheck/ShellCheck OS-snapshot packaging versus latest supported upstream policy, recording any deliberate maintenance exception and qualifying parser/native-platform behavior.

## 5. Deferred — kept open, not waived

These need external infrastructure, credentials or a commercial decision. Air-gap expansion stays deferred; existing air-gap behavior must not regress.

- [ ] X01 Complete immutable evidence/canonical vulnerability migration with dual-write, backfill, reconciliation metrics, compatible finding views, and explainable correlation/split/merge before moving assignment/SLA/exception state.
- [ ] X02 Extend enterprise semantic/reachability/exploitability analysis with explicit evidence, confidence, language coverage, benchmark corpus, false-positive/false-negative measures, and independently validated claims.
- [ ] X03 Add attack-path correlation and constrained dynamic/runtime verification only with isolation, scope/authorization, budgets, reproducible proof, and governance controls.
- [ ] X04 Add governed organization-wide remediation/approvals, certified fixer images, compliance/reporting/SLA/exception workflows, ticketing/SIEM, and support obligations backed by real enforcement.
- [ ] X05 Extend independently versioned signed plugin contracts/marketplace and paid/custom scanner integration using existing container/execution/evidence/secret interfaces.
- [ ] X06 Expand CI integrations beyond the qualified initial provider (GitLab/Jenkins/Azure DevOps and documented supported platforms) using the stable API/CLI gate/evidence contract.
- [ ] X07 DEFERRED — object/archive upload sources, S3-compatible artifact backend, signed completion webhooks where not covered by A13, and additional source-provider integrations; preserve clear boundaries and quotas.
- [ ] X08 DEFERRED — certified disconnected Enterprise delivery, customer-controlled keys/residency, export/import upgrades and recovery drills under the same trust/evidence model (L23).
- [ ] X09 DEFERRED — Cloud control plane, tenant isolation, billing/metering, managed upgrades/operations, data retention/residency, and commercial support/SLOs.
- [ ] X10 Resolve open commercial/architecture choices from PRD §25 through recorded decisions: edition thresholds, entitlement policy, supported scale/deployment defaults, trust/key ownership, integration priorities, and supported certification matrix.

## 6. Milestone exit checks

- [ ] Q01 M0: source/deployment baseline reconciled, GoKart implementation and runtime check pass, security alerts dispositioned, no false-green required gate, and reviewable changes/evidence retained.
- [ ] Q02 M1: clean-host onboarding and headless CI complete a real scan; downloads recover across failure/restart; required missing analysis blocks success; user can understand readiness, scope, result, and next action.
- [ ] Q03 M2: daily investigate/triage/fix/test-coverage journeys pass permissions, keyboard/mobile/theme/error checks; measured fleet performance meets recorded SLO; no contradictory counts or duplicate primary workflows.
- [ ] Q04 M3: supported Compose/Kubernetes/database/architecture/Enterprise matrix passes, genuine IdP and customer integrations are qualified, rollback and backup/restore are demonstrated, and operational dashboards/runbooks work.
- [ ] Q05 Final release: required static/unit/race/contract/migration/UI/browser/image/security tests pass on the exact candidate; publish signed evidence with known limitations, verify real remote scan/fix/PR/gate journey, record promotion/rollback, and update docs/changelog/this plan.

## 7. Reference

- Evidence for the 101 tasks closed before 2026-10-24 and the execution log are in `docs/archive/2026-11-14/PLAN-2026-10-24.md` §18; validation records are under `docs/validation/`.
- After any change under `internal/` or a Dockerfile, run `make scanners-sync` and commit `scanners/scanner-lock.yaml`; `go run ./cmd/scannertools lock --check --require-resolved` verifies it.
