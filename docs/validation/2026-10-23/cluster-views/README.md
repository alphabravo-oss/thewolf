# D02 — consistent scan views and gate persistence

Candidate: the uncommitted working tree on 2026-10-23. This completes scoped D02.2 and extends
[manual membership](../manual-clusters/README.md), with D02 still open.

## Reproduction and implementation

The [original regression](reproduction.txt) splits one issue into two but receives
a scan header with `finding_count: 1`. The compact result also previously combined
live finding totals with the last cached quality gate, regardless of membership
changes. Report generation read current rows but omitted the path suppressions
used by the finding list and gates.

Migration 063 adds `findings_revision` to scans and gate results. Existing scans
start at zero; existing gate rows start at **-1**, which makes their unknown input
revision ineligible for a current cache read. No finding ID or fingerprint changes.
Finding insertion/update, cluster finalization, manual regrouping, member triage
and recovery reset advance the scan revision in their write transactions. Failed
batches roll the revision back too. Ordinary scan progress/heartbeat updates
cannot reset it. Lease-bound inserts also reject evidence for another scan.

`GetScanFindingSnapshot` reads a scan/revision and its primary findings in one
transaction (repeatable read on PostgreSQL). Gate persistence locks the scan,
compares the evaluated revision, then writes; an intervening finding mutation
returns `ErrStaleFindings` instead of committing an obsolete result. Cached gate
queries only return matching revisions. Gate and compact-result HTTP requests
return 409 for a concurrent change, allowing the caller to retry a fresh read.

The compact result evaluates its gate from the same snapshot used for its counts.
Snapshot, scanner-run, policy-read and gate-write failures cannot become empty
success or a default-policy pass. The shared policy resolver now propagates read
errors to both gate and measured-coverage consumers; malformed stored policies
also fail evaluation instead of silently selecting a different policy.

Manual merge/split refreshes stored scan counts in the membership transaction,
using the same built-in, `.wolfignore` and gitignore matcher as the UI/gates.
Gate response counts use unsuppressed findings. On-demand Markdown/SARIF reports
read consistent snapshots, apply that matcher, and identify their inputs with
`X-Wolf-Findings-Revision`. Suppressed evidence remains auditable in SARIF with
its suppression metadata. Native IDs and member evidence remain intact.

## Verification

- `TestManualClustersRefreshScanViews` exercises real HTTP split/merge, header
  counts, compact totals/gates, shared revisions and report headers. A repo-local
  ignore rule produces zero visible findings in the stored header and gate while
  SARIF retains one suppressed primary. Injected snapshot/run/policy read failures
  return 500; an actual finding update between evaluation and persistence returns
  409 from both gate and compact-result endpoints.
- `TestFindingSnapshotsSQLite` / `TestFindingSnapshotsPostgres` cover additive
  upgrade and repeated migration, invalidation of legacy gates, stale-write
  rejection, rollback after a partially inserted batch, and progress updates
  carrying old scan structs. Four readers race a writer for consistent revision/
  signal snapshots and conditional gate writes. Triage invalidation and rejection
  of a valid lease used to insert into another scan are checked too.
- Existing manual transaction, membership, fresh/reclaimed executor, measured
  coverage and policy contracts also run on SQLite and real PostgreSQL.
- [Final full Go race suite](race-final.txt) passes **135 packages**; [final Go lint](lint-final.txt) reports **zero issues**.
  [Generated context/lock sync](sync-final.txt) and [check summary](checks.json)
  cover the final candidate. [Source hashes](source.json) and [receipt hashes](sha256.json)
  distinguish it from previous snapshots. UI source was not changed.
- [Initial focused contracts](contracts.txt) and [revised contracts](contracts-revised.txt)
  retain intermediate qualification. The first [broad run](race.txt) exposed an
  orphan-cleanup fixture using ordinary creation with missing scan/repo IDs and
  foreign keys disabled. That fixture now explicitly seeds historical corruption
  through SQL and verifies ordinary persistence rejects it; cleanup behavior is
  still tested. [Initial compile failure](initial-fix.txt) and [lint failure](lint.txt)
  are retained, followed by the repaired runs.
- [Disposable PostgreSQL receipt](postgres.json) records the pinned image, owned
  container, loopback port and cleanup. No production service was changed.

## Remaining gates

D02 is not complete. Saved artifact files and baseline/first-seen state still
need revision-aware reconciliation; original artifact downloads remain saved
outputs. Existing `gate-result.json` / `diff.json` writes can overwrite a shared
path while older artifact rows retain its old checksum, so immutable artifact
publication is a required next repair. Whole-scan completion/lease recovery and
all retry windows remain unqualified.

This revision tracks persisted findings, not changes to policies, measured
coverage or external ignore files; those policy/configuration version contracts
remain D05/D22 work. The stored manual count reflects rules available when that
edit commits. Manual regrouping currently reads repo rules under its scan lock;
large-repository contention and versioned suppression snapshots remain to qualify.
Orphan maintenance is a separate cleanup path. Upgrade API/workers together;
mixed-version writes are not qualified. No corroboration UI or production rollout
is implied by these database/API checks.

The high/high [gosec gate](gosec.json) remains blocked by 33 findings (29 G703,
one G702, three G704), without analysis errors. The prior
[security ledger](../scan-recovery/security-triage.json) retains the 43 original
alerts and seven manual issues. No waiver or suppression was added.

The current plan contains **232 unique tasks, 48 checked**.
