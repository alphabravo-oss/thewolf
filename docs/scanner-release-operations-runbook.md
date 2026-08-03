# Scanner release operations runbook

This runbook covers the durable scanner release factory after the database
migrations are installed. It does not authorize moving a deployment into
`canary` or `stable_control`; those changes require the qualification evidence
and approvals defined by the active release policy.

## Operating objectives and alerts

Recommended initial objectives:

| Signal | Warning | Critical |
|---|---:|---:|
| Last completed discovery | older than 36 hours | older than 72 hours |
| Weekly candidate | absent after catch-up window | absent for 10 days |
| Queue age | older than 15 minutes | older than 60 minutes |
| Lease churn | 3 reclaims in 15 minutes | attempt budget exhausted |
| Stable release age | policy-specific | policy maximum exceeded |
| Registry parity | one mirror degraded | no verified source available |
| Worker release drift | any after rollout deadline | signature/manifest verification failure |
| Canary health | threshold approaching | any hard gate or rollback threshold crossed |

Alert labels must remain bounded. Use state, trigger, risk, component type,
step class, gate, target class, cohort, and failure class. Never use a release
ID, candidate ID, tool version, worker ID, URL, or actor as a metric label.

Every page should link to the durable aggregate and its audit-safe events.
Notification delivery failure is secondary: do not reverse a completed
discovery, publication, or rollout transition because email, webhook, or SIEM
delivery failed.

## Alert evaluator operation and response

The durable evaluator is disabled operationally until its worker is deployed,
and every policy alert is disabled until explicitly enabled. Derive thresholds
from measured queue/service baselines and the active release objective; do not
copy warning values from this runbook without confirming they fit the
installation.

Pre-enable validation:

1. Validate the next policy revision with `wolf scanner policy validate`.
2. Confirm each enabled duration is between one minute and 365 days and each
   count/depth is bounded.
3. Confirm a completed discovery exists before enabling `missed_discovery`,
   and a stable release exists before enabling `stale_stable_release`.
4. Start one alert replica, confirm its database-only egress and readiness,
   then scale to the intended replica count.
5. Confirm only one completed schedule lease exists per evaluation period.
6. Exercise a synthetic open, recovery, and reopen and verify one lifecycle
   event/outbox record per destination at each transition.

Triage:

```bash
wolf scanner alert list --severity=critical
wolf scanner alert show <id>
wolf scanner audit list --limit=200
```

- `missed_discovery`: follow **Upstream source outage** and scheduler health.
- `stale_stable_release`: inspect blocked/held updates and candidate gates;
  never publish solely to clear release age.
- `queue_backlog`: identify the queue in evidence, then check worker
  readiness, oldest work, capacity, and external provider health.
- `lease_churn`: check database latency, clock synchronization, resource
  starvation, restarts, and the heartbeat-to-lease ratio.
- `repeated_gate_failure`: compare the window evidence with candidate and
  build audit records before changing inputs or policy.
- `mirror_drift`: treat a conflicting digest as an integrity incident and
  follow **Registry outage or mirror drift**.
- `rollout_failure`: pause assignments and follow **Canary failure and
  rollback**.
- `signature_health`: stop new assignment, move control to read-only if trust
  is uncertain, and follow **Signature, provenance, or compromised-tool
  incident**.

An alert resolves only when a later evaluation has a valid baseline and the
condition is below threshold. Removing a baseline or disabling a rule does not
manufacture recovery. Do not edit alert rows, fingerprints, events, or outbox
records manually. Fix the source condition; the next leased period will
resolve it. If the evaluator is degraded, stop the suspected owner, wait for
lease expiry, and allow a replica to reclaim it.

## Daily checks

1. Open **Scanners → Overview** and confirm discovery coverage is complete or
   that every hold/unsupported source has an owned exception.
2. Confirm scheduler, discovery, proposal, build, registry, rollout, and
   notification queues have no items older than their objective; review every
   registry or notification dead letter.
3. Confirm every active worker reports the desired release, verified manifest,
   and no verification error.
4. Check primary and mirror observations for digest parity.
5. Review candidates that are blocked, awaiting approval, or exhausting retry
   attempts.
