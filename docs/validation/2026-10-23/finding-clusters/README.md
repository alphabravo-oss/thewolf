# D02 — persisted finding membership

Verified 2026-10-23 against the uncommitted working tree. This extends the earlier
[finalization repair](../cluster-finalization/README.md); D02 is still open.

## Behavior

Migration 062 adds `cluster_id` and its index. Empty means legacy/provisional;
a primary references its own ID, and each member references that primary. No
native row is deleted by clustering. `FinalizeFindingClusters` locks the scan
under its current execution lease, validates every primary and member against
scan/repository scope, and commits all links plus primary metadata in one
transaction. Status changes through original member IDs serialize on the same
scan lock and affect the issue's members. Original IDs, native titles/rules and
fix-item references remain valid.

The runner and persistence use one shared cluster key. SCA keeps D01's typed
package/advisory/location identity. Non-SCA grouping still uses path/line and
fine category or rule/title, with explicit coarse category and quoted components
to prevent delimiter ambiguity. Semantic symbol/snippet clustering is still
D19 work. The [key reproduction](key-reproduction.txt) demonstrates the prior
path/line/rule collision; [repair checks](key-fixed.txt) pass. An initial fixture
enum typo is retained separately in `key-fixture-compile-error.txt`.

Primary selection prefers open, unsuppressed evidence before severity, so a
suppressed/triaged critical member cannot hide an open high-severity member.
[Reproduction](visibility-reproduction.txt). Commit checks current visibility
again to reject a stale primary, and preserves current suppression decisions.
This is not full suppression-policy qualification under D05.

Scan/repository/fleet lists and statistics, baseline/fix selection and combined
reports now select primaries. Authorized finding detail retains original member
IDs and includes cluster evidence. Contributor filters and totals include both
tools; those facets overlap and do not sum to issue count. Parsed per-tool JSON
retains every native row, while combined JSON/SARIF includes primary/cluster
provenance. Imported SARIF cannot attach itself to existing cluster IDs.

Recovery reads all evidence and clears membership before resetting an incomplete
tool. It cannot leave a completed member pointing at a deleted primary. Canonical
dual-write uses the primary's identity while retaining every native evidence row.
Its write errors now fail the scan rather than being discarded. This exposed an
existing PostgreSQL boolean-to-INTEGER suppression encoding failure, retained in
[the failing contracts](contracts-complete.txt). The shared upsert now encodes
both true and false correctly on both databases.

## Verification

- [Store contracts](store-final.txt): real SQLite/PostgreSQL upgrade from a schema
  without the new column, repeated migrations/finalization, foreign-primary and
  cross-repository rejection, missing/duplicate membership rejection, stale and
  cancelled leases, original ID/fix reference retention, and native evidence.
- A trigger rejects the primary metadata update after membership writes; the
  transaction rolls every change back. Eight concurrent real transactions mix
  finalization with member triage and retain consistent membership/status.
  Recovery deletes an incomplete primary while keeping its completed member and
  existing fix reference valid.
- [Executor and HTTP contracts](contracts-facets.txt): fresh/reclaimed scans on
  both databases retain two tool observations but expose one primary and one
  fleet issue. Report IDs, enrichment, exact stored score, per-tool artifacts
  and contributor counts agree. Insert/read/finalization/canonical write failures
  fail the scan. Owner detail includes both members; cross-owner detail is 404.
  The existing scan-list ownership contract returns 403, without evidence data.
- `WOLF_TEST_POSTGRES_DSN=... go test -race ./cmd/... ./internal/... ./pkg/... ./plugins/... -count=1`
  passes **135 packages** after the final key repair. [Output](race-final.txt).
- `golangci-lint v2.14.0 run`: [zero issues](lint-final.txt). Node 26.11.1 /
  pnpm 12.11.2 [UI typecheck](ui-typecheck.txt) and [lint](ui-lint.txt) pass.
  Only optional UI types changed; no new corroboration UI or browser rollout
  qualification is claimed.
- [Final context/lock sync](sync-qualified.txt): 53 tools, 51 pins. The source
  hashes include the regenerated lock and current code. The API-only matcher
  and its tests were removed; shared identity, store and HTTP tests cover the
  actual path instead of testing the obsolete matcher.
- [PostgreSQL receipt](postgres.json) identifies the pinned disposable image,
  loopback port and cleanup. Nothing was deployed or published.

Earlier receipts preserve intermediate failures and their fixes. In particular,
`store-before-list-reconciliation.txt` shows stored membership before list reads
were changed, and `contracts.txt` records the corrected test expectation for the
existing 403 scan-list ownership contract. These are not final passing receipts.

## Remaining gates

D02 remains unchecked: manual vulnerability merge/split must be reconciled with
finding membership; whole-scan completion, baseline and canonical writes are not
one qualified transaction. All lease-loss/failure/retry windows still need
qualification. Existing historical rows are not automatically backfilled (D19).
Additional read/write and finalization latency needs large-repository measurement.
The [consumer documentation](../../../interface-parity.md#persisted-finding-clusters)
records compatibility, evidence and overlapping tool facets.

The high/high [gosec gate](gosec-final.json) still fails with **33 findings**
(29 G703, one G702, three G704), with no analysis errors. The previous
[security ledger](../scan-recovery/security-triage.json) retains 43 baseline
alerts and seven manual issues. No suppression, exception or release waiver
was added. The plan remains **230 unique tasks, 46 checked**.
