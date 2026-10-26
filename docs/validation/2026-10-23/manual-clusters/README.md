# D02.1 — atomic manual finding membership

Verified 2026-10-23 against the uncommitted working tree. This completes the
scoped D02.1 database task; D02 and the release gate remain open. No deployment,
publication or Enterprise pin update was performed.

## Reproduction and repair

The [original HTTP regression](reproduction.txt) shows a successful vulnerability
split leaving only one finding primary with the old two-tool confidence. The
previous handler performed creation, evidence moves, deletion and refresh as
independent writes, ignored several errors, and never reconciled finding links.
Automatic persistence could subsequently recreate a deleted merge source.

The shared SQLite/PostgreSQL store now commits manual evidence moves, finding
membership, contributor metadata, deterministic primary scores, canonical
metadata and source deletion in one transaction. It validates repository scope,
every requested finding ID, nonempty proper split subsets, and terminal status
of every affected scan. Unknown/duplicate IDs and self merges are rejected;
active scans return HTTP 409. Request decoding rejects unknown fields and
trailing JSON. Existing ownership/fleet authorization remains in place.

Manual operations preserve original finding IDs, native scanner descriptions,
locations, rules and triage. Splitting an old primary promotes the remaining
member without dangling references. Primary selection reuses the runner's
visibility/severity preference, so suppressed critical evidence cannot hide an
open high-severity issue. Cross-scan canonical merges keep a separate finding
primary inside each scan. Each promoted primary is scored from its own stored
inputs using the existing scorer; absent enrichment is not borrowed or invented.

Manual reasons distinguish user association from automatic identity matching.
Inherited confidence is cleared rather than presenting a manual association as
independent scanner agreement. Contributor lists still retain each tool.
Evidence-read errors now fail detail/evidence/intelligence reads instead of
returning empty success.

Canonical persistence also runs in one transaction. It reads persisted manual
membership under the same repository/scan locks, preserving splits and merges
on replay. Automatic groups use primary identity and native evidence, with direct
ID lookup/grouping replacing the prior repeated linear evidence search. Manual
decisions are observation-scoped; they do not create rules for future scans.

## Verification

- `WOLF_TEST_POSTGRES_DSN=... go test -race ./cmd/... ./internal/... ./pkg/... ./plugins/... -count=1`:
  [final full suite](race-final.txt), including real PostgreSQL tests. The earlier
  [full suite](race.txt) passed before the promoted-primary score correction.
- `TestManualClustersSQLite` / `TestManualClustersPostgres` inject actual database
  triggers: canonical evidence insertion failure rolls back the preceding issue
  insert; split metadata failure rolls back creation and evidence moves; merge
  deletion failure rolls back the preceding membership changes. Snapshots compare
  all native findings, canonical rows and evidence before/after each failure.
- Eight concurrent canonical replays run alongside manual merge and member triage
  on both databases. No source is recreated, native IDs remain, and counts agree.
  Primary split, malformed subset, active scan, suppressed-primary preference,
  cross-scan membership and final promoted-primary scoring checks are included.
- HTTP regression checks split/merge/replay, original finding IDs, manual reasons,
  input rejection, active-scan 409 and evidence-read 500. Existing cross-owner
  tests also pass. Fresh/reclaimed scan executor contracts cover insert/read/
  finalization/canonical persistence failures on both databases.
- [Verbose contracts](contracts-final.txt) precede the final scoring assertion;
  the final full suite covers that assertion too. The retained
  [test-router fixture failure](contracts-fixture-route-missing.txt) came from
  the fixture omitting the production evidence route; the fixture now registers
  it. No production route was added for that test.
- [Go lint](lint-scored.txt), [context/lock sync](sync-scored.txt), and final full
  suite scanner metadata checks qualify the current source. UI behavior was not
  changed; no new browser or deployment qualification is claimed.
- [PostgreSQL receipt](postgres.json) identifies the pinned disposable container,
  loopback port and removal. [Source identity](source.json), [check summary](checks.json)
  and [receipt hashes](sha256.json) delimit this candidate and its evidence.

## Remaining work and compatibility limits

Manual edits change live finding lists/statistics and canonical membership.
Saved scan finding-count fields, gate/baseline results and artifact snapshots are
not regenerated in these transactions. Whole-scan completion, those derived
consumers and remaining lease-loss/recovery/retry windows still need D02 work.
Historical completed scans are not automatically backfilled. Persistent manual
policies for future scans and historical identity continuity remain unqualified.
The new per-repository serialization needs large-repository contention measurement
before any narrower locking scheme is justified. No corroboration UI is enabled
before D01–D08 acceptance.

The final high/high [gosec gate](gosec-final.json) remains failing with 33 findings
(29 G703, one G702, three G704), and no analysis errors. No exception or suppression
was added. The previous [security ledger](../scan-recovery/security-triage.json)
retains the 43 original alerts and seven manual issues; static count changes do
not close their review requirements. The plan has 231 unique tasks, 47 checked.
