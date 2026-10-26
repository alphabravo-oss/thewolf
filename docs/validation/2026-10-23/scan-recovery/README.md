# Recovery source identity and evidence — 2026-10-23

Progress on A14/D24/A10 and related R03.9/O08 boundaries. These broader parents
remain incomplete. Changes are uncommitted, newer than qualified images and not
deployed. No production service was changed.

## Confirmed defects

The [executor reproduction](reproduction.txt) changed a prepared source, seeded a
completed tool and an incomplete tool with durable findings, then ran the real
queued-scan executor. It completed the scan, replaced source identity and discarded
the incomplete tool's prior finding without first verifying source equality.

Stale-worker cleanup also scheduled retryable pending workspaces for deletion;
the old test expected that behavior. The replacement regression now checks the
actual directory without a live process hold: retryable source survives, while
an exhausted failed source remains eligible for cleanup.

Reviewing the digest uncovered ambiguous serialization rather than a SHA-256
weakness: one binary file could serialize identically to two separate files,
and a regular file could serialize identically to symlink text. Executable-mode
changes were also invisible. The [corrected failing harness](digest-reproduction-fixed-harness.txt)
reproduces all three on the old implementation. The first harness accidentally
called a parent test's Fatal from a subtest; its output is retained in
[digest-reproduction.txt](digest-reproduction.txt), not treated as three proofs.

## Repairs

- Recovery reads run records/findings and verifies prepared tree digest, source
  kind/path and commit before resetting unfinished tools or reusing completed
  results. Changed/missing/legacy identity fails while retaining prior evidence.
  Read/reset errors are propagated. Claims lost before any identity or evidence
  was saved can still prepare normally.
- Git and GitHub preparation now compute the same workspace-content identity used
  for later reuse. Preparation-only credentials are removed before hashing.
  Retained SSH archives preserve their remote provenance despite lacking `.git`.
- Versioned `sha256-v2:` digests use sorted, byte-length-framed paths/payloads,
  entry type and permissions. They include directories, ignore Git metadata,
  hash symlink text and reject unsupported special files. Nonblocking regular-file
  opens validate descriptor identity/type; detected size/mode/mtime changes fail.
  A fixed reference vector protects the encoding format.
- Stale-worker cleanup retains pending retry sources. Recovery finding deletion
  locks and checks the scan row/lease/cancellation state in the same transaction
  on SQLite and PostgreSQL before deleting one tool's rows. Stale/blank/incorrect
  leased owners and cancelled scans are rejected.

No new dependency or database schema was introduced. The internal Store deletion
method now takes the execution lease. [Operator documentation](../../../scan-isolation.md#scan-recovery-source-identity)
records compatibility and limits. Legacy completed evidence is not rewritten.
Drain jobs before upgrade: a recovered legacy digest cannot establish v2 equality
and requires a new scan. This deliberately avoids claiming an unchanged source
from an incompatible historical digest.

## Verification

- [Focused contracts](contracts.txt) ran real SQLite and disposable PostgreSQL
  lease/deletion tests, stale-workspace cleanup, queued executor recovery and
  source identity reuse. The later full/final suites also cover injected run-record
  read, finding read and reset failures.
- The executor cases cover changed, missing/legacy identity, unchanged source,
  retained SSH archive provenance and database failure. Unchanged source reruns
  only the unfinished tool and keeps the completed finding/run record. Rejection
  leaves both previous findings and run attempts intact.
- Native Git fetch/checkout runs against a local `git upload-pack` fixture via a
  substituted SSH executable; no network endpoint is contacted. The test uses
  explicit fixture credentials, verifies their removal, and compares first/reused
  digest and commit. It is not SSH cryptographic-authentication qualification.
  GitHub's injected cloner uses a real local Git tree to prove digest consistency;
  live GitHub clone/concurrency qualification is still separate.
- `WOLF_TEST_POSTGRES_DSN=... go test -race ./cmd/... ./internal/... ./pkg/... ./plugins/... -count=1`
  passed **135 packages** before the final v2 digest repair. [Output](race.txt).
- After the v2 repair and final metadata regeneration,
  `WOLF_TEST_POSTGRES_DSN=... go test -race ./internal/scantarget ./internal/api/routes ./internal/scanjob ./internal/db ./internal/fix/workspace ./internal/fix/lineage ./internal/cli ./cmd/wolf ./internal/scannerbuild ./internal/scannertools/... -count=1`
  passed **18 affected packages**. [Output](final-race.txt).
- A final guard retains `PrepareExisting`'s local path when an older/new child scan
  has no recorded source path, while preserving nonempty remote provenance. After
  resync, API routes and the three context/manifest/lock packages passed another
  [four-package race run](final-source-fallback.txt).
- Final `golangci-lint v2.14.0 run` reports [zero issues](lint-final.txt).
  [Scanner sync](sync-final.txt) verifies context/lock, 53 tools and 51 pins.
- `scantarget` cross-compiles for Linux ARM64, Windows AMD64 and macOS ARM64.
  [Receipts](cross-compile.json). Runtime verification ran on Linux only.
- [PostgreSQL receipt](postgres.json) records the pinned image, loopback ephemeral
  port and successful removal of the disposable container and its volume.
  Existing live service containers remained running; no production rollout occurred.

The high/high gosec gate still fails with **33 findings** (29 G703, one G702,
three G704), without analysis errors. [Report](gosec.json) and
[ledger](security-triage.json) preserve all 43 baseline alerts plus seven manual
issues. Unchanged static counts do not negate the manually reproduced digest bug.
No suppression or accepted exception was added.

## Remaining gates

The comparison is not an atomic or immutable filesystem snapshot. Source writers
can still mutate content after preparation; GitHub's shared cache can change
concurrently. Full hashing resource/cancellation bounds, independent scanner Jobs,
all platform/RWX behavior, policy/release intent and aggregate/coverage recovery
qualification remain open. Reset transactions are per tool, not an atomic reset
of every tool and scan metadata. Administrative TTL cleanup can still affect a
long-queued unheld workspace, which must fail safely if unavailable.

A14, D24 and their larger qualification gates remain unchecked. The plan remains
**230 unique tasks, 45 checked**. [Source hashes](source.json), final checks and
`sha256.json` identify this exact working-tree evidence; older receipts remain
historical and unchanged.
