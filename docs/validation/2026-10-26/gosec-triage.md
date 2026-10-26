# R03 gosec disposition — 2026-10-26

Implementation reviewer: **fp4 (Codex)**. This is a source-level implementation review,
not independent security or release approval. Reviewed caller ownership assumptions must
be revisited if callers, mounts, path construction, or subprocess policy change; review
checkpoint **2026-11-26**, owned by the R03 maintainer. No accepted exploitable exception,
rule-level exclusion, CI change, or disabled gate was introduced.

The complete current-tree baseline was **29 findings: 25 G703, one G702, three G704**,
with **zero analysis errors**. The historical 38-findings report predates existing repairs;
its decline to 29 happened **before this work**. The current 29 findings are dispositioned
below: **two fixed and 27 false positives**, plus one manually discovered and fixed
pagination SSRF/token-disclosure sibling. False-positive annotations are inline at the
exact reviewed sink and name the rule and boundary-specific reason.

The authoritative invocation is `.github/workflows/go.yml`'s security job. Searching
workflows, Makefile and scripts found no additional gosec config or rule exclusions.
Only its existing generated-context directory exclusion is used:

```sh
go build -C scanners/go-tools -mod=readonly -o /tmp/fp4/gosec github.com/securego/gosec/v2/cmd/gosec
GOCACHE=/tmp/fp4/go-build /tmp/fp4/gosec -fmt json -out /tmp/fp4/gosec-final-tree.json -severity high -confidence high -exclude-dir=internal/scannerbuild/context ./cmd/... ./internal/... ./pkg/... ./plugins/...
```

This builds gosec **v2.29.0 from the locked scanners/go-tools module**, using Go 1.27.3.
The first default-cache attempt had missing compiled-cache entries and was discarded as
incomplete; the private GOCACHE changes no scan rules or package selection. A subsequent
rerun overlapped another worker's missing `plugin` import in `routes/scans.go`; its zero
findings with analysis errors was also discarded. The final complete rerun is recorded
in the verification section below.

“Operator-owned” means deployment configuration, trusted release-worker scratch and
ancestors, or an explicitly selected local CLI path. It does **not** mean arbitrary
repository or request data is trusted. Fresh proposal checkouts are private MkdirTemp
roots and reject repository symlinks before editing; their scanner-definition programs
are intentionally executable trusted build inputs. Same-OS-user replacement of these
private roots is not a boundary supplied by these functions. Broader sandbox, mount,
privileged-writer, DNS and source-snapshot qualification remains under R03.3/O05/D24.

