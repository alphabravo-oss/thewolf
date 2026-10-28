# Scanner isolation

Default is **deny-by-default network** (`--network none`) plus `--cap-drop ALL`
and `no-new-privileges`. Tools that set `network_required: true` in
`scanners/tools.yaml` run with `--network bridge` (override with
`WOLF_SCANNERS_ALLOW_NETWORK`).

| Knob | Default | Meaning |
|---|---|---|
| `WOLF_SCANNERS_NETWORK` | `none` | Network for offline tools |
| `WOLF_SCANNERS_ALLOW_NETWORK` | `bridge` | Network for `network_required` tools |
| `WOLF_SCAN_ISOLATION` | `standard` | `standard`/`strict` = deny-by-default; `relaxed` = legacy all-bridge |

Kubernetes scan Jobs already run without docker.sock on the API pod
(`WOLF_SCAN_RUNTIME=kubernetes` on workers). The Wolf Enterprise Helm NetworkPolicy
`*-scanner-network-required` is egress-empty unless you set
`networkPolicy.scannerEgressCIDRs`.

Community Compose still uses Docker-out-of-Docker for laptops. On shared hosts
use `docker-compose.hardened.yml` (socket proxy). Enterprise must not mount
docker.sock on the API Deployment.

## Managed workspace cleanup

Git and SSH source preparation holds a shared regular-file lease outside its source tree
through scanning, enrichment and final report generation. Resumed scans and local sources that
reuse a managed directory also hold it. Worker reclaim and administrative reap
require an exclusive nonblocking lock; an in-use source is retained. All managed
deletion uses the opened storage root and accepts only direct temporary-source
children, so nested path aliases cannot redirect cleanup outside that root.

The administration action defaults to 72 hours, rejects invalid/overflowing
ages, and reports removed and retained-in-use counts. Filesystem failures are
reported as incomplete cleanup, with details in server diagnostics. Release of
a borrowed workspace does not delete it; the owner or a later reap performs
cleanup after all uses finish.

