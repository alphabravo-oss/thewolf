# D05.4 — native Git ignore evaluation

Candidate: uncommitted working tree, 2026-10-23. This qualifies the tested native Git
matching/error behavior. D05 and the full plan remain open.

## Reproductions and repair

[Filename reproduction](reproduction.txt) shows newline/quoted/Unicode filenames
being lost and ordinary names containing `..` being skipped. The old line-based
protocol also allowed a finding filename to be interpreted as multiple inputs.
[Expanded contracts before file validation](contracts-final.txt) retain the second
reproduction: native Git silently ignores a `.gitignore` directory without diagnostics.

Git evaluation now lives alongside the existing native command and metadata readers
in `internal/fix/git/ignore.go`. It reuses those boundaries, including linked-worktree
and common-directory resolution; no metadata parser or dependency was added.
`ApplyGitignore` remains the finding projection in the suppression package.

The command uses NUL-delimited literal paths and validates returned framing/membership.
It preserves native tracked-file, anchoring, nested and negation behavior. A source
must have its own Git metadata; parent repositories are not implicitly discovered.
Inherited Git redirection and machine/global `core.excludesFile` decisions are disabled.
Native command defaults also disable fsmonitor and other external helpers.

Relevant ancestor `.gitignore` files and the common `info/exclude` are checked through
pinned roots and nonblocking regular-file opens before matching. Symlinks, directories
and FIFOs are errors. Git errors, stderr diagnostics, malformed output and deadlines
produce no partial answer. Earlier default/explicit-file decisions are made on a copy
and committed to the input slice only after all required matching succeeds.

Operation contexts now reach suppression from API/database/native count paths and
from both report-writer callers (CLI and scan publication). Native evaluation has a
ten-second ceiling; cancelled/deadline contexts retain `errors.Is` identity. Recorded
manifest responses validate actual candidate paths before using a cached artifact.

## Verification

Final **136-package uncached race suite** passed with real PostgreSQL; Go lint
reports zero issues and scanner sync passes. Gosec retains **29 high/high findings**
(25 G703, one G702, three G704), no analysis errors and no waivers. The owned fixture
and its anonymous volume were removed and their absence verified.

```sh
WOLF_TEST_POSTGRES_DSN='<owned loopback fixture>' go test -race -count=1 ./internal/fix/git ./internal/scan/suppress ./internal/scan/report ./internal/api/routes ./internal/db ./cmd/wolf -run 'TestGitignore|TestIgnoreErrors|TestApplyRepo|TestWriteAll|TestLocalScanExitContract|TestCommand'
make scanners-sync
WOLF_TEST_POSTGRES_DSN='<owned loopback fixture>' make test
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run
```

- [Expanded focused checks](contracts-expanded.txt) pass; the final suite adds the
  last nested/common-directory/symlink/FIFO and report-cancellation cases.
- Real Git tests cover literal special filenames and newline injection, `./` names,
  invalid/traversal/NUL paths, nested negation, excluded parent directories, anchored
  paths, common excludes, tracked files, parent-source isolation and linked worktrees.
- Real configuration tests inject another repository, global/system config, environment
  config entries, XDG excludes and a filesystem-monitor sentinel. Only project rules
  affect the result and the helper sentinel is absent.
- Controlled executable cases cover missing Git, fatal/partial/warning results,
  invalid output and normal no-match exit 1. No failed batch mutates its original
  default, native or imported flags. A short deadline terminates a sleeping process;
  already-cancelled contexts and report cancellation retain the cancellation error.
- SQLite and real PostgreSQL compare full finding/scan/rule/audit/canonical snapshots
  around failed create/revoke/expiry; repaired metadata permits expiry retry. HTTP
  checks cover live lists/detail/stats/result/gates/manifest/report/SARIF/tools and
  failed import followed by identical-upload retry after metadata repair.
- [Race suite](race.txt), [lint](lint.txt), [scanner sync](sync.txt), [security](gosec.json),
  [check summary](checks.json), [source hashes](source.json), [receipt hashes](sha256.json)
  and [owned database fixture](postgres.json) identify the candidate. Earlier logs
  remain historical; no UI/browser or deployed-container qualification is claimed.

## Compatibility and remaining requirements

Global/system excludes and repository `core.excludesFile` overrides no longer hide
findings. Put intended project policy in `.gitignore`/`.wolfignore`. The runtime
Dockerfile already includes Git; local Git sources now fail honestly if it is absent.
Non-relative, invalid path-segment and NUL-containing finding paths remain visible
unless another suppression source matches. Only candidates not already suppressed
need Git evaluation. Empty/no-Git sources retain the prior absence behavior.

Source snapshots are not implemented here. An unavailable parent source still takes
the legacy absent-source path; archives without `.git` do not gain Git semantics.
Native scans may read prepared workspaces while later views use `Repo.SourcePath`.
Valid rule/default/environment edits can leave cached manifests/header counts stale.
These remain D05 work alongside immutable input versions and provenance.

Validation pins readers, but the subsequent native Git command still opens paths;
concurrent metadata/rule replacement is not an atomic snapshot. Registered linked
metadata uses the existing external-root opener. Native config includes and broader
filesystem/subprocess access remain R03 review scope. Command defaults are not an OS
sandbox and this change does not waive any security alert.

The ten-second ceiling does not interrupt a blocking kernel filesystem operation.
The shared command helper bounds descendant pipe draining separately. Candidate
paths, rule-file opens and result buffers have no new global size quota; large-scale
performance needs measurement. Database mutations still perform this work under
transaction locks. Full scan recovery/deployment and historical identity/absence
requirements remain open.