| rule | file:line | source→sink | trust boundary | exploitable? | disposition (fixed/false-positive) | check |
|---|---|---|---|---|---|---|
| G704 | `internal/scannertools/httpcache/cache.go:164` (run baseline 145; R03-01) | Latest package/registry metadata and toolchain endpoints → shared Do → HTTP transport; remote Location could select another origin | Configured discovery origin → remote server-controlled redirect; copied client now enforces original scheme, authority, method, and ten-hop bound while retaining caller policy | Yes before; constrained at shared read/redirect boundary now | fixed | TestDiscoveryRedirectBoundary; TestCacheRequestOwnershipAndCancellation |
| G704 | `internal/scannerregistry/client.go:1043` (run baseline 1043; R03-02) | Registry target configuration → endpoint registry-map lookup → doAuthorized → HTTP Do (including Bearer retry) | Registry-management permission selects an egress origin; remote registry cannot expand it. endpoint checks exact authority; redirect guard covers scheme/host/port/userinfo/method; Bearer realms use token-host allowlist and reject redirects | No under the stated caller/ownership boundary | false-positive | TestRegistryRedirectBoundary; TestClientRejectsUnapprovedBearerTokenHost; TestRegistryJSONRejectsOversizeAndTrailingData |
| G704 | `internal/scannerregistry/client.go:1019` (run baseline 1019; R03-03) | Registry target configuration → endpoint registry-map lookup → doAuthorized → HTTP Do (including Bearer retry) | Registry-management permission selects an egress origin; remote registry cannot expand it. endpoint checks exact authority; redirect guard covers scheme/host/port/userinfo/method; Bearer realms use token-host allowlist and reject redirects | No under the stated caller/ownership boundary | false-positive | TestRegistryRedirectBoundary; TestClientRejectsUnapprovedBearerTokenHost; TestRegistryJSONRejectsOversizeAndTrailingData |
| G703 | `internal/scannertools/manifest/manifest.go:168` (run baseline 168; R03-05) | CLI root or worker discovery-manifest configuration → LoadFile → os.ReadFile | Local operator-selected input file; HTTP request/manifest content cannot choose this pathname. Other callers append fixed scanners/tools.yaml to managed checkout roots | No under the stated caller/ownership boundary | false-positive | gosec rerun |
| G703 | `internal/scannerreleasebackend/source.go:273` (run baseline 273; R03-06) | Controller workspace → partial-install ledger names → safeMaterializedTopLevel → RemoveAll | Ledger names may be corrupt but must be single non-dot components without separators/NUL; reserved .wolf-* names are rejected. RemoveAll removes child symlinks, not their targets. Controller owns the workspace and its ancestors | No under the stated caller/ownership boundary | false-positive | TestGitSourceMaterializerRestoresExactSourceForPostCheckoutBuild |
| G703 | `internal/scannerreleaseadapter/quality_execution.go:801` (run baseline 801; R03-07) | Canonical MaterializeExecutableCorpus in MkdirTemp, or fixed extracted DB tree → qualityBuildContext → WalkDir/read → tar | No request supplies source paths. Corpus writes compiled relative names with O_EXCL in a fresh private directory; DB entries are allowlisted. Walk rejects symlinks. Container receives the generated volume, not write access to the host source | No under the stated caller/ownership boundary | false-positive | TestQualityBuildContextIsDeterministicAndContainsNoHostPaths |
| G703 | `internal/scannerreleaseadapter/actions.go:1142` (run baseline 1142; R03-08) | Remote tar header → exact databaseName/metadata.json allowlist → O_EXCL OpenFile | Rejects traversal, absolute/nested names, duplicates, nonregular entries and oversized content; exclusive creation cannot follow an existing leaf symlink/hardlink; scratch and executable configuration are deployment-owned (per-job emptyDir in Kubernetes), not agent/repository write mounts. | No under the stated caller/ownership boundary | false-positive | TestExtractTrivyDatabaseArchiveRejectsHostileLayouts |
| G703 | `internal/scannerreleaseadapter/actions.go:1114` (run baseline 1114; R03-09) | Fixed trivy-cache/db or trivy-cache/java-db → extraction directory MkdirAll | Target derives only from compiled databaseSpec names and private scratch, before parsing any header; scratch and executable configuration are deployment-owned (per-job emptyDir in Kubernetes), not agent/repository write mounts. | No under the stated caller/ownership boundary | false-positive | TestExtractTrivyDatabaseArchiveAcceptsExactRuntimeLayout |
| G703 | `internal/scannerreleaseadapter/actions.go:1104` (run baseline 1104; R03-10) | findSingleGzipArchive validated private-stage result → extractTrivyDatabaseArchive → Open | Archive path is an enumerated regular, non-symlink entry in worker-owned scratch; remote archive headers do not select it; scratch and executable configuration are deployment-owned (per-job emptyDir in Kubernetes), not agent/repository write mounts. | No under the stated caller/ownership boundary | false-positive | TestFindSingleGzipArchiveRejectsAmbiguousAndSymlinkedLayers; TestExtractTrivyDatabaseArchiveRejectsHostileLayouts |
| G703 | `internal/scannerreleaseadapter/actions.go:1065` (run baseline 1065; R03-11) | Fixed private OCI stage after digest-bound ORAS pull → findSingleGzipArchive → WalkDir | Rejects symlinks and nonregular/empty/oversized entries; accepts exactly one gzip file. Entry names never select an output pathname; scratch and executable configuration are deployment-owned (per-job emptyDir in Kubernetes), not agent/repository write mounts. | No under the stated caller/ownership boundary | false-positive | TestFindSingleGzipArchiveRejectsAmbiguousAndSymlinkedLayers |
| G703 | `internal/scannerreleaseadapter/actions.go:1008` (run baseline 1008; R03-12) | Operator scratch root + compiled trivy-oci-db/java-db → MkdirAll | Recreates the fixed stage after removing it; remote bytes have not yet been pulled; scratch and executable configuration are deployment-owned (per-job emptyDir in Kubernetes), not agent/repository write mounts. | No under the stated caller/ownership boundary | false-positive | gosec rerun |
| G703 | `internal/scannerreleaseadapter/actions.go:1005` (run baseline 1005; R03-13) | Operator scratch root + compiled trivy-oci-db/java-db → RemoveAll | Only the two compiled databaseSpec names; child symlinks are removed; scratch and executable configuration are deployment-owned (per-job emptyDir in Kubernetes), not agent/repository write mounts. | No under the stated caller/ownership boundary | false-positive | gosec rerun |
| G703 | `internal/scannerreleaseadapter/actions.go:977` (run baseline 977; R03-14) | Operator scratch root + literal trivy-cache → MkdirAll | Recreates the fixed child after removing it; no archive-supplied path; scratch and executable configuration are deployment-owned (per-job emptyDir in Kubernetes), not agent/repository write mounts. | No under the stated caller/ownership boundary | false-positive | gosec rerun |
| G703 | `internal/scannerreleaseadapter/actions.go:974` (run baseline 974; R03-15) | Operator scratch root + literal trivy-cache → RemoveAll | Only compiled cache name; RemoveAll does not traverse child symlinks; scratch and executable configuration are deployment-owned (per-job emptyDir in Kubernetes), not agent/repository write mounts. | No under the stated caller/ownership boundary | false-positive | gosec rerun |
| G703 | `internal/scannerproposal/editor.go:355` (run baseline 355; R03-16) | Server-resolved component updates → fixed toolchains/installer paths or enumerated Dockerfiles → file write | CheckoutGenerator creates private MkdirTemp checkout of the exact definition commit, rejects all checkout symlinks before Edit, and leaf paths are checked again. Update values change file content, never destination. Executed definition tooling is trusted executable input | No under the stated caller/ownership boundary | false-positive | TestApplyBaseImageUpdatesMovesAllOwnedDockerfiles; TestApplyGoToolchainUpdateBindsChecksumsInDefinitionAndInstaller; TestCheckoutGeneratorRejectsSymlinkedParitySourceBeforeWrite |
| G703 | `internal/scannerproposal/editor.go:283` (run baseline 283; R03-17) | Server-resolved component updates → fixed toolchains/installer paths or enumerated Dockerfiles → file write | CheckoutGenerator creates private MkdirTemp checkout of the exact definition commit, rejects all checkout symlinks before Edit, and leaf paths are checked again. Update values change file content, never destination. Executed definition tooling is trusted executable input | No under the stated caller/ownership boundary | false-positive | TestApplyBaseImageUpdatesMovesAllOwnedDockerfiles; TestApplyGoToolchainUpdateBindsChecksumsInDefinitionAndInstaller; TestCheckoutGeneratorRejectsSymlinkedParitySourceBeforeWrite |
| G703 | `internal/scannerproposal/checkout.go:669` (run baseline 669; R03-18) | Fixed parity mappings + enumerated regular source entries → copyParityFile → destination write | Fresh private checkout; rejectCheckoutSymlinks precedes synchronization, old context is removed, and parityFiles rejects nonregular/symlink inputs. No request-controlled destination or untrusted concurrent writer is admitted | No under the stated caller/ownership boundary | false-positive | TestCheckoutGeneratorRejectsSymlinkedParitySourceBeforeWrite; TestCheckoutGeneratorUsesExactCommitAndProducesDeterministicCompleteDiff |
| G703 | `internal/api/static.go:111` (run baseline 111; R03-20) | HTTP URL.Path → fs.ValidPath → root.Stat → ServeFileFS(root.FS()) | Request paths are untrusted; os.Root confines both lookups and later opens, including swapped/escaping symlinks | No under the stated caller/ownership boundary | false-positive | TestStaticUIConfinesSymlinksAndTraversal |
| G703 | `internal/api/static.go:57` (run baseline 57; R03-21) | WOLF_UI_DIR or compiled search default → startup index.html Stat | Startup configuration only; no HTTP request controls dir or this metadata probe | No under the stated caller/ownership boundary | false-positive | TestStaticUIConfinesSymlinksAndTraversal |
| G703 | `internal/api/routes/scanner_release_bundles.go:2225` (run baseline 2225; R03-26) | WOLF_SCANNER_BUNDLE_TRUST_POLICY_FILE → loadBundleTrustStore → readStrictJSONFile → Open | Only caller supplies operator trust configuration, never imported bundle names. Regular-file/size check and limited strict JSON decode constrain contents; operator controls the policy path | No under the stated caller/ownership boundary | false-positive | TestScannerReleaseBundleImportFailsClosedAndLabelsBreakGlass |
| G703 | `internal/api/routes/scanner_bundle_image_verifier.go:115` (run baseline 115; R03-27) | WOLF_SCANNER_BUNDLE_IMAGE_TRUST_POLICY_FILE → loadBundleImageVerifier → ReadFile | Operator controls absolute verifier/policy paths. Bundle fields cannot select them. Stat checks regular file and policy size; same-UID policy replacement is outside this caller-input path-traversal finding | No under the stated caller/ownership boundary | false-positive | TestLoadBundleImageVerifierRequiresCompleteExecutableConfiguration |
| G703 | `internal/api/routes/fixes.go:495` (run baseline 495; R03-30) | Owned job/console ID → fixstore log pathname → shared SSE relay → file read | Artifact entries versus host filesystem. Both text and PTY relays now use artifacts.Open: initialized root, confined open, regular file, nonblocking FIFO rejection and hardlink rejection | Yes before; constrained at shared read/redirect boundary now | fixed | TestFixLogRelaysConfineArtifactReads |
| G703 | `internal/api/routes/admin.go:172` (run baseline 172; R03-33) | Configured artifact/workspace/database locations → AdminDisk → Stat | Admin-only aggregate size accounting; no request-selected pathname. WalkDir does not follow child symlinks. Storage roots and ancestors are operator-owned | No under the stated caller/ownership boundary | false-positive | gosec rerun |
| G703 | `internal/api/routes/admin.go:155` (run baseline 155; R03-34) | Configured artifact/workspace/database locations → AdminDisk → WalkDir + DirEntry.Info | Admin-only aggregate size accounting; no request-selected pathname. WalkDir does not follow child symlinks. Storage roots and ancestors are operator-owned | No under the stated caller/ownership boundary | false-positive | gosec rerun |
| G703 | `cmd/wolf/backup.go:143` (run baseline 143; R03-35) | Local wolf backup --output → optional .enc suffix → WriteFile | Explicit local operator output authority; database/archive contents cannot choose output path. No service caller. Existing overwrite/backup durability semantics remain O07 | No under the stated caller/ownership boundary | false-positive | gosec rerun |
| G703 | `cmd/scannertools/propose.go:281` (run baseline 281; R03-36) | Compiled scannerProposalInputs / walked non-symlink names → fresh MkdirTemp workspace → WriteFile | CLI root is explicit operator input; copy rejects symlink entries, preserves relative paths, and only writes the private newly created destination. No service-selected destination or archive filename | No under the stated caller/ownership boundary | false-positive | TestRunProposeCreatesReproducibleApplicableBundle |
| G703 | `cmd/scannertools/propose.go:256` (run baseline 256; R03-37) | Compiled scannerProposalInputs / walked non-symlink names → fresh MkdirTemp workspace → WriteFile | CLI root is explicit operator input; copy rejects symlink entries, preserves relative paths, and only writes the private newly created destination. No service-selected destination or archive filename | No under the stated caller/ownership boundary | false-positive | TestRunProposeCreatesReproducibleApplicableBundle |
| G703 | `cmd/scannertools/bump.go:276` (run baseline 276; R03-38) | CLI root + fixed scanners/versions.env → bumpVersionsEnv → WriteFile | Both callers pass the same fixed pathname under the operator-selected root; manifest variable/version values modify contents only. This local maintenance command intentionally edits its selected definition tree | No under the stated caller/ownership boundary | false-positive | TestBumpVersionsEnv |
| G702 | `internal/scannerreleaseadapter/actions.go:751` (run baseline 751; R03-39) | Compiled action/executable catalog + validated immutable subjects → runBoundedWithEnvironment → exec.CommandContext | No shell interpretation; executable is a compiled absolute path (including allowlisted qualification scripts), arguments are separate values, inherited environment is allowlisted, output is bounded and context limits lifetime. Git calls here are fixed rev-parse or show -s with a full commit and no signature display; scratch and executable configuration are deployment-owned (per-job emptyDir in Kubernetes), not agent/repository write mounts. | No under the stated caller/ownership boundary | false-positive | TestProductionFixedCheckoutExecutesRealGitAndVerifiesCommit; TestSafeDockerEnvironmentSeparatesRegistryAndEngineCredentials |
| G704 (manual sibling) | `internal/scannertools/latest/latest.go:447` | Remote Docker tags Link rel=next → next authenticated getDockerTagsPage request | A Link is a new request, so HTTP redirect policy alone cannot protect it. dockerNextURL now rejects scheme/authority/userinfo changes before returning a next page | Yes before: cross-origin request and Bearer-token disclosure | fixed | TestDockerPaginationConfinesBearerToken; TestCheckerDockerRegistryFollowsPagination |

