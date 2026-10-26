# D02.6 — transactional scan completion

Candidate: the uncommitted working tree on 2026-10-23. This qualifies terminal
publication for native scans and SARIF imports. D02/D04/D21/D25 retain the broader
requirements below; nothing was deployed or published externally.

## Reproductions and changes

The [original regression](reproduction.txt) shows a scan completing despite a gate
policy read failure. Completion previously cleared execution ownership before gate
evaluation, gate artifact publication and automatic baseline creation. A crash or
failure in that interval could leave a successful scan without required results.
Imports began as completed before any findings or artifacts had been persisted.

`CompleteScan` now commits terminal status, canonical vulnerability/evidence
reconciliation, the final gate and its artifact reference, and the optional
first-success baseline in one SQLite/PostgreSQL transaction. For imports, the
checksum claim also commits there. The existing repository/scan locks serialize
these writes. Current lease, cancellation, owner/branch, finding revision and gate
artifact scope must match. Ordinary progress/evidence remains available while the
scan is running; successful completion is published only after required report
files are written and the final database transaction succeeds.

Gate evaluation reuses the shared snapshot evaluator. Its immutable file is staged
before the transaction, and the API exposes its reference only on commit. Aborted
attempts can leave unreferenced files; they do not expose a success link. Native
failure uses a conditional terminal write that cannot overwrite cancellation,
reclaim, or an ambiguously committed success. Inline execution uses the same guard.
Canonical projections are no longer updated by a failed native/import finalization.

The [gate identity regression](transaction-initial.txt) showed that replacing a
result retained the previous result ID while publishing a new ID in the artifact.
The current gate row now identifies the current evaluation; original artifact
snapshots remain historical. A second [regression](gate-race-reproduction.txt)
proves that an evaluation captured while running could overwrite the final gate
without any finding change. Migration 067 binds gates to `scan_status` as well as
`findings_revision`; writes reject either mismatch and cache readers exclude stale
or unbound legacy results. Old gates are reevaluated, not assigned invented state.

SARIF imports remain running until findings, clusters, baseline history, run records
and artifacts succeed. The same terminal transaction publishes canonical evidence,
gate, artifact reference and import claim. Earlier failures leave a failed scan and
no reserved checksum, so the identical upload can be retried. Duplicate successful
imports retain HTTP 409 and cannot leave another completed scan. Run-record errors
now propagate. Parser output retains tool names even for empty runs, so a clean
import does not lose its execution metadata merely because it found no issues.

## Verification

```sh
WOLF_TEST_POSTGRES_DSN='<isolated PostgreSQL URL>' go test -race -count=1 -timeout 120s ./internal/db ./internal/api/routes ./internal/finding/sarifio -run 'TestScanCompletion|TestScanFinalizationKeeps|TestCompleteScanCreates|TestImport|TestFinalizeScan|TestFindingSnapshot|TestQualityPolicies'
make scanners-sync
WOLF_TEST_POSTGRES_DSN='<isolated PostgreSQL URL>' make test
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run
```

- [Focused contracts](contracts-final.txt): real SQLite/PostgreSQL triggers reject
  canonical rows/evidence, gate, artifact, baseline and import writes. Full table
  snapshots prove rollback, including the prior running gate and execution lease.
  A later successful retry commits all outputs. Duplicate import rollback, stale
  leases, cancellation, revision conflict, foreign artifact scope, inline completion
  and rejection of a late running gate are covered.
- [Reopen checks](reopen.txt): file-backed SQLite close/reopen and PostgreSQL
  reconnection preserve both rolled-back provisional state and committed completion.
  Repeated migration 067 preserves data, invalidates unbound cached gates and permits
  requalification. Synthetic PostgreSQL schema upgrades reconnect before migration.
- Native fresh, resumed and leased execution tests retain original evidence IDs,
  persisted enrichment and report history. Injected gate/completion failures produce
  failed scans. The completion boundary observes a running scan with its original
  lease until the terminal commit. Standard scans create one first-success baseline;
  subsequent success retains it and fast scans do not create one.
- HTTP import tests cover findings/clustering/history/run/artifact/gate/commit
  failures, failed-scan visibility, absence of a checksum reservation, identical
  upload retry, duplicate success rejection, and a zero-finding run with a passing
  gate. Native and import completion share the same database primitive.
- [Full race suite](race.txt), [lint](lint.txt), [scanner sync](sync.txt),
  [gosec](gosec.json), [check summary](checks.json), [source hashes](source.json),
  [receipt hashes](sha256.json), and [owned PostgreSQL fixture](postgres.json)
  identify the candidate. The final uncached race suite passed **136 packages** with PostgreSQL enabled;
  lint reports zero issues and scanner sync verifies 53 tools and 51 version pins.
  Gosec remains release-blocking at **29 high/high** findings (25 G703, one G702,
  three G704), with no analysis errors or new waivers.

## Remaining requirements

Completion is a database publication boundary, not one transaction spanning the
filesystem and database. Raw findings and individually recorded non-gate artifacts
remain provisional while a scan runs; failed attempts retain diagnostic evidence.
Unreferenced files and partial bundles still need retention/quota cleanup. Download
integrity and immutable historical snapshots retain D02.3's platform limits.

A concurrent finding change rejects stale completion. Automatic rerender/retry of
that finalization conflict, a full worker-process kill matrix at every publication
boundary, and deployment/power-loss qualification remain open. SQLite close/reopen
and PostgreSQL reconnection are not represented as those process-kill tests. Recovery
must also retain source identity and respect the existing lease/attempt contracts.

Native run-metadata persistence and complete source/parser/coverage qualification
remain D09/D24 work. Fixed/resolved absence proof, canonical first/last-seen timelines, deleted-history
backfill, scope/coverage baseline qualification and full UI parity remain D04/D21.
Import format/path/quota/provenance and duplicate-request resource cleanup remain
D25. A failed import whose failure-state write also loses the database can remain
running; it cannot be marked complete by that failed request, and the error is
logged for recovery. No new dependency or general transaction framework was added.