6. Review exceptions expiring within the policy notification window.

## Weekly image rebuilds and vulnerability database refresh

The repository workflow `wolf-scanners release factory` has two independent
schedules:

- `02:17 UTC` daily: read-only upstream discovery plus a review artifact that
  resolves both Trivy database tags to exact digests and proposes eight-day
  lock replacements. It does not mutate Git, publish an image, or move a
  channel.
- `03:43 UTC` Sunday: a no-cache, complete scanner/fixer candidate rebuild
  from the last reviewed immutable scanner lock. New version or OS-package
  proposals remain separate review work and do not silently suppress the
  weekly freshness rebuild.

Before approving the daily database patch, validate it offline and inspect
the exact identities:

```bash
make scanners-vulnerability-dbs-check
go run ./cmd/scannertools vulnerability-dbs --check --json
```

For an authorized on-demand database proposal, dispatch
`refresh-vulnerability-dbs`. For local proposal preparation, use:

```bash
make scanners-vulnerability-dbs-refresh
git diff -- scanners/quality scanners/scanner-lock.yaml internal/scannerbuild/context
make scanners-validate
```

The refresh resolves `trivy-db:2` and `trivy-java-db:1` independently, records
exact SHA-256 digests and UTC validity bounds, synchronizes the embedded build
context, and regenerates the canonical scanner lock. Image jobs load their
database repository references from these reviewed locks at runtime; no
second hard-coded digest can drift. If either lock is stale, mismatched, or
cannot be resolved, validation and candidate publication fail closed.

On-demand candidate/security rebuilds may move only the `candidate` alias.
Only the `release` operation may request `stable`; it requires the protected
`scanner-release` environment, a content-addressed approval receipt, and a
required Docker Hub mirror. Publication verifies the release ID, scanner-lock
digest, definition digest, exact platforms, signatures, attestations, SBOMs,
and OCI referrer inventories before any channel alias moves.

## Trace an operation across components

Start with the `X-Wolf-Operation-ID` or `X-Wolf-Trace-ID` returned by the API,
or the corresponding fields on a scanner release audit event. Query structured
logs for that exact identifier across `api`, `scheduler`, `discovery-worker`,
`proposal-worker`, `build-worker`, `registry-worker`, and
`rollout-controller`. Durable events retain the same correlation even after a
lease is reclaimed by another replica.

An absent correlation on an event created before migration 045 is expected.
The next state transition creates a durable correlation for that legacy
aggregate. A new operation ID after a retry of a post-migration aggregate is
not expected: check database restore history and the
`scanner_operation_correlations` row before retrying or mutating the resource.
Do not place trace or operation IDs in metric labels.

## Upstream source outage

Symptoms:

- a discovery is completed with partial coverage or fails with zero coverage;
- multiple items share `unreachable`, `rate_limited`, or authentication error
  classes;
- the weekly candidate refers to a pending latest discovery.

Response:

1. Do not mark affected components current.
2. Verify source status using a credential-free request from the discovery
   worker network. Do not copy tokens into command-line arguments.
3. Check per-host concurrency, response retry guidance, DNS, proxy, and CA
   configuration.
4. Retry with the same component scope and a new operator idempotency key after
   the upstream recovery.
5. For a critical advisory, use the security-triggered on-demand path. It
   changes priority, not evidence or approval gates.
6. If a manual hold is required, record owner, reason, compensating control,
   and expiration in policy/definition data.

## Proposal or Git failure

1. Inspect candidate proposal events and the redacted executor error class.
2. If the base branch moved, regenerate from the new immutable definition
   commit. Do not force-push.
3. If a proposal branch changed, inspect the human edits and either:
   - accept them through normal review, then create a new candidate bound to
     the resulting commit and lock; or
   - create a new branch from the expected definition commit.
4. If Git writes are unavailable, export the deterministic patch bundle,
   verify its digest, apply it in a controlled checkout, and bind the candidate
   to the reviewed commit.
5. A deleted branch or closed pull request is not silently recreated over the
   old candidate identity; record supersession and create a new proposal.

## Build or evidence failure

1. Identify the failed durable step and attempt. Never infer success from an
   external CI log alone.
