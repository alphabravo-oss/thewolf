# Scanner registry operations UI

The registry administration surface exposes the durable reconciliation API
without changing the existing target configuration or synchronous connectivity
workflow. The default `/scanners?tab=registries` route still opens **Targets**.
The additive workspaces are:

- `registry_view=jobs` for reconciliation, repair, cleanup, retry, exact image
  evidence, and resumable events;
- `registry_view=quarantine` for the read-only retained-object inventory and
  guarded cleanup entry point.

The selected target, job, kind/state filters, and quarantine state are encoded
in the URL. A browser refresh or shared administrative link therefore restores
the same bounded view.

## Authorization and safety

All registry reads remain available when scanner release management is in
observe-only mode. Reconcile, repair, cleanup, and retry controls require the
existing candidate-mode registry-management capability. A UI control never
expands the server-side permission:

- every command sends a fresh `Idempotency-Key`;
- dead-letter retry sends the exact loaded job `ETag` as `If-Match`;
- every command requires an audit reason;
- repair requires an exact typed destination-registry name;
- cleanup requires an exact typed destination-registry name;
- source and destination cannot be the same repair target;
- the UI never offers an individual quarantine-object delete.

The cleanup confirmation does not assert that the visible object is safe to
delete. It queues a durable cleanup job. The worker re-authorizes every object
inside its deletion transaction against protection, retention, state, release
images, release artifacts, release manifests and locks, release tools, approval
evidence, candidate locks, build outputs, and published candidates.

## Job operations

The **Reconciliation jobs** workspace supports the full durable lifecycle:

| Operation | Required inputs | Result |
| --- | --- | --- |
| Reconcile | destination, published release, reason | Read-only exact manifest and OCI referrer readback |
| Repair | destination, verified source, published release, re-sign policy, reason, typed destination | Digest-idempotent copy followed by exact destination readback |
| Cleanup | destination, reason, typed destination | Retention- and reference-gated exact-digest deletion job |
| Retry | dead-lettered job, current ETag, reason | Same durable job returned to the bounded retry lifecycle |

List filters cover target, kind, and state. The selected job view shows
destination/source identity, release scope, re-sign policy, attempt budget,
actor, audit reason, timestamps, and terminal state. An expired active lease is
reported as stale and directs the operator to durable recovery instead of
queueing a duplicate.

Worker-owned `summary` JSON is not rendered. Failure presentation is limited to
the bounded `error_class` and an allowlisted remediation message. Raw
`error_detail` is intentionally excluded from the UI model.

## Exact evidence and events

Each persisted image observation displays separate expected, source, and
destination digest columns for:

- immutable image manifest;
- signature referrer;
- provenance referrer;
- SBOM referrer.

An evidence row is **Verified** only when the expected and destination digests
are equal and, when a source observation exists, the source also equals the
expected digest. Missing evidence is shown as pending rather than inferred.
Worker-owned image `detail` JSON is not rendered.

Registry job events use the existing authenticated fetch-based SSE reader. It
reconnects with `Last-Event-ID`, drops duplicate sequences, retains at most 500
events in the browser session, and replays persisted events once for terminal
jobs. Only event type, state, actor, reason, timestamp, and bounded correlation
identifiers are displayed. Event payload JSON is never rendered. When a trace
or operation identifier is available, the job links directly to the exact
Audit filter.

## Quarantine inventory

The quarantine workspace is read-only and filterable by registry and lifecycle
state. Each object exposes only its repository, digest, kind, lifecycle state,
protection bit, retention class/window, discovery time, last-reference time,
and bounded version/timestamps. Worker metadata and raw deletion errors are
excluded from the UI model.

Eligibility is deliberately labeled **provisional**:

- visible protection, retention, and lifecycle blocks are explained exactly;
- an object with no visible block is only “potentially cleanup eligible”;
- database-reference authorization is always described as pending until worker
  execution;
- stale deletion leases direct the operator to worker recovery.

## Loading and recovery states

Job and quarantine lists poll bounded endpoints independently. The UI preserves
the last successful list if a background refresh fails and labels it as stale.
List loading, empty filters, terminal detail absence, detail fetch failure,
active evidence absence, SSE reconnect, stale worker lease, and capability
denial have distinct states and retry/remediation text. A detail failure does
not remove the still-usable job history.

## Validation evidence

The registry UI is covered by:

- typed client unit tests for filters, path encoding, ETag propagation, and
  idempotent command headers;
- component tests for exact evidence, audit correlation, typed repair
  confirmation, observe-only capability behavior, provisional eligibility, and
  raw summary/detail/event/metadata exclusion;
- one complete Playwright registry journey at both 1440×1000 and 390×844,
  including axe WCAG checks, focus behavior, typed repair and cleanup
  confirmations, dead-letter retry, exact evidence, bounded remediation,
  secret canaries, and page-width containment;
- the existing all-panel desktop/mobile semantic, axe, and responsive matrix;
- the existing scanner settings visual and behavior regression test, which
  protects the pre-existing scanner UI.

Validated locally on 2026-07-30:

```text
pnpm typecheck                 passed
pnpm lint                      passed
pnpm test:e2e:typecheck        passed
pnpm test                      108 passed
pnpm build                     passed
pnpm test:e2e                  51 passed, 13 intentional viewport skips
```
