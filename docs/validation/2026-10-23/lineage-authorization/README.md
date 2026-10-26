# D06.6 — authorize related lineage resources

2026-10-23. The lineage API now applies the existing scan ownership policy to referenced origins and children, and the existing private-resource policy to remediation records and agent jobs. Administrator sessions retain scan oversight, but cannot use lineage to read another owner's private remediation/job data. API keys remain owner-scoped. Empty-owner recovery remains administrator-session-only. Derived run summaries receive only the authorized scans and jobs.

An inaccessible origin falls back to the requested owned scan, matching the existing missing-origin behavior. A database error is not treated as a missing origin. Failures loading the origin, children, latest remediation, remediation jobs, or repository jobs return a generic 500 before any partial history is serialized. The endpoint still returns the latest remediation only when visible; it does not substitute an older remediation. Permitted legacy repository jobs remain visible even when the latest remediation is private.

`TestScanLineageAuthorizesRelatedResources` uses the production router and SQLite. Twelve mixed-owner histories combine four principals (user session/key, administrator session/key) with owned, foreign and ownerless remediation records. Each has owned, foreign and ownerless scans/jobs. Unique workspace sentinels, record counts and IDs verify both disclosure denial and preserved allowed records. A foreign child is deliberately linked to an allowed job to catch leakage through derived run summaries. Twelve further requests cover foreign/ownerless versus missing origins with user sessions/keys and administrator sessions. `TestScanLineageRelatedLoadFailures` injects five database failures behind the real router and checks generic server errors. Total: **29 lineage requests**, excluding authentication/setup.

`reproduction.txt` preserves the original disclosure and five false-success failures. `contracts.txt` caught a test-fixture mistake: `UpdateScan` deliberately does not change lineage identity, so it did not create the intended missing-origin case. The corrected test creates a distinct child with a missing origin; production identity-update behavior was not broadened. `contracts-qualified.txt` records the passing focused checks; the lineage library had no tests matching that focused expression and is covered by the full suite.

```sh
go test -race ./internal/api ./internal/api/routes ./internal/fix/lineage -run 'Test.*(Lineage|Ownership|Remediation)' -count=1
make scanners-sync
WOLF_TEST_POSTGRES_DSN='<owned fixture recorded in postgres.json>' make test
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run
/tmp/wolf-plan-gosec-bin -fmt json -out docs/validation/2026-10-23/lineage-authorization/gosec.json -severity high -confidence high -exclude-dir=internal/scannerbuild/context ./cmd/... ./internal/... ./pkg/... ./plugins/...
```

## D06 acceptance audit

The original D06 requirement is per-resource ownership/role/scope for finding read/update, raw evidence, SARIF import and artifact downloads, with cross-user IDs, root confinement, traversal, symlinks and unauthorized metadata leakage. Current source inspection traced the production route scopes in `internal/api/server.go`, shared ownership helpers, each listed resource loader, related-record assembly, and the four `artifacts.ReadArtifact` API call sites. The full current race suite reruns the accumulated regressions below.

| Criterion | Current implementation and runnable evidence |
|---|---|
| Finding reads and mutations | `findingVisibleToCaller` / `findingAccessibleToCaller` and token-aware `fleetVisible`; `TestFleetAuthorizationDoesNotElevateAPITokens` covers permitted owner/admin reads and updates, private evidence, read-only versus write scopes, rejected foreign single/bulk mutations and unchanged stored status. |
| Human role versus token scope | Production middleware resolves current human roles; token claims cannot borrow them. Fleet/export tests cover limited/admin-scoped keys and immediate session demotion. `TestLegacyOwnerlessResourcesRequireAdministratorSession` covers empty-owner records and administrator recovery. |
| Scan-specific and aggregate exports | `ExportFindings` authorizes selected scans before loading rows/policies. `findings_export_test.go` covers CSV/JSON, suppression flags, aggregate owner isolation, wrong scope, anonymous/missing IDs and demotion. |
| Raw output and artifact downloads | Scope middleware then shared scan authorization precede artifact listing/reads. `TestScanFindingAndImportResourceNonDisclosure` covers foreign/missing IDs, insufficient scopes and unauthenticated requests plus allowed owner/key/admin raw reads. `TestArtifactSnapshotDownloadsAndFailures` verifies cross-scan artifact IDs are rejected even when both scans are owned. |
| SARIF import | Write-scan scope and repository ownership precede SARIF parsing. The production-router non-disclosure test checks foreign/missing repositories, insufficient scope/anonymous callers, denied administrator imports, unchanged foreign import history and two distinct successful owner session/key imports. Format/path/quota semantics remain D25. |
| Root confinement, traversal and symlinks | `TestReadFileConfinesStoredPaths`, `TestArtifactReadsStayWithinTheirScan`, `TestLegacyNamedArtifactRequiresFullManifestIdentity` and `TestScanArtifactRootRemainsPinnedOnRename` cover outside paths, invalid/traversal scan components, sibling keys, escaping file/directory links, hardlinks, top-level aliases, legacy collisions and pinned directory replacement. `TestArtifactEndpointsBindRecordedPathsToTheirScan` exercises 28 API combinations, including all four readers. |
| Unauthorized metadata disclosure | The 32-case/320-request production-router non-disclosure matrix compares foreign/missing response status and body and checks no content/download headers. The new 29-request lineage matrix checks related origins, children, private remediation/jobs and derived run summaries. Canonical evidence access is covered by the fleet matrix. |

Prior evidence: [export authorization](../export-authorization/README.md), [fleet roles and scopes](../fleet-authorization/README.md), [ownerless recovery](../legacy-ownership/README.md), [artifact scan boundaries](../artifact-scan-scope/README.md), [direct-resource non-disclosure](../resource-nondisclosure/README.md). Those receipts describe historical candidates; this directory records the combined current candidate.

D06 is an application authorization/read-confinement acceptance gate, not release/security approval. The runtime filesystem evidence is Linux. Platform runtime qualification, privileged writers, raw/staging writer isolation and resource bounds remain R03/O05/D24; retention remains O08/D24. Artifact migration is not performed. No promise of timing indistinguishability or mid-connection stream revocation is made; authentication is evaluated at request admission. Those limits do not waive any remaining deployment, isolation, session, streaming or artifact requirements elsewhere in PLAN.md. R03's security findings remain release-blocking. Nothing was deployed.

Final verification: the uncached **136-package race suite** passed with SQLite and
the recorded PostgreSQL fixture; lint reports **0 issues**, and scanner sync
validates 53 tools, 25 upstream images and 51 version pins. Gosec remains failed
with **29 high/high findings** (25 G703, one G702, three G704), without analysis
errors or waivers. The owned PostgreSQL container and anonymous volume were
removed and absence verified. `checks.json`, `source.json` and `sha256.json`
record outcomes and working-tree/receipt identity; they are not a release bundle.