## Original-inventory reconciliation and remaining limits

All 43 October alerts remain in [the original ledger](../2026-10-23/security-triage.json).
The table maps the 29 current sinks back to those IDs. The other 14 original alerts are
not silently waived: R03-04 moved to pinned workspace hashing (R03.9); R03-19 to atomic
confined installation (R03.11); R03-22–25 to scan-bound snapshot publication/read helpers
(D06.4); R03-28–29 to confined metadata readers (R03.2/R03.3); R03-31–32 to pinned browse
roots (R03.2). The earlier verification evidence is retained; those repairs were not reimplemented here.
R03-40–43 changed/deleted native diff call sites and now use the hardened shared Git
command defaults and explicit diff-driver guards. Their disappearing static call sites
**do not complete R03.3**: linked Git metadata, object stores, repository filters,
concurrent source replacement and comprehensive process/resource isolation remain open.
This report closes the current CI alert inventory, not the whole R03 parent task.

Private registries and configured discovery endpoints remain intentionally supported;
this does not install a blanket public-IP-only policy. Endpoint DNS/network authorization
is deployment policy. Discovery metadata redirects and Docker pagination must now stay
on their initial scheme and authority (including port); cross-origin CDN/migration
redirects fail explicitly. Registry redirects already had that compatibility limit.
Artifact reads reuse the existing configured-root boundary; general writer isolation,
fix-job-to-file alias binding within that root, and bounded SSE drain size are separate
remaining hardening concerns, not certified by this root-escape regression.

