# D06.3 — deny default access to ownerless legacy resources

Candidate: uncommitted working tree, 2026-10-23. No deployment or ownership
migration was performed. D06 and the complete plan remain open.

## Reproduction and repair

The [production-router reproduction](reproduction.txt) demonstrates that an empty
owner was treated as a grant to every caller. A regular session and an
administrator-owned API token could read legacy repositories, collections, scans,
findings, canonical evidence, raw tool logs and artifact downloads. Mutating
requests renamed resources, changed finding status, cancelled a fix job, froze a
remediation and deleted a secret without owning any of those records.

`canModifyOwned` now requires a nonempty caller identity and either a human
administrator role or an explicit matching owner. Empty ownership requires an
administrator session, preserving a recovery path without guessing a replacement
owner. All callers were traced, including repository/collection/node/secret and
scan/finding/canonical access.

Private remediation had independent empty-owner grants. Its existing ownership
predicate is now shared across jobs, selected scan inputs, remediation acceptance
and fixer consoles. It delegates the common identity/legacy rule while preserving
owner-only access when an owner exists, including between administrators. No new
role, dependency, API, schema or automatic backfill was introduced. The
[authentication guide](../../../authentication.md) describes legacy recovery and
private remediation boundaries.

## Runnable regression

```sh
go test -race -count=1 ./internal/api ./internal/api/routes -run 'TestLegacyOwnerlessResourcesRequireAdministratorSession|TestOwnershipRequiresIdentityAndPreservesPrivateFixScope|TestFixJobsAreTenantScoped|TestFixerConsoleRequiresAdminAndIsTenantScoped'
make scanners-sync
WOLF_TEST_POSTGRES_DSN='<owned loopback fixture>' make test
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run
```

The new HTTP fixture uses real stored rows and production authentication/routing.
An explicitly created empty-ID legacy principal permits FK-backed ownerless rows;
constraints remain enabled and authorization reads are not mocked. There is no
login credential for that legacy fixture principal.

[Focused checks](contracts.txt) cover:

- Eleven read endpoints, each requested by a regular session, an administrator's
  super-scope API token and an administrator session: denied callers receive no
  private content while administrator recovery reads succeed.
- Six mutation endpoints with both denied credential types. Database assertions
  verify repository/collection names, finding status, fix status, remediation state
  and secret existence remain unchanged.
- Administrator recovery operations for repository update, fix cancellation,
  remediation acceptance and secret deletion.
- Shared predicate cases for absent/empty identity, matching/foreign owners,
  administrator access, and private fix ownership.
- Existing cross-user job and cross-administrator console isolation regressions.

## Verification

The [focused checks](contracts.txt) and final [136-package uncached race suite](race.txt)
pass. The full suite uses SQLite and real PostgreSQL; the new production-router
legacy fixture uses SQLite. [Lint](lint.txt) reports zero issues and
[scanner synchronization](sync.txt) validates 53 tools and 51 version pins.
The [security scan](gosec.json) retains 29 high/high findings (25 G703, one G702,
three G704), no analysis errors and no new waivers. R03 remains release-blocking.

The owned [PostgreSQL fixture](postgres.json) and anonymous volume were removed,
and their absence verified. [Check results](checks.json), [source hashes](source.json)
and [receipt hashes](sha256.json) identify this working-tree qualification.
Historical receipts were not rewritten; no live service was replaced.

## Remaining acceptance

This qualifies the empty-owner authorization boundary and direct administrator
recovery. It does not assign owners, invent an ownership migration, or add a fleet
listing of private fix jobs. The normal lists retain their existing visibility
policy. Deployment operators must identify historical records before any separate
ownership repair; this test does not assert that the deployed database contains
such records.

Uniform 403/404 non-disclosure, the complete import/raw-evidence/artifact matrix,
in-root cross-scan artifact aliases and other D06 requirements remain open.
Artifact confinement and integrity checks remain separate qualifications. D05
source/policy generations and stored-count freshness are also unchanged.
