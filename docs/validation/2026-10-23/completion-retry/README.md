# D02.7 — publication retries and executor-process recovery

Candidate: the uncommitted working tree on 2026-10-23. This qualifies bounded
finalization-conflict retries and executor-process kills immediately before/after
the terminal commit. The full plan, deployment and broader recovery matrix remain
open. No production service was changed.

## Reproduction and repair

The [original regression](reproduction.txt) applies user triage while completion is
publishing its report. All scanners succeed, but the revision conflict fails the
scan. Retrying only the gate would let the saved reports describe older findings.

Native scans and SARIF imports now share `publishScanResults`. Each attempt reads
one current persisted snapshot, checks execution ownership/cancellation, writes a
fresh report bundle and evaluates the gate from those findings. The existing
terminal transaction verifies the revision again. A finding conflict causes a fresh
snapshot and rerender; successful scanners and their evidence are not rerun or
reinserted. The in-memory result used after completion also uses the committed
snapshot's findings.

Three attempts bound work under continuous mutation. Cancellation, lease loss,
policy errors, filesystem/publication failures and other database errors stop the
loop. Exhausted conflicts fail explicitly with retained evidence. This ceiling and
the possible durable-retry upgrade are documented in the code. Existing immutable
artifact references remain historical and retain valid checksums after rerendering.

The shared path replaces separate native/import snapshot and report-writing flows.
Original SARIF bytes now go directly through the existing confined immutable
publisher, eliminating the intermediate ordinary raw-file write. No dependency,
schema change or general retry framework was introduced.

## Verification

```sh
WOLF_TEST_POSTGRES_DSN='<isolated PostgreSQL URL>' go test -race -count=1 -timeout 120s ./internal/api/routes ./internal/db -run 'TestScanCompletion|TestScanFinalizationKeeps|TestImportCompletion|TestCompleteScanCreates|TestImportSARIFAPI'
make scanners-sync
WOLF_TEST_POSTGRES_DSN='<isolated PostgreSQL URL>' make test
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run
```

- [SQLite/PostgreSQL contracts](contracts.txt) cover successful rerender after a
  concurrent triage change, exactly three attempts under continuous mutation,
  cancellation and lease reclaim between attempts, and publication/policy failure
  after a conflict. Failed attempts publish no automatic baseline and retain native
  evidence. The final JSON report matches persisted finding visibility and revision.
- HTTP import checks cover a successful conflict retry with a fresh saved report,
  bounded conflict exhaustion, subsequent identical-upload retry and duplicate
  success rejection. Original imported bytes are published through the existing
  artifact confinement/integrity mechanism.
- [Process recovery checks](recovery-final.txt) execute four real child-process kill
  cases: SQLite and PostgreSQL, immediately before and after terminal commit. The
  subprocess runs the real scan executor and persistence/publication path using
  controlled finding plugins. An external append counter proves the two successful
  scanners execute only once each. It is outside the repository's source identity.
- Before commit: no canonical result or baseline has been published; the owned
  process is killed, its lease is reclaimed, triage is applied and a second worker
  claim resumes the same scan. All finding IDs, cluster links, confidence, scores,
  original introduction and first-seen dates survive. Triage survives recovery.
- After commit: the owned process is killed before returning from completion. The
  scan stays completed, has no reclaimable lease and cannot be claimed again.
  Both boundaries retain a current gate/baseline and verified final
  findings/manifest/SARIF/gate artifacts; old artifact checksums still verify.
- The child-only test intentionally skips when run without its parent fixture flag.
  PostgreSQL cases use an owned isolated schema/database fixture; they are enabled
  in the full qualification command. These are executor process tests, not Docker
  scanner-image or deployed worker-service qualification.
- [Initial checks](initial.txt) retain two fixture corrections: audit lists include
  suppressed rows, while visible scan counts exclude them; report fixtures now use
  the configured artifact backend root. Production confinement was preserved.
- [Full race suite](race.txt), [lint](lint.txt), [scanner sync](sync.txt),
  [gosec](gosec.json), [check summary](checks.json), [source hashes](source.json),
  [receipt hashes](sha256.json) and [owned PostgreSQL fixture](postgres.json)
  identify this candidate. The final uncached race suite passed **136 packages** with PostgreSQL enabled.
  Lint reports zero issues; scanner sync verifies 53 tools and 51 version pins.
  Gosec reports 29 high/high findings (25 G703, one G702, three G704), with no
  analysis errors or new waivers.

## Remaining requirements

Three-attempt exhaustion is an explicit failure, not an automatically queued retry.
Durable finalization scheduling under sustained contention remains unqualified.
Non-finding inputs such as policy, coverage and ignore-file versions are not added
to the finding revision by this change; D05/D22 retain those consistency criteria.

The process tests cover the two terminal commit boundaries with controlled plugins.
The full process-kill matrix during raw production, imports, database disconnects,
source preparation and container/Kubernetes worker lifecycles remains open, as do
platform power-loss behavior and production deployment qualification. Partial
bundles and unreferenced staged files still require retention/quota cleanup.

Canonical timelines, fixed/resolved absence proof, deleted-history backfill,
native run-metadata/source/parser/coverage qualification and full UI parity remain
D04/D09/D21/D24 work. Source-level resolution proof remains D04.
R03 remains release-blocking at 29 high/high findings, with no new waivers.
