# Scanner release persistence migration runbook

This runbook covers scanner-release migrations
`030_scanner_release_management_*` through the current additive scanner
release migrations. These migrations are additive and do
not replace scanner definitions, release locks, or existing scan records.

## Migration boundary

- Migration 030 creates scanner release operational tables, indexes, database
  constraints, immutable-evidence triggers, event streams, and scheduler
  leases.
- SQLite and PostgreSQL use separate migration 030 files because their trigger,
  boolean, and timestamp syntax differ.
- Migration 031 adds `scanner_release_id` and
  `release_manifest_digest` to `scanner_run_records`.
- Migration 031 deliberately does not add a foreign key. Empty release
  provenance is valid for legacy and compatibility-mode scans until a legacy
  release snapshot has been imported.
- Migrations 032 and 033 add scan release assignment and registry observation
  fields used by the release-aware runtime and control plane.
- Migration 034 adds discovery scope/result detail, worker lease/cancellation
  ownership, bounded claim attempts, normalized per-item resolver results, and
  durable candidate selection descriptors. It does not enqueue or execute work
  during migration.
- Migration 035 adds opaque-token rollout reconciliation claims and cohort
  start, observation, deadline, and completion timestamps. It does not change
  a rollout state or worker desired release during migration.
- Migration 036 adds candidate-scoped proposal ownership, exact lease tokens,
  heartbeats, bounded attempts, cancellation-safe lifecycle timestamps, and
  redacted failure classification. It does not call a Git provider or mutate a
  scanner definition during migration.
- Migration 037 adds ISO-week release sequence counters and candidate-bound
  name reservations. Both are written only inside immutable publication
  transactions, so a failed publication consumes no release name.
- Migration 041 adds nullable-by-empty-value re-scan lineage and an auditable
  release-selection reason to scan rows. It does not rewrite existing scans,
  select a release, enqueue work, or alter active assignments.
- Migration 042 adds customer-controlled signer profiles without storing
  private key material.
- Migrations 043 through 045 add registry reconciliation, versioned
  backup/restore, and operation correlation.
- Migration 046 adds durable fixed-context custom scanner-image build
  aggregates, per-variant results, bounded logs, leases, attempt budgets, and
  cancellation state. It does not execute Docker, resolve a secret, or enqueue
  a build during migration.
- No migration changes `scanners/tools.yaml`, a release lock, an existing
  scanner image reference, or the deployment's desired release.

## Before upgrade

1. Stop administrative changes to scanner release policy and registry
   configuration during the maintenance window.
2. Confirm the application database is healthy and has sufficient free space
   for indexes.
3. Take and verify a database backup:
   - SQLite: use the SQLite online backup API or `.backup`; do not copy a live
     WAL database as independent files.
   - PostgreSQL: use the organization's normal consistent backup or
     `pg_dump --format=custom`.
4. Record the running Wolf version and the checksum or image digest of the
   binary performing the migration.
5. Confirm no table beginning with `scanner_release_` was manually created by
   an earlier experimental build.

## Upgrade

1. Start exactly one upgraded API replica and allow its normal `Migrate`
   operation to complete.
2. Check startup logs for `scanner release migration` errors.
3. Confirm migrations can run again without error. All DDL is restartable and
   additive.
4. Confirm the following read-only checks:

   ```sql
   SELECT COUNT(*) FROM scanner_update_policies;
   SELECT COUNT(*) FROM scanner_release_events;
   SELECT COUNT(*) FROM scanner_schedule_leases;
   SELECT scanner_release_id, release_manifest_digest
   FROM scanner_run_records
   LIMIT 1;
   SELECT state, attempt, max_attempts, lease_expires_at, coverage
   FROM scanner_discovery_runs
   ORDER BY created_at DESC
   LIMIT 1;
   SELECT rollout_id, state, attempt, lease_expires_at, available_at
   FROM scanner_rollout_claims
   ORDER BY updated_at DESC
   LIMIT 1;
   SELECT state, proposal_attempt, proposal_max_attempts,
          proposal_lease_expires_at
   FROM scanner_release_candidates
   ORDER BY created_at DESC
   LIMIT 1;
   SELECT period_key, next_sequence
   FROM scanner_release_sequence_counters
   ORDER BY period_key DESC
   LIMIT 1;
   SELECT state, attempt, max_attempts, lease_expires_at
   FROM scanner_custom_builds
   ORDER BY created_at DESC
   LIMIT 1;
   ```

