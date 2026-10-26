# Scan-time visibility and focused completion checks — 2026-10-23

Completed scans now retain their effective defaults, `.wolfignore` rules and native
Git ignore path decisions. SQLite/PostgreSQL capture this once under the execution
lease before grouping/publication. Source keys and finding revisions guard commit;
failed reads/writes roll back flags, policy and revision. Reports carry the policy.
Source deletion or later file/environment edits cannot change completed visibility.
Wolf suppressions can still be created, revoked or expired; source-hidden members
stay hidden and remaining cluster members can become primary.

Legacy scans without a snapshot retain persisted flags. Migration 069 repairs their
visible header counts once and invalidates changed revisions; it does not invent old
rules. Migrations 022 and 069 now initialize data only when adding their columns,
preventing PostgreSQL restarts from silently promoting a demoted first user.

Additional D07/D16/D17 fixes: bootstrap validates configuration and database errors;
registration requires explicit enablement; existing rate buckets use the installed
limiter's continuous refill; no-op/missing AI does not fabricate assessment scores;
path scoring uses segment boundaries; high-priority reports retain uncategorized
findings and resolve the requested tool guidance.

Checks:

- `migration-final.txt`: SQLite/PostgreSQL finding snapshot, completion and ignore-policy
  race regressions pass, including migration/reopen and rollback.
- `database-suite.txt`: full DB race run passed all but the new role-upgrade fixture,
  which retained a PostgreSQL prepared SELECT shape after deliberately dropping a
  column. Reusing the existing reconnect helper repaired that fixture;
  `role-migration-final.txt` passes both stores. No production workaround was added.
- `focused-expanded.txt` and `database-final.txt`: suppression/report/HTTP and database
  policy checks. Earlier failing logs remain as history; migration replay was fixed
  after the broad runs, not concealed.
- `../scoring-registration-checks.txt`: real native finalization/report parity on both
  stores, scorer/no-op and registration policy regressions pass.
- `../bootstrap-checks.txt`, `../rate-limiter-checks.txt`: startup/auth and middleware
  race checks pass. `../rate-limiter-reproduction.txt` retains the pre-fix failure.
- `../knowledge-scoring-final.txt`: full auth, knowledge, report, scorer and AI package
  race checks; includes virtual-time lockout recovery and all five guidance tools.
- `scanner-sync-final.txt`, `metadata-final.txt`, `lint-final.txt`: final generated
  scanner context, metadata checks and lint results.

Limits: scan capture uses separate source reads, not an atomic filesystem snapshot.
Legacy decisions cannot be reconstructed. Login/request counters remain process-local
and reset on restart. Historical AI scores are retained. Broader security findings,
source isolation, distributed operation and deployment gates remain unchecked in PLAN.
This batch adds focused checks instead of rerunning the full suite after every fix;
the earlier full-suite failures and their targeted repairs are retained explicitly.
