# Candidate qualification — 2026-10-23

This is evidence for [PLAN](../../PLAN.md), not a release approval. The candidate
is an uncommitted working tree on `postgres-modes`, HEAD
`23b64ce09c569b7f49c465b53573e40c791b6fc5`. Existing edits were preserved. No
production service was replaced, no image was published externally, and the
Enterprise pin was not changed. At this initial qualification pass, the checklist had 225 unique tasks,
39 completed; that pass completed 12. See PLAN for current completion status. The remaining roadmap is not certified
or represented as finished.

## Identity and review boundaries

- [Deployment baseline](deployment-baseline.json): running API/UI/scan-worker
  and fixer versions, image/binary identities, Compose provenance, read-only
  health and SQLite schema inspection, and Enterprise/core comparison.
- [Change inventory](change-inventory.json): changed paths, content hashes,
  generated-file flags, proposed review units and dependencies. Shared files
  still need hunk-level review. This is not a complete source archive.
- [Local images](local-images.json): native AMD64 image identities and labels.
- [Runner inventory](../../runner-build-matrix.md): all 14 Dockerfile paths and
  their 18 runtime/fixture roles, inputs, consumers, platforms and qualification
  requirements.

The live API/scan-worker reports `0.2.2-test-coverage-20261013-064155`; the live
fixer reports `0.2.2-auto-20261013-045023-r2`. Both use `23b64ce-dirty`, which
cannot identify the old working tree. Individual current edits cannot safely
be attributed to that deployment. SQLite has `user_version=0` and no migration
ledger; the baseline records a schema fingerprint instead of inventing a
migration number. Enterprise HEAD is `69175243831651f9d3b42bc9a17dd41e8656364d`,
with core pinned to `5cb9a9fff69d986577d457e3f78dcaec65747b72`.

The final API/worker candidate is `0.2.2-plan-20261023-r5`, stamped
`23b64ce-dirty-37aac45cd752`. Its scanner lock is
`sha256:37aac45cd7525a2f09755cbffc3d1306acf934bd967b94c0eb46110b8f1344e3`,
with definition
`sha256:011d0fe83974a57da40edcb95583c4ec6371a3abea1a4a4bae94fc8e76893000`.
The combined fixer smoke used revision r2, before the schedule-store repair;
its evidence is explicitly separate from r5 API/worker qualification.

## Checks and outcomes

Commands run from the Community root unless `ui/` is specified. PostgreSQL
checks used a disposable PostgreSQL 18.6 instance with isolated test schemas;
`WOLF_TEST_POSTGRES_DSN` must target a test instance, never the live database.

| Check | Command | Outcome / retained evidence |
|---|---|---|
| Go, SQLite and PostgreSQL | `WOLF_TEST_POSTGRES_DSN=... go test -race -count=1 -timeout 120s ./...` | Passed, 135 packages with tests, 15 without tests; [output](go-race.txt). Opt-in external tests remain subject to their environment gates. |
| Final changed Go packages | `go test -race ./internal/api/routes ./internal/fix/orchestrator ./internal/fix/workspace ./internal/scantarget ./internal/scannerbuild ./internal/scannertools/manifest ./internal/scannertools/lock -count=1` | Seven packages passed after the final directory-close lint cleanup; [output](go-race-final-changes.txt). The complete 135-package run preceded that cleanup. |
| CI linter | `go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run` | Passed, zero issues; [output](golangci.txt). |
| Static Go analysis | `go vet ./...` | Passed after r3 changes. |
| Registry concurrency regression | `go test -race ./internal/scannerregistry -count=50` | Passed all 50 repetitions; [output](registry-repeat.txt). |
| Metadata, quality, build inputs, Helm | `make scanners-sync`; `make scanners-validate helm-validate` | Passed. 53 tools, 23 families, 53 parser adapters, eight scanner/fixer platform tuples, six OS variants / 112 package pins. Helm rendering is not cluster qualification. |
| UI checks | In `ui/`: `pnpm typecheck && pnpm lint && pnpm test && pnpm build && pnpm test:e2e:typecheck` | Passed with pinned Node 26.11.1 / pnpm 12.11.2; 53 tests in 11 files; [output](ui-checks.txt). |
| Browser checks | In `ui/`: `pnpm test:e2e --workers=4` | 77 passed, two project-specific skips; [output](browser-checks.txt). Desktop Chromium/Firefox and mobile Chromium. API fixtures are mocked; this is separate from real scans below. |
| Reachable Go vulnerabilities | `go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 -show verbose ./...` | Zero reachable vulnerabilities after toolchain update; [output](govulncheck.txt). |
| Git-history secrets | `go run github.com/zricethezav/gitleaks/v8@v8.30.1 git --redact --no-banner .` | 351 commits / 27.75 MB, no leaks; [output](gitleaks.txt). Existing historical fixture ignores remain. This command does not scan ignored local files. |
| Application security | Locked `scanners/go-tools` build of gosec, high severity/high confidence, excluding generated context | **Failed: 38 findings** (31 G703, four G702, three G704); [current report](gosec-current.json), [all 43 original alerts](security-triage.json). No new suppression or accepted exception. |
| License and public/private hygiene | Existing `.github/workflows/go.yml` checks | Required policy/NOTICE/contribution files and README wording passed; public imports/CI contain no private-overlay dependency. This is not counsel approval or a complete third-party license inventory. |
| Runtime image security | Trivy 0.75.0, locked verified DBs, `--scanners vuln,secret,license --severity HIGH,CRITICAL --ignore-unfixed`, existing CI license allowlist, **no CVE exceptions** | Passed after OS package upgrades; [six prior package findings](runtime-trivy-before.json), [final zero findings](runtime-trivy.json). Does not claim absence of unfixed/lower-severity vulnerabilities or license obligations. |
| Release exception policy | `python3 scanners/ci/validate-trivy-exceptions.py .trivyignore --image runtime --output <file>` | **Failed**: existing exceptions expired 2026-09-10; [output](trivy-exceptions.txt). No extensions or new exceptions were added. |
| Shell | `bash -n` and scanner-image `shellcheck --severity=warning` on the native/Compose/Kind harnesses | Passed. Host ShellCheck was absent; used the immutable scanner image's installed binary. |
| API/worker image | `scanners/ci/smoke-deployment-image.sh <runtime-digest> linux/amd64 runtime` | Passed; [output](runtime-smoke.txt). |
| Combined fixer image | Non-root `docker run --network none --read-only` with bounded writable home/tmp; invoke `wolf`, `claude`, `codex`, `opencode`, `go`, `node` versions and locate `goimports` | Passed; [output](fixer-engines-smoke.txt). No agent credentials or model requests were used. |

