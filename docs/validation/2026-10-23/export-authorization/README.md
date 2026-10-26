# D06.1 — authorize scan-specific finding exports

Candidate: uncommitted working tree, 2026-10-23. No deployment was performed.
D06 and the full plan remain open.

## Reproduction and repair

The [reproduction](reproduction.txt) exercises the real `/api/v1` router and its
authentication/scope middleware. Supplying another user's `scan_id` to
`/findings/export` returned that scan's private findings in both CSV and JSON,
with either value of `include_suppressed`. The read-findings scope alone did not
constrain resource ownership. A demoted administrator could also continue to
export another user's scan.

The handler now calls the existing `loadScanForCaller` before loading findings or
evaluating source policies. This is the same boundary used by scan detail and
other scan-resource endpoints. No new permission system, dependency, or role
semantics were introduced. The existing aggregate export path continues to use
its scoped current-open finding query.

The runnable regression is `TestFindingExportAuthorization` in
`internal/api/findings_export_test.go`. Forty format/suppression/principal cases
and a subsequent administrator-demotion request cover:

- Owner session and read-findings API-token success, with valid parsed CSV/JSON.
- Foreign session/token rejection, without finding content or download headers.
- Administrator human-session access and immediate revocation on demotion.
- A scoped administrator API token cannot borrow the human-session role to read
  a foreign scan through this endpoint, matching existing scan-resource policy.
- Insufficient scope, anonymous access and missing scan IDs.
- Aggregate owner export excludes the other user's findings.

```sh
go test -race -count=1 ./internal/api -run '^TestFindingExportAuthorization$'
make scanners-sync
WOLF_TEST_POSTGRES_DSN='<owned loopback fixture>' make test
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run
```

## Verification

The [focused race regression](contracts.txt) and final [136-package uncached race
suite](race.txt) pass. The full suite includes SQLite and real PostgreSQL; the new
HTTP regression uses SQLite through the production router. [Lint](lint.txt) reports
zero issues and [scanner synchronization](sync.txt) validates 53 tools and 51 pins.
The [security scan](gosec.json) still fails with 29 high/high findings (25 G703,
one G702 and three G704), no analysis errors and no new waivers. This repair does
not close R03.

The isolated PostgreSQL [fixture receipt](postgres.json) records its exact container,
image, port and anonymous volume, plus verified removal of both resources. No
existing service was modified. [Check results](checks.json), [source hashes](source.json)
and [receipt hashes](sha256.json) identify this working-tree evidence; they are not
a merged release or deployment attestation.

## Limits and next acceptance

This repair preserves the scan boundary's existing 403 for a foreign scan and
404 for a missing scan. It does not qualify a uniform non-disclosure policy;
finding-detail endpoints already use 404 for foreign rows. Legacy ownerless
resources remain governed by `canModifyOwned`'s existing compatibility rule.
The wider D06 review must reconcile those cases and role/scope behavior across
aggregate findings, finding detail/update, imports, raw evidence and downloads.
Existing artifact confinement checks are separate evidence, not proof of every
resource pairing or in-root alias being safe.

The adjacent D05 header trace found that `ListScansPage` sorts by persisted
`finding_count` before SQL pagination. Replacing only response counts with live
projections would leave sort order incorrect. Stored count freshness therefore
remains coupled to persisted source/policy generations, not merely a display fix.
The source-lifetime preference remains unresolved; this change does not alter
existing live-rule behavior.
