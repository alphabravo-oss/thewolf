# F09 progress: fixer state and claim fencing

2026-10-24, working tree based on Community `23b64ce09c569b7f49c465b53573e40c791b6fc5`. This is implementation evidence, not deployment qualification. F09 remains open.

## Changes

- Migration 072 adds a durable job revision. Both databases use one conditional update implementation: stale revisions return `ErrFixJobConflict`, and terminal statuses cannot be reopened. Claims, stale reclamation, and transactional sibling supersession advance the revision.
- Heartbeats update only `heartbeat_at`, conditioned on the original worker and claim timestamp and an active status. They cannot rewrite summaries or statuses, or renew a replacement claim. The worker stops engine context on renewal failure and joins its heartbeat goroutine on exit.
- Failure handling retains the worker's original revision instead of loading a newer claim and marking it failed. Follow-up lineage work requires the persisted result to still match the completed run.
- Claim selection excludes another active job sharing a branch, remediation, or checkout. A short PostgreSQL advisory transaction lock serializes claims; SQLite uses its existing immediate write transaction. Engines on separate branches remain concurrent. SQLite query failures now propagate rather than appearing to be an empty queue, and the claimed row is read inside its transaction.
- Resume uses the same repository lock as creation and rejects a busy branch. API cancellation/resume conflicts return HTTP 409. Cancellation is persisted before branch cleanup; active workers receive cancellation before their cleanup runs. An old claim cannot discard a cancelled replacement claim's workspace.
- Workspace, loop-progress, and live-summary persistence failures stop orchestration instead of proceeding to more engines or publication.

## Verification

Disposable PostgreSQL 18.6, plus real SQLite files with two independent store connections. No external agent, push, PR, or deployment was performed.

```sh
WOLF_TEST_POSTGRES_DSN='<disposable PostgreSQL DSN>' go test -race ./internal/db ./internal/api/routes -run 'Test(Fix|UpdateFix|ClaimNextFix|ReclaimStaleJobs|CreateFix|ResumeFix|CancelFix)' -count=1
go test -race ./internal/fix/... -count=1
go test -race ./internal/fix/worker ./internal/fix/orchestrator -count=1
golangci-lint run ./internal/db/... ./internal/models/... ./internal/fix/... ./internal/api/routes/...
make scanners-sync
```

- [Database and HTTP contracts](fix-leases-contracts.txt): PASS. `TestFixJobFencesStaleTransitionsAndHeartbeats` exercises replacement claims with the same worker label, stale results and heartbeats, competing updates, terminal resurrection, eight concurrent claimers on shared/separate branches, busy resumes, and creation versus resume on both databases. Existing creation tests also reopen stores and replay additive migrations. `TestFixMutationConflictReturns409` covers API conflict responses.
- [Fixer package suite](fix-leases-fixer.txt): PASS, including existing once-mode, durable log/diff, publication-retention and lineage checks. New worker regression waits for cancellation after reclaim or user cancellation and asserts that the old worker cannot replace the newer row with failure.
- [Final worker/orchestrator check](fix-leases-final-worker.txt) follows the last ownership guard changes.
- [Scoped Go lint](fix-leases-lint.txt): zero issues.
- [Scanner source synchronization](fix-leases-sync.txt) and [owned fixture cleanup](fix-leases-fixture-cleanup.txt) are recorded separately.

## Cleanup ordering follow-up

The orchestrator now schedules normal workspace cleanup only after its final result update succeeds. A failed or stale final write retains the workspace and returns the persistence error; failed runs retain their checkouts as well. Cleanup failures preserve the path and record an error on the saved job. Successful cleanup clears the stale workspace path. If saving cleanup metadata fails after the result was saved, the worker logs that distinction instead of pretending the result itself failed.

The shared workspace cleanup stops when Git refuses removal (including locked worktrees) rather than bypassing Git with recursive deletion. It retains its temporary root on failure for a retry. Discard stops if cleanup fails, and branch deletion treats an already-removed branch as success. `DiscardPath` requires readable metadata and the expected branch; a `wolf-ws-` directory prefix alone no longer authorizes deletion.

Verification:

