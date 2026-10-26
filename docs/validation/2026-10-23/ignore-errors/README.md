# D05.3 — ignore-file errors and visible statistics

Candidate: the uncommitted working tree on 2026-10-23. This qualifies `.wolfignore`
input validation and error propagation at the tested consumers; D05 remains open.

## Reproduction and repair

[Reproduction](reproduction.txt) shows unknown/empty filters becoming unrestricted
path suppressions, invalid globs being accepted, and negation silently disappearing.
A valid prefix of the file was also returned after a later read/parse failure.

The shared parser now rejects the entire file with source/line context. It retains
valid filters, aliases, literal equals signs in paths and comments. `!` negation
remains unsupported and returns an explicit error. `os.Root` confines the file to
its parent source directory. Nonblocking open plus regular-file validation prevents
a FIFO from hanging a reader and rejects directories and outside-root symlinks.
Missing files retain their optional behavior; no new dependency was introduced.

`ApplyRepo` returns errors before mutating any input finding. Every existing caller
now handles that error: native count finalization, report writing, API finding/detail/
triage views, preview, result, statistics, tools, trends, live reports, gates and
manifest validation, plus database cluster selection and count refresh. Repository
lookup errors no longer fall back to defaults. A recorded manifest cannot bypass
invalid live `.wolfignore` validation; explicit historical downloads stay unchanged.

Preview applies both candidate projections in one batch with the same parsed file.
The statistics endpoint now projects the scan snapshot through the shared matcher
before aggregating visible primary severity/category/reporting-tool counts. The
underlying database audit aggregation remains unchanged.

## Verification

Final **136-package uncached race suite** passed with real PostgreSQL; Go lint
reports zero issues and scanner sync passes. Gosec retains **29 high/high findings**
(25 G703, one G702, three G704), no analysis errors and no waivers. The owned database
fixture and its anonymous volume were removed and their absence verified.

```sh
WOLF_TEST_POSTGRES_DSN='<owned loopback fixture>' go test -race -count=1 ./internal/scan/suppress ./internal/scan/report ./internal/db ./internal/api/routes ./cmd/wolf -run 'TestWolfIgnore|TestApplyRepo|TestWriteAllRejects|TestIgnoreErrors|TestLocalScanExitContract'
make scanners-sync
WOLF_TEST_POSTGRES_DSN='<owned loopback fixture>' make test
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run
```

- [Focused checks](contracts.txt): parser errors, preserved conjunctions and comments,
  injected read failure/oversize line, no input mutation, directory/outside-symlink
  rejection, Unix FIFO nonblocking rejection, and no report files on invalid rules.
- The actual CLI command/runner/writer returns failure for invalid rules; only its
  Docker availability probe is stubbed. This is not a deployed container scan.
- SQLite and real PostgreSQL compare complete finding/scan/rule/audit/canonical
  snapshots around failed create, revoke and expiry. All changes roll back; expiry
  retries successfully after the file is repaired.
- HTTP checks exercise finding lists/detail/statistics, result, gate, recorded
  manifest, live markdown/SARIF, tools, preview, rule creation and single triage.
  Invalid rules return failure without saving a rule or triage change. An import
  that fails can retry identical SARIF after repair. Statistics change when the
  explicit valid rule is added/removed.
- [Final race suite](race.txt), [Go lint](lint.txt), [scanner sync](sync.txt),
  [security scan](gosec.json), [check summary](checks.json), [source hashes](source.json),
  [receipt hashes](sha256.json) and [owned PostgreSQL fixture](postgres.json) identify
  the final candidate. Earlier focused/lint logs predate the final small changes;
  the full suite includes all final checks.

## Remaining requirements

Source selection is not solved here. Missing parent directories still take the
legacy missing-file path; no retained source is reconstructed. Native execution
can read a prepared workspace while later APIs use `Repo.SourcePath`. Git ignore
still uses its existing best-effort subprocess behavior, environment/configuration
and path framing; that work is next. Policy/default/environment/file decisions are
not persisted/versioned, and valid file edits can leave cached manifests/header
counts stale. D05 retains these requirements.

Distinct filesystem reads can race each other or a write; previews share a parsed
file only within their one evaluation. Single triage prevalidates known-invalid
input, but a change after that check can still affect response projection after
the database commit. Full transactional policy snapshots remain necessary.
The parser retains bufio.Scanner's roughly 64 KiB per-line limit and does not add
a total-file quota or interrupt disk reads via context. Database transactions can
still perform filesystem/Git work under their locks. Aggregate/pagination/fleet
performance and all historical absence/identity requirements remain open.

No UI change or browser/deployment qualification is claimed. Security findings
remain release-blocking; no waiver was added.
