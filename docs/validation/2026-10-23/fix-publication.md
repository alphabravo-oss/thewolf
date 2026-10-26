# Fix publication recovery — 2026-10-23

D13 is complete; F10 has implementation progress. No external PR or deployment was created.

- Generic failed jobs with a retained branch previously bypassed verification through the legacy resume-to-push path. Only explicitly paused review/publication jobs may resume now. Invalid, oversized, unknown-field and trailing JSON requests are rejected.
- Push resumes require the retained workspace and matching persisted review summary. They preserve counts, rounds and report evidence; missing/corrupt summaries fail without publishing or deleting the workspace.
- GitHub PR errors, nil/empty responses and returned errors preserve the branch and pause publication. Successful URLs persist in the existing summary JSON and appear on the agent detail page. A PR retry after successful push does not push again.
- PR creation uses the repository owner's configured token through the existing credential resolver. It runs outside the repository with temporary CLI configuration, explicit repository/base/head, a timeout and no inherited Git/GitHub overrides. Output and body redact the configured token; shared text redaction handles credential assignments. A matching open PR is reused on retry.
- Verification failures show their actual error and cannot offer Retry push. Failed push and failed PR creation have distinct messages/actions on agent detail; the scan Agents panel no longer offers failed verification for publishing or hides failure behind a prior push.

Checks:

- [Targeted API and recovery checks](fix-publication-checks.txt), followed by [final race checks including PR-only retry](fix-publication-final.txt).
- [All 21 fixer packages pass race tests](fix-publication-race.txt): `go test -race ./internal/fix/...`. This includes missing-image/incomplete verification, rollback, retained-workspace and scoped-credential checks.
- [Six Chromium/mobile/Firefox browser checks](fix-publication-browser.txt): `pnpm exec playwright test agents-state.spec.ts --workers=2`. UI typecheck, lint and browser typecheck passed. Browser APIs and the GitHub executable in the credential test are deterministic fixtures.
- [Go lint](fix-publication-lint.txt), [scanner context/lock sync](fix-publication-sync.txt).

Remaining: F07.6 final combined-workspace verification, F09 crash/lease and cleanup qualification, F10 richer verified finding evidence and early publication permission readiness, and F15 a real authorized remote journey. Existing PR lookup handles normal sequential retries; durable concurrent publication fencing remains F09/F11.