```sh
go test -race ./internal/fix/orchestrator ./internal/fix/workspace -count=1
go test -race ./internal/api/routes -run 'Test(CancelFix|FixMutationConflict)' -count=1
golangci-lint run ./internal/fix/orchestrator/... ./internal/fix/workspace/...
make scanners-sync
```

[Orchestrator/workspace regressions](fix-cleanup-tests.txt) cover success ordering, failed final writes, stale-claim conflicts, persisted cleanup errors, real Git worktree locks, cleanup retry, mismatched/unreadable metadata, and cancelled cleanup. [Existing cancellation API checks](fix-cleanup-api.txt), [scoped lint](fix-cleanup-lint.txt), and [scanner sync](fix-cleanup-sync.txt) are recorded separately. No deployment or external publication was performed.

## Cancellation coordination follow-up

Cancellation now keeps `workspace_path` and `result_branch` with a durable `cancellation_cleanup` pause reason. It takes the same repository lock as creation and resume. New creation is refused while cancellation is pending, and claims cannot start on the reserved branch/remediation/workspace. The active worker acknowledges only after its engine returns, including cancellation racing the initial claim. It restores a prepared workspace path if cancellation beat that path's first persistence.

API and worker cleanup share `CompleteFixCancellation`: it checks the revision, takes claim/repository exclusion, refuses another active/review job on the branch, runs cleanup, and atomically clears recovery fields while discarding the remediation. Cleanup and late acknowledgement failures retain the pending marker and paths. Another cleanup attempt with an obsolete revision cannot delete a newly created branch. A repeated DELETE retries cleanup without restarting the agent; completed cancellation is idempotent. The job page polls pending cancellation, shows errors, and offers **Retry cleanup** only after the worker releases its claim. Accepting a remediation rejects pending cancellation and propagates job-list failures.

Automatic follow-up jobs now use the existing idempotent creation transaction, which rechecks the predecessor inside that transaction. A cancellation that wins after the lineage precheck cannot be bypassed by the old direct enqueue path.

Verification:

- [SQLite/PostgreSQL and HTTP results](fix-cancellation-database-api.txt): pending state survives cleanup failures; a real late SQL trigger failure rolls back remediation discard and retains recovery data; concurrent creation waits for cleanup and starts a new session; stale cleanup cannot run after acknowledgement; queued branch claims remain blocked. HTTP checks cover recovery paths, failed cleanup, blocked new work/acceptance, and repeated DELETE recovery.
- [Worker and orchestrator race checks](fix-cancellation-worker.txt): cancellation before and during engine execution is acknowledged only after the engine stops; previous claim and result fencing regressions remain green.
- [Lineage/OpenAPI checks](fix-cancellation-lineage-openapi.txt): normal follow-ups still work and a cancellation injected immediately before creation prevents a late follow-up.
- [UI type/lint checks](fix-cancellation-ui.txt) and [three browser checks](fix-cancellation-browser.txt): desktop/mobile Chromium and Firefox exercise pending polling, cleanup failure, retry, and absence of agent resume.
- [Final scoped Go lint](fix-cancellation-lint.txt), [scanner synchronization](fix-cancellation-sync.txt), and [owned PostgreSQL fixture cleanup](fix-cancellation-fixture-cleanup.txt) are recorded separately.

The cleanup transaction deliberately holds claim/repository exclusion during local filesystem/Git work. This is a throughput ceiling for large checkouts; it does not qualify filesystem fencing across database connection loss, process suspension or abandoned engine containers. No deployment or external publication was performed.

## Atomic local handoff and child-scan admission

**Accept branch** now shares repository/claim exclusion with cleanup and submission. It refuses active jobs, pending cancellation, and child scans using the checkout. The cleanup callback verifies a clean worktree whose HEAD matches a branch commit retained in the authoritative local repository; an unpublished clone cannot be deleted as a local handoff. Cleanup errors preserve the open remediation and recovery path. Successful acceptance records the commit, freezes the remediation, clears the workspace path and finishes its paused jobs in one transaction. Retry after cleanup but before database acknowledgement uses the surviving origin branch; repeating an acknowledged handoff does not delete anything again.

