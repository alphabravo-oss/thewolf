# D05.2 — persisted cluster suppression and preview

Candidate: the uncommitted working tree on 2026-10-23. This qualifies durable
suppression changes on persisted clusters, their representative/history/canonical
updates, and the tested API preview/view paths. D05 and the full plan remain open.

## Reproduction and repair

The [regression](reproduction.txt) suppresses the critical alpha observation of a
persisted two-tool cluster while the high beta observation remains unsuppressed.
Previously alpha stayed primary, hiding beta from primary-based lists/counts/gates.

Rule reconciliation now keeps the existing cluster membership and selects its
representative with `identity.PreferPrimary`, the predicate used by native
deduplication and manual grouping. It prefers open unsuppressed evidence, then
severity, retaining the existing primary on a tie. Path defaults are projected for
selection without overwriting native durable/import suppression flags. Changing
the representative changes cluster links, not original finding IDs or fix references.

Manual grouping and rule-driven promotion share the existing cluster-writing logic,
extracted into one helper. Promoted scores use the chosen member's own stored
signals. Native cluster confidence remains its existing confidence; manual groups
retain empty confidence. Contributors remain auditable. Native titles, rules,
fingerprints and canonical evidence membership are not replaced with another row's.

Rule reconciliation locks repository observations in sorted order before history
reads, then handles pinned observations chronologically. Pinned promoted history is
rederived using existing baseline logic. Unpinned legacy clusters clear only affected
unverifiable derived history; unrelated legacy claims remain. Counts, revisions,
canonical metadata and audit still share the same transaction and rollback boundary.

Canonical reconciliation anchors an already-published cluster to its existing
canonical key when its evidence has one owner. A promoted member with a different
native stable fingerprint therefore does not manufacture a new canonical issue on
replay. Existing manual membership remains manual. Cross-repository canonical
evidence is rejected, and running scans acquire no new canonical row from rule
reconciliation.

Preview reads native members of the latest scan and respects persisted grouping.
It returns matched issue count/primaries, matched evidence count and newly hidden
issue count. A match on one member can therefore produce one matched issue and zero
newly hidden issues. Matching the remaining visible member yields one newly hidden
issue. The UI confirmation uses these counts and reports that a suppression was saved.

## Verification

The final uncached race suite passed **136 packages** against the owned PostgreSQL
fixture; Go lint reports zero issues. UI typecheck/lint and scanner sync pass.
The security scan retains **29 high/high findings** (25 G703, one G702, three G704)
with no analysis errors or waivers. The owned fixture and anonymous volume were
removed and their absence verified. Final race/lint process handles were lost at
context compaction; terminal logs and absence of running processes establish their
results, rather than a retained tool exit receipt.

```sh
WOLF_TEST_POSTGRES_DSN='<isolated PostgreSQL URL>' go test -race -count=1 ./internal/db ./internal/api/routes -run 'TestPartialClusterSuppression|TestSuppression|TestManualClusters|TestManualBaseline'
make scanners-sync
WOLF_TEST_POSTGRES_DSN='<isolated PostgreSQL URL>' make test
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run
cd ui
pnpm typecheck
pnpm lint
```

- [Focused contracts](contracts-final.txt) run against SQLite and real PostgreSQL.
  They cover partial/full suppression, revoke, expiry, original-ID/fix-reference
  preservation, cluster membership, native score inputs, pinned history binding,
  unpinned legacy clearing and preservation of unrelated legacy state.
- Repeated automatic reconciliation preserves canonical ID/key after promotion
  between different native stable fingerprints. Manual split/merge followed by
  suppression and replay preserves manual membership and empty confidence.
- A late audit trigger fails after finding flags, cluster links, scores, history
  and canonical metadata have changed. The entire database snapshot rolls back.
  Concurrent rule creation and canonical replay converge on the same issue and
  representative. Existing lifecycle and manual baseline contracts also pass.
- HTTP import-backed checks compare partial/full previews, finding lists, gates,
  old finding detail/evidence, canonical metadata, live manifest counts and SARIF
  before/after rule changes. An unsuppressed beta keeps a failing gate; suppressing
  both members yields zero visible findings; revocation restores alpha visibility.
- [Initial contracts](contracts-initial.txt) and [expanded contracts](contracts.txt)
  retain a fixture correction: PostgreSQL stores the score at single precision, so
  the check uses a 1e-6 tolerance instead of assuming double-precision persistence.
  No scoring algorithm or database precision was changed to satisfy the test.
- [Full race suite](race.txt), [Go lint](lint.txt), [UI typecheck](ui-typecheck.txt),
  [UI lint](ui-lint.txt), [scanner sync](sync.txt), [security scan](gosec.json),
  [check summary](checks.json), [source hashes](source.json), [receipt hashes](sha256.json)
  and [owned database fixture](postgres.json) identify this candidate.

## Remaining requirements

D05 still needs ignore-file source selection/error handling, versioned default/file/
environment decisions and cache freshness when those inputs change. These checks
qualify durable-rule mutations on persisted clusters, not every asynchronous path
or rule-file race. Changes during earlier native enrichment/finalization may still
surface a conflict/failure instead of an automatic retry; D02 retains that broader
recovery requirement. Preview is advisory and not a reservation of the observed
finding revision.

A primary finding ID may change when the previous representative becomes unsuitable;
all original finding IDs, URLs and foreign references remain valid. Canonical ID/key
stability is qualified for the tested unchanged membership and manual groups, not
intentional historical identity migrations (D19). Full first/last/fixed/resurfaced
history and deleted-source reconstruction remain D04.

Known durable rules and imported suppression provenance still need the remaining
D05 policy work. Polling expiry, repository-wide reconciliation scale, large preview
pagination and UI/browser deployment qualification retain the D05.1 limits. The UI
change is confirmation text/count wiring, checked by typecheck/lint; no browser
journey or production deployment is claimed. Gosec findings and the release gate
remain open; no exception was added.