2. Verify the build worker still owns the lease. A stale worker must not
   publish a late result.
3. Retry only steps marked retryable and idempotent. Checkout, lock, policy,
   signature, parser, platform, source, and secret-scan failures require a new
   corrected input or explicit new candidate.
4. Treat missing required evidence as a hard block.
5. For resource exhaustion, increase only the affected isolated backend limit;
   keep total concurrency within registry, database, and artifact-storage
   capacity.
6. Preserve evidence summaries and redacted logs. Large raw artifacts remain
   content-addressed with their retention class.

## Registry outage or mirror drift

1. Run the connectivity check and the compatibility-preserving synchronous
   comparison first:

   ```bash
   wolf scanner registry check <mirror-id>
   wolf scanner registry reconcile <mirror-id> --release=<release-id>
   ```

   Distinguish connectivity, authorization, missing platform, digest mismatch,
   and trust-evidence failure.
2. Do not rewrite an immutable tag. A conflicting digest is an integrity
   incident.
3. If the primary remains verified and policy permits a degraded mirror,
   publication may remain valid while rollout waits or uses the verified
   target.
4. Queue repair from an already verified immutable source. `preserve` copies
   the recorded signature/provenance/SBOM closure; `required` fails closed
   unless an authorized re-sign adapter is configured:

   ```bash
   wolf scanner registry repair <mirror-id> \
     --release=<release-id> \
     --source=<primary-registry-id> \
     --re-sign-policy=preserve \
     --reason="incident INC-123: restore exact release closure" \
     --idempotency-key=INC-123-mirror-repair
   wolf scanner registry jobs --registry=<mirror-id>
   wolf scanner registry job <job-id>
   wolf scanner registry job-events <job-id>
   ```

   Completion requires exact source and destination manifest digest readback
   plus exact signature, provenance, and SBOM referrer digest readback.
5. Keep channel aliases unchanged until immutable aggregate verification is
   complete.
6. A copy interrupted after content upload records the attempted immutable
   object in durable quarantine with a minimum retention window. Inspect it
   before cleanup:

   ```bash
   wolf scanner registry quarantine --registry=<mirror-id>
   wolf scanner registry cleanup <mirror-id> \
     --reason="INC-123 closed; expired unreferenced partial publishes" \
     --idempotency-key=INC-123-quarantine-cleanup
   ```

   Cleanup re-authorizes every object immediately before an exact-digest
   delete. It refuses protected objects, unexpired retention, every digest
   present in release image/evidence/build records, and every candidate already
   published as a release. Release publication and evidence recording also
   refuse to race an object in `deleting`. Never edit quarantine state to
   bypass these checks.
7. A `dead_letter` registry job or `delete_failed` quarantine object contributes
   to the existing critical `mirror_drift` alert. Repair credentials, egress,
   registry capability, or signer authority, then retry with the current ETag:

   ```bash
   wolf scanner registry retry-job <job-id> \
     --if-match=<version> \
     --reason="registry write policy repaired" \
     --idempotency-key=INC-123-retry-1
   ```

The administrative UI exposes the same durable workflow under **Scanners →
Registries → Reconciliation jobs** and **Quarantine inventory**. It requires
typed destination confirmation for repair and cleanup, preserves the existing
registry target/configuration workflow, and never displays raw worker payloads.
See [Scanner registry operations UI](scanner-registry-operations-ui.md).

### Registry worker deployment

The registry worker is opt-in and has no Docker/Kubernetes control-plane
socket. It needs database access plus egress only to configured registry and
token-service origins.

- Compose: `docker compose --profile scanner-release-registry up -d
  scanner-release-registry`
- Helm: set `scannerRelease.registry.enabled=true`; configure the bounded
  `egressPorts` list if registries do not use 443.
- Direct: `wolf scanner-release-worker --role=registry`

The default heartbeat is 10 seconds, the lease is 45 seconds, and one
operation is bounded at 30 minutes. The lease must remain longer than twice
the heartbeat. Scale replicas for queue latency; leases and content-addressed
writes make competing replicas safe. Readiness and Prometheus component
metrics use `component="registry"`. Repeated process loss is reclaimed within
the attempt budget and then dead-lettered for explicit operator recovery.

