# Scanner release management architecture

Wolf treats scanner freshness as an independently operated supply chain. The
application API does not build images and an active scan never follows a
mutable image tag.

## Trust and ownership boundaries

| Boundary | Source of truth | Mutable data | Immutable data |
|---|---|---|---|
| Definition | Git | proposal branch and pull request | definition commit and `scanner-lock.yaml` digest |
| Operation | Wolf database | queue state, leases, desired rollout state | approvals, evidence digests, release events |
| Artifact | OCI registry/artifact storage | quarantine refs and channel aliases | image, platform, SBOM, provenance, signature, and release-manifest digests |
| Runtime | scan and worker records | desired release for future assignments | release snapshot assigned to a queued or active scan |

The release API accepts commands and exposes persisted state. Dedicated
process roles own discovery, definition proposals, build evidence, and rollout
reconciliation. Long-running work therefore survives an API restart.

```text
daily/weekly/on-demand command
             |
             v
        durable scheduler
             |
     +-------+--------+
     |                |
 discovery       weekly candidate
     |                |
     +-------> proposal worker
                      |
               immutable lock/commit
                      |
                durable build DAG
                      |
       quarantine -> evidence -> policy
                      |
            approval / auto-approval
                      |
             atomic publication
                      |
             canary -> stable cohorts
```

## Release identity

A scanner release uses `scanner-set-YYYY.WW.N`, but the name is only a display
and channel-management identity. The security identity is the signed release
manifest digest. The manifest binds:

- the exact definition commit and lock digest;
- every Wolf-owned and upstream image digest;
- every supported platform digest;
- tool versions and parser compatibility;
- SBOM, provenance, test, vulnerability, license, and signature evidence;
- the policy revision and decision digest.

Every managed release contains eight Wolf-owned images: scanner `default`,
`jvm`, `rust`, and `codeql`, plus fixer `base`, `api`, `claude`, and `codex`.
The durable inventory records `image_kind` as `scanner` or `fixer`. Existing
pre-field inventories normalize to `scanner`; new publication rejects an
unknown kind, an incomplete owned-image set, a missing platform, or divergent
digests for the same image across registry targets.

Fixer engines are first-class supply-chain artifacts but never scanner runtime
assignments. The factory publishes and verifies `fixer-base` first. API,
Claude, and Codex engine builds receive only its exact
`repository@sha256:digest` reference, record that reference in their metadata,
and fail aggregate-manifest creation if it differs. All eight images receive
final-image vulnerability/secret/license scans, SPDX SBOMs, provenance,
signatures, immutable tags, optional mirror parity, and offline-bundle
coverage. Rollout, cache preparation, synthetic scanner health, and per-scan
image selection filter the inventory to `image_kind=scanner`.

Wolf allocates the ISO-week sequence inside the same database transaction that
publishes the immutable release inventory and advances the candidate. The
reservation is candidate-bound and survives idempotent retries; a failed
publication rolls the reservation and counter update back.

Publication is intent-only at the API boundary. The caller supplies a reason,
an optional release name, and the exact `receipt_digest` exposed by the
completed candidate. The server reloads the completed durable build,
reconstructs the exact complete DAG from its immutable image/platform
snapshot, requires every expected latest step and evidence digest to be
successful, recomputes the canonical receipt
digest, checks its candidate/build/commit/lock/policy bindings, and obtains the
manifest, signer identity, 49-tool inventory, eight owned images, and artifacts
from that receipt. Approvals must bind the same receipt digest. Callers cannot
submit or override authoritative release inventory or signature status.

## Scoped candidate exceptions

Exceptions are append-only approval records, not edits to prior evidence. An
exception records the exact policy gate, accountable owner, separate approving
actor, justification, compensating control, failing evidence digest, and UTC
expiration. The API rejects creator-approved or owner-self-approved records,
past or over-policy expirations, gates the candidate policy does not allow,
and every non-bypassable supply-chain gate.

