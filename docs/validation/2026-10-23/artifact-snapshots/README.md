# D02 / D24 — immutable artifact records and verified downloads

Candidate: the uncommitted working tree on 2026-10-23. Extends
[consistent finding views](../cluster-views/README.md). This qualifies the scoped
D02.3 contract; D02 and D24 retain their broader acceptance gates.

## Reproduction and repair

The [original regression](reproduction.txt) makes two gate requests and proves
that the second overwrites the first record's file while its stored checksum
continues to describe the old bytes. Diff files shared the same defect. Native
report and parsed-tool output paths could also be reused during retries.

The shared artifact store now publishes each record to its own
`<scan ID>/<artifact ID>/<filename>` path. Publication validates single-component
names, opens a confined filesystem root, copies into a private staging directory,
computes size and SHA-256 from those exact bytes, flushes/closes the file, renames
the directory and syncs the Unix directory chain. Failed copies remove staging
files. Concurrent publications never reuse an earlier record's path. Filenames
and artifact types remain compatible with existing consumers.

Metadata is recorded only after successful filesystem publication. A failed DB
call retains the snapshot: a lost connection can report failure after a commit,
so deleting it could destroy a committed artifact. Existing retention can remove
unreferenced files later; publication and DB insertion are not one transaction.
Report bundle, tool-log, parsed-tool, gate, diff and SARIF-import callers propagate
publication failures. Late executor/import failures update completed status
without overwriting newer finding counts/revisions or a cancellation. An automatic
baseline is not created after a detected gate-artifact publication failure.

All recorded-file readers now use a shared confined, regular-file-only reader.
FIFO opens are nonblocking. A portable storage key takes precedence over the old
absolute file path, allowing storage relocation. Reads verify stored size and
SHA-256 before returning bytes. Corrupt downloads return HTTP 409
`artifact_integrity`; they do not send the altered content. Download headers say
`X-Wolf-Artifact-Integrity: verified` or `unverified` for legacy rows without a
checksum. Existing scan ownership and scan/artifact association checks remain.

Migration 064 adds nullable `scan_artifacts.findings_revision`. Native/imported
report bundles and gate/diff artifacts record their input revision; raw logs and
legacy rows have no asserted revision. Diff payloads contain each scan's revision.
Records retain historical revisions after edits. The manifest endpoint generates
a current manifest when the saved version is stale, while the original download
still returns its original bytes and revision. Live/saved manifest downloads
expose `X-Wolf-Findings-Revision` when known. Legacy unversioned manifests remain
readable without claiming their finding revision is current.

`report.WriteAll`, shared by CLI/native/import execution, now reports failures for
all six outputs. It no longer silently returns a successful partial bundle. Counts
include both previously persisted and newly applied suppressions, including repeat
generation; findings JSON and manifests carry the available finding revision.

## Runnable checks and retained evidence

```sh
go test -race ./internal/artifacts ./internal/scan/report
WOLF_TEST_POSTGRES_DSN='<isolated test database URL>' go test -race ./internal/db ./internal/api/routes -run '(FindingSnapshots|ArtifactSnapshot|GateArtifact|ManifestUses|ScanFinalizationKeeps|ImportSARIF|Compare)' -count=1
make scanners-sync
WOLF_TEST_POSTGRES_DSN='<isolated test database URL>' go test -race ./...
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run
```

- Store regressions cover 16 concurrent publishers, unique paths/checksums,
  failed-reader cleanup, cancelled publication, traversal/symlink rejection,
  FIFO handling, relocation, corruption/size mismatch and legacy reads.
- HTTP regressions concurrently publish gates/diffs, download every historical
  record, reject cross-scan artifact IDs, reject corruption, and propagate metadata
  and comparison write failures. A changed finding produces a live manifest at
  revision 1 while its saved revision-0 download remains unchanged. Existing
  cross-user download and root-escape tests also run in the full suite.
- SQLite and real PostgreSQL tests cover additive/repeated migration with existing
  rows, nullable legacy revision, metadata round trips, and late failure updates
  that preserve finding state and cannot overwrite cancelled/running/failed scans.
- Native executor tests verify fresh/resumed output identity and checksum reads,
  failed artifact metadata writes, late gate publication failure, and a real log
  creation failure. Findings remain persisted when artifact output fails.
- Every deterministic output gets an actual filesystem write-failure regression;
  a partial bundle does not return a manifest path. Suppression/revision regression
  checks cover repeat report generation.
- [Focused PostgreSQL contracts](contracts-postgres.txt),
  [final full race suite](race-final.txt), [lint](lint-final.txt), [scanner sync](sync-final.txt),
  [check summary](checks.json), [source hashes](source.json) and
  [receipt hashes](sha256.json) identify the checked candidate.
  [PostgreSQL fixture receipt](postgres.json) records its pinned image and cleanup.
- Intermediate receipts retain the missing-import compile error, a test fixture
  field-name correction, recovery fixtures without initialized artifact storage,
  and the manifest revision regression caught by expanded
  checks. They are superseded by the passing final qualification, not discarded.

## Limits and remaining work

These snapshots prevent application publications from overwriting earlier records;
they are not a filesystem WORM facility. External modification is detected at
read time when a checksum exists. Legacy corruption cannot be repaired from a
checksum alone. Existing endpoints still buffer file content; large-artifact
memory bounds/streaming and complete secret-redaction coverage remain D24/P work.

Whole-bundle/database/scan completion is not atomic. A failed attempt can leave a
partial set of records or an unreferenced file. Raw pre-parser producer files and
working report files have their existing lifecycle; every raw-output write,
stale-lease/recovery window, deletion/publication race and retention/database
reconciliation still needs qualification. Repeated requests retain additional
snapshots; quotas and bounded retention remain D24/P work. A report's finding revision does not
version external suppression rules, policy, measured coverage or scan-run state.
Baseline/first-seen reconciliation remains open. Two compared scans each have a
consistent snapshot, not a shared transaction covering both scans.

Directory fsync and power-loss behavior on Windows, network filesystems, all
supported architectures and storage exhaustion remain platform gates. Source and
image/tool/schema provenance, SBOM completeness, all download permission cases and
production deployment are not certified by these tests. Upgrade API/workers
together; mixed-version writes are not qualified. No corroboration UI is enabled.

The [gosec gate](gosec-final.json) still fails with **29 high/high findings** (25 G703,
one G702, three G704), with no analysis errors. Four prior `scans.go` G703 sites
are absent after shared publication replaced those sinks; this inventory change
is not four independently reviewed security dispositions. The original
[43-alert and seven-manual-issue ledger](../scan-recovery/security-triage.json)
remains authoritative historical evidence. No waiver or suppression was added.