## Signature, provenance, or compromised-tool incident

1. Move scanner release management to `read_only` if the trust boundary is
   uncertain.
2. Revoke the affected release with a reason and explicit active-scan impact
   policy.
3. Stop new assignment immediately. Identify queued and active scans by their
   persisted release and image digests.
4. Roll back to a verified, rollback-eligible release whose artifacts remain
   present.
5. Rotate compromised Git, registry, and signing identities as applicable.
6. Reconcile every protected release against the new trust policy.
7. Preserve the revoked release, evidence, approval, and events for forensics.
8. Build a security-rebuild candidate from reviewed inputs; emergency priority
   never disables hard gates.

## Canary failure and rollback

Automatic rollback should trigger on any configured signature, manifest, pull,
parser, finding-loss, crash-loop, infrastructure-rate, duration, resource, or
deadline threshold.

Manual response:

1. Pause the rollout at a safe assignment boundary.
2. Confirm the prior release is present, verified, and rollback eligible.
3. Request rollback with the current rollout version and a concrete reason.
4. Observe cohorts restore in reverse order and wait for desired/observed
   convergence.
5. Confirm new assignments use the restored release. Active scans retain their
   original snapshot unless the revocation policy explicitly cancels them.
6. Record affected workers/scans, trigger evidence, start/end times, and
   measured recovery duration.
7. Do not resume the failed release. Correct it through a new candidate.

## Worker loss and lease recovery

- Build, discovery, proposal, notification, registry, and rollout ownership
  use opaque lease tokens.
- Never alter a lease row manually while a process may still be running.
- Stop the suspected process, wait for lease expiration, and let another
  replica reclaim it.
- A claimed-but-not-running safe operation can requeue within its attempt
  budget. An executing build is classified as worker loss rather than silently
  replayed.
- Repeated lease loss usually indicates clock, database latency, process
  starvation, or undersized heartbeat/lease configuration. Lease duration must
  exceed twice the heartbeat interval.

## Notification backlog or dead letter

1. List the affected records with
   `wolf scanner notification list --state=dead_letter` and inspect one with
   `wolf scanner notification show <id>`.
2. Preserve the notification ID. It is the stable provider idempotency key;
   do not create an ad hoc replacement message with a different identity.
3. Distinguish `adapter_not_configured`, invalid destination/response, provider
   rejection, timeout, and retry-budget exhaustion. Inspect provider logs
   using the opaque destination alias; never paste provider credentials into
   the retry reason.
4. Repair the deployment-owned alias mapping, adapter image, secret reference,
   CA/proxy, or egress policy. Do not edit the immutable notification payload,
   event, destination, or policy revision in the database.
5. Retry with the record's current ETag, a concrete reason, and an
   incident-scoped idempotency key:

   ```bash
   wolf scanner notification retry <id> \
     --if-match=<version> \
     --reason="webhook alias mapping repaired" \
     --idempotency-key=<incident-id>-<notification-id>
   ```

6. Confirm the state becomes `delivered`, the queue-depth gauge falls, and the
   append-only audit history contains `notification.operator_retry`,
   `notification.claimed`, and `notification.delivered`.
7. If the provider accepted the original request but Wolf lost its lease or
   database connection, verify that the provider deduplicated on the
   notification ID before treating a second acceptance as an incident.

Never reverse the underlying release transition solely to clear a notification
failure. A dead letter is an operational delivery failure, not evidence that
the source transition failed.

## Backup, restore, and disaster recovery

The release backup command is an offline control-plane recovery boundary, not
a public API endpoint and not a substitute for the database platform's normal
backup. It exports the complete scanner release domain in dependency order:
policies, registry and signer references, discovery, candidates, builds,
immutable releases and artifacts, approvals, rollouts and worker observations,
events, operation correlations, schedules, alerts, notifications, and registry
reconciliation/quarantine state, and durable custom-build aggregates, variant
results, and bounded logs. Format version 1 requires migration 046.

It deliberately excludes:

- application secret rows and plaintext secret values;
- the restore-maintenance lease;
- backup/restore audit rows;
- scan, repository, user, and finding data outside the release domain.

