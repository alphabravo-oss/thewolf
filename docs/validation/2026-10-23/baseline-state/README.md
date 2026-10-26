# D02 / D04 / D21 — transactional baseline state and observation history

Candidate: the uncommitted working tree on 2026-10-23. This qualifies scoped D02.4;
D02, D04 and D21 retain the remaining requirements listed below. Builds/deployment
and production qualification are separate gates.

## Reproduction and changes

The [original regression](reproduction.txt) proves that a resurfaced finding is
assigned its reappearance scan as its introduction, losing its original first
observation. The old path also ignored saved branch baselines, stopped history at
20 scans, mixed branches/future scans when finding resurfaced issues, updated only
primaries, rewrote whole finding structs and ignored persistence failures.

`PersistFindingBaseline` now derives and commits the current observation's state
in one shared SQLite/PostgreSQL transaction. It reuses repository and ordered scan
locks, checks execution ownership/cancellation, and reads the actual persisted
primary/member rows. A late update error rolls back the entire batch, the baseline
selection and its finding-revision increment. Concurrent triage is preserved.

Migration 065 adds `baseline_scan_id` and nullable `baseline_resolved_at` to scans,
and nullable `first_seen_at` / `last_seen_at` to findings. Historical rows retain
unknown dates until explicitly processed. The first derivation pins its selected
baseline and observation cutoff; retry/replay does not pick a newer baseline or
include scans from the future. Ordinary progress and enrichment updates cannot
erase this metadata. No finding ID or fingerprint changes.

The saved `auto` baseline is the branch backlog; when none exists, the latest
eligible prior scan supplies compatibility behavior. A finding still absent from
that saved backlog remains new even after an intervening scan has seen it. Earlier
observations absent from the immediately preceding eligible scan are resurfaced.
History is branch/source scoped and no longer capped at 20 scans. First-seen and
introduction come from the earliest known retained history; last-seen identifies
this observation. Members receive their primary issue's classification without
changing native evidence, membership, suppression, status or score.

Named baselines remain explicit comparison choices; simply creating a named
baseline does not activate it for future scans. Shared baseline creation validates
repository owner, branch, completed timestamp, failure/cancellation state and tool
completion. Failed, cancelled, missing-tool and malformed tool metadata cannot
become a new baseline. The same eligibility filter excludes those scans from
history selection. Auto-baseline reads/writes propagate errors; a concurrent
unique-name winner is reused. Native finalization fails on baseline persistence
errors rather than reporting success.

JSON reports previously omitted baseline state entirely. They now include state,
introduced/resolved scan IDs and first/last observation dates, as does SARIF.
Report/manifest JSON identifies the selected baseline when present. API scans
expose their pinned baseline/cutoff. Server-side SARIF import derives baseline
state from Wolf's history instead of accepting an uploaded `existing` claim;
the original raw import remains preserved. The standalone SARIF parser retains
its existing round-trip behavior.

## Checks

```sh
WOLF_TEST_POSTGRES_DSN='<isolated PostgreSQL URL>' go test -race ./internal/db ./internal/api/routes ./internal/scan/report ./internal/finding/diff ./internal/finding/gates -run '(Baseline|ScanFinalizationKeeps|ImportSARIF|Compare|FastPRPolicy)' -count=1
make scanners-sync
WOLF_TEST_POSTGRES_DSN='<isolated PostgreSQL URL>' make test
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run
```

- Both databases: additive/repeated migration with existing observations; 24
  intervening scans; saved backlog versus last scan; first introduction after a
  baseline; original introduction on reappearance; primary/member agreement;
  exclusion of foreign-branch, future, failed, cancellation-pending and incomplete
  history; invalid branch/owner baseline rejection; pinned replay and no-op revision.
- Real late-row database triggers prove rollback after earlier rows were updated.
  A SQLite database close/reopen and fresh PostgreSQL connections preserve the
  pinned selection and observation dates. Stale whole structs cannot reset history. Concurrent replay/triage preserves user
  status, while stale leases and cancelled scans cannot write baseline state.
- Native fresh/resumed executor tests compare JSON history with persisted rows.
  Injected derivation and auto-baseline failures fail the scan. Existing gate,
  artifact, recovery and manual-membership contracts remain in the broad suite.
- HTTP SARIF import tests upload a forged `existing` classification and verify
  persisted `new` state with local introduction and observation dates. Export
  tests verify JSON/SARIF carry the stored dates and baseline metadata.
- [Focused PostgreSQL contracts](contracts-postgres-revised.txt),
  [final uncached race suite](race-final.txt), [lint](lint-final.txt),
  [final scanner sync](sync-qualified.txt), [check summary](checks.json),
  [source hashes](source.json) and [receipt hashes](sha256.json) identify this
  candidate. [PostgreSQL fixture receipt](postgres.json) records isolation/cleanup.
- Intermediate receipts retain the original first-seen failure, baseline fixtures
  missing completed timestamps, the JSON export gap exposed by a new check, and
  a warm PostgreSQL prepared-plan fixture failure. Upgrade fixtures reconnect
  after simulating old schemas, matching process startup; [three repeated
  PostgreSQL upgrade runs](upgrade-repeat.txt) pass.
  Fixtures now represent completed executions rather than weakening validation.
  Hot schema migration with old prepared statements remains unqualified.

## Remaining requirements

D04 is not complete. This change records current observation history. It does not
infer **fixed/resolved** from absence, which requires qualified tool/path coverage;
`resolved_in_scan_id` is exported but missing findings are not newly resolved here.
First/last-seen across canonical vulnerabilities, history deletion/backfill and
manual merge/split rederivation still need reconciliation. Legacy v1/v2 identity
continuity remains D19. Historical dates are the earliest known retained evidence,
not proof of observations deleted before this migration.

D21 still needs complete named/default-branch/explicit strategy selection and
persisted source revisions, baseline management/opt-out, differing scan scopes,
partial parser/coverage evidence, and full restart/retry qualification. Saved
baselines without adequate execution metadata fail validation and require
requalification; no silent fallback to an unverified backlog is provided. The
`auto` choice follows the existing branch/name convention; no second configuration
system or UI was introduced.

The transaction currently locks branch history and streams its identity columns.
The code marks this contention ceiling with a `ponytail:` comment; large-fleet
performance remains P work. It does not provide atomic whole-scan completion,
artifact/canonical publication or concurrent policy/ignore-file versioning.
Imported scans retain their existing multi-step lifecycle, including duplicate
import and intermediate visibility concerns under D25. Corroboration UI remains
blocked by the full D01–D08 acceptance gates. No deployment is implied.


The [gosec gate](gosec.json) remains blocked by **29 high/high findings** (25 G703,
one G702, three G704), without analysis errors. No exception or suppression was
added. UI source was not changed and no service was deployed.
