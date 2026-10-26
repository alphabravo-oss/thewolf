# F11 durable fix creation — 2026-10-24

Completed the shared REST/CLI/MCP creation contract without adding a queue or
replacing the existing fixer worker. No external PR or deployment was performed.

`Idempotency-Key` plus a normalized request digest is immutable on the job.
The `(user_id, idempotency_key)` partial unique index protects keyed requests.
Same-key retries return the original job (HTTP 200); changed intent returns 409.
Settings changes do not change a replayed job. Keys remain scoped to their owner.

A single database transaction attaches/creates the remediation, checks busy/frozen
state, inserts the job and supersedes older paused review jobs. Failed enqueue
rolls everything back. Repository/scan/finding ownership and source relationships
are enforced; explicit finding selection cannot expand into a child scan.
Whole-scan requests may select the latest completed child in the same session.
Requested custom branches are honored or explicitly conflict with the existing
session. Main/master/default branches cannot be selected for new fixes.

The PostgreSQL fixture also reproduced integer-backed boolean encoding failures
in fixer inserts, updates and attempts. Explicit boolean-to-integer SQL conversion
now preserves review/push/verification state on PostgreSQL and SQLite.

Both UI creation controls retain keys after uncertain responses and replace keys
when intent changes or a completed submission is followed by a new one. CLI
`fix create` and `agent create` accept `--idempotency-key`. MCP `wolf.fixes.create`
requires a key and invokes the existing REST router, preserving fixing policy,
write scope, owner checks and audit. Dry-run is default; explicit push retains the
F10 publication/PR consequences.

Checks:

- [Both database contracts](fix-creation-databases-final.txt): eight simultaneous
  retries, two independent SQLite stores, PostgreSQL 18.6 connections, database
  close/reopen and migration replay, payload conflict, owner isolation, failure
  rollback, explicit/whole-scan selection, and persisted boolean verification.
- [HTTP, actual MCP SDK transport and CLI](fix-creation-contracts-final.txt):
  original job replay, changed push intent rejected, cross-owner scope rejection,
  default branch rejection, disabled policy, missing write scope/key, durable
  HTTP audit, and stable CLI header/body propagation.
- [OpenAPI checks](fix-creation-openapi.txt).
- [Worker and lineage race checks](fix-creation-worker-checks.txt).
- [UI retry hook, typecheck and lint](fix-creation-ui.txt).
- [Whole-repository Go lint](fix-creation-lint.txt), [source/lock sync](fix-creation-sync.txt).

Commands: with the disposable PostgreSQL DSN, `go test -race ./internal/db -run
'Test(Fix|UpdateFix|Autofix)' -count=1`; the named HTTP/MCP/CLI tests are
`TestCreateFixIdempotencyAndOwnership`, `TestMCPClientAuthorizationAndExecution`
and `TestFixCreationCLIForwardsStableKeyAndIntent`. UI: `pnpm exec vitest run
src/lib/fixes.test.tsx`, `pnpm typecheck`, `pnpm lint`.

Compatibility: unkeyed legacy REST creation remains supported without replay
protection. The UI preserves pending keys while mounted, not across reloads;
scripts should provide a stable key across process invocations. F09 retains
worker/lease/resume race qualification, and F15 retains the real remote fix/PR
journey. The owned disposable PostgreSQL fixture is removed after verification.