No `docs/PLAN.md`, `internal/api/routes/setup.go`, `ui/`, Git state, deployment containers,
or port 18778 was modified. No current finding required a handoff for the protected files.

## Verification

- **CI gosec: PASS, 29 → 0 findings, zero analysis errors**, exit 0. Final scan: 561 files / 150,820 lines. Raw baseline: `/tmp/fp4/gosec-baseline.json`; final: `/tmp/fp4/gosec-final-tree.json`. No protected-file finding remains.
- **`go build ./... && go vet ./...`: PASS**, exit 0; `/tmp/fp4/build-vet-final.log` is empty. The earlier sandbox attempt could not traverse `workspaces`; the first unsandboxed build overlapped the other worker's missing import. The successful rerun followed that repair.
- **Touched-package race checks: 9/11 packages passed** in `/tmp/fp4/race.log`. All three new regressions passed, as did the full `internal/api/routes` package in this race run. `internal/api` failed `TestLegacyOwnerlessResourcesRequireAdministratorSession` (new handoff behavior rejects an ownerless fixture without repository/branch) and `TestMCPClientAuthorizationAndExecution` (coverage upload returned conflict). `internal/scannertools/manifest` failed its stale-lock check. No data race was reported. The initial sandbox-only HTTP tests could not bind loopback sockets; required tests were rerun outside the sandbox.
- **Generated-input/consumer race checks: 2/4 passed** in `/tmp/fp4/generated-race.log`: scannerbuild and scannerdiscovery passed; manifest and lock failed stale-lock checks. Across both race invocations, **11 distinct packages passed, three failed**. Every authored production Go package was exercised with `-race`.
- **`make scanners-sync`: PASS at execution**, including context check and scanner metadata validation; `/tmp/fp4/scanners-sync.log`. It generated lock `sha256:b538dd992954c48e1beda44348dcdf7d7e6b260665f30d5d878825a6aabd0cd1`. Subsequent shared-checkout edits invalidated that lock before the following tests. The lead must rerun `make scanners-sync` after all workers finish, then rerun the focused lock/manifest checks. Repeatedly regenerating against active writers would not establish a stable candidate.
- **Full suite: run exactly once**, `GOCACHE=/tmp/fp4/go-build go test ./... 2>&1 | tee /tmp/fp4/full-suite.log | tail -40` with pipefail. Exit 1: **131 packages passed, five failed, 14 had no tests**. Remaining failures: `internal/api` / `TestLegacyOwnerlessResourcesRequireAdministratorSession` (`legacy_ownership_test.go:171`); `internal/api/routes` / `TestScanPreflightSelectionMatchesExecutionForLocalTree` (`scan_preflight_test.go:249`); `internal/fix/verify` / `TestBranchBuildChecksLeaveNoGeneratedFiles/go` (`branch_test.go:109`, Go could not obtain Git VCS status, exit 128); `internal/scannertools/lock` / `TestRepositoryLockIsDeterministicGolden`; `internal/scannertools/manifest` / `TestDefaultManifestValidation`. These are not converted to passes or excluded. Handoff, preflight, and build-check code were not changed by fp4. No opt-in PostgreSQL/container/production qualification is claimed.

