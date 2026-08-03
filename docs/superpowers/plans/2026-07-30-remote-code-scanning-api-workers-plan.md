# Remote Code-Scanning API and Scalable Worker Architecture

**Status:** Implemented and locally validated; Kind/production rollout pending  
**Date:** 2026-07-30  
**Goal:** Make Wolf a durable, headless remote code-scanning service without
breaking the existing UI or `/api/v1` clients.

## Implementation record

The implementation described below is now present in the working tree. Checked
items have automated or live local evidence. Unchecked items are intentionally
left open when they require a real Kubernetes cluster, production rollout, or
manual browser coverage that was not available during this implementation.

### Delivered

- [x] Additive scan submission for registered repositories, one-shot HTTPS/SSH
      Git, and one-shot SSH working trees.
- [x] Standard, full, and targeted profiles with tool/category/path selectors
      and truthful requested/effective scope reporting.
- [x] Registered encrypted source credentials with ownership, transport type,
      host binding, masking, strict SSH host keys, and no plaintext responses.
- [x] Durable SQLite/PostgreSQL queue, atomic claims, random leases, heartbeats,
      bounded retries, stale recovery, sticky cancellation, and lease-guarded
      scan state, findings, scanner runs, progress events, and finalization.
- [x] Replayable database-backed SSE with monotonic IDs and `Last-Event-ID`.
- [x] Separate `wolf scan-worker`, queue-default Compose deployment, multiple
      PostgreSQL workers, and one-release inline rollback mode.
- [x] Stable compact result, scanner-run, worker-status, and runtime-capability
      endpoints plus matching CLI commands and OpenAPI coverage.
- [x] Runtime-neutral scanner invocation and native Kubernetes Jobs with
      security contexts, RBAC, NetworkPolicies, cancellation, reconciliation,
      result capture, and Helm packaging.
- [x] Shared-filesystem artifact backend contract and stable storage keys.
- [x] `wolf serve --api-only` while preserving the normal embedded SPA.
- [x] UI runtime-capability handling that preserves Docker preflight in Compose
      and skips unsupported Docker actions in Kubernetes.
- [x] Operator and API documentation with examples and deployment constraints.

### Remaining operational validation

- [ ] Run the existing full UI workflow manually in a browser.
- [ ] Run the Helm deployment and native scanner Jobs in an isolated Kind
      cluster with RWX test storage.
- [ ] Canary and roll out migrations, API, and workers in the target production
      environment; retain inline rollback for the first release.
- [ ] Repair the pre-existing UI lint script by choosing and pinning an ESLint
      toolchain/configuration; `eslint` is currently referenced by the script
      but absent from the package dependencies.

### Non-blocking follow-up hardening

- [ ] Move the shared executor out of the route package and replace its remaining
      concrete clock/ID/global dependencies with injected seams.
- [ ] Remove the final legacy global artifact facade after all older callers
      consume the backend/workspace interfaces directly.
- [ ] Add one end-to-end sentinel test that searches every log, row, event, and
      artifact surface for source credential plaintext.

### Validation evidence (2026-07-30)

- [x] `go test ./...`
- [x] `go vet ./...`
- [x] Race tests for database, worker, route, SSE, source, and Kubernetes runtime
      packages.
- [x] UI TypeScript typecheck, all Vitest tests, and production Vite build.
- [x] Normal and hardened Compose configuration rendering.
- [x] Fresh/repeated SQLite and PostgreSQL migration tests.
- [x] Live Compose PostgreSQL test with two workers claiming two scans
      concurrently and returning completed durable results.
- [x] Live credential masking/deletion and idempotent-repeat/conflict checks.
- [x] Helm lint plus strict kubeconform validation: 18 resources valid, zero
      invalid, zero errors, zero skipped.
- [x] Docker scanner image smoke test across all bundled tools.
- [ ] UI ESLint command (blocked by the dependency/configuration gap above).
- [ ] Kind end-to-end test (`kind` is not installed; the existing desktop
      Kubernetes context was deliberately not modified).

## Locked decisions

- Self-hosted, single-organization deployment is the initial product model.
- `/api/v1/scans` evolves additively. There is no parallel jobs API or v2 fork.
- Existing UI request, response, status, polling, SSE, and cancellation contracts
  are compatibility requirements.
