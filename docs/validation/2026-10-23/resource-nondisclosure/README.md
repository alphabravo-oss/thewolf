# Direct-resource non-disclosure and access-policy consistency

D06.5, 2026-10-23. This qualifies the direct-resource paths below, not the entire D06 authorization gate or production deployment.

Shared scan/repository loaders now return the same generic 404 for inaccessible and missing IDs. Finding detail/triage no longer echo missing IDs in a different error body. Scan views reuse the shared loader. Repository deletion and baseline access also reuse it. Gate and comparison authorization use the existing ownership predicate, preserving administrator-session oversight while keeping API tokens owner-scoped. SARIF import remains repository-owner-only, including for administrator sessions; access is checked before parsing SARIF. Comparison database failures remain generic server errors rather than being misclassified as missing scans or exposing database diagnostics.

`internal/api/resource_disclosure_test.go` exercises the production router, middleware and SQLite store. Its 32 endpoint cases cover repositories/baselines, finding detail/triage/export, SARIF import, both comparison operands, baseline attachment, measured-coverage import, and 20 scan read paths including raw output/downloads/stream entry. Each compares foreign and missing IDs for a user session, owner-scoped user API key, administrator API key, insufficient-scope key, and anonymous request: **320 negative requests**. Denied mutations leave the foreign repository, finding and import history unchanged. Positive requests retain owner session/key and administrator-session raw reads, gates and comparisons; two distinct valid owner imports succeed. Administrator import requests for foreign/missing repositories are denied. Existing ownerless recovery, export, measured-coverage, cluster, preflight and cross-user artifact checks now assert the 404 contract. The cluster-view failure fixture verifies comparison snapshot errors return 500 without exposing the underlying diagnostic.

The matrix uses the existing per-IP request helper with separate IPs per endpoint case. Production rate limits remain enabled. It tests status/body non-disclosure, not timing indistinguishability.

## Reproduction and fixture corrections

- `reproduction.txt`: pre-fix direct-resource differences. Later requests hit the real rate limiter, so those 429 cases are not evidence of the access defect.
- `contracts.txt`: initial compile failure from four unused scan variables after loader consolidation; corrected.
- `contracts-corrected.txt`: compiled candidate; the test still exceeded its shared-IP request budget and exposed a remaining baseline repository guard.
- `contracts-qualified.txt`: IP-isolated matrix and baseline repair; only the positive duplicate-import expectation failed. The existing API correctly returns 409 for duplicate SARIF, so the test now uses distinct valid documents rather than weakening duplicate handling.
- `contracts-final.txt`: focused suite caught the old measured-coverage 403 expectation; corrected to the new shared 404 contract.
- `contracts-verified.txt`: passing focused race run.
- `race.txt`: passing uncached full race suite, 136 packages, with SQLite and the recorded PostgreSQL fixture.
- `lint.txt`: staticcheck found the manifest loader's first scan binding was overwritten by its subsequent snapshot load. The final cleanup removes that unused binding and names the snapshot directly; request behavior is unchanged.
- `contracts-final-candidate.txt`, `sync-final.txt`, `lint-final.txt`: final targeted race checks, regenerated source lock, and lint after that binding cleanup. The full PostgreSQL suite preceded this non-behavioral cleanup; it was not rerun afterward.
- `gosec-final.json`: final security inventory; 29 high/high findings remain release-blocking (25 G703, one G702, three G704), no analysis errors or waivers.

## Commands and boundaries

```sh
go test -race -count=1 ./internal/api ./internal/api/routes -run 'TestScanFindingAndImportResourceNonDisclosure|Test.*(Ownership|Export|RBAC|Preflight|Cluster|CrossUser|Gate|Baseline|Coverage)'
go test -race -count=1 ./internal/api ./internal/api/routes ./internal/scannerbuild ./internal/scannertools/lock -run 'TestScanFindingAndImportResourceNonDisclosure|Test.*(Manifest|Snapshot|Ownership|Export|RBAC|Preflight|Cluster|CrossUser|Gate|Baseline|Coverage|Lock|Sync|Embedded)'
make scanners-sync
WOLF_TEST_POSTGRES_DSN='<owned temporary PostgreSQL fixture>' make test
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run
/tmp/wolf-plan-gosec-bin -fmt json -out docs/validation/2026-10-23/resource-nondisclosure/gosec-final.json -severity high -confidence high -exclude-dir=internal/scannerbuild/context ./cmd/... ./internal/... ./pkg/... ./plugins/...
```

Final outcomes are recorded in `checks.json`; `postgres.json` identifies the isolated fixture and cleanup verification. Source hashes identify relevant working-tree inputs, not an immutable release bundle. Receipt hashes cover this evidence directory. Historical receipts remain unchanged.

The complete role/scope/resource matrix remains open. Lineage requires authorization of related origins/children/remediation jobs, beyond the requested scan checked here. Active-stream revocation, collection/private-resource response consistency, storage retention/resource limits and independent review remain unqualified. API errors for insufficient scope remain 403 and missing authentication remains 401. No UI was changed, no policy was waived, and nothing was deployed.