Reproduction commands for the race runs:

```sh
GOCACHE=/tmp/fp4/go-build go test -race ./cmd/wolf ./cmd/scannertools ./internal/api ./internal/api/routes ./internal/scannerproposal ./internal/scannerreleaseadapter ./internal/scannerreleasebackend ./internal/scannerregistry ./internal/scannertools/httpcache ./internal/scannertools/latest ./internal/scannertools/manifest -count=1
GOCACHE=/tmp/fp4/go-build go test -race ./internal/scannertools/manifest ./internal/scannerbuild ./internal/scannertools/lock ./internal/scannerdiscovery -count=1
```

PLAN evidence for the lead: **R03 current CI alert inventory 29 → 0 with no analysis
errors; 27 exact-sink false-positive annotations, shared artifact-read and discovery
redirect fixes, and Docker pagination origin repair; focused security race checks pass.
The full R03/R03.3 security and overall test/release gates remain open as described.**


## Files changed by this worker

Behavior changed only in `internal/api/routes/fixes.go`,
`internal/scannertools/httpcache/cache.go`, and `internal/scannertools/latest/latest.go`.
The other 14 production Go files received only narrow sink annotations. Three new
regression test files cover the three repairs. Authored files:

- `cmd/scannertools/bump.go`
- `cmd/scannertools/propose.go`
- `cmd/wolf/backup.go`
- `docs/validation/2026-10-26/gosec-triage.md`
- `internal/api/routes/admin.go`
- `internal/api/routes/fix_log_boundary_test.go`
- `internal/api/routes/fixes.go`
- `internal/api/routes/scanner_bundle_image_verifier.go`
- `internal/api/routes/scanner_release_bundles.go`
- `internal/api/static.go`
- `internal/scannerproposal/checkout.go`
- `internal/scannerproposal/editor.go`
- `internal/scannerregistry/client.go`
- `internal/scannerreleaseadapter/actions.go`
- `internal/scannerreleaseadapter/quality_execution.go`
- `internal/scannerreleasebackend/source.go`
- `internal/scannertools/httpcache/cache.go`
- `internal/scannertools/httpcache/redirect_test.go`
- `internal/scannertools/latest/latest.go`
- `internal/scannertools/latest/pagination_boundary_test.go`
- `internal/scannertools/manifest/manifest.go`