- Initial source inputs are registered repositories, one-shot Git, and one-shot
  SSH. Archive/object uploads are deferred.
- API-created sources are upserted as visible repositories so scan history,
  findings, trends, baselines, ownership, and existing UI pages keep working.
- Source credentials are registered first and referenced by ID. Scan requests,
  queue rows, logs, events, findings, and artifacts never contain plaintext
  source credentials.
- Completion uses polling and SSE. Signed webhooks are deferred.
- Production scan execution runs in separate workers.
- Docker Compose runs a fixed operator-selected number of Docker-backed workers.
- Kubernetes runs native per-tool Jobs using PostgreSQL and an RWX workspace PVC;
  it does not use Docker sockets or Docker-in-Docker.
- Filesystem/shared-volume artifacts remain the first backend behind a storage
  interface. S3-compatible storage is deferred.
- SQLite supports one effective worker for development/small Compose installs.
  PostgreSQL is required for multiple workers and Kubernetes.

## Compatibility contract

The following behavior must be captured by tests before the executor is moved:

- [x] `POST /api/v1/scans` and the `/api` redirect alias accept the current fields:
      `repo_id`, `collection_id`, `branch`, `tools`, `disabled_tools`,
      `ai_enabled`, `ai_engine`, and `ai_model`.
- [x] A valid legacy request returns `201` and `{"data": <scan>}`.
- [x] The initial public status remains `pending`.
- [x] A request containing neither `repo_id` nor `source` returns `400`.
- [x] Existing scan response fields retain their names and types.
- [x] `tools_selected`, `tools_completed`, and `tools_failed` remain JSON-encoded
      strings.
- [x] Public scan statuses remain `pending`, `running`, `completed`, `failed`,
      and `cancelled`; internal queue phases never leak as new status values.
- [x] Findings are persisted after each tool completes.
- [x] `GET /scans/{id}/tools` continues returning per-tool `queued`, `running`,
      `completed`, `failed`, or `cancelled`.
- [x] Whole-scan and per-tool cancellation routes retain their response shapes.
- [x] Findings, stats, report, manifest, SARIF, coverage, gate, diff, raw output,
      artifact, scanner-run, comparison, and recommendation routes remain usable.
- [x] Existing SSE event names and JSON fields remain compatible.
- [x] Existing local, GitHub, and registered SSH scans continue to work.
- [ ] Docker Compose keeps the existing SPA and scanner-image management behavior.
- [x] Kubernetes exposes unsupported Docker-only capabilities cleanly so the UI
      disables those actions instead of failing.

## Additive public API

### Scan source

`POST /api/v1/scans` accepts exactly one of `repo_id` or `source`.

```json
{
  "source": {
    "kind": "git",
    "name": "payments",
    "url": "https://git.example.com/acme/payments.git",
    "ref": "refs/heads/main",
    "credential_id": "credential-id"
  },
  "profile": "targeted",
  "categories": ["sast", "secrets"],
  "tools": ["semgrep", "gitleaks"],
  "include_paths": ["src/**"],
  "exclude_paths": ["vendor/**"],
  "client_reference": "build-98312"
}
```

Git source fields are `kind=git`, `name`, `url`, optional `ref`, and optional
`credential_id` for public HTTPS sources. SSH source fields are `kind=ssh`,
`name`, and either `node_id` plus `path`, or `host`, `port`, `username`, `path`,
`base_path`, `credential_id`, and `known_hosts`. New SSH connections require
explicit host-key material.

Validation rules:

- [x] Reject requests containing both `repo_id` and `source`.
- [x] Preserve legacy tool-selection behavior when `profile` is absent.
- [x] Treat `source.ref` as authoritative and reject a conflicting top-level branch.
- [x] Reject absolute/traversing include/exclude patterns.
- [x] Reject unknown tools, categories, profiles, source kinds, and credential types.
- [x] Canonicalize and upsert the source before creating the scan.

### Profiles and scope

- `standard`: current language-detected automatic selection.
- `full`: all scanners applicable to the detected languages/repository, including
  heavy scanners; DAST remains excluded without a target.
- `targeted`: requires a tool, category, or include path selector.
- A legacy non-empty `tools` list without `profile` remains an exact tool list.

Every scanner run records requested and effective scope. Repository-wide tools may
run against the full snapshot, but must explain why they could not honor path scope.

### Idempotency

