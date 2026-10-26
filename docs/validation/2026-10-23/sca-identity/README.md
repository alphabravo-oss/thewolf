# D01 — dependency finding identity

Verified 2026-10-23 against the uncommitted working tree on `postgres-modes`.
This is implementation evidence, not a deployed or released candidate.

## Defect and repair

The [reproduction](reproduction.txt) collapsed three dependency occurrences into
one: the same CVE in different packages or lockfiles shared the old runner key.
New SCA identity version 2 includes package name/version/ecosystem, supplied PURL,
advisory and normalized location. Length framing keeps component boundaries
unambiguous; package/path case is preserved. Fine category and source-module
or display enrichment do not replace known dependency identity. Existing SAST
identity remains version 1. Prefix normalization no longer strips `/repo` from
unrelated `/repository` paths.

The six parser fixtures cover Trivy, Grype, OSV, npm audit, pip-audit and cargo
audit. They preserve distinct advisories in a lockfile and the same advisory in
different packages, versions or locations. Trivy/Grype retain PURLs; Grype emits
all reported locations. Actual npm/pip Execute paths, using an injected command
fixture, retain the selected manifest. Explicit OSV/pip/cargo aliases can identify
a CVE while preserving the original rule. CVEs are no longer put in CWE fields
by npm/pip/OSV parsers. Severity normalization remains D08.

Migration 061 adds five optional dependency metadata fields on both databases.
Single, batch and execution-lease inserts share the existing insert statement;
updates and JSON/SARIF export/import retain metadata. API cluster matching uses
the shared identity. Canonical vulnerability grouping already uses the stable
fingerprint; its regression verifies separate packages keep separate evidence.

## Checks

- [Final focused contracts](contracts-final.txt): all six scanner fixtures,
  same-dependency cross-tool agreement, distinct advisory/package/location keys,
  case and component-boundary distinctions, historical v1 preservation,
  selected npm/pip manifest execution, API counts and canonical evidence,
  JSON/SARIF round trips. The SQLite and PostgreSQL cases both ran, without skip.
- Those database cases simulate the preceding schema with an existing v1 row,
  run the additive upgrade, insert distinct package/advisory combinations using
  single/batch writes, repeat migration, persist source enrichment and inspect
  current-fleet rows. Legacy IDs and empty metadata remain unchanged.
- `WOLF_TEST_POSTGRES_DSN=... go test -race ./cmd/... ./internal/... ./pkg/... ./plugins/... -count=1`
  passed **135 packages**. [Output](race.txt).
- `golangci-lint v2.14.0 run`: [zero issues](lint.txt).
- Node 26.11.1 / pnpm 12.11.2: [UI typecheck](ui-typecheck.txt) and
  [lint](ui-lint.txt) pass. UI row keys use finding IDs; this change adds optional
  type fields without changing rendering. No new browser qualification is claimed.
- [Scanner context/lock sync](sync.txt) passes: 53 tools, 51 pins.
- [PostgreSQL receipt](postgres.json) records the pinned disposable image and
  loopback port; the fixture is removed after validation. Production untouched.

Earlier `focused.txt` records initial failed checks (missing migration
registration and the old npm manifest expectation); `focused-fixed.txt` and
later final receipts retain the corrected results. All evidence is retained.

## Compatibility and remaining gates

[Consumer documentation](../../../interface-parity.md#dependency-finding-identity)
records the v1/v2 boundary. Historical rows are not guessed or backfilled.
Different/missing PURLs or versions can conservatively remain separate; this is
not exhaustive PURL canonicalization or an advisory alias graph. Native scanner
execution is separately qualified under R05. D02 cluster reconciliation, D04
baseline continuity, D08 normalization and D19 legacy backfill remain open.

The high/high [gosec report](gosec.json) still fails with **33 findings**:
29 G703, one G702 and three G704, with no analysis errors. The existing
[security ledger](../scan-recovery/security-triage.json) retains the original
43 alerts and seven manual issues. No suppression, exception or release waiver
was added. Source hashes and receipt hashes identify this exact validation;
subsequent working-tree edits do not retroactively qualify it.
