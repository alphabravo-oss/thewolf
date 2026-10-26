# F10 publication completion — 2026-10-24

F10 implementation and focused acceptance are complete. No external PR, remote
push or deployment was performed by this qualification.

The publication path now checks owner-scoped GitHub credentials, repository push
permission/archive state and the worker's GitHub CLI before pushing. Missing or
denied readiness pauses with a retained review branch. Publication resumes skip
the earlier generic clone-writability gate so credential repair remains retryable.
The remote service still decides branch protection and PR write permissions;
rejections remain explicit and recoverable.

The persisted review summary includes addressed finding IDs, tool/rule, category,
severity, path/line and recorded validation-stage outcomes. Raw command output is
excluded. PR retries reuse that snapshot even if source finding records change.
Skipped tests and missing older evidence are labeled; no full repository test pass
is inferred. Oversized finding lists are bounded in the PR with a disclosure and
complete data retained in the job summary. PR lookup excludes cross-repository
branches. Existing successful pushes are not repeated on PR recovery.

UI, CLI help and OpenAPI explain that explicit push also creates a GitHub PR.
Successful PR URLs persist; `publication_not_ready`, `push_failed` and
`pr_create_failed` are distinguishable in the job summary. Configured credentials
are redacted from PR payloads/errors and native Git push/clone errors.

Evidence:

- [Readiness, owner-token boundary, archived/read-only/denied/missing-CLI,
  review-snapshot and orchestration race checks](fix-publication-final.txt).
- [PR evidence/body bounds, fork exclusion, OpenAPI and CLI contracts](fix-publication-contracts.txt).
- [Git publication credential regression](fix-publication-secret-checks.txt).
- [Six desktop/mobile Chromium and Firefox checks](../2026-10-23/fix-publication-evidence-browser.txt), including readiness, failed verification, failed push, failed PR and persisted PR link. UI typecheck/lint and browser typecheck passed.
- [Go lint](fix-publication-lint.txt), [scanner lock/context sync](fix-publication-sync.txt).

Commands: `go test -race ./internal/fix/orchestrator ./internal/fix/pr
./internal/fix/wiring ./internal/fix/writability`, `go test -race
./internal/api/openapi ./internal/cli ./internal/fix/workspace`, and from `ui`,
`pnpm exec playwright test agents-state.spec.ts --workers=2`.

HTTP, GitHub executable and browser fixtures are local/deterministic. F15 retains
the genuine remote journey. F07.6 retains final combined-workspace verification;
F09/F11 retain crash/lease and concurrent-publication idempotency qualification.