The browser skips are the mobile-navigation test in the desktop project and
the reduced-motion/browser-chrome test in the mobile project. They are retained
as skips, not counted as passes. T3 preview failed with the root Chrome sandbox
error; the repository's documented Playwright workflow was used.

## Real scan and restart receipts

Each lane used the actual API, persistent database, separate scan worker, and
immutable default scanner image. Each submitted a Bandit fixture, repeated its
idempotency key, verified terminal state, source commit/tree digest, scanner
run/image identity and persisted findings/artifacts, then restarted the API and
worker and repeated the scan. This is ordinary restart recovery; it is not
active-job loss, disk exhaustion, multi-replica load, or release rollback.

| Lane | Receipt | Result |
|---|---|---|
| Native + SQLite | [native-sqlite.json](native-sqlite.json) | Passed |
| Native + PostgreSQL 18.6 | [native-postgres.json](native-postgres.json) | Passed |
| Compose + SQLite | [compose-sqlite.json](compose-sqlite.json) | Passed |
| Compose + PostgreSQL 18.6 | [compose-postgres.json](compose-postgres.json) | Passed |
| Kind / genuine Kubernetes Jobs + PostgreSQL 18.6 | [kind-postgres.json](kind-postgres.json) | Passed |

Reproduce using the checked-in `scripts/e2e/remote-scan-native.sh` and
`remote-scan-compose.sh`. Set `WOLF_RUN_REMOTE_SCAN_NATIVE_E2E=1` or
`WOLF_RUN_REMOTE_SCAN_COMPOSE_E2E=1`, `WOLF_E2E_DATABASE=sqlite|postgres`,
`WOLF_E2E_TIMEOUT_SECONDS=120`, and these exact inputs:

```text
WOLF_E2E_RUNTIME_IMAGE=wolf@sha256:11f7ec73bfb3cd7714e72631f577d25c4db64059255da7b520a4b1ece0ac8af3
WOLF_E2E_SCANNER_IMAGE=wolf-scanners@sha256:7daf286f883b35cd4bfa1c7e5c43f368035e66878019b1a51f72e5a288403644
WOLF_E2E_POSTGRES_IMAGE=postgres@sha256:77f585114c32fbca283dc835b0596f4e52b51b4c6662d7810b2f4084f60a1873
```

For native runs, `WOLF_E2E_WOLF_BINARY` points to the binary extracted from the
same runtime image; each receipt records its SHA-256. Local image names are
not remotely published artifacts. Rebuild from the recorded candidate inputs
before running on another host. See the runner inventory for build commands;
use `VERSION`, `COMMIT`, and `BUILD_DATE` to preserve truthful source identity.

The Kind lane used `scripts/e2e/remote-scan-kind.sh`, Kind v0.31.0,
`WOLF_RUN_REMOTE_SCAN_KIND_E2E=1`, `WOLF_E2E_KIND_LOAD_LOCAL_IMAGES=1` and
`WOLF_E2E_KIND_MEMORY_PVS=1`, with the same three immutable image inputs above.
Kind was recovered from the existing integration-adapter image
`sha256:1e1e400811b7e6b34e9ec7f8618bcf485ba20627c75982ec5e3fd912b0b734cf`
and its musl loader, rather than changing repository toolchain pins. The
single-node test disables NetworkPolicy and uses disposable hostPath PVs;
network-policy/multi-node storage qualification remains O02–O05. Initial and
post-restart real Kubernetes scan durations were recorded in the receipt.

