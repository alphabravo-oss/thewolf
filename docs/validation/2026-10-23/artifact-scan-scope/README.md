# D06.4 — bind artifact reads and snapshot publication to a scan

Candidate: uncommitted working tree, 2026-10-23. No deployment or historical
artifact migration was performed. D06 and the complete plan remain open.

## Reproduction and repair

The [initial reproduction](reproduction.txt) demonstrates that changing a record's
scan ID still returned another scan's bytes from a direct path or storage key.
A legacy directory's short-ID suffix did not establish ownership either.

A separate [baseline reproduction](baseline-links-reproduction.txt) used the
previous snapshot implementation, hash-checked against the preceding qualification,
in an isolated temporary module. It confirms relative file/directory aliases,
hardlinks, invalid scan IDs, cross-scan directory publication and legacy prefix
collisions. Publication added an unwanted snapshot to the foreign directory.
The working-tree implementation was not reverted. Fatal assertions were converted
to nonfatal assertions in that temporary copy to retain every failure; the new
helper-specific pinning test was omitted because the old helper did not exist.
The first attempt selected Go 1.27.0 and failed before tests; its
[toolchain error](baseline-links-initial.txt) is retained. The actual reproduction
used the required Go 1.27.3. [Baseline input hashes and transformations](baseline-source.json) identify the experiment; this is not a retained historical source bundle.

Shared artifact reads now resolve a recorded path/key beneath a scan directory.
Full-ID directories must match `ScanID`. The directory must be real and the opened
root must identify the directory checked by `Lstat`. File operations then stay
inside that pinned root. Relative links within the same scan remain supported;
cross-scan links and multiply linked files are rejected. Link-count checks use
existing standard/installed platform APIs; unsupported platforms fail closed.

Snapshot publication uses the same directory boundary for file creation, rename
and sync. Its IDs, relative storage keys, checksums and relocation behavior remain
unchanged. A symlink at the scan-directory path cannot redirect publication into
a sibling scan.

Legacy named directories require a matching full scan ID in a confined regular
`manifest.json`, capped at 1 MiB and parsed as a complete JSON document. Missing,
malformed, oversized, trailing-document or colliding-prefix manifests fail.
Full-ID legacy paths without checksums remain readable without claiming integrity
verification. Unbound old bundles are retained for explicit operator verification
and migration; no guessed ownership is introduced.

Manifest, download and raw log/JSON endpoints share typed boundary-error handling.
Identity failures return `409 artifact_scope`, without private content or fallback
to another artifact. Raw JSON checksum failures now use the same integrity response
as the other endpoints. The manifest fixture now uses an actual per-scan directory
and asserts producer metadata survives, so fallback cannot mask a failed read.

## Runnable checks

```sh
go test -race -count=1 ./internal/artifacts ./internal/api/routes -run 'TestArtifact|TestLegacyNamedArtifact|TestScanArtifactRoot|TestPublish|TestGetScanManifestFromArtifact|TestManifestUsesCurrentRevision|TestLiveManifestCounts'
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -c -o /tmp/artifacts-windows.test.exe ./internal/artifacts
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go test -c -o /tmp/artifacts-darwin.test ./internal/artifacts
make scanners-sync
WOLF_TEST_POSTGRES_DSN='<owned loopback fixture>' make test
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run
```

[Qualified contracts](contracts-qualified.txt) cover 28 API cases across download,
raw log, raw JSON and manifest endpoints: foreign absolute path/key, relative file
alias, hardlink, scan-directory alias, legacy short-ID collision and valid legacy
identity. Shared-package checks additionally cover invalid IDs, same-scan links,
legacy manifest errors/size, pinned directory rename/replacement, publication
isolation, cancellation, immutable/checksummed snapshots and store relocation.
Earlier [artifact checks](artifacts.txt), [contracts](contracts.txt) and
[expanded contracts](contracts-final.txt) are retained. [Windows](windows-build.txt)
and [macOS](darwin-build.txt) package cross-compilation pass; these are not runtime
qualification for those platforms.

## Final verification

The [136-package uncached race suite](race.txt) passes with SQLite and real
PostgreSQL. New HTTP cases use SQLite. [Lint](lint.txt) reports zero issues;
[scanner synchronization](sync.txt) validates 53 tools and 51 pins. The
[security scan](gosec.json) retains 29 high/high findings (25 G703, one G702,
three G704), no analysis errors and no new waivers. R03 remains release-blocking.

The owned [PostgreSQL fixture](postgres.json) and anonymous volume were removed
and their absence verified. [Check summary](checks.json), [source hashes](source.json)
and [receipt hashes](sha256.json) identify the tested working tree. Historical
qualification records remain unchanged. No service was replaced or deployed.

## Limits

Active artifact storage must be writable only by the service identity. Confinement
and link-count checks do not protect against a privileged writer copying foreign
bytes into a scan, removing other hardlinks before validation, or changing a legacy
manifest/content in place. Checksummed snapshots detect content changes when read;
unchecksummed legacy files cannot make that claim. Hardlink-based deduplication of
active artifact storage is unsupported.

A live directory rename can leave stale metadata/orphaned output even though pinned
operations do not write into its replacement. Broader raw/staging writers, retention
coordination, distributed filesystems, resource limits and non-Linux runtime evidence
remain separate gates. This closes the reproduced read/publication identity boundary,
not every filesystem threat or the full D06 authorization/import matrix. The
[storage documentation](../../../scan-isolation.md#artifact-scan-ownership) records
the operational compatibility limits.
