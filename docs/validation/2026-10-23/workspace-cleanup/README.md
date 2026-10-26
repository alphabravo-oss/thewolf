# Managed workspace cleanup — 2026-10-23

Evidence for PLAN O08.1. Changes are uncommitted and newer than the qualified
images. No production service was changed. O08 and the full plan remain open.

## Repair and scope

Previously, source cleanup and administrative reap used pathname deletion
without coordinating with live preparation, retried scans or enrichment. The
configured-root check also allowed a nested path whose parent symlink redirected
deletion outside the root. Administrative reap ignored per-entry deletion errors
and accepted invalid/overflowing age values.

`scantarget` now owns shared regular-file leases for managed sources and requires
an exclusive nonblocking lease before deletion. Git/SSH preparation acquires its
lease before materializing source. Resumed/local reuse and CLI scans retain it;
the existing API cleanup handoff keeps the original lease through enrichment.
Borrowed-source release never deletes the source. Stale-worker reclaim and
administrative reap call the same confined cleanup. Unmanaged local sources
retain their ownership and literal path, including leading/trailing spaces.

Cleanup accepts only direct recognized temporary children of the opened root.
Lease files live outside source trees; their lifetime is serialized by a stable
namespace lock to avoid replacement races. Recursive deletion does not hold that
root-wide lock. Completed and unused crash-orphaned lease files are removed;
active orphan leases remain. Default temporary metadata is namespaced by user,
while configured service roots share metadata between cooperating processes.
The API bounds and validates retention input, reports retained-in-use counts,
and returns a failure on incomplete cleanup. Settings displays actual counts.

The first draft used directory locks. It was replaced before completion because
[NFS emulates Linux flock with byte-range locks and exclusive locks require a writable descriptor](https://man7.org/linux/man-pages/man2/flock.2.html).
The final implementation opens regular lease files read/write and uses native
Unix flock or Windows LockFileEx. That design choice is not NFS certification.

[Operator requirements](../../../scan-isolation.md#managed-workspace-cleanup)
cover service-owned roots, consistent worker configuration/identity, preservation
of live lock metadata, and draining old workers. Actual runtime qualification
here is Linux cooperating processes. RWX mount configurations, Windows/macOS
runtime behavior, remote jobs/subprocesses surviving a crashed owner, mixed
versions, artifact/image retention and low-disk prevention remain open. Advisory
locks cannot protect work performed by an old/noncooperating process.

## Verification

- Source regressions cover creation, resumed/local reuse, lexical/resolved aliases,
  subdirectories, recent/unmanaged content, root rename/replacement, nested
  symlink escapes, source ownership and literal paths. Separate child processes
  hold a source while the prior owner attempts cleanup; both graceful exit and
  process kill release the native lease. Metadata checks prove source trees stay
  empty until preparation, completed leases are removed, and orphan pruning does
  not unlink a held lease.
- The worker test exercises a real stale claim/retry with the prior executor
  retaining its source. The CLI fixture attempts cleanup during plugin execution
  and verifies release when the command finishes. API checks reject nine invalid
  payloads without deletion, retain active work, remove it after release, and
  return `cleanup_failed` for an inaccessible/missing configured root.
- `go test -race ./internal/scantarget ./internal/scanjob ./internal/scan/... ./internal/api/... ./internal/fix/... ./internal/cli ./cmd/wolf ./internal/serve ./internal/scannerruntime/... ./internal/scannerbuild ./internal/scannertools/manifest ./internal/scannertools/lock -count=1`
  passed **44 packages**, with one package having no tests, but failed two scanner
  metadata packages because the final regression test changed definition inputs.
  The [failed run is retained](race-before-final-sync.txt).
- After the final Windows test-portability adjustment, `make scanners-sync`
  regenerated and verified the context and lock. [Output](sync.txt).
  `go test -race ./internal/scantarget ./internal/scannerbuild ./internal/scannertools/manifest ./internal/scannertools/lock -count=1`
  then passed all four packages. [Output](final-regressions.txt). Together these
  runs cover **46 distinct passing packages**; the broad suite was not rerun in
  full after regeneration.
- `go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run`
  reports [zero issues](lint.txt). UI [typecheck](ui-typecheck.txt) and
  [lint](ui-lint.txt) scripts passed. The UI change is response typing and toast
  text; no new browser or full deployment qualification is claimed.
- `CGO_ENABLED=0 GOOS=... GOARCH=... go test -c ./internal/scantarget` succeeded
  for Linux ARM64, Windows AMD64 and macOS ARM64. [Commands, outcomes and test
  binary hashes](cross-compile.json). Compilation is not platform execution.
- Final `git diff --check`, generated-context verification, plan ID/count checks,
  and evidence hash checks passed.

The CI-equivalent locked gosec command
`gosec -fmt json -out gosec.json -severity high -confidence high -exclude-dir=internal/scannerbuild/context ./cmd/... ./internal/... ./pkg/... ./plugins/...`
still **fails with 36 findings**: 29 G703, four G702 and three G704, with no
analysis errors. [Report](gosec.json) and [ledger](security-triage.json) retain all
43 original alerts and four manual issues. No exception or suppression was added.
The original R03-04 excerpt is workspace-parent creation, not tree-digest reading;
this ledger corrects the earlier attribution. Parent creation moved files and
stopped being reported, but its configuration/ownership review remains open.
Static alert disappearance is not proof of security resolution.

[Source hashes and generated identities](source.json) identify this working-tree
candidate. `sha256.json` covers the evidence directory's other files. Historical
receipts from previous repairs remain unchanged.
