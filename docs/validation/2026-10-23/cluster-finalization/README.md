# D02 — persisted finalization repairs

2026-10-23; uncommitted working-tree implementation. D02 remains unchecked.

## Reproductions and repairs

The [executor reproduction](reproduction.txt) writes a final report with an empty
finding ID even though both tool findings have database IDs. The runner copies
plugin output before the API persistence callback assigns IDs. Reclaimed scans
also previously replaced the aggregate with raw retained rows without clustering.

Finalization now reads persisted evidence and applies the existing deduplicator
to it. A stable tool/ID input order removes completion-order dependence when
selecting equal-severity primaries. No evidence rows are deleted or their IDs
replaced. Successful tools retained from a previous attempt are included.

Enrichment previously shared the report/scoring slice with a background writer.
It now completes before scoring, persists its fields with cluster metadata and
releases source ownership after finalization. This removes the competing
background update. It also means source mapping latency is included in scan
completion; this is not large-repository performance qualification.

Finding insert failures now fail the scan and the affected tool's durable run
record instead of being logged as successful completion. Final aggregate reads
and metadata update errors also fail the scan. Cancelled status stays sticky.
The existing Store methods remain in use; no new dependency or schema was added.

PostgreSQL testing exposed a second real mismatch: stored REAL scores were
`2.6666667461395264` while in-memory JSON scores were `2.666666666666667`.
[The failing contracts](contracts-postgres.txt) and [initial full run](race.txt)
retain this result. Reports now reload finalized primary rows, including actual
database precision and baseline metadata. The equality check was not weakened.

## Verification

The [final executor contracts](contracts-final.txt) run with real SQLite and a
pinned disposable PostgreSQL instance. Both databases cover fresh scans,
reclaimed scans with retained tool evidence, failed inserts, failed final reads
and failed metadata updates. Successful runs retain two evidence rows and one
report primary with a real persisted ID, matching enrichment, exact score and
two-tool corroboration. Failure cases cannot report successful completion.

`WOLF_TEST_POSTGRES_DSN=... go test -race ./cmd/... ./internal/... ./pkg/... ./plugins/... -count=1`
passes **135 packages** after the final repair ([output](race-final.txt)).
`golangci-lint v2.14.0 run` reports [zero issues](lint-final.txt).
The high/high [gosec gate](gosec-final.json) still fails with **33 findings**
(29 G703, one G702, three G704), with no analysis errors.
The [PostgreSQL fixture](postgres.json) and its anonymous volume were removed
after validation. The plan remains **230 unique tasks, 46 checked**.

[Source hashes](source.json), [checks](checks.json) and `sha256.json` identify this
exact uncommitted candidate; nothing was deployed. Scanner
context/lock regeneration is captured in [sync-final.txt](sync-final.txt).
No UI code changed in this repair; D01's preceding UI typecheck/lint receipts
still apply to its optional type fields. No rollout or new browser qualification
is claimed.

## Remaining D02 work

Compatibility finding lists still expose the per-tool rows while reports select
cluster primaries. Full transactional cluster membership and coherent counts,
foreign-reference migration, manual merge/split policy, concurrent triage,
lease loss during all finalization writes, baseline/canonical dual-write error
propagation and retry qualifications remain open. Existing baseline and
canonical writes still have best-effort paths; this scoped fix does not claim
an atomic finalization transaction. D04/D19 compatibility and backfill work
also remains open.

The release security gate remains separate. The high/high gosec receipts retain
the current alerts; no exception or suppression was added. The prior
[security ledger](../scan-recovery/security-triage.json) remains applicable to its
43 original alerts and seven manually identified issues.