`POST /scans` accepts `Idempotency-Key`.

- [x] Same user, key, and normalized request returns the original scan with `201`.
- [x] Reusing the key with a different request returns `409 idempotency_conflict`.
- [x] Add the header to CORS.
- [x] The UI remains unaffected because it sends no idempotency key.

### Credentials

Add `/api/v1/credentials` with `read:credentials` and `write:credentials` scopes.
Initial types are `git_https`, `ssh_private_key`, `ssh_password`, and the existing
GitHub/GitLab token types. Credential responses return only ID, type, name, allowed
hosts, masked metadata, and timestamps. Keep `/config/secrets` as a compatibility
adapter.

### Consolidated result

Add `GET /api/v1/scans/{id}/result` with status/phase, immutable provenance,
severity/tool totals, requested/effective scope, quality-gate summary, and links to
findings/SARIF/manifest/report/artifacts. Do not embed all findings.

## Durable execution design

The existing `scans` row is the durable job. Migration `026` adds normalized request,
client reference, idempotency key/digest, phase, claimed worker, lease token/expiry,
heartbeat, attempts, cancellation request, failure code/message, execution backend,
and source fingerprint. Scanner-run records gain runtime reference/backend, attempt,
cancellation request, and requested/effective scope.

`scan_events` stores monotonic event IDs, scan ID, event type, JSON payload, and time.
`scan_workers` stores worker ID, backend, capacity, active count, version,
capabilities, and heartbeat.

Queue invariants:

- [x] API submission persists `pending` and returns without network source preparation.
- [x] PostgreSQL claims with `FOR UPDATE SKIP LOCKED`.
- [x] SQLite uses transactional compare/update and supports one effective worker.
- [x] A claim assigns a random lease token and increments the attempt.
- [x] Scan state, findings, scanner-run records, progress events, and finalization
      are conditioned on the current lease token.
- [x] Heartbeats extend leases.
- [x] Stale jobs requeue while attempts remain, otherwise fail `worker_lost`.
- [x] Completed tools are not rerun after recovery.
- [x] A stale in-progress tool is reset and its incomplete-attempt findings are
      replaced before retry.
- [x] Cancellation is persisted, observed by workers, and propagated to the runtime.
- [x] Durable SSE supports `Last-Event-ID` replay and terminal closure.

## Implementation phases

### Phase 0 — Freeze behavior

- [x] Add golden API tests for legacy scan create/detail/tools/findings/cancellation.
- [x] Add SSE payload compatibility tests.
- [ ] Add UI tests for new scan, preflight, bulk scan, polling, and cancellation.
- [ ] Add a browser smoke test for the existing UI scan workflow.
- [x] Confirm the baseline is green before moving execution.

### Phase 1 — Extract the scan application service

- [ ] Separate request validation, record creation, target preparation, execution,
      per-tool persistence, finalization, and artifact generation from HTTP handlers.
- [x] Introduce a normalized request containing credential IDs but no plaintext.
- [ ] Add injected seams for target resolver, runner, clock, IDs, events, artifacts,
      and database.
- [x] Retain a temporary inline compatibility executor backed by the same service.
- [x] Keep all Phase 0 tests unchanged and green.

### Phase 2 — Queue, leases, recovery, and events

- [x] Add migration 026 and wire it into SQLite/PostgreSQL migration runners.
- [x] Implement claim, heartbeat, finalize, requeue, cancel, event, and worker stores.
- [x] Add lease-token guards and bounded stale recovery.
- [x] Remove API-startup logic that cancels all pending/running scans.
- [x] Resume at tool granularity using scanner-run records.
- [x] Replace process-local-only scan progress with durable events plus an optional
      in-memory wakeup.
- [x] Add idempotency normalization/digest/conflict behavior.
- [x] Test concurrent claims, split brain, expiry, retries, and API restarts.

### Phase 3 — Worker and Compose

- [x] Add `wolf scan-worker` with worker ID, backend, capacity, poll interval,
      heartbeat, lease duration, and once mode.
- [x] Register worker capacity/capabilities and expose operator status.
- [x] Add a Compose `scan-worker` service sharing database, artifact, workspace,
      repo, and scanner-cache mounts.
