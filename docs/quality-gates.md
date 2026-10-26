# Quality Gates

Quality gates evaluate normalized findings against a policy and produce a
deterministic verdict: `pending`, `incomplete`, `pass`, `warn`, or `fail`.
A verdict includes a reason when execution or tool coverage is insufficient.

## Default Behavior

If no stored policy applies, Wolf evaluates the default policy from
`internal/finding/gates`. Gates can be evaluated after a scan without rescanning.

## API

- `GET /api/v1/scans/{scanID}/gate`
- `GET /api/v1/policies`
- `POST /api/v1/policies`
- `PUT /api/v1/policies/{id}`

## CLI

```sh
wolf scan gate <scan-id>
wolf scan gate <scan-id> --fail-exit-code
wolf policy list
wolf policy create --name default-security --mode enforce --rules-file policy.json
```

When `--fail-exit-code` is set, only `pass` and `warn` return success.
`fail`, `pending`, `incomplete`, malformed responses and unknown verdicts cannot
produce a successful CI gate. Gate rejection uses exit code `5`.

Pending/running scans return `pending`. Failed/cancelled scans, invalid tool
metadata, zero selected tools, failed tools, and missing selected-tool results
return `incomplete`, even when there are no findings. Run `wolf scan wait <id>`
before evaluating a required gate.

## Policy Shape

Policies match findings by severity, confidence, category, fine category,
baseline state, tool, path, package metadata, fix availability, known exploited
status, and suppression state where supported.

## Artifacts

Gate evaluation writes `gate-result.json` as an internal report artifact when an
artifact directory is available.

## Validation

Run:

```sh
go test ./internal/finding/gates ./internal/api/routes ./internal/cli
```

## Measured automated test coverage

Repository **Tests** shows coverage from actual test-execution reports, the
tested commit, report scope, uncovered files, and coverage requirements. The
legacy `/scans/{id}/coverage` endpoint remains a **test-presence heuristic**;
its file-matching percentage is never used to satisfy a measured coverage gate.
The separate **Scanner coverage** page describes security scanner capabilities.

Wolf imports nine report formats without a language allowlist or additional
scanner-image dependencies. Run the repository's own tests in CI, using its
existing coverage producer, then upload the reports. Export capability does
not mean Wolf installs or executes that language's test environment.

