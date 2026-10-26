# Local Git metadata confinement — 2026-10-23

Progress on PLAN R03.3, which remains unchecked. The working tree is uncommitted
and newer than qualified images. No deployment was performed. This repair does
not complete native/SSH Git isolation, the security gate, or the product plan.

## Confirmed defect and repair

The old `GitInfo` route checked `.git` through the approved browse root, then
passed the pathname to native Git. A permitted directory with a `.git` file
pointing at an outside repository returned that repository's branch information.
The [failing reproduction](reproduction.txt) records HTTP 200 and the fixture
branch `outside-secret`. Root replacement also separated the prior check from
Git's subsequent pathname lookup.

The registered-repository branch endpoint was a second caller: it read the
origin with native Git and then enumerated branches. Both endpoints now enforce
`WOLF_BROWSE_ROOTS` for the source, linked Git directory and common directory,
before reading metadata or looking up remote branches/credentials. The common
browse helper now uses the standard platform path-list parser. A file selected
in the setup picker still browses its parent; a registered source or metadata
pointer must identify a directory.

Shared `BranchesRoot`, `CurrentBranch`, `OriginURLRoot` and their existing local
wrappers replace native branch/origin subprocesses with pinned `os.Root` reads.
`repo.ValidateLocal` reuses these helpers. The installed go-git/go-billy packages
parse a bounded reference snapshot and local configuration; go-billy's existing
version is reclassified as a direct dependency, with no new module/version.
The snapshot contains HEAD and local/remote references; tags are filtered from
packed references before Git parsing. No object contents, config includes,
hooks, or alternate object stores are copied or executed for branch inspection.

The implementation retains local/remote deduplication, remote-prefix removal,
remote-HEAD exclusion, sorting, linked worktrees, detached/unborn HEAD and
SHA-1/SHA-256 reference names. Current-branch lookup reads HEAD rather than
traversing every branch. Origin parsing retains local `insteadOf` rules and
explicitly enabled worktree configuration. The existing HTTPS GitHub origin
check still runs before credential selection.

## Bounds and compatibility

[Operator documentation](../../../scan-isolation.md#local-git-metadata-boundaries)
records the required source/metadata roots, upgrade impact, 8 MiB reference-data
and 1 MiB configuration limits, 4 KiB loose-reference/pointer limit, 10,000-entry
bounds, and depth limit. Nonblocking opens followed by descriptor type/size
checks prevent FIFO hangs and special-file reads. Errors return no partial
branch list. Request cancellation is checked during metadata traversal; this
is not a guarantee that an unhealthy filesystem's kernel I/O is interruptible.

Existing local sources outside approved browse roots now need operator
configuration before their registered branch lookup succeeds. Global/system
Git config, process Git overrides and `include`/`includeIf` are not consulted;
origin settings relying on them need explicit local configuration for this
endpoint. Local worktree configuration and local URL rewrites are preserved.

Reftable branch storage explicitly fails closed and remains a compatibility gap.
Reference syntax/structure checks do not establish commit/object integrity or an
atomic snapshot of concurrent Git writers. Full source/object-store isolation,
remaining native Git hooks/filters/environment/resource controls, SSH commands,
included-configuration compatibility and independent review remain R03.3.
[The remaining native-command inventory](native-git-inventory.json) records 27
literal/gitPath invocation sites plus known indirect release/SSH paths. It is a
search inventory, not a completed audit of generic command dispatch.

## Verification

- [Focused regressions](focused.txt) cover the original escape, allowed/disallowed
  linked metadata/common directories, explicit multiple allowlist roots, invalid
  metadata pointers, escaping reference/config symlinks, root rename/replacement,
  hostile global Git variables, no Git executable on PATH, local URL rewrites,
  common/worktree origin precedence and the existing credential-origin cases.
- Source checks exercise packed/loose/remote references, remote HEAD exclusion,
  unborn/detached HEAD, SHA-256 repositories, more than 10,000 unrelated tags,
  malformed hashes/names, missing object storage, reference-byte/entry/depth
  bounds, cancelled contexts and reftable rejection. A real Unix FIFO test
  proves inspection returns without waiting for a writer. Final additions are
  covered by the final full package rerun below.
- `go test -race ./internal/fix/... ./internal/repo ./internal/scantarget ./internal/api/... ./internal/cli ./cmd/wolf ./internal/scannerbuild ./internal/scannertools/manifest ./internal/scannertools/lock -count=1`
  passed **34 packages**. [Output](race.txt).
- After preserving filesystem errors during format detection, adding the object
  directory structure check and its regression, and regenerating metadata,
  `go test -race ./internal/fix/git ./internal/repo ./internal/api/routes ./internal/scannerbuild ./internal/scannertools/manifest ./internal/scannertools/lock -count=1`
  passed all **six** affected packages. [Output](final-regressions.txt).
- `go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run`
  reports [zero issues](lint.txt).
- `make scanners-sync` regenerated and verified context and lock, validating 53
  tools and 51 version pins. [Output](sync.txt). Final `git diff --check`, context
  verification, plan ID/count checks and evidence/source hash checks passed.
- `CGO_ENABLED=0 GOOS=... GOARCH=... go test -c ./internal/fix/git` succeeded for
  Linux ARM64, Windows AMD64 and macOS ARM64. [Commands, outcomes and test-binary
  hashes](cross-compile.json). Runtime tests ran on Linux; cross-compilation does
  not qualify platform runtime behavior. No new UI/browser or deployment run is
  claimed for these backend changes.

The locked CI-equivalent command
`gosec -fmt json -out gosec.json -severity high -confidence high -exclude-dir=internal/scannerbuild/context ./cmd/... ./internal/... ./pkg/... ./plugins/...`
still fails with **36 findings**: 29 G703, four G702 and three G704, with no
analysis errors. [Report](gosec.json) and [security ledger](security-triage.json)
retain all 43 original alerts and five manual issues. This metadata escape was
found manually; unchanged static counts do not mean the repair had no effect.
No suppression or accepted exception was added.

[Source identity](source.json) records the changed source/tests/docs and generated
identities. `sha256.json` covers the other evidence files. Previous qualification
and repair receipts remain unchanged. The plan remains **230 unique tasks,
45 checked**: this is progress on an existing larger task, not closure of its
remaining acceptance criteria.
