# D02.5 — manual membership and baseline history

Candidate: the uncommitted working tree on 2026-10-23. This qualifies the scoped
manual-observation reconciliation task; D02/D04/D21 retain the requirements below.
No deployment, publication, or corroboration UI qualification is implied.

## Reproductions and repair

The [merge reproduction](reproduction.txt) shows an existing primary with a member
still classified as new and carrying a different introduction scan. After a split,
simply copying dates back would attribute the first issue's history to the promoted
member. The [gate reproduction](gate-reproduction.txt) separately proves that an
empty baseline state incorrectly matched a new-only failure rule.

Manual merge/split now reuses the baseline derivation inside the existing membership
transaction. Repository and sorted scan locks cover the affected observations and
their preceding history in one order. Older affected observations are recalculated
first. Membership, derived dates/state, canonical metadata, visible counts and one
finding-revision increment per affected scan commit together. A late history-write
failure rolls everything back; native evidence, finding IDs and triage are retained.

Migration 066 adds an internal `baseline_identity_key` column. It binds derived
history to the native identity of the primary that supplied it. Reusing stored dates
requires that binding to match the identity being derived. A manually joined member
can no longer lend another issue's earlier dates to a later independent observation,
or retain those borrowed dates after being split out. No fingerprint or public ID
changes. The binding is internal and cannot be supplied through model JSON.

Pinned scans keep their selected baseline and observation cutoff. Legacy/provisional
scans without a pinned selection cannot reproduce that decision: manual regrouping
clears only the affected issues' derived history to unknown. Unrelated legacy
findings keep their history. Migration itself leaves existing dates intact and the
new binding empty; explicit rederivation uses retained native observations without
guessing provenance for old dates.

A baseline-filtered rule no longer treats empty state as new. If an otherwise
matching non-ignore rule needs unknown baseline state, the gate is incomplete
unless a known rule already establishes failure. Suppression and other rule filters
still apply. Default rules without baseline filters retain their behavior.

## Verification

```sh
WOLF_TEST_POSTGRES_DSN='<isolated PostgreSQL URL>' go test -race -count=1 -timeout 120s ./internal/db ./internal/api/routes ./internal/finding/gates -run 'TestManualBaseline|TestFindingBaseline|TestManualCluster|TestUnknownBaseline'
make scanners-sync
WOLF_TEST_POSTGRES_DSN='<isolated PostgreSQL URL>' make test
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run
```

- [Database/API contracts](contracts.txt): SQLite and real PostgreSQL merge/split,
  native history after a manual join, split introduction/date restoration, and
  canonical baseline state. Real triggers reject late history writes during both
  merge and split; whole-table snapshots prove rollback of finding data, membership,
  evidence, canonical rows, scans and revisions.
- Four concurrent operations cover cross-scan merge, baseline replays and triage.
  Additive/repeated upgrades preserve legacy dates, leave binding unknown and permit
  rederivation. Unpinned regrouping clears affected claims without clearing unrelated
  issues. PostgreSQL fixture connections are renewed after synthetic old-schema DDL,
  matching process startup rather than claiming live hot-migration support.
- HTTP checks verify fast-gate fail → pass → fail through merge/split, restored
  canonical state and SARIF introduction/dates, with matching finding revisions.
  Unpinned legacy claims become unknown and the gate incomplete after regrouping;
  SARIF omits those unsupported dates/state. Existing immutable-artifact checks
  remain in the broad suite; downloaded historical bytes are not rewritten.
- [Race suite](race.txt), [lint](lint.txt), [scanner sync](sync.txt),
  [check summary](checks.json), [source hashes](source.json),
  [receipt hashes](sha256.json), and [owned PostgreSQL fixture](postgres.json)
  identify the candidate. The final uncached race suite passed **136 packages**
  with the PostgreSQL contracts enabled; lint reports zero issues and scanner
  sync verifies 53 tools and 51 version pins.
- Fresh [gosec inventory](gosec.json) remains release-blocking at **29 high/high**
  findings (25 G703, one G702, three G704), with no analysis errors or new waivers.
  This change does not close R03 or discard the original security ledger.

## Remaining requirements

D02 and D04 are not complete. Fixed/resolved state requires tool/path coverage proof;
this change does not resolve issues merely because they are absent. Whole-scan
completion, canonical publication and artifact bundles are still separate operations
and need failure/recovery coordination. Canonical first/last-seen timelines,
retention/backfill and v1/v2 identity continuity remain open.

Manual membership is observation-scoped. It does not establish a new cross-scan
identity alias or automatic future-merge policy. Dates describe the earliest known
retained observations; history deleted before a trustworthy identity binding cannot
be reconstructed. Older scans without a pinned baseline selection need explicit
requalification to regain supported baseline state after regrouping.

The shared lock path discovers repository scan metadata and locks relevant history.
Its contention ceiling is marked with a `ponytail:` comment; indexed/bounded history
work and fleet performance qualification remain P work. Scope/parser/coverage
compatibility, explicit/named/default-branch strategy management and full UI parity
remain D04/D21. No dependency or parallel history subsystem was introduced.