```bash
wolf scanner candidate exception CANDIDATE_ID \
  --gate vulnerability \
  --owner security-on-call \
  --reason "upstream advisory is not reachable in the scanner runtime" \
  --compensating-control "quarantine candidate and block external egress" \
  --evidence-digest sha256:... \
  --expires-at 2026-08-06T12:00:00Z \
  --idempotency-key exception-CANDIDATE_ID-vulnerability

wolf scanner candidate retry CANDIDATE_ID \
  --if-match CURRENT_VERSION \
  --idempotency-key retry-CANDIDATE_ID-after-exception
```

Retry preserves every prior attempt and enqueues a new complete DAG attempt
from the prior immutable image/platform snapshot; enqueue failures compensate
the candidate back to blocked rather than leaving it runnable without work. The trusted worker
discards executor-supplied exceptions, reloads the durable exception ledger,
and includes that exact set in the new policy-decision digest. Expiration or
any exception change therefore makes prior approvals stale.

Movable aliases such as `candidate` and `stable` are updated only after
immutable publication succeeds. A scan resolves an alias once, persists the
release and image digests, and uses that snapshot for its entire lifetime.

## Scan assignment, retries, and explicit re-scans

Worker recovery retries the same durable scan row. The persisted
`scanner_release_id` and `release_manifest_digest` are therefore unchanged
when a lease expires, a worker restarts, or desired release state moves during
a rollout.

Changing scanner releases is an explicit operation that creates a new scan:

```bash
wolf scan rescan-release SCAN_ID \
  --release RELEASE_ID \
  --reason "compare the newly approved scanner rules" \
  --idempotency-key change-review-42
```

The caller needs both `write:scans` and `operate:scanner-supply-chain`.
`POST /api/v1/scans/{id}/release-rescans` requires `release_id`, `reason`, and
an `Idempotency-Key`. The new row records `rescan_of_scan_id` and the selection
reason; the source row is never rewritten. Legacy, deprecated, and revoked
releases cannot be selected.

## Importing pre-control-plane configuration

Administrators can take an observe-only snapshot of the scanner image
configuration that existed before managed releases once release management is
in `candidate` mode or higher:

```bash
wolf scanner release import-legacy-config \
  --reason "record deployment state before enabling release control" \
  --digest default=sha256:... \
  --digest upstream-trivy=sha256:...
```

Configured digest references are accepted directly. A configured tag requires
an operator-supplied immutable digest for its image key; Wolf does not pull,
retag, or otherwise mutate the reference during import. Image keys are
`default`, `wolf-<tool>`, and `upstream-<tool>`.

The resulting release is marked `imported`, `legacy`, protected from cleanup,
not rollback eligible, and visibly `legacy_unverified`. Missing signature,
SBOM, build-provenance, and platform evidence are recorded as limitations.
The import is idempotent and never updates `desired_scanner_release_id`,
worker assignments, or queued/active scans. It is historical migration
evidence, not a runnable managed release.

## Process roles

`wolf scanner-release-worker` supports independently scalable roles:

- `scheduler`: timezone-aware daily discovery and weekly candidate enqueue;
- `discovery`: bounded upstream resolution with durable leases and partial
  coverage;
- `proposal`: deterministic Git or patch proposal creation;
- `build`: dependency-ordered evidence execution through a shell-free
  executor;
- `rollout`: canary/stable reconciliation, health gates, pause, and rollback.

The active policy owns scanner freshness, not the worker command line. Its
schedule document independently controls daily and weekly enablement,
timezone, jitter, catch-up, `maximum_stable_image_age`, and
`force_weekly_rebuild`. The default seven-day maximum means a weekly candidate
may record an audited no-op only when all discovered inputs are unchanged and
the current stable release is still within that age. Otherwise the scheduler
forces a complete rebuild of all four scanner and four fixer images. Operators
can edit these fields through the same revisioned Policy API and UI used for
maintenance windows; the next scheduler tick reloads the active revision.

Production deployments should separate roles and credentials. In particular,
the API, scheduler, discovery, and rollout processes do not require a Docker
socket or registry push/signing credentials.

## Managed factory operations

`.github/workflows/scanners-image.yml` is the complete managed factory:

- daily at 02:17 UTC it performs read-only freshness discovery;
- weekly on Sunday at 03:43 UTC it refreshes package evidence and runs a full
  candidate build with caches disabled for security-sensitive rebuilds;
- authorized dispatch supports validation, discovery, package refresh,
  candidate, security rebuild, protected release, and verification;
- scanner quality covers seven supported scanner/platform tuples;
- fixer quality covers all eight fixer/platform tuples, including non-root,
  strict version, auth-boundary, vulnerability, secret, license, and SBOM
  gates;
- publication commits four scanner images and the fixer base, then builds the
  three fixer engines from the verified exact base digest;
- the aggregate commit accepts exactly four scanner and four fixer variants,
  and channel aliases move only after that signed commit succeeds.

The factory uses GHCR as primary. Docker Hub mirroring can be disabled,
best-effort, or required; required mode fails closed when credentials or digest
parity are unavailable. Release and verification jobs re-read platform
manifests, signatures, provenance, and SBOM attestations from the registry.

The API/runtime, proposal-worker, and three managed release-lane adapter images
use a separate immutable transaction and aggregate so they do not expand or
change the canonical eight-image scanner/fixer release. See
[deployment image releases](deployment-image-releases.md).

## Operation and trace correlation

Every scanner supply-chain API response includes:

- `X-Wolf-Operation-ID`, the bounded durable workflow identity;
- `X-Wolf-Trace-ID`, the 32-character distributed trace identity; and
- `Traceparent`, the effective W3C version 00 trace context.

Clients may supply a valid W3C `Traceparent` and a safe
`X-Wolf-Operation-ID` (8–128 ASCII letters, digits, `.`, `_`, `:`, `/`, or
`-`). Malformed values are ignored and replaced; they are never reflected into
logs. Wolf persists the effective trace and operation with the first immutable
event for each aggregate and reuses it after queue retries, lease transfers,
API restarts, and worker restarts.

Structured scheduler, discovery, proposal, build, registry, and rollout logs
carry the same `trace_id`, `operation_id`, `component`, `aggregate_type`, and
`aggregate_id` fields. Error logs use bounded error classes and do not copy raw
tool output or credentials. Correlation identifiers are intentionally excluded
from Prometheus labels to preserve bounded cardinality.

## Compatibility modes

`WOLF_SCANNER_RELEASE_MODE` is an ordered capability boundary:

| Mode | Behavior |
|---|---|
| `disabled` | release-management routes are unavailable; existing scanner inventory and scan routes continue |
| `read_only` | supply-chain inventory, audit, evidence, and health are visible |
| `candidate` | discovery, proposals, policy, and candidate builds are enabled |
| `canary` | immutable publication and canary rollouts are enabled |
| `stable_control` | stable rollout, deprecation, and revocation controls are enabled |

`WOLF_SCANNER_LEGACY_BUILD_ENDPOINTS=true` keeps established synchronous
scanner build endpoints available during migration. They return deprecation
and successor headers. Set it to `false` only after all callers have moved to
durable Custom builds; the read-only inventory and normal scan flows are
unaffected.

The administrative UI and its legacy Settings rebuild entry points use the
first-class durable Custom build resources. See
[durable custom-build UI](scanner-custom-build-ui.md) for create constraints,
safe detail fields, reconnect/poll behavior, and automated qualification.

## Managed and sovereign operation

The managed factory publishes to GHCR and can verify a Docker Hub mirror.
Customer-managed factories use the same lock, evidence, and policy contracts
with a private OCI registry and their own Git/signing credentials. Air-gapped
installations transfer a deterministic signed bundle and verify it before any
release record or rollout is created.

See:

- [scanner release worker](scanner-release-worker.md)
- [managed/customer factory reproducibility](scanner-factory-reproducibility.md)
- [offline release bundles](scanner-release-offline-bundles.md)
- [persistence migration runbook](scanner-release-persistence-runbook.md)
- [operations runbook](scanner-release-operations-runbook.md)
- [complete implementation plan](superpowers/plans/2026-07-30-scanner-release-management-platform-plan.md)
