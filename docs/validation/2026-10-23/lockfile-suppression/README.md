# D03 — category-safe lockfile suppression

Candidate: the uncommitted working tree on 2026-10-23. This qualifies built-in
lockfile suppression and the tested native/import API/report paths. The overall
plan, broader D05 suppression contract and production qualification remain open.

## Reproductions and repair

The [original regression](reproduction.txt) shows that the built-in rule hid 11 of
12 analysis categories in every tested lockfile. Its special case exempted SCA but
suppressed license, container and unknown categories alongside SAST/secrets.

Lockfile defaults now declare the existing SAST and secrets analysis categories.
The shared matcher checks that predicate, including when path matches are cached.
Rule reasons are audit labels, not control flow: an explicit `.wolfignore` rule
with `reason=default:lockfile` still works normally. No dependency or schema change
was needed.

The [first integration run](initial.txt) also reproduced a detail/list mismatch:
list rows carried default suppression flags, but finding detail and evidence did
not. Detail, evidence and the status-update response now use the existing shared
path matcher. These are response projections; they do not rewrite original rows.

## Verification

```sh
go test -race -count=1 ./internal/scan/suppress ./internal/api/routes -run 'TestApply_LockfileCategories|TestApplyMemoizationPreservesRulePredicates|TestLockfileSuppressionViewsAndArtifacts'
WOLF_TEST_POSTGRES_DSN='<isolated PostgreSQL URL>' go test -race -count=1 -timeout 120s ./internal/api/routes -run TestScanFinalizationKeepsPersistedIdentityAndEnrichment
make scanners-sync
WOLF_TEST_POSTGRES_DSN='<isolated PostgreSQL URL>' make test
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run
```

- [Focused final check](focused-final.txt): eight built-in lockfile names, three
  path forms and twelve categories (288 finding cases), plus defaults disabled,
  explicit repository rules with the same reason label and mixed-category cache
  behavior. SAST and secrets alone match the lockfile defaults.
- Native finalization uses controlled scanner plugins and real SQLite/PostgreSQL
  persistence for package-lock.json, Cargo.lock and go.sum. Two scanner observations
  retain their evidence, merge into one visible primary and fail the persisted
  default gate. Final JSON, native tool artifacts and immutable checksums agree.
  These checks extend the existing finalization suite; they do not run real scanner
  binaries or qualify runner images. D01 separately covers the six SCA parsers.
- HTTP import checks all three required paths with SCA/SAST/secrets/license rows.
  Six visible rows remain, with six suppressed audit rows. The gate fails on exactly
  the three high-severity SCA finding IDs; license rows do not match the default
  high-security rule. Lists, detail, cluster evidence and triage responses agree.
- Live SARIF, saved JSON/SARIF/manifest, both FIX documents and verified HTTP
  downloads retain the correct visibility/audit distinction. All 12 occurrences
  remain in JSON/SARIF; manifest reports six visible and six suppressed.
- Intermediate [native/import run](focused.txt), [matcher run](matcher-and-http.txt)
  and [report run](matcher-and-http-final.txt) retain fixture corrections. The default
  security gate does not fail high-severity license findings. FIX-HIGH requires a
  fine category, so the fixture now supplies one. FIX-ALL contains suppressed rows
  in an audit appendix; its main body alone is the visible work list. No production
  policy or renderer was changed to accommodate these expectations.
- Final qualification receipts: [race](race.txt), [lint](lint.txt),
  [scanner sync](sync.txt), [gosec](gosec.json), [check summary](checks.json),
  [source hashes](source.json), [receipt hashes](sha256.json) and
  [owned PostgreSQL fixture](postgres.json). The final uncached race suite passes
  **136 packages** with PostgreSQL enabled. Lint reports zero issues; scanner sync
  verifies 53 tools and 51 version pins. Gosec remains at 29 high/high findings
  (25 G703, one G702, three G704), with no analysis errors or new waivers.

## Remaining limits

This repair scopes the lockfile rule. Other defaults (such as vendor/test directories),
explicit repository rules, gitignore and stored user/import suppression decisions
can still suppress an SCA finding. Existing stored suppression flags are preserved;
this does not silently clear historical decisions or rewrite saved artifacts.

D05 remains open for broad suppression preview, expiration, consistent ignore-file
sources/error handling and versioned rule snapshots. Rule changes do not advance
finding revisions, so stored gate/header/artifact cache freshness across such changes
is not qualified here. `.wolfignore` negation remains unsupported. Browser UX,
all runner variants, production deployment and independent security review remain
separate requirements. No service was deployed and no security exception was added.