Pending child scans now acquire the same workspace admission lock before insertion. They cannot attach after handoff or while a fixer owns the checkout. Conversely, fix creation, resume and claims wait for pending/running child scans. Cancellation cleanup also waits for those readers. Historical completed scan records remain insertable. Shared remediation updates cannot reopen frozen/discarded state or restore a released path, and lineage no longer queues work against a closed remediation. Orchestration and lineage propagate remediation persistence failures.

Verification:

- [SQLite/PostgreSQL race checks](fix-handoff-database.txt): active child scans prevent handoff, fix creation, resume and claim; cleanup failure and a late SQL trigger failure preserve recovery state; a concurrent creator waits and then observes the frozen session; stale updates and late child scans cannot reopen it; repeated acceptance does not repeat cleanup.
- [HTTP checks](fix-handoff-api.txt): real Git worktree lock failure retains the remediation and path, retry succeeds after unlocking, the branch survives, paused controls become terminal, repeated acceptance succeeds, and new fixing against the frozen origin is refused.
- [Fixer package race suite](fix-handoff-fixer.txt): real local branch preservation, uncommitted-edit refusal, retry after filesystem cleanup, unpublished-clone retention, and existing workspace/publication/worker/lineage checks pass.
- [Scoped Go lint](fix-handoff-lint.txt), [scanner synchronization](fix-handoff-sync.txt), and [owned fixture cleanup](fix-handoff-fixture-cleanup.txt) are recorded separately. No running deployment or external publication was changed.

## Subprocess cancellation

Noninteractive fixer commands now reuse `plugin.CommandContext`, the existing scanner process-group helper. Cancelling an engine, verification/build command, dependency updater, Git operation, publication command or CLI probe kills its process group. Previously these commands generally used `exec.CommandContext`, which killed only the immediate child: a wrapper's descendant could retain output pipes and keep the worker blocked after cancellation. OpenCode retains its streaming/stall watchdog.

`go test ./internal/fix/engine -run '^TestEngineCancellationStopsChildren/custom$' -count=1` reproduced the defect before the change: `cancelled engine is still waiting on its child process` after two seconds. The regression starts a real shell wrapper with a sleeping child that inherits output, waits for the child's PID, cancels the engine, and requires prompt return. It covers Claude, Codex, custom/configured engines and OpenCode, with explicit fixture cleanup.

Verification: [all fixer packages with the race detector](fix-process-cancellation.txt), run using `go test -race ./internal/fix/... -count=1`; [scoped lint](fix-process-lint.txt), zero issues; and [scanner context/lock synchronization](fix-process-sync.txt), run using `make scanners-sync`. This verifies cooperative cancellation while the worker is alive. It does not prove shutdown after worker SIGKILL, stop processes that detach into another session/group, or fence a suspended worker. Interactive PTY consoles remain separate because their session wrapper and leases need their own recovery handling.

## Console claim and input recovery

Console heartbeats now renew only the original worker/claim timestamp, without rewriting status, URL or error fields. A renewal error cancels the session context. Status and URL updates use that same claim, cannot rewrite terminal sessions, and initial persistence must succeed before execution. The runner and its worker no longer reload a replacement claim to save their result; final writes after cancellation have a 30-second deadline. SQLite claims read their result before committing and propagate real query failures.

Input draining now validates and locks the active claim in the same transaction as selection/deletion. Concurrent drainers cannot duplicate keystrokes, and stale/cancelled workers cannot consume them. Expired login, shell and installation consoles become `exited` with `Worker heartbeat expired; start a new console.` They are not automatically requeued: a new terminal cannot recover the old terminal's state safely. The existing console UI renders this error and enables starting another session. Cancellation racing a claim returns HTTP 409 for refresh/retry.

Verification:

- [SQLite and PostgreSQL 18.6 race checks](console-claims-database.txt): `WOLF_TEST_POSTGRES_DSN=… go test -race ./internal/db -run 'TestFixerConsole' -count=1 -v`. Both stores reject stale timestamps, preserve progress during heartbeat, deliver concurrent input only once, preserve cancellation, prevent expired-session replay and support cancellation before claim.
- [Fixer and API race checks](console-claims-fixer-api.txt): `go test -race ./internal/fix/... ./internal/api/routes -count=1`. Real console output/URL handling, cancellation immediately before result persistence, refusal to restart cancelled sessions, and heartbeat shutdown on expiry are included. [Final console/worker race checks](console-claims-final-worker.txt) also pass after bounding final persistence contexts.
- [Scoped lint](console-claims-lint.txt), [scanner synchronization](console-claims-sync.txt) and [owned PostgreSQL fixture removal](console-claims-fixture-cleanup.txt) are recorded separately.