- [x] Move scan Docker execution to workers.
- [x] Preserve API Docker access temporarily for existing image build/pull UI.
- [x] Update the hardened Compose override for worker socket-proxy access.
- [x] Make queue mode the Compose default; retain inline mode for one release.
- [x] Refuse/warn on multiple SQLite workers and require PostgreSQL for scale.

### Phase 4 — Credentials and source materialization

- [x] Add credential scopes, handlers, encrypted structured payloads, masking,
      ownership, and allowed-host enforcement.
- [x] Preserve legacy secret endpoints through adapters.
- [x] Add source request validation/canonicalization and concurrency-safe repo upsert.
- [x] Materialize one-shot SSH details as caller-owned remote nodes with strict
      host-key verification.
- [x] Keep decrypted credentials only in target preparation.
- [ ] Add one exhaustive sentinel leak test spanning responses, rows, audit
      events, logs, SSE, and artifacts. Response/row/known-host masking tests
      and a live response check are complete; the single cross-surface sentinel
      harness remains future hardening.

### Phase 5 — Generic Git and SSH resolution

- [x] Support public/private Git HTTPS and Git-over-SSH.
- [x] Permit approved schemes only and reject local/custom helper transports.
- [x] Resolve/revalidate DNS; block loopback, link-local, multicast, Unix sockets,
      and metadata destinations by default.
- [x] Permit self-hosted RFC1918 sources subject to operator CIDR policy.
- [x] Enforce credential host bindings.
- [x] Resolve refs to detached immutable commits and persist tree digest/provenance.
- [x] Use isolated per-scan workspaces and remove temporary credential files.
- [x] Preserve current registered local/GitHub/SSH behavior and GitHub-token fallback.
- [x] Enforce SSH base paths and the existing dirty-tree policy.

### Phase 6 — Full and targeted profiles

- [x] Extend manifest metadata with applicability and scope capabilities.
- [x] Implement standard/full/targeted planning while preserving legacy semantics.
- [x] Wire include/exclude paths into the runner.
- [x] Persist requested/effective scope and skip reasons.
- [x] Extend preflight without changing the current `missing` array contract.
- [x] Test legacy, standard, full, category, tool, path, exclusion, and unsupported
      scope scenarios.

### Phase 7 — Artifact/workspace abstraction

- [ ] Replace direct global filesystem use with artifact/workspace interfaces.
- [x] Implement shared-filesystem backends first.
- [x] Store stable relative keys while preserving current artifact response fields.
- [x] Separate source, runtime output, final artifact, and cache directories.
- [x] Mount source read-only and clean terminal/stale workspaces.
- [x] Preserve report, manifest, SARIF, raw output, and downloads.
- [x] Leave the storage interface ready for later S3 support.

### Phase 8 — Native Kubernetes runtime

- [x] Introduce a runtime-neutral invocation model: image, command, args, workdir,
      environment, mounts, stdin, resources, network class, timeout, scan/tool IDs.
- [x] Refactor Docker execution to consume the neutral model.
- [x] Preserve plugin `exec.Cmd`-style behavior to avoid rewriting every parser.
- [x] Add a hidden Wolf command that creates/watches Kubernetes scanner Jobs.
- [x] Use an init container to provide a small scanner-exec helper to upstream images.
- [x] Capture stdout/stderr separately on the RWX workspace and reproduce the real
      tool exit code for existing plugins.
- [x] Extend upstream-image metadata with explicit Kubernetes commands.
- [x] Mount source read-only and a per-tool result directory read-write.
- [x] Apply resource limits, non-root security context, read-only root, dropped
      capabilities, no privilege escalation, and RuntimeDefault seccomp.
- [x] Label Jobs by scan/tool/user/network/lease.
- [x] Ship default-deny and network-required NetworkPolicies.
- [x] Implement timeout, cancellation, result collection, foreground cleanup, and
      abandoned-Job reconciliation.
- [x] Add least-privilege worker RBAC.
- [x] Require PostgreSQL and RWX storage for Kubernetes.
- [x] Add Helm manifests for API, workers, PVCs, Services, RBAC, NetworkPolicies,
      probes, and autoscaling.
- [x] Expose runtime capabilities so Docker-only UI actions disable cleanly.

### Phase 9 — Headless API, OpenAPI, and docs

- [x] Add `wolf serve --api-only` and `WOLF_API_ONLY=true`.
- [x] Disable only SPA mounting; keep API/OpenAPI available.
- [x] Fully document legacy and new scan request fields and credential flows.
- [x] Add examples for public/private Git, registered/one-shot SSH, full, targeted,
      idempotency, polling, SSE, cancellation, and result retrieval.