## Failures discovered and repaired

1. Go 1.27.2 had reachable [GO-2026-6809](https://pkg.go.dev/vuln/GO-2026-6809)
   in `crypto/x509`. Updated every active Go build/toolchain pin to 1.27.3,
   including official archive checksums and image digest. The remaining
   module-only GO-2026-5932 concerns `x/crypto/openpgp`, which this application
   does not import or call. It remains visible in verbose vulnerability output.
2. Trivy DB locks had expired. Refreshed both exact database digests through the
   existing resolver; they expire 2026-10-31T07:50:12Z. Freshness must be checked
   again at release time.
3. Workspace hashing used pathname reads and ignored local digest errors.
   Confined traversal/reads through `os.Root`, retained literal symlink hashing,
   and made failed hashing stop local/existing-workspace preparation. Regression
   tests cover renamed roots, symlinks, missing directories and non-directories.
4. At Community capacity, duplicate scan submission returned `community_limit`
   before checking its idempotency key. Moved the existing capacity guard after
   normalized-request replay/conflict handling. Regression proves same-key
   replay succeeds, changed payload conflicts, and new/keyless requests remain
   blocked. Concurrent admission across different new keys remains A10.
5. Native PostgreSQL 18 failed to start with a data-child tmpfs. Mounting
   `/var/lib/postgresql` supports its versioned data layout. Compose already
   supplies explicit `PGDATA`; that existing volume layout was preserved.
6. Compose's root-owned fixture caused Git to withhold commit provenance from
   the non-root worker. The harness now owns only its disposable fixture as the
   runtime user and verifies Git access before starting. No `safe.directory=*`
   or ownership-security bypass was introduced.
7. PostgreSQL schedule listing compared an integer to a boolean; create/update
   also bound booleans into that integer column. Kept the compatible schema,
   converted boolean parameters with SQL `CASE`, and filtered `enabled = 1`.
   Both databases pass create/read/toggle/enabled-list/delete contract tests.
8. Two parallel registry-copy tests used the global HTTP client; another test's
   server shutdown could close those idle connections. Reused each fixture's
   existing client. Fifty repeated race-enabled suite runs passed afterward.

9. The final linter reported six unchecked directory-root closes. Made the
   existing best-effort descriptor cleanup explicit; all seven affected
   consumer/generated-input packages passed race checks and lint reports zero.
10. Runtime image scanning found six fixable high-severity package findings in
    Docker CLI/buildx, libcrypto/libssl and libexpat. All five Alpine deployment
    targets now upgrade base packages before installation; the scheduled
    deployment workflow already disables the build cache. Rebuilt the runtime
    and reran the locked-DB policy with no CVE exceptions: zero matching
    vulnerability, secret or license findings. Other target images still need
    their own rebuild and scan; this is not full-matrix sign-off.
11. A repeated native PostgreSQL run exposed a readiness race: the temporary
    initialization server answered a Unix-socket probe before final startup.
    Native, Compose and Helm probes now require loopback TCP. Four r5 lanes
    pass after the change; the permanent volume layout remains unchanged.

12. Kind's generic local-image loader imported digest-only OCI archives under
    anonymous repository names and requested uncached foreign platforms. The
    harness now uses containerd's existing `--base-name` and `--platform`
    options, verifies the imported exact digest, and preserves host image tags.
    The final clean-cluster run passes without manual retagging. Earlier
    diagnostic runs failed during import or were invalidated by harness edits;
    those are not passing qualification evidence.

Earlier failed attempts remain represented here: a full test run caught a stale
scanner mirror after source changed; regeneration and the final full suite
passed. The first combined-fixer build could not resolve a bare local digest;
using its local tag plus the same immutable digest built successfully. No
mutable-only base was substituted. Initial native/Compose failures above were
rerun successfully after the corresponding fixes.

The [final live-service identity check](deployment-unchanged.json) confirms no
production image or service start time changed. All owned disposable test
databases and Kind clusters were removed. A redacted gitleaks directory scan
of this evidence bundle also passed.

## Open release gates

R03 and expired release exceptions remain blocking: all original alerts are retained and none has an
independent disposition. Git subprocess confinement, path replacement, archive
and artifact boundaries, and configured endpoint/DNS policy still need work.
The 43-to-38 count change is not a security sign-off. The existing CodeQL CVE
exceptions expired on 2026-09-10 and must be resolved against rebuilt artifacts
and the owned exception policy; they were neither extended nor hidden.

P00.3/P00.6 remain open: source units need review and protected-branch landing;
the exact old dirty deployment cannot be reconstructed from its labels.
Signed external publication, ARM64 execution, the remaining scanner/fixer
variants, authenticated agent remediation/PRs, failure/rollback drills, real
IdP/Enterprise qualification, legal decisions and the longer product roadmap
remain unchecked in PLAN. Successful local checks do not complete Q01–Q05.
