# D05.5 — one projection and fresh live manifest counts

Candidate: uncommitted working tree, 2026-10-23. This change preserves existing
live-rule behavior; it does not settle or implement long-lived source/policy snapshots.
D05 and the complete plan remain open.

## Reproductions and repair

[Reproduction](reproduction.txt) demonstrates two defects. A valid `.wolfignore`
edit leaves cached manifest counts visible at the unchanged finding revision.
Separately, a file edit between total calculation and gate evaluation produces a
single response with one visible critical finding and a passing zero-finding gate.

Gate evaluation now receives already-projected findings. The three call paths were
traced: standalone gate requests project their loaded snapshot, scan result responses
share their existing projected set, and publication uses the set mutated by the
shared report writer. No second path-rule read changes the gate alone. Finding detail
loads its evidence before projecting all response observations together.

`RefreshManifestCounts` recomputes count/revision/baseline fields from the caller's
projected set. It preserves unrelated recorded top-level producer metadata and JSON
number precision, including fields absent from Wolf's current typed manifest. An
unchanged current manifest retains its bytes. Malformed content, mismatched scan
identity and negative raw totals fail. Explicit historical downloads are unchanged.
The live endpoint is private/no-store. Fallback metadata assembly reuses the already
loaded/projected snapshot rather than loading and matching it again; repository
metadata lookup failures propagate.

## Verification

Final **136-package uncached race suite** passed with real PostgreSQL; Go lint
reports zero issues and scanner sync passes. Gosec retains **29 high/high findings**
(25 G703, one G702, three G704), with no analysis errors or waivers. The owned
fixture and anonymous volume were removed and their absence verified.

```sh
go test -race -count=1 ./internal/api/routes ./internal/scan/report -run 'TestLiveManifestCountsFollowPolicy|TestScanResultUsesOneSuppressionProjection|TestPublicationGateUsesWrittenSuppressionProjection|TestFindingDetailUsesOneSuppressionProjection|TestManifestUsesCurrentRevisionAndPreservesSavedDownload|TestRefreshManifestCounts'
make scanners-sync
WOLF_TEST_POSTGRES_DSN='<owned loopback fixture>' make test
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run
```

[Qualified focused checks](contracts-qualified.txt) exercise:

- File-rule add/remove without a finding revision, default-environment changes,
  finding-revision changes, producer-field retention and unchanged original download.
- Controlled file mutation after scan-result projection: totals and gate agree on
  the first snapshot, and a subsequent request observes the changed rule.
- Controlled file mutation after manifest artifact registration: the persisted
  publication gate agrees with the written manifest.
- Detail/evidence rule changes between DB reads: one final projection keeps their
  suppression flags coherent.
- Invalid manifest content/identity, baseline replacement/removal, raw-count retention
  and preservation of an extension integer larger than JavaScript's safe integer.

Earlier [contracts](contracts.txt), [final-attempt log](contracts-final.txt) and
[corrected-attempt log](contracts-corrected.txt) preserve fixture corrections: the
publication hook initially ran on the original SARIF artifact before report projection,
and generic finding enrichment does not mutate native file paths. The final fixtures
hook the manifest registration and insert a separate vendor observation. Product
identity preservation was not loosened to satisfy the test.

[Initial full-suite run](race-initial.txt) exposed a confinement-test fixture that
labeled generic JSON as a manifest. The fixture now contains valid scan identity
and count fields, including in the outside-root file, so the exposure assertion
still tests confinement rather than being masked by schema rejection. The focused
[confinement check](confinement.txt) and final full suite include that correction.

[Full race suite](race.txt), [lint](lint.txt), [scanner sync](sync.txt),
[security scan](gosec.json), [check summary](checks.json), [source hashes](source.json),
[receipt hashes](sha256.json) and [owned database fixture](postgres.json) identify the
candidate. New HTTP regressions use SQLite; the full suite includes the existing
real PostgreSQL transaction, completion, recovery and suppression contracts.

## Remaining requirements

These are per-operation projections, not immutable policy snapshots. The parser and
native Git operation can still observe external filesystem edits while reading inputs.
Different requests may observe different current rules. Policy/default/file edits do
not yet advance a persisted suppression generation or protect terminal commit against
an older policy generation. Durable-rule revision guards remain separate.

Native execution can use a prepared source while later views use `Repo.SourcePath`;
cleaned/unavailable workspaces, archives without Git metadata, and source reconstruction
remain D05. Stored scan-header/list counts are not refreshed by an external file/env
edit. Full snapshot/provenance and scale qualification remain open. The historical raw
finding count is retained; other live counts describe the current finding projection.

No UI/browser/deployment qualification is claimed. Security alerts remain release
blockers; no waiver was added.