- [x] Document Compose constraints and Kubernetes PostgreSQL/RWX/RBAC requirements.
- [x] Make missing OpenAPI request fields fail CI.

### Phase 10 — Rollout

- [ ] Deploy additive migrations before queue execution.
- [ ] Deploy workers and verify heartbeats before enabling queue mode.
- [ ] Canary local, GitHub, SSH, explicit-tool, AI, cancel, and failure UI flows.
- [ ] Verify API and worker restart recovery.
- [x] Make queue mode the Compose default.
- [ ] Support Kubernetes only after native Job integration tests pass.
- [x] Retain inline rollback mode for one release, then remove it.

## Validation matrix

Backend:

- [x] Fresh and upgraded SQLite/PostgreSQL migrations.
- [x] Concurrent claim, lease expiry, stale-worker rejection, retry exhaustion.
- [x] Idempotent duplicate/conflicting submissions.
- [x] Whole/tool cancellation and durable SSE replay.
- [x] Source/credential ownership, host binding, encryption, and redaction.
- [x] Profile/scope planning and artifact/workspace cleanup.

UI:

- [ ] Scan from scans page, repository page, and bulk toolbar.
- [x] Docker image preflight behavior is preserved by component regression tests.
- [ ] Immediate navigation from the `201` response.
- [ ] Pending/running polling and queued/running/terminal tool transitions.
- [ ] Partial findings, filters, suppressions, report, SARIF, manifest, raw output,
      and artifact downloads.
- [ ] Whole and per-tool cancellation.
- [ ] API-created repositories/scans render in existing pages.
- [x] Kubernetes capability mode disables unsupported Docker actions without errors.

Compose:

- [x] API + one SQLite worker.
- [x] API + multiple PostgreSQL workers.
- [ ] Local, private Git, and SSH live scans (local live; private Git/SSH covered
      by automated resolver and route tests).
- [ ] API/worker termination during preparation and scanning.
- [ ] Hardened socket-proxy deployment and existing image-management UI
      (configuration renders successfully; interactive UI flow not run).

Kubernetes:

- [ ] Kind install with PostgreSQL and RWX test storage.
- [ ] Concurrent scans create independent scanner Jobs with correct resources.
- [ ] Read-only source, stdout/stderr parity, cancellation, lease recovery, and no
      duplicate claims.
- [ ] Offline/network-required egress policies.
- [ ] Job/workspace cleanup and artifact retention.
- [ ] API Pods have neither Docker sockets nor scanner-Job RBAC.

Required commands:

- [x] `go test ./...`
- [x] `go vet ./...`
- [x] Queue/event/worker packages under `go test -race`
- [x] `pnpm --dir ui typecheck`
- [x] `pnpm --dir ui test`
- [x] `pnpm --dir ui build`
- [x] Docker scanner smoke test
- [x] Compose end-to-end test
- [ ] Kind end-to-end test
- [x] OpenAPI route/schema coverage
- [ ] Secret-leak scan over logs, rows, events, and artifacts

## Definition of done

- [ ] Existing UI workflows pass without request, response, status, or navigation
      changes.
- [x] Existing `/api/v1/scans` clients remain compatible.
- [x] API-created Git/SSH sources appear as normal repos with immutable provenance.
- [x] Submission returns before network preparation/execution.
- [x] API restarts do not cancel/lose scans.
- [x] Worker crashes do not duplicate completed work or permit stale finalization.
- [x] Docker Compose uses separate workers.
- [ ] Kubernetes uses native Jobs without Docker sockets/DinD.
- [x] Full/targeted scans accurately report effective scope.
- [x] Credentials are encrypted, referenced by ID, and omitted from public
      responses/events/artifact metadata.
- [x] Polling/SSE and cancellation work across processes.
- [ ] All backend, UI, Compose, Kubernetes, OpenAPI, security, and regression checks
      pass.

## Deferred work

Archive uploads, object-source ingestion, S3 artifacts, webhooks, hosted
multi-tenancy, billing, and external secret-manager integrations are intentionally
deferred. Docker-specific image management remains available in Compose; Kubernetes
uses capability-based disablement until a registry-native build service is designed.
