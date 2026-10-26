# D05.1 — durable suppression lifecycle and current preview

Candidate: the uncommitted working tree on 2026-10-23. This qualifies durable
rule lifecycle operations, current-observation preview and the tested persistence/
API paths. D05, the overall plan and production qualification remain open.

## Findings and repair

The [scope regression](reproduction.txt) reproduces a branch-specific rule matching
an observation with no branch. Inspection also found that creation/revocation
ignored finding-update and audit errors, expiry had no scheduled unflag operation,
and preview loaded all historical repository findings.

Shared SQLite/PostgreSQL transactions now publish a rule mutation, matching native
finding flags, scan count/revision changes, existing canonical suppression flags
and audit entries together. Only suppression columns change; triage and scoring are
preserved. Existing repository and sorted scan locks serialize these operations
with other finding and canonical writes. The canonical update changes visibility
only; it neither publishes running-scan canonical rows nor moves the current
canonical observation to an older scan.

Rules use repository identity and exact branch matching. A known durable rule's
previous flag is reevaluated against all rules in deterministic newest-first order,
including revoked/expired records needed to identify its ownership. Removing one
rule falls back to another active match. Independent/imported decisions with unknown
rule IDs stay intact. Original scanner evidence and immutable artifact bytes remain.

Native/import inserts read current rules inside their existing scan transaction.
The executor's once-per-scan rule cache is removed. Stale enrichment rechecks rules
before writing, so it cannot erase a newer durable decision. Persistence failures
propagate through the existing scan/import failure boundary.

The existing server schedule loop runs expiry at startup and each minute with a
30-second deadline. Repository transactions are idempotent across retries/replicas.
Failures are logged and retried. The database returns the count of successfully
expired rules if a later repository fails. No new scheduler, dependency or schema
was introduced.

Preview selects the newest scan in the requested branch, or newest repository scan
when no branch is provided, with deterministic timestamp/ID ordering. It returns
that scan's ID/branch. An empty or running newest scan does not silently fall back to
history. Creation rejects past expiration timestamps. Unsupported `package_advisory`
CLI help and false `.wolfignore` negation comments were removed.

## Verification

```sh
WOLF_TEST_POSTGRES_DSN='<isolated PostgreSQL URL>' go test -race -count=1 ./internal/db ./internal/finding/suppression ./internal/api/routes -run 'TestSuppression|TestScheduleLoopExpires|TestSuppressionsAPI|TestBulkUpdateFindings'
make scanners-sync
WOLF_TEST_POSTGRES_DSN='<isolated PostgreSQL URL>' make test
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run
```

- [Final focused contracts](contracts-final.txt) run against SQLite and real
  PostgreSQL. Failure triggers at the rule, finding, scan, canonical and late audit
  boundaries prove whole-transaction rollback. Equivalent revoke/expiry checks
  preserve prior rows and retry successfully.
- Scope checks cover main/dev/unknown branches and mismatched repositories.
  Stale enrichment and later inserts respect current rules. Overlapping rules fall
  back correctly; independent imported decisions remain. A concurrent status change
  survives revocation. Repeated revoke/expiry operations produce no duplicate audit.
- SQLite close/reopen and PostgreSQL reconnection preserve pending expiration and
  allow the same job to complete after restart. Audit records retain the human
  creator/revoker; automated expiry uses an empty actor. Finding revision changes
  invalidate old stored gates and update scan counts and canonical visibility.
- HTTP import-backed checks cover historical/current branch observations, latest
  empty scans, no matching branch, preview identity, rule creation, expiration,
  active/inactive listing and past-date rejection. Finding lists, scan counts,
  default gates, live manifests/revisions and SARIF flags agree before/after expiry.
- A schedule-loop check proves expiry runs immediately at startup with a bounded
  context and that cancellation stops the loop without waiting a minute.
- [Initial checks](initial.txt), [database checks](contracts-initial.txt),
  [expanded contracts](contracts.txt), [full race suite](race.txt), [lint](lint.txt),
  [scanner sync](sync.txt), [gosec](gosec.json), [check summary](checks.json),
  [source hashes](source.json), [receipt hashes](sha256.json) and
  [owned database fixture](postgres.json) retain qualification evidence. The final
  uncached race suite passes **136 packages** with PostgreSQL enabled. Lint reports
  zero issues. Scanner sync verifies 53 tools and 51 pins. Gosec remains at 29
  high/high findings (25 G703, one G702, three G704), without analysis errors or
  additional waivers.

## Remaining requirements

D05 is not complete. Rule matching only part of an already-persisted cluster still
needs a consistent cluster-level decision and preview contract; current native
observations keep their own rule matches. Existing canonical suppression refresh
uses the persisted primary for the current canonical observation. Neither this
change nor its tests claim to qualify partial-cluster promotion or regrouping.

Ignore-file source selection/errors, default/gitignore rule snapshots and cache
freshness when files/environment change remain open. Historical flags with unknown
or colliding imported IDs need a provenance policy; this change does not silently
clear unknown imported decisions. Stored baseline/history transitions remain D04.

Expiry visibility may lag by the one-minute polling interval and retry after an
outage. The job timeout bounds each attempt; a repository too large to reconcile
within it needs indexed scope processing. Mutation reconciliation currently visits
all repository observations; its code comment records that ceiling. Preview returns
matching rows from one scan, without pagination or measured fleet-scale bounds.
The periodic runtime is wired and its startup behavior tested, not production-
deployed or qualified against an unavailable database/large backlog SLO.

Bulk API batches can still partially complete across independent findings/rules;
each rule is atomic, not the whole batch. The internal Store revoke method now
requires an actor ID and Store adds the expiry method; downstream custom store
implementations must implement those changes. REST and CLI request syntax is
unchanged. Security findings, independent review and release/deployment gates remain
open. No security exception or deployment was performed.
