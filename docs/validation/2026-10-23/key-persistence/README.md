# Key persistence — 2026-10-23

Evidence for PLAN G04.1. Changes are uncommitted and newer than the previously
qualified images. No production service, database or key was changed. The full
plan, rotation lifecycle and release gates remain unfinished.

## Repair

JWT initialization previously generated another key after a failed/short read
and could continue with an ephemeral key if persistence failed. Encryption-key
initialization also treated read errors as a reason to write a new key. Both
used a read-then-write sequence that allowed concurrent first starts to replace
each other's persisted key. The encryption key's process-global slice was also
unsynchronized despite reloads during concurrent credential resolution.

Both file loaders now use `secrets.LoadOrCreateKeyFile`: open the configured
directory as `os.Root`, retain existing content or propagate read errors, create
an exclusive private temporary file, write/sync/close, then publish with a hard
link that cannot replace an existing entry. Read the canonical key after a
publication race. Existing malformed content is rejected by the format-specific
caller and preserved. API, scan worker and fixer initialization fails before
backend/database initialization when key loading fails; the custom-build
credential resolver already returned an error. Encryption operations read an
immutable, copied, atomically published key snapshot.

The formats remain compatible: generated JWT keys contain 32 raw bytes and
encryption keys contain 64 hexadecimal characters representing 32 bytes.
`WOLF_MASTER_KEY` retains precedence and its existing raw-for-JWT/hex-for-AES
interpretation. New key files use `0600`. [Operator recovery guidance](../../../deployment.md#persisted-keys-and-startup-failures)
explains shared keys, permissions, backup restoration and why deleting a key is
not a recovery procedure.

This is initialization/reload correctness, not asymmetric JWT rotation,
versioned decryption, independent security approval or power-loss certification.
Creation requires hard-link and directory-sync support. The checks ran on this
Linux host; other operating systems and distributed filesystems remain subject
to the supported deployment qualification matrix. Initial configured-directory
ownership remains an operator trust boundary. Interrupted processes may leave
private temporary entries; automated key recovery/retention is not certified.

## Verification

- `TestKeyFileConcurrentProcesses` forces **eight separate processes** to observe
  an absent key before releasing their candidate writers. Every process loads
  the persisted winner; the final file has the expected size/mode and no
  temporary entries remain.
- `TestKeyFileLoadsWinnerCreatedDuringGeneration` deterministically inserts a
  winning file after the initial read. A replacement rename would fail this
  regression even if a concurrent stress run happened to mask the overwrite.
- `TestKeyFilePermissionDeniedPreservesWritableKey` drops to UID/GID 65534 when
  run as root and reads a write-only key. Real `EACCES` stops generation and
  preserves the file; this does not rely on root honoring ordinary mode bits.
- `TestKeyFilePreservesExistingEntriesAndPinsDirectory` covers existing content,
  non-file entries, escaping/dangling links and rename/replacement of the opened
  directory during generation.
- Concurrent master-key reload/encryption checks prove ciphertext stays
  decryptable and caller-owned slices cannot mutate a published key. JWT checks
  prove concurrent initialization and access/refresh-token continuity. Invalid
  file/environment, unavailable-home/persistence and early startup failures are
  covered. Negative startup fixtures have no Docker executable on PATH.
- `go test -race ./internal/secrets ./internal/serve ./internal/auth/... ./internal/api/... ./internal/scantarget ./internal/cli ./internal/fix/... ./internal/scannercustombuildworker ./cmd/wolf ./internal/scannerbuild ./internal/scannertools/manifest ./internal/scannertools/lock -count=1`
  passed **38 packages**. [Output](race.txt).
- After the final test additions and regeneration,
  `go test -race ./internal/secrets ./internal/serve ./cmd/wolf ./internal/scannerbuild ./internal/scannertools/manifest ./internal/scannertools/lock -count=1`
  passed all six packages. [Output](final-regressions.txt).
- `go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run`
  reports zero issues. [Output](lint.txt). The final edit after lint only isolates
  PATH in the two negative startup tests; both packages were race-tested again.
- `make scanners-sync`, scanner metadata validation, final context verification
  and `git diff --check` passed. [Generation output](sync.txt).

The [real API restart harness](restart-check.py), invoked as
`python3 docs/validation/2026-10-23/key-persistence/restart-check.py /tmp/wolf-plan-key-persistence`,
uses a disposable home/database and localhost API with no environment key. It
registers a fixture user, stores a masked encrypted credential, gracefully stops
and restarts the API, authenticates with the original access token and rotates
the original refresh token. Both persisted keys and stored ciphertext remain
unchanged. It emits no keys, passwords or tokens and removes its temporary
state. [Receipt and binary SHA-256](restart.json). Ciphertext decryption itself
is covered by the Go regression; the HTTP secrets endpoint correctly stays
masked. Scanner initialization is skipped in this focused restart check.

Fresh high/high gosec analysis still **fails with 37 findings**: 30 G703, four
G702 and three G704, with no analysis errors. [Report](gosec.json) and
[ledger](security-triage.json) preserve all 43 original alerts plus the three
manually discovered issues. No suppression or accepted exception was added.
The final test-only edits do not change the analyzed production code.

[Source identity](source.json) records changed source/test/documentation hashes,
scanner lock and the final restart binary. `sha256.json` covers the evidence
directory's other files.