Registry credential IDs, signer/KMS references, trust-policy references, and
OCI locations remain opaque references. Restore their backing secret-manager
objects through the deployment's secret recovery process. All release-worker
lease capabilities are cleared in the backup. Active lease expirations are
reset to the Unix epoch so replacement workers can reclaim them immediately;
an interrupted quarantine deletion is restored as `delete_failed` and must be
authorized again.

### Create and verify a release backup

Use a dedicated filesystem with encryption, retention, and access controls.
The destination must be a new file; the command refuses to overwrite an
existing backup and publishes a fully synced file with mode `0600`.

```bash
wolf scanner-release-backup export \
  --output=/secure-backups/scanner-release-2026-07-30.json \
  --actor=backup-automation@example.test \
  --reason="weekly release control-plane backup; change CHG-1234" \
  --idempotency-key=CHG-1234-release-export-2026-07-30
```

Capture the printed `payload_sha256`, format version, per-table counts, command
exit status, and protected-storage object version. A completed export
idempotency key cannot be reused because the database intentionally does not
retain another copy of the payload. The JSON document is bounded at 512 MiB on
read; use the database platform's encrypted snapshot mechanism as the primary
path if the release domain approaches that size.

The embedded SHA-256 identities detect truncation and accidental corruption;
they are not an artifact signature. Store the file in an authenticated,
versioned/WORM-capable backup system, compare its digest with a separately
protected change record, and apply the organization's backup-signing control
where malicious replacement is in scope.

Create the database snapshot, Git mirror, and immutable artifact-storage
snapshot in the same recovery window. The recovery set is:

- the operational SQLite/PostgreSQL backup for non-release application data;
- this versioned release-domain export;
- scanner definition Git history;
- immutable OCI images and release manifests;
- SBOM, provenance, signature, and large evidence objects;
- offline trust roots and encrypted secret-manager objects.

The database alone cannot reconstruct missing registry artifacts. Registry
artifacts alone cannot reconstruct approvals, correlations, or operational
audit history.

### Restore preflight

Provision an isolated target at migration 046 or later. Restore non-release
application records through the normal database recovery path, but leave every
scanner release table empty. Existing active scan rows are allowed and are
never part of this import; their pinned release IDs and manifest digests are
not reassigned.

Keep API and worker traffic drained. Preflight is read-only and checks the
format/version, completeness marker, global and per-table SHA-256 identities,
exact table/column schema fingerprint, row widths and value types, and that
the entire target release domain is empty:

```bash
wolf scanner-release-backup preflight \
  --input=/secure-backups/scanner-release-2026-07-30.json
```

`restorable` must be `true`, every reason list must be empty, the reported
payload digest must match the export record, and the target backend must be the
intended recovery database. Corrupt, truncated, unknown-version, partial,
schema-mismatched, or non-empty-target backups fail closed before any release
row is written.

### Execute restore

Stop scanner release workers before restore and confirm no release mutation is
in flight. The exact confirmation phrase is intentionally cumbersome:

```bash
wolf scanner-release-backup restore \
  --input=/secure-backups/scanner-release-2026-07-30.json \
  --actor=dr-operator@example.test \
  --reason="isolated recovery exercise; incident INC-4567" \
  --idempotency-key=INC-4567-release-restore-1 \
  --confirm=RESTORE_SCANNER_RELEASE_STATE
```

Restore obtains a 30-minute database-backed maintenance lease, rechecks the
empty target and schema inside a serializable transaction, and excludes
concurrent release writes. PostgreSQL takes exclusive locks on the exact
release tables; SQLite obtains its database write lock. Rows are inserted in
referential order, reread, and compared against every table digest before
commit. SQLite additionally runs `foreign_key_check`. Any error rolls back the
transaction and records bounded failure evidence outside it.

During the current maintenance lease:

- `/health` remains live;
- `/ready` returns `503`;
- release-worker readiness is false;
- `wolf_scanner_release_restore_maintenance_active` is `1`;
- the release status snapshot reports `restore_maintenance`.

Inspect maintenance and the durable audit trail with:

```bash
wolf scanner-release-backup status --limit=100
```

