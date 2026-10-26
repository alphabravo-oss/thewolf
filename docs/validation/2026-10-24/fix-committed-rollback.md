# F07.6 — rollback of committed agent edits and complete review diffs

Before an agent runs, the workspace now saves the accepted HEAD as its rollback checkpoint. The checkpoint survives reopen and retries until Wolf accepts a commit. Rollback restores that checkpoint's version of a rejected file, including deleting files added by the agent. If the rejected edit was already committed, Wolf makes a path-only revert commit; it preserves earlier accepted content, other agent commits, and unrelated staged work. Git failures still stop the job with the workspace retained.

Saved review diffs now compare against the persisted branch verification base, including committed, staged and untracked changes. They no longer become empty simply because Wolf or an agent committed the repair. HEAD-relative dirty-file checks retain their cleanup semantics.

Verification:

- `go test -race ./internal/fix/... -count=1` passes ([receipt](fix-committed-rollback.txt)).
- `TestRollbackAgentCommitSurvivesReopen` uses real Git to prove checkpoint persistence, restoration of a previously accepted repair, removal of a committed new file, preservation of an independently committed file and an unrelated staged file, and checkpoint release after acceptance.
- `TestRunSharedFileRollbackMatchesAttemptsAndDiff/agent-commit` runs the orchestrator against a real worktree: a rejected committed edit leaves no rejected content in HEAD or the checkout, and no finding is reported kept.
- `TestBranchChangedFilesSurviveCommitsAndReopen` also checks that the saved patch contains committed, staged and untracked content after reopen and a default-branch ref move.
- Scoped Go lint reports zero issues ([receipt](fix-committed-rollback-lint.txt)); scanner context/lock synchronization is recorded [here](fix-committed-rollback-sync.txt).

F07.6 remains open for final extra-file and cross-tool/committed-round acceptance and the complete cancellation/restart attempt-history matrix. These tests use real Git and injected verifier outcomes; they do not qualify a real remote scanner/push/PR journey. No deployment or remote publication occurred.