The required `make scanners-sync` also changed the following generated files. It
mirrors the **combined shared checkout**, including concurrent workers’ changes;
those generated copies are not additional security fixes authored by fp4.
The original protected setup source and UI files were not edited.

<details>
<summary>Generated context and lock changes (41)</summary>

- `internal/scannerbuild/context/_wolf-source/cmd/wolf/backup.go`
- `internal/scannerbuild/context/_wolf-source/internal/api/routes/admin.go`
- `internal/scannerbuild/context/_wolf-source/internal/api/routes/fixes.go`
- `internal/scannerbuild/context/_wolf-source/internal/api/routes/sample/Dockerfile`
- `internal/scannerbuild/context/_wolf-source/internal/api/routes/sample/README.md`
- `internal/scannerbuild/context/_wolf-source/internal/api/routes/sample/go.mod.txt`
- `internal/scannerbuild/context/_wolf-source/internal/api/routes/sample/main.go.txt`
- `internal/scannerbuild/context/_wolf-source/internal/api/routes/sample/package-lock.json`
- `internal/scannerbuild/context/_wolf-source/internal/api/routes/sample/package.json`
- `internal/scannerbuild/context/_wolf-source/internal/api/routes/sample/secrets.env`
- `internal/scannerbuild/context/_wolf-source/internal/api/routes/scan_preflight.go`
- `internal/scannerbuild/context/_wolf-source/internal/api/routes/scan_sources.go`
- `internal/scannerbuild/context/_wolf-source/internal/api/routes/scanner_bundle_image_verifier.go`
- `internal/scannerbuild/context/_wolf-source/internal/api/routes/scanner_release_bundles.go`
- `internal/scannerbuild/context/_wolf-source/internal/api/routes/scanners.go`
- `internal/scannerbuild/context/_wolf-source/internal/api/routes/scans.go`
- `internal/scannerbuild/context/_wolf-source/internal/api/routes/setup.go`
- `internal/scannerbuild/context/_wolf-source/internal/api/static.go`
- `internal/scannerbuild/context/_wolf-source/internal/db/scan_completion.go`
- `internal/scannerbuild/context/_wolf-source/internal/plugin/applicability.go`
- `internal/scannerbuild/context/_wolf-source/internal/plugin/parseutil.go`
- `internal/scannerbuild/context/_wolf-source/internal/scan/runner/runner.go`
- `internal/scannerbuild/context/_wolf-source/internal/scannerproposal/checkout.go`
- `internal/scannerbuild/context/_wolf-source/internal/scannerproposal/editor.go`
- `internal/scannerbuild/context/_wolf-source/internal/scannerregistry/client.go`
- `internal/scannerbuild/context/_wolf-source/internal/scannerreleasebackend/source.go`
- `internal/scannerbuild/context/_wolf-source/internal/scannertools/httpcache/cache.go`
- `internal/scannerbuild/context/_wolf-source/internal/scannertools/latest/latest.go`
- `internal/scannerbuild/context/_wolf-source/internal/scannertools/manifest/manifest.go`
- `internal/scannerbuild/context/_wolf-source/plugins/container/hadolint.go`
- `internal/scannerbuild/context/_wolf-source/plugins/general/gitleaks.go`
- `internal/scannerbuild/context/_wolf-source/plugins/general/grype.go`
- `internal/scannerbuild/context/_wolf-source/plugins/general/semgrep.go`
- `internal/scannerbuild/context/_wolf-source/plugins/general/trivy.go`
- `internal/scannerbuild/context/_wolf-source/plugins/infra/kubelinter.go`
- `internal/scannerbuild/context/_wolf-source/plugins/infra/kubescape.go`
- `internal/scannerbuild/context/_wolf-source/plugins/infra/pluto.go`
- `internal/scannerbuild/context/_wolf-source/scanners/tools.yaml`
- `internal/scannerbuild/context/scanners/tools.yaml`
- `internal/scannerbuild/context/tools.yaml`
- `scanners/scanner-lock.yaml`

