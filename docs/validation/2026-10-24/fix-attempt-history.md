# F07.6 — attempt history and completion evidence

An attempt is now saved as `running` before its engine executes. Verification finalizes that same identity and start time; the database permits only one final verdict. Attempts that receive no verified edit become `unverified`; interrupted execution becomes `interrupted`. The UI displays and filters these states explicitly.

Restart reads the durable journal, finalizes abandoned running entries, continues per-finding attempt numbers, and restores accepted results newer than the saved summary. Only a completed round advances restart past a loop: a pre-engine `CurrentLoop` write no longer causes unfinished work to be skipped. A clean resume rescan avoids repeating already-verified work. Successful retries clear stale job errors. Returned attempt lists come from the database, including earlier workers and hygiene passes, with their persisted IDs.

History read/start/finalization failures stop execution or publication. Cooperative cancellation finalizes pending history through a bounded context after engines stop. Cancellation cleanup atomically finalizes any remaining running rows before releasing the checkout; failed cleanup rolls back both changes and can be retried. This uses the existing schema and conditional updates, with no new dependency or migration.

## F07.6 acceptance map

| Requirement | Authoritative implementation/check |
|---|---|
| Discover real staged/untracked/committed edits, including skipped or omitted file claims | [Actual edit discovery](fix-actual-edits.md); `TestActualEditsOverrideAgentFileClaims`, `TestBranchChangedFilesSurviveCommitsAndReopen` |
| Same-file rejection and cross-file dependency safety after selective rollback | [Verification barriers](fix-verification-barriers.md); `TestRunSharedFileRollbackMatchesAttemptsAndDiff`, `TestSelectiveRollbackRechecksCrossFileBuild` |
| Reject committed agent edits without erasing accepted work, including reopen | [Committed rollback](fix-committed-rollback.md); `TestRollbackAgentCommitSurvivesReopen` |
| Final extra-file, cross-tool and committed-round acceptance | [Final branch gate](fix-final-branch.md); `TestFinalBranchVerificationAfterToolCommits`, `TestFinalBranchIncludesEarlierRoundClearances`, `TestGateBranchChecksAllToolsAndFindingCounts` |
| Propagate history write/read failures before more execution/publication | `TestAttemptPersistenceFailureStopsPublication`, `TestHistoryFailuresStopBeforeExecutionOrPublication` |
| Cancellation during engines, turn verification, and final verification | `TestAttemptHistorySurvivesCancellationCrashAndReopen`, `TestCancellationDuringVerificationRetainsTerminalHistory`; no push/cleanup or false running/verified verdict |
| Restart from an unfinished attempt and from a verified result newer than the summary | Real SQLite close/reopen in `TestAttemptHistorySurvivesCancellationCrashAndReopen`; preserved ID, interrupted history, incremented number, no skipped unfinished loop or repeated verified work |
| Atomic final verdict and cancellation cleanup/retry | SQLite/PostgreSQL `TestFixAttemptFinalizationIsAtomic`, `TestFixCancellationRetainsOwnershipUntilCleanup`; concurrent finalizers, cancelled writes, stale identities/verdicts, rollback and retry |

## Validation

- Fixer race suite: `go test -race ./internal/fix/... -count=1` ([receipt](fix-attempt-history.txt)). Final orchestrator changes and cancellation cases: [receipt](fix-attempt-history-final.txt).
- SQLite/PostgreSQL lifecycle checks: [initial database receipt](fix-attempt-history-database.txt) and [final database/worker cancellation receipt](fix-attempt-history-cleanup.txt).
- Every package compiles: `go test ./... -run '^$'` ([receipt](fix-attempt-history-build.txt)). This is a compile check, not a claim that every repository test ran.
- Scoped Go lint: zero issues ([receipt](fix-attempt-history-lint.txt)). Removed the obsolete in-memory attempt-list parameter after lint identified it.
- UI typecheck/lint and 55 tests pass ([receipt](fix-attempt-history-ui.txt)).
- Scanner context/lock synchronization: [receipt](fix-attempt-history-sync.txt). Both owned PostgreSQL containers were removed: [receipt](fix-attempt-history-fixture-cleanup.txt).

F07.6 is complete as the workspace acceptance and attempt-history gate. Restart tests reproduce persisted crash boundaries by closing/reopening storage; they do not claim that worker-death process fencing is solved. F06 retains broader build/test coverage, F07 retains iteration/budget/stop-policy qualification, F08 retains immutable image/policy and full evidence inputs, F09/F13 retain worker/process/adversarial isolation, and F15 retains the real remote journey. No deployment or remote publication occurred.