| Ecosystem | Open-source producer / export | Wolf format |
| --- | --- | --- |
| Go | `go test -coverprofile=coverage.out -coverpkg=./... ./...` | `go` (statements) |
| JavaScript / TypeScript | [Vitest](https://vitest.dev/guide/coverage), Jest, Istanbul/nyc: LCOV or `coverage-final.json` | `lcov`, `istanbul` |
| Python | [coverage.py](https://coverage.readthedocs.io/) / pytest-cov: XML or LCOV | `cobertura`, `lcov` |
| Java / Kotlin / Scala / Groovy on the JVM | [JaCoCo](https://www.jacoco.org/jacoco/): XML | `jacoco` |
| C# / F# / Visual Basic on .NET | [Coverlet](https://github.com/coverlet-coverage/coverlet): Cobertura or OpenCover | `cobertura`, `opencover` |
| C / C++ / Objective-C | [gcovr](https://gcovr.com/en/stable/), LCOV, [LLVM](https://llvm.org/docs/CommandGuide/llvm-cov.html): Cobertura/LCOV | `cobertura`, `lcov` |
| Rust | [cargo-llvm-cov](https://github.com/taiki-e/cargo-llvm-cov): `--lcov` / `--cobertura` | `lcov`, `cobertura` |
| Swift | Instrumented tests, then `llvm-cov export -format=lcov` | `lcov` |
| Dart / Flutter | [Dart coverage](https://pub.dev/packages/coverage), `flutter test --coverage`: LCOV | `lcov` |
| Ruby | SimpleCov `.resultset.json` (line arrays or line/branch objects) | `simplecov` |
| PHP | [PHPUnit](https://docs.phpunit.de/en/12.4/code-coverage.html): Clover XML | `clover` |
| Elixir | [ExCoveralls](https://excoveralls.hexdocs.pm/readme.html): Cobertura XML | `cobertura` |
| Other languages | A conforming exporter/converter to [generic coverage XML](https://docs.sonarsource.com/sonarqube-server/analyzing-source-code/test-coverage/generic-test-data), LCOV, or another format above | `sonar` or corresponding format |

These are report-format integrations, not a claim that every version of every
producer has been qualified. Configure coverage to include **untested source
files**, not only imported/executed modules. Percentages cover the reported
scope; Wolf cannot infer missing executable code from an incomplete report.
JUnit/TRX test-result files and percentage-only badges are not coverage reports.

### Import and inspect

```sh
# Use the full SHA that CI actually tested. It must equal the scan's resolved
# commit_sha. Modified source trees cannot accept commit-based coverage.
wolf scan import-coverage "$SCAN_ID" \
  --commit "$TESTED_COMMIT" --format lcov --file coverage/lcov.info
wolf scan test-coverage "$SCAN_ID"
wolf scan coverage-files "$SCAN_ID"
wolf scan wait "$SCAN_ID"
wolf scan gate "$SCAN_ID" --fail-exit-code
```

API: `PUT /api/v1/scans/{id}/test-coverage`, with `read:scans` for the GET
summary/files endpoints and `write:scans` for import. UI, CLI and MCP use these
same endpoints. Ownership and scope checks apply to each request.

```json
{
  "commit_sha": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  "reports": [
    {
      "format": "go",
      "content": "mode: set\nexample.com/app/main.go:1.1,2.2 1 1\n",
      "strip_prefix": "example.com/app",
      "path_prefix": "backend"
    },
    {
      "format": "lcov",
      "content": "SF:src/app.ts\nDA:1,1\nend_of_record\n",
      "path_prefix": "frontend"
    }
  ],
  "excluded_paths": ["generated/**"]
}
```

Use `--request-file bundle.json` for mixed formats and component-specific path
mappings. `strip_prefix` removes a CI workspace or Go module prefix;
`path_prefix` adds a repository source root. JaCoCo paths are package-relative:
typically add `src/main/java`, `src/main/kotlin`, or the appropriate module root.
Absolute paths without an explicit strip prefix and parent traversal are rejected.

Each PUT atomically **replaces the complete bundle for that scan**, with up to
64 reports and 20 MiB of report text (40 MiB JSON request). Failed validation
preserves prior data. Merge overlapping test shards with the original coverage
producer before uploading; cross-report overlap is rejected to prevent double
counting. Source report hashes, uploader identity, import time and exclusions
are retained. Exclusions document the producer's scope; they do not remove files
from the imported measurements. Coverage is evidence asserted by authenticated
CI; importing a report does not independently rerun or attest its tests.

### Coverage rules and inheritance

Policy precedence is repository, the scan's explicit collection, then global.
Global policies require an administrator. Repository and collection overrides
require ownership or administrator rights. Keep one enabled policy per scope;
existing policy selection uses the first enabled name alphabetically. A selected
policy replaces inherited rules. The Tests editor copies the effective policy's
security rules when creating a repository override.

Add coverage rules alongside existing security rules:

```json
[
  {"id":"backend-tests","action":"fail","test_coverage":{"metric":"statements","minimum":80,"path_prefix":"backend"}},
  {"id":"frontend-tests","action":"fail","test_coverage":{"metric":"lines","minimum":80,"path_prefix":"frontend"}},
  {"id":"changed-tests","action":"fail","test_coverage":{"metric":"lines","minimum":90,"scope":"changed"}}
]
```

Overall metrics are `lines`, `branches`, `statements`, and `functions`, when
provided by the report. A native Go profile measures statements; Wolf never
converts that percentage into line or branch coverage. Totals are weighted by
coverable items, not averaged file percentages. A metric missing from any file
within a rule's source root yields `incomplete`; use component roots for mixed
metrics. Empty denominators are `not_applicable`, not fabricated 100% coverage.
Missing reports, commit mismatches, and unavailable required metrics cannot
produce a successful required gate. Scanner incompleteness remains blocking.

### Changed-code coverage with diff-cover

Reuse [diff-cover](https://github.com/Bachmann1234/diff_cover) in CI to compare
the tested commit to the intended base. Run from the repository root, with a
clean checkout at `TESTED_COMMIT`, and coverage paths matching that root:

```sh
diff-cover coverage/lcov.info --compare-branch "$BASE_COMMIT" \
  --format json:diff-coverage.json
wolf scan import-coverage "$SCAN_ID" \
  --commit "$TESTED_COMMIT" --format lcov --file coverage/lcov.info \
  --base-commit "$BASE_COMMIT" --diff-cover-file diff-coverage.json
wolf scan gate "$SCAN_ID" --fail-exit-code
```

The request-file equivalent adds `base_commit_sha` and the parsed diff-cover
JSON object as `diff_cover`. Wolf checks every covered/uncovered changed line
against the full report bundle and recomputes totals. Base/head provenance is
asserted by the CI uploader. Changed coverage is **line coverage**, limited to
changed lines represented in the producer's report; omitted files/lines are
not measured. Native Go profiles require an appropriate line-report export to
use this integration. Coverage report generation and diff-cover stay in CI,
so normal Wolf security scans do not rerun test suites or install toolchains.