A replay with the same restore key and same payload returns the original
completed operation without writing again. Reusing that key for another
payload fails. A failed operation may be retried with the same key only for the
same payload after correcting the transient cause. Never edit recovery or
maintenance rows manually. An abandoned lease stops affecting readiness after
its expiry; investigate the owner and then retry through the command.

### Post-restore validation

1. Confirm maintenance returned to `normal` and readiness recovered.
2. Compare restored table counts, payload digest, and audit operation ID with
   the change/incident record.
3. Resolve every protected and rollback-eligible release manifest and image
   from its recorded OCI location by exact digest. Verify platform, SBOM,
   provenance, and signature identities; do not move a channel.
4. Confirm registry and signer references resolve through the recovered secret
   manager without placing credential values in logs or backup evidence.
5. Start one worker replica per role. Confirm expired/sanitized scheduler,
   proposal, build, registry, and rollout ownership is reclaimed normally.
6. Confirm restored operation correlations match event trace/operation IDs.
7. Compare active scans before and after recovery. Status, phase, scan lease,
   pinned release, manifest digest, and timestamps must remain unchanged.
8. Run a new synthetic scan pinned to an exact restored release. Only after
   that passes, exercise canary and rollback under the normal approval policy.

The repository contains a deterministic, non-production DR contract using
separate source/target databases and a fake immutable OCI registry:

```bash
go test ./internal/db \
  -run TestScannerReleaseBackupRestoreContractSQLite \
  -count=1

WOLF_TEST_POSTGRES_DSN='<disposable postgres DSN>' \
  go test ./internal/db \
  -run TestScannerReleaseBackupRestoreContractPostgres \
  -count=1
```

The PostgreSQL test creates and drops two unique schemas and must run only
against a disposable qualification database. Passing the deterministic test
does not constitute a production disaster-recovery drill.

Every migration that adds or changes a scanner release table must review the
compiled backup allowlist, dependency order, portable value encoding, lease
sanitization, and DR fixtures. A semantic format change requires a new format
version and explicit compatibility path; never make an older binary guess how
to restore a newer document. A column-only mismatch fails the schema
fingerprint, while a missing domain table fails the exact table-set check.

### Recovery objectives and drill evidence

Set the export/database/artifact cadence from the approved recovery-point
objective; weekly release-image refresh does not itself define an acceptable
database RPO. Run an isolated restore validation at least quarterly and after
format, schema, registry, signer, or secret-manager recovery changes. Record:

- source and target backend/version, migration, recovery timestamp, and RPO;
- payload digest and per-table counts;
- start, preflight, commit, readiness recovery, and validation timestamps;
- missing/unavailable Git, OCI, signature, provenance, SBOM, or secret objects;
- unchanged active-scan evidence;
- release/rollout reconciliation results;
- measured RTO, deviations, owners, and corrective-action due dates.

## Capacity and retention guidance

- Size discovery for the number of update sources, with low per-host
  concurrency to avoid upstream throttling.
- Size proposal workers for Git latency; one active proposal per worker is the
  safe default.
- Build concurrency is constrained by the largest platform image, QEMU cost,
  registry push bandwidth, and evidence storage. Start with two dependency-ready
  steps per worker and scale replicas by platform.
- PostgreSQL is recommended for multiple scheduler/worker replicas. SQLite is
  appropriate for one-node operation with serialized short writes.
- Keep stable and at least one prior verified release protected for the full
  rollback window. Keep revoked artifacts for forensics.
- Store bounded summaries in the database and large logs/reports in
  content-addressed artifact storage.
- Run retention in preview mode first. Never delete an artifact referenced by
  a stable, active-rollout, rollback-eligible, legal-hold, or active-scan
  release.

## Controlled enablement and rollback

Advance one mode at a time:

1. `disabled` → validate legacy inventory and scan regression tests.
2. `read_only` → observe daily discovery, health, audit, and no runtime writes.
3. `candidate` → exercise proposals and full evidence builds without rollout.
4. `canary` → complete a production-like canary and rollback drill.
5. `stable_control` → enable stable assignment only after all definition-of-done
   gates are recorded.

To retreat, move to the immediately lower capability mode and leave the
database/artifacts intact. Turning off the UI or API mutation surface does not
change a worker’s already assigned immutable scan snapshot.