</details>

## Receipt identity

- `/tmp/fp4/gosec-baseline.json`: SHA-256 `d5fcaec6c93c39ca4640cafb51c71a553f481fc4bb7770a4077706e6b658f57b`.
- `/tmp/fp4/gosec-final-tree.json`: SHA-256 `d174b1d6e3ca8a62b7b868885b3f8f842ec5dce0f68274e234f49fc271c2cd32`.
- `/tmp/fp4/race.log`: SHA-256 `cd18e2642babd7fcdf00e649fb69d8153078002faf8166a00414da432ee70bba`.
- `/tmp/fp4/generated-race.log`: SHA-256 `28aeafcbae04c45ca2e2f2b33354634c22ef3a075a84ab0ec4cb789f6b6acae2`.
- `/tmp/fp4/full-suite.log`: SHA-256 `616afd42d65d49756ef1494dfb6af56e5f577d4351438c34187bd554d81a6e1b`.
- `/tmp/fp4/build-vet-final.log`: SHA-256 `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855`.
- `/tmp/fp4/scanners-sync.log`: SHA-256 `b6ccc20c7cbb470e68de215810f711a5b869a703b85b3c3a17de795cb45e11b9`.

Reviewed authored source/test set fingerprint (sorted path → SHA-256 JSON):
`c9ade39d41daf0577631d2e2219ea4c8877f4201e6ba254c23183968f1e80cb8`;
individual file hashes are retained in `/tmp/fp4/source-sha256.json`.
