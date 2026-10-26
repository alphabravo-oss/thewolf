# Native Git defaults and source failure handling — 2026-10-23

Progress on PLAN R03.3 and D24; neither parent is complete. Changes are in the
uncommitted working tree and are newer than the qualified images. No deployment
was performed.

## Reproduced defects

[The first reproduction](reproduction.txt) shows two failures on the old code:
source preparation executed a repository fsmonitor helper, and a corrupt Git
index was accepted with a commit identity and `dirty="clean"`.
[The resume reproduction](resume-reproduction.txt) shows that missing/non-directory
retained workspaces silently fell through to preparing a replacement source.
The broken-Git case in that second receipt already had the first fallback guard;
missing/file cases demonstrate why retaining an `os.Stat` precheck was insufficient.

## Repair and compatibility

The shared `internal/fix/git.Command` applies defaults to source preparation,
legacy fix Git helpers, fix workspaces, patch application, hygiene, lineage,
push and live-diff commands. It strips inherited Git configuration/redirection
variables, ignores global/system configuration, disables fsmonitor, hooks,
signing and credential helpers, and suppresses interactive prompts and optional
index refresh. Explicit per-call askpass/SSH credentials and author/committer
identity remain supported. Existing diff-specific external-driver/textconv guards
remain at the diff callers. No new dependency was added.

`PrepareExisting` now accepts caller context, propagates inspection/status errors,
and bounds Git inspection by ten seconds or the earlier caller deadline. GitHub
source inspection uses the same state reader. Ordinary non-Git and unborn source
states remain supported. A recorded prepared workspace is now authoritative: any
preparation failure returns a durable source-preparation failure, preserving the
scan's recorded identity instead of silently preparing another source.

[Operator documentation](../../../scan-isolation.md#native-git-command-defaults-and-retained-sources)
records the upgrade implications: ambient credential helpers, global identities,
URL rewrites, aliases and signing are no longer supported by converted native
commands; supported explicit credentials and identity settings are required.
Missing retained workspaces require a new scan after correcting the source.

## Verification

- [Focused checks](focused.txt) passed source-state, credential, helper-execution
  and retained-source regressions. Subsequent added broken-HEAD and explicit-SSH
  tests are included in the full race suite below.
- `TestCommandIgnoresInheritedGitRedirection` proves commands remain in their
  intended real repository despite inherited Git directory/worktree/config
  overrides. Commit tests prove identity survives and repository hooks/signing
  do not run. An actual HTTP Basic challenge proves explicitly supplied askpass
  authenticates while repository/inherited helpers remain unexecuted; the fixture
  deliberately returns 404 after authentication, not a successful remote clone.
  A separate local executable proves explicit SSH transport selection without
  contacting a network endpoint.
- Real source fixtures cover fsmonitor, corrupt index and HEAD, clean/dirty,
  non-Git/unborn, and cancellation. Queued-scan execution against SQLite proves
  broken, missing and non-directory retained sources fail without replacement
  and preserve prior source identity.
- `go test -race ./internal/fix/... ./internal/repo ./internal/scantarget ./internal/api/... ./internal/scanjob ./internal/cli ./cmd/wolf ./internal/scannerbuild ./internal/scannertools/manifest ./internal/scannertools/lock -count=1`
  passed **35 packages**. [Output](race.txt).
- Lint initially found an unused assignment in a new test, retained in
  [lint-initial.txt](lint-initial.txt). After fixing it and regenerating metadata,
  `go test -race ./internal/scantarget ./internal/scannerbuild ./internal/scannertools/manifest ./internal/scannertools/lock -count=1`
  passed all four packages. [Output](final-regressions.txt).
- `go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run`
  reports [zero issues](lint.txt).
- `make scanners-sync` regenerated and verified context and lock, validating 53
  tools and 51 pins. [Final output](sync-final.txt).
- Linux ARM64, Windows AMD64 and macOS ARM64 compilation of `internal/fix/git`
  passed. [Receipts](cross-compile.json). Runtime tests ran on Linux only; these
  compilation checks do not qualify Windows/macOS runtime behavior.

## Remaining boundaries

This is **not an OS sandbox**. Repository clean/smudge/process filters and includes,
external metadata/object directories, path replacement, symlinks, transport
redirects/DNS, inherited general service environment, output limits and process
resource limits remain open. `WaitDelay=2s` bounds pipe drainage after command
exit/cancellation; it does not give background-context commands a deadline.
GitHub cache mutation and retained local source edits between attempts still need
provenance/concurrency qualification. Global-config compatibility in suppression's
Git ignore lookup is unchanged; release/CLI runners and SSH shell operations also
need separate review. The [inventory](native-git-inventory.json) records four direct
Git/gitPath sites including the shared factory, 24 shared-call sites and known
indirect release/SSH runners; it is a search inventory, not a complete audit.

The CI-equivalent high/high gosec command still fails with **33 findings**:
29 G703, one G702 and three G704, with no analysis errors.
[Report](gosec.json), [console](gosec-console.txt), and
[ledger](security-triage.json) retain all 43 original alerts plus six manual issues.
Moving native calls into a wrapper reduced static call-site visibility; the
reduced count is not evidence of complete Git isolation or grounds to close the
original alerts. No new suppression or accepted exception was added. Independent
security review and deployed-image qualification remain outstanding.

[Source identity](source.json) records the relevant source/tests/docs and final
scanner identities. `sha256.json` covers other evidence. Earlier receipts are
unchanged. The plan remains **230 unique tasks, 45 checked**; this turn advances
existing acceptance criteria without marking their unfinished parents complete.
