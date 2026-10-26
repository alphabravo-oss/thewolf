# Fix repository preflight and source branch — F02

Date: 2026-10-24. F02 implementation acceptance complete; no deployment performed.

Concrete repairs:

- Fresh fix workspaces now receive the durable job's source branch. Local worktrees resolve that exact local branch (or its origin tracking branch); GitHub clones resolve the origin branch instead of silently starting on the remote default. Branch lookup uses exact refs, not revision expressions. Missing branches fail with a fetch/retry explanation. Verification metadata pins the selected starting commit.
- Finding-only jobs without a scan ID now save their findings' common source branch. Mixed source branches are rejected with an actionable branch-conflict response. Jobs with a scan use the existing origin-scan branch selection.
- Fresh workspace creation no longer silently adopts an existing branch after a failed create. Resumes continue through the existing metadata-backed Open path. Git worktrees/clones preserve the user's checkout and uncommitted edits.
- The local preflight validates a real Git worktree with a committed HEAD using the existing guarded Git command and a five-second context bound. A directory named .git alone is no longer enough.
- The selected GitHub secret must belong to the repository owner. Missing ownership, another owner's secret, wrong key type, empty or unreadable selected credentials fail closed rather than substituting another credential. Automatic selection remains owner-scoped. GitHub permission-probe errors redact the credential before reaching the API/UI.
- Production SSH preflight now reports unsupported remediation with a local-clone/GitHub alternative. Previously it could report ready, only for workspace preparation to fail with ErrSSHUnsupported. The existing SSH probe is retained for future remote execution and rejects another owner's node when injected. This does not implement SSH remediation or close F15.

Acceptance mapping:

| F02 requirement | Evidence |
| --- | --- |
| Local/GitHub/SSH preflight and reasons | Existing TestCheck_Local, TestCheck_GitHub, TestCheck_SSH; real local TestLocalFSProbe and TestLocalFSProbeRejectsFakeOrUnbornGitTree; public route TestRepoFixable now uses a real committed Git repository; TestDefaultPreflightRejectsUnsupportedSSHWithoutConnecting |
| Registered credential ownership | TestSelectedGitHubCredentialOwnership, TestSSHPreflightRejectsAnotherOwnersNode, selected-credential rejection/selection cases in TestCheck_GitHub; TestGitHubProbeErrorDoesNotExposeCredential |
| Archived/read-only repositories | TestCheck_GitHub archived and no-push cases; TestGitHubRepoPushInfo exercises actual HTTP decoding of archived/read-only/forbidden responses; local no-write probe case remains covered |
| Branch/base selection | TestPrepareUsesSourceBranchAndPreservesUserCheckout runs real local worktrees and GitHub-style file-URL clones from a non-default release branch; rejects missing/revision-expression bases and existing branches. TestFindingOnlyFixPinsBranchAndRejectsMixedBranches proves durable finding-only selection; TestFixCreationAtomicReplayAndScope verifies scan-origin selection. TestRun_KeepAndEscalateThenUnfixable asserts propagation to the preparer. |
| Clean isolated workspaces | The same real-Git test checks an empty workspace diff, the selected release commit and file, and unchanged original branch/uncommitted user content. Existing prepare, retained-workspace, rollback and worker regressions pass. |

Checks:

- `go test -race ./internal/fix/... ./internal/db ./internal/api/routes ./internal/github -run 'Test(Prepare|Run|Check|LocalFS|DefaultPreflight|SelectedGitHub|SSHPreflight|GitHubProbe|RepoFixable|CreateFix|FindingOnlyFix|FixCreation|GitHubRepoPushInfo|Published|Rollback|BranchChanged|LegacyWorkspace|Worker|Publication)' -count=1` — [passed](fix-preflight-tests.txt). Database execution used SQLite; PostgreSQL-only cases retain their existing environment skip. Shared SQL remains portable and both implementations compile.
- `go test ./... -run '^$'` — [all Go packages compile](fix-preflight-compile.txt), including every caller of the changed workspace-preparer interface.
- `golangci-lint run ./internal/fix/... ./internal/db/... ./internal/api/...` — [zero issues](fix-preflight-lint.txt).
- `make scanners-sync` — [embedded context/lock regeneration and validation](fix-preflight-sync.txt).

Scope: these are clean, separate Git working trees, not OS sandboxes. Source branches resolve at preparation time; exact scan-commit/content immutability remains F08. Remote-host SSH execution and a real authorized remote push/PR journey remain F15. Process isolation and death fencing remain under their existing tasks. No new dependencies or schema migrations.
