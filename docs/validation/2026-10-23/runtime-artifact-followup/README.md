# Runtime startup and artifact reads — 2026-10-23

Evidence for PLAN S02.2 and R03.12. These changes are uncommitted and newer than
the parent directory's qualified images. No deployment, image rebuild, release
approval, or full-plan completion is claimed. Existing edits and live services
were preserved.

## Changes and limits

Startup now takes an explicit scanner runtime. The API and capability response
use the same normalized `WOLF_SCAN_RUNTIME` value as the worker's default;
`--backend` takes precedence for the worker. Kubernetes loads validated image
configuration without local Docker discovery or pulls. Loading configuration
does not mark a runtime ready. Docker still probes its daemon and reports errors;
local scan, doctor and pull retain their Docker behavior. Actual worker/node,
credentials, storage and image readiness remain the broader S02 requirement.

All four stored scan-artifact read sites—download, raw tool log, raw tool JSON,
and manifest—now use the same confined reader. It requires an initialized
artifact store, derives a key within that root, and reads through `os.Root`.
The original download-only lexical check did not prevent symlink escape; the
other handlers also accepted unconstrained stored paths. Valid files and
relative symlinks within the root still work. Authorization checks remain before
the reads; unreadable manifests retain the existing database-derived fallback.
Artifact writes, checksum/stat paths, hardlinks, cross-scan aliases within the
root, configured-root ownership, bounded reads and safe retention are separate
unfinished requirements. This repair is not whole-store isolation certification.

## Verification

- `TestLoadAndInstallUsesSelectedRuntime` uses a failing Docker sentinel to prove
  no Kubernetes/unknown-runtime Docker calls, retained Docker diagnostics,
  validated image configuration, and no readiness promotion from configuration.
- `TestScanWorkerRuntimeSelection` covers normalized/default/unknown environment
  values and explicit flag override.
- `TestReadFileConfinesStoredPaths` covers valid artifacts, confined links,
  direct outside paths, escaping file/directory links, missing files and an
  uninitialized backend.
- `TestArtifactReadEndpointsConfineStoredPaths` covers twelve API combinations
  across all four read sites. Existing manifest and cross-owner download checks
  also pass.
- `go test -race ./internal/setup/scanners ./internal/serve ./internal/api ./internal/api/routes ./cmd/wolf ./internal/plugin/container ./internal/scannerruntime/kubernetes ./internal/artifacts ./internal/scannerbuild ./internal/scannertools/manifest ./internal/scannertools/lock -count=1`
  passed **10 packages**; serve has no test files. [Output](race.txt).
- `go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run`
  passed with zero issues. [Output](lint.txt).
- `make scanners-sync`, the final generated-context check, scanner metadata
  validation and `git diff --check` passed. No UI source changed.

The first broad race command mistakenly named `internal/runtime/kubernetes`;
that package does not exist. Its [failed invocation output](race-initial-invalid-package.txt)
is retained; the corrected run above passed after both repairs.

Fresh locked gosec high/high analysis **still fails with 37 findings** (30 G703,
four G702, three G704), with no package-analysis errors. [Current report](gosec.json)
and [security ledger](security-triage.json) preserve all original 43 alerts and
both manually discovered findings. This artifact read issue was not detected by
the original static report; the existing artifact write/stat/checksum alerts
remain unresolved. No exception or suppression was added.

[Source identity](source.json) records changed source/test hashes and HEAD.
Scanner lock: `sha256:6ccf987d4abe5ab4346124863fe5312abd7e5b00f448de3ba1c9db116f495c2e`.
Definition: `sha256:6f1c1a82febfdc044a39020f4d0fd2689166cfad360df98bcd047536a69ce07c`.
The evidence checksum file covers this directory's other files.

PLAN G04.1 records a separate, still-open key-persistence issue discovered while
tracing setup: existing read errors can trigger replacement key writes, and
concurrent first starts can choose different keys. No key-loading change is
included in this batch.