Lease files are opened for writing so exclusive locks do not depend on directory
locking (not supported by NFS's lock emulation). A stable namespace lock protects
lease creation/removal; recursive deletion does not hold that root-wide lock.
Completed workspaces leave no per-workspace lease, and reap removes unused orphan
leases after interrupted cleanup. Do not delete lock metadata while Wolf is
running: replacing a locked inode breaks coordination.

The configured root and its metadata must be owned by the service identity,
with the same `WOLF_WORKSPACE_ROOT` configuration and compatible filesystem
identity across cooperating processes. Unconfigured local use preserves the
system temporary directory and gives metadata a per-user namespace; it is not
cross-user service coordination. The admin action requires a configured root.

Native file locking is implemented for Linux, macOS, supported BSD targets and
Windows. Managed source preparation/cleanup fails explicitly on other platforms
or filesystems without this capability. Runtime evidence currently covers Linux;
cross-compilation is not runtime or shared-filesystem qualification. This protects
cooperating processes; it
does not certify remote jobs or subprocesses that outlive a crashed owner.
Before enabling cleanup across an upgrade, drain old workers that do not hold
these locks. RWX/distributed filesystem locking, orphaned Docker/Kubernetes
jobs, rolling upgrades, artifact retention and rollback-image retention remain
qualification gates in PLAN O04/O08/O12/O13.

## Local Git metadata boundaries

Local setup branch discovery and registered local-repository branch lookups use
`WOLF_BROWSE_ROOTS`. Each working directory, linked-worktree/submodule Git directory,
and common Git directory must be inside an approved root. The default remains the
service user's home directory. Separate roots with the platform path-list separator
(`:` on Unix, `;` on Windows). Before upgrading, add the approved locations of any
existing local sources or linked metadata stored outside that default; registration
alone does not bypass the metadata allowlist.

Branch inspection reads pinned directories and bounded reference files without
running Git. Loose and packed references, linked worktrees, SHA-1/SHA-256 reference
names, detached HEAD, and an unborn branch are supported. Reference names are not
proof that every referenced commit/object is present or valid. Object contents,
alternates and repository hooks are not read or executed during this inspection.
Reftable repositories return an explicit unsupported-format error; reftable support
and full Git execution isolation remain open in PLAN R03.3.

Origin lookup reads the bounded local configuration and, when enabled, the linked
worktree configuration. Local URL rewrite rules remain effective. Global/system Git
configuration, process Git overrides, and `include`/`includeIf` files are not used.
Sources that rely on included/global origin settings must declare the intended
origin in their local configuration for this endpoint. This policy also prevents an
untrusted include from reading another host file during branch discovery. Broader
include compatibility remains part of the open Git boundary work.

Reference input is limited to 8 MiB, individual loose references and directory
pointers to 4 KiB, configuration input to 1 MiB, reference/directory entries to
10,000, and reference-directory nesting to 64 levels. Packed tags are filtered
before Git reference parsing and do not consume the branch-reference limit, but
the packed file still counts against the byte limit. Non-regular, oversized or
malformed files produce errors; no branch list is silently truncated. File opens
are nonblocking before checking file type, preventing a substituted FIFO from
hanging a request. Native/SSH Git commands used for scanning, mutation, diffing,
release work and transport still require their separate isolation qualification.


## Native Git command defaults and retained sources

Source preparation and the fix Git/workspace, patch, hygiene, lineage, push and
live-diff helpers use a shared native-command boundary. It discards inherited
`GIT_*` overrides except author/committer name, email and date, ignores global
and system Git configuration, disables interactive prompts and optional index
refresh writes, and suppresses fsmonitor, hooks, signing and credential-helper
execution. Repository-configured SSH commands are replaced with `ssh`.
Per-call credentials created by Wolf still supply `GIT_ASKPASS` or
`GIT_SSH_COMMAND`; credential ownership/host checks are unchanged. Native Git
commands dependent on global aliases, URL rewrites, user identity, signing or
credential helpers need explicit supported configuration. Local/global credential
helpers are intentionally not a fallback for unattended pushes; configure the
supported Wolf credential or transport instead. Author identity can be explicit
per-command, repository-local, or supplied by author/committer environment fields.

Existing workspace inspection has a ten-second Git deadline, honors earlier
caller cancellation, and fails on Git inspection/status errors instead of
reporting a clean checkout. Ordinary non-Git directories remain supported with
unknown Git state. A scan with a recorded prepared workspace now fails source
preparation if that workspace is missing, corrupt or no longer a directory;
it does not silently fetch a potentially changed branch. The failed scan retains
its recorded source identity. Start a new scan after correcting the source.

These defaults are **not an OS sandbox**. Repository clean/smudge/process filters,
configuration includes, external gitdirs/objects, symlinks and path replacement,
transport redirects/DNS, command output limits and process resource limits still
require R03.3 qualification. The two-second command `WaitDelay` only bounds pipe
draining after exit/cancellation; it is not an execution deadline. Background
contexts in older callers still need explicit command deadlines. General service
environment variables are still inherited. Scanner suppression's Git ignore
lookup, release/CLI build runners and SSH shell execution are outside this shared
boundary. The GitHub cache can still be mutated concurrently. Recovery checks source identity
before reusing evidence, but this is not an immutable snapshot during scanning. Read-only scanner mounts do not resolve these preparation-time gaps.


## Scan recovery source identity

A recovered scan validates its prepared content digest, commit (where Git metadata
exists), source kind and source path before deleting incomplete-tool findings or
reusing completed results. Changed or unavailable original identity fails source
preparation and preserves prior evidence. Missing run-record/finding reads and
failed recovery resets also fail preparation. A claim lost before any source
identity or evidence was saved can still prepare its source normally.

Git, GitHub, local working trees and SSH archives now record the same versioned
workspace digest, `sha256-v2:<hex>`. Its sorted entries use byte-length-framed
paths and payloads plus entry type and permissions; files, symlinks and directory
entries are distinct. `.git` directories and worktree pointer files are excluded.
Symlink text is hashed without reading its target. Special files fail preparation;
regular-file reads check the descriptor identity/type and reject detected size,
mode or modification-time changes while hashing. Git credential preparation files
are removed before computing the source digest.

Earlier NUL-delimited digests were ambiguous for binary contents and symlink text,
and omitted mode changes. Legacy `sha256:`/`git:` or missing identities cannot
prove equality to v2. Drain active jobs before upgrading; if an older job is
recovered after upgrade, start a new scan when its identity cannot be verified.
Historical completed artifacts retain their original identity; they are not
rewritten or retroactively certified by this change.

For retained SSH archives, the durable remote path/node/commit/dirty state remains
the source provenance even though the archive contains no `.git`. Stale-worker
cleanup keeps a workspace queued for another attempt; exhausted failed jobs remain
eligible for the existing confined, lease-aware cleanup. Recovery finding deletion
locks/checks the scan lease in the same transaction,
rejecting replaced or cancelled owners before deleting the requested tool's rows.

This is a recovery-time comparison, not a frozen filesystem snapshot. Concurrent
writers during or after preparation, shared GitHub-cache mutation, independent
scanner Jobs surviving a worker, administrator retention cleanup of long-queued
sources, policy changes between attempts and complete artifact/release provenance
still need the plan's broader isolation and recovery qualification. Recovery resets
are transactional per tool, not one transaction across all tools and scan metadata.

## Artifact scan ownership

Recorded artifact reads are confined to their scan directory, not merely the
shared artifact root. Full-ID directories must match the record's `scan_id`.
Relative symlinks may stay inside that directory; links to sibling scans,
directory aliases and files with multiple hardlinks are rejected. Publication
also pins a real per-scan directory before creating or renaming snapshots.

Legacy `<repo>_<timestamp>_<short-id>` directories require a regular, readable
`manifest.json` containing the matching full scan ID. The manifest is limited
to 1 MiB and must be one complete JSON document. A matching short suffix alone
is insufficient. Missing, mismatched or unbound legacy evidence returns an
explicit artifact-scope error; Wolf does not silently use a fallback report for
that error or delete the original files. An operator must verify and migrate
unbound bundles before serving them. Existing full-ID bundles without checksums
remain readable but do not claim integrity verification.

Keep active artifact storage writable only by the service identity. Hardlink-based
storage deduplication is unsupported for active artifacts; copies preserve the
single-link boundary. Confinement is not protection against arbitrary privileged
writers copying or modifying bytes inside the scan directory. Renaming a live
scan directory can leave stale metadata or orphaned output, even though pinned
operations do not follow its replacement into another scan. Retention coordination,
shared-filesystem behavior and non-Linux runtime qualification remain plan gates.