5. Start the remaining replicas.
6. Keep scanner release management in read-only or disabled feature mode until
   the application-level compatibility checks complete. Migration success does
   not authorize a rollout.

## Validation

Run the repository contracts against each configured database engine:

```bash
go test -count=1 ./internal/scannerrelease ./internal/db
WOLF_TEST_POSTGRES_DSN='postgres://...' \
  go test -count=1 -run TestScannerReleaseRepositoryContractPostgres \
  ./internal/db
```

Validate these invariants in a non-production database:

- Repeating a transition idempotency key creates one event.
- A stale aggregate version is rejected.
- Aggregate state and its event commit or roll back together.
- Published release identity, tools, images, artifacts, approvals, and events
  reject update or deletion.
- An expired active schedule lease can be taken over, but a completed period
  cannot be acquired again.
- A discovery lease token can only be heartbeated and finalized by its owner.
- Discovery terminal state, detailed counts, normalized items, and terminal
  audit event commit atomically.
- An expired discovery claim requeues within its retry budget and fails
  `worker_lost` after exhaustion.
- A proposal can be heartbeated and finalized only by the exact lease owner;
  a stale result is rejected after takeover and attempt exhaustion is durable.
- Candidate proposal finalization and its audit transition commit atomically;
  queued build-plan reconciliation is idempotent after a restart.
- A generated `scanner-set-YYYY.WW.N` name is unique and candidate-bound.
  Failed publication rolls back its counter update and reservation, while an
  idempotent publication retry returns the original name.
- A rollout claim can be heartbeated or released only with its exact owner and
  token; expiration permits one audited takeover, while release cooldown
  prevents a pending health observation from starving other targets.
- Existing scans and scanner runs remain readable with empty release
  provenance.
- A custom build reserves its publish version atomically with enqueue,
  idempotent replay returns the same aggregate, CodeQL push fails closed,
  stale lease owners cannot persist logs/results, and expired ownership is
  safely reclaimed within the attempt budget.
- Custom-build logs enforce the 4,000-line, 4 MiB aggregate, and 8 KiB line
  budgets without converting an unexpected database/lease error into success.

## Rollback

There are intentionally no destructive down migrations.

If application rollback is required:

1. Stop upgraded replicas before starting an older binary.
2. Restore the prior desired scanner release if the application had progressed
   beyond read-only feature mode. Database rollback alone never changes worker
   release assignment.
3. Start the prior application version. The new tables and additive
   `scanner_run_records` columns are ignored by older code.
4. Preserve all release, approval, event, and evidence records for audit and
   diagnosis.
5. If policy requires removal of the additive schema, restore the verified
   pre-upgrade database backup into a new database and cut over to it. Do not
   drop release tables in place.

Never remove immutable release or approval records merely to make a retry
succeed. Resolve the idempotency key, state version, or release identity
conflict that caused the failure.

## Backup and restore

Release DB backup is necessary but not sufficient for disaster recovery.
Registry artifacts and external evidence objects are content-addressed and must
be backed up or mirrored according to their retention class. After restoring:

1. Reconcile every protected or rollback-eligible release manifest with its
   registry targets.
2. Verify signatures, provenance, and artifact digests.
3. Reconcile desired and worker-observed release state.
4. Do not resume a rollout whose prior rollback release is unavailable.
5. Reclaim restored in-flight custom builds before enabling build workers;
   backup migration 046 sanitizes raw lease tokens and expirations.