This closes the older console database lease path. It does not establish process/session termination after worker death or fencing of detached PTY children; those remain under F09 process recovery. No schema migration, dependency or UI change was required.

## Published workspace cleanup and retry

After a successful publication is durably recorded and its remediation frozen, the worker releases the clean checkout through the existing handoff transaction. The transaction requires a matching succeeded/pushed job and commit; GitHub jobs also require a saved PR URL without a publication error. Failed push/PR jobs retain their checkout. Cleanup verifies workspace metadata, branch, clean working tree and exact published HEAD before removal, and preserves the original publication timestamp and result evidence. Job and remediation workspace paths clear atomically. Filesystem errors or late database acknowledgement failures leave recovery metadata for retry. Repeating an acknowledged cleanup does nothing.

The existing accept endpoint also handles this published-workspace cleanup retry. The job page shows **Retry cleanup** while the successful published job retains a checkout, preserves the PR link, and never resumes an engine or republishes. It polls during automatic cleanup until completion or a visible error. Legacy jobs without remediation ownership now also clean up after a successful publication resume, after the final result is persisted.

Verification:

- [SQLite/PostgreSQL race contracts](fix-published-cleanup-database.txt): reject failed PR/missing PR evidence, retain paths on cleanup and late acknowledgement failures, safely retry, atomically clear both paths, retain publication receipt/timestamp, and exclude active workspace users through the existing handoff checks.
- [Real Git workspace and orchestrator checks](fix-published-cleanup-tests.txt): reject dirty clones and commits different from the published SHA; remove a matching clean clone; retry after physical removal; retain failed PR work; clean successful publication resumes.
- [Worker/API race checks](fix-published-cleanup-worker-api.txt): automatic cleanup, visible cleanup errors with recovery paths, PR-failure retention, and existing authenticated handoff routes; [full fixer race suite](fix-published-cleanup-fixer.txt) also passes.
- [UI type/lint checks](fix-published-cleanup-ui.txt), [browser checks](fix-published-cleanup-browser.txt), [scoped Go lint](fix-published-cleanup-lint.txt), [scanner synchronization](fix-published-cleanup-sync.txt), and [owned PostgreSQL fixture cleanup](fix-published-cleanup-fixture-cleanup.txt).

Commands: `WOLF_TEST_POSTGRES_DSN=… go test -race ./internal/db -run 'TestPublishedFixCleanup|TestFixHandoff' -count=1 -v`; `go test -race ./internal/fix/... -count=1`; focused route/worker checks recorded above; `pnpm typecheck`, `pnpm lint`, `pnpm test:e2e:typecheck`, and `pnpm test:e2e e2e/agents-state.spec.ts --grep 'published agents retry'`; scoped `golangci-lint run` and `make scanners-sync`. Provider publication is simulated in these checks; actual remote publication qualification remains F15. No external PR or deployment was changed.

## Remaining F09 work

Database fencing is not filesystem or process fencing. An engine that ignores cancellation, a suspended worker resuming after lease expiry, and abandoned container/workspace recovery still require qualification. Cancellation coordination and normal retry are covered above. A worker that disappears before acknowledging engine shutdown remains pending rather than guessing that deletion is safe; abandoned-engine recovery is still open. Local handoff, shared child-scan admission and post-publication cleanup/retry are covered above. Worker death between branch side effects and result persistence, and follow-up lineage enqueue after worker death, also remain open. Console claim/heartbeat/input fencing is covered above; detached PTY and worker-death process recovery remain open. Full container/process-kill recovery and running deployment qualification are not claimed by these tests.

Stop/drain old fixer binaries before deploying the changed claim protocol: old binaries do not honor the new revision guard. No running deployment was changed in this batch.
