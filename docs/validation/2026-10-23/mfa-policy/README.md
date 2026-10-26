# D07.5 — mandatory-MFA policy and human credential boundary

2026-10-23. The pre-fix production-router checks reproduced normal repository access and a false optional-MFA status during policy-read failure, password login issuing an unenrolled session during that failure, and a read-only API key obtaining a fresh enrollment secret and changing its owner's pending MFA state. `reproduction.txt` retains those failures.

The shared policy reader now distinguishes absence (`sql.ErrNoRows`) from unavailable/malformed policy. Missing or empty values preserve optional MFA; boolean strings are normalized case-insensitively and parsed with the standard library, preserving existing mixed-case values. The settings writer uses the same parser and canonicalizes accepted values before persistence, rejecting an invalid policy before any submitted setting is written. Password login checks policy before issuing an unenrolled session. Status and the destructive disable action handle policy failures explicitly; protected human business requests return a private 503. A failure loading required MFA state also returns 503 rather than claiming enrollment is absent.

The guard allows only the actual enrollment/identity/logout endpoint and method pairs, not arbitrary path suffixes. Scoped machine credentials retain their MFA exemption for automation. A shared human-session guard protects all four MFA self-service endpoints, including status: API keys cannot change factor state regardless of scope or whether they arrive in an Authorization header or cookie. Ordinary user and administrator human credentials can still enroll; a real MFA-issued browser cookie can disable optional MFA with a valid unused recovery code. The administrator reset route retains its existing role/scope boundary.

The Security tab now represents unavailable or failed status as unknown, offers a scoped retry, and hides idle setup/disable actions until status is known. It continues showing enrollment-in-progress and freshly generated recovery codes independently of a failed status refresh. This fixes the previous false “Off” display without discarding codes that are shown only once. OpenAPI describes the human credential requirement and generic service-unavailable response.

## Runnable checks

- `TestMFAPolicyFailureDeniesProtectedAccess` rejects policy-read failures for repository access and MFA status without exposing storage diagnostics.
- `TestMFAPolicyFailureDoesNotIssueUnenrolledSession` checks the password-login path: no access token or cookie on policy failure.
- `TestMFAReadOnlyKeyCannotEnroll` verifies both absence of disclosed enrollment material and unchanged stored secret.
- `TestMFAMachineCredentialMatrix` covers read-repos, write-credentials and admin-scope keys across all four MFA operations, valid-code activation/disable attempts, unchanged pending state, a machine key in a cookie, and a real MFA-issued browser cookie completing optional disable.
- `TestMFARegularHumanCanEnrollButMachineCannot` qualifies a regular owner independently of the administrator fixture.
- `TestMFAMandatoryEnrollmentAndAutomation` verifies the login enrollment flag, confinement of unenrolled human repository/token/settings requests, scoped machine access, accessible identity/status/enrollment, normal access after enrollment and rejection of mandatory disable.
- `TestMFAPolicyOutagePreservesSafeOperations` covers machine access, identity, enrollment/activation and logout during outage while enrolled human business access remains closed.
- `TestMFADisableChecksPolicyAtAction` forces the policy read to fail only after guard admission; the destructive action still rejects without changing MFA or consuming a recovery code.
- `TestMFAPolicySettingsValidation` covers absent/empty/false policy, malformed stored policy, rejected multi-setting writes with no partial changes, canonicalized true policy and immediate enforcement on subsequent requests.
- `TestMFAStateReadFailureIsUnavailable` checks the separate user-state read and keeps its private diagnostic out of responses.
- `TestMFAEnrollmentAllowlistMatchesExactOperation` directly qualifies the middleware's endpoint/method matching, rejecting lookalike suffixes and wrong methods. This is a path-classification regression check, not a claim that a matching exploit route existed.
- `ui/e2e/mfa-policy.spec.ts` runs two mocked-API browser journeys on desktop Chromium, mobile Chromium and desktop Firefox: initial failure/retry/required status; activation followed by failed refresh with one-time codes preserved through recovery. All six cases pass. Browser contracts are separate from the real API/database tests.

```sh
go test -count=1 ./internal/api -run '^Test(MFAPolicyFailure|MFAReadOnlyKey)'
go test -race -count=1 ./internal/api ./internal/auth ./internal/api/openapi -run 'Test.*MFA|TestEveryEndpoint|TestBuild'
# ui/, using Node 26.11.1 and pnpm 12.11.2:
pnpm typecheck && pnpm lint && pnpm test && pnpm test:e2e:typecheck
pnpm exec playwright test e2e/mfa-policy.spec.ts --workers=2
# product root; regenerate before any Go build/test that embeds the tree:
make scanners-sync
WOLF_TEST_POSTGRES_DSN='<owned fixture in postgres.json>' make test
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run
/tmp/wolf-plan-gosec-bin -fmt json -out docs/validation/2026-10-23/mfa-policy/gosec.json -severity high -confidence high -exclude-dir=internal/scannerbuild/context ./cmd/... ./internal/... ./pkg/... ./plugins/...
```

The first command failed before repair. `focused.txt`, `focused-expanded.txt` and `focused-final.txt` retain the passing stages; the final run includes state-read errors and the regular-user case. `ui-checks.txt` transcribes the untruncated terminal result for the four successful UI checks: 53 tests in 11 files, clean typecheck/lint and browser-test typecheck. The existing jsdom canvas warning did not fail a test. `browser.txt` includes the build invoked by the repository Playwright workflow and all six passing cases. Its temporary server stopped and port absence was verified in `browser-cleanup.json`. The pinned pnpm launcher downloaded its requested binary; no package manifest, dependency or lock change was introduced.

## Remaining scope

This is fail-closed request authorization, not cancellation of already authorized requests or complete revocation of preexisting access/browser/refresh/API credentials. Enrollment guard admission and the destructive disable action independently check policy; each read must succeed. Registration/federation/refresh issuance as a whole and full session invalidation remain A03/E; no claim that every issuance path has been changed. API keys intentionally remain exempt from interactive enrollment requirements, but cannot manage MFA. An unavailable/malformed policy grants no administrator exception; restore the backing settings or repair invalid persisted data through trusted database maintenance.

Distributed login accounting, bootstrap/no-repromotion/break-glass qualification and complete identity-provider acceptance remain open. The UI change qualifies this Security-tab failure/recovery journey, not all U09 error states. Nothing was deployed; no broad production or release-readiness claim is made.

## Final verification

The final case-insensitive policy candidate passes the uncached 136-package SQLite/PostgreSQL race suite (`go-race-final.txt`), zero-issue Go lint and scanner synchronization. The earlier full-suite log also has 136 passing packages; its terminal exit handle was lost during context restoration, so the final run supplies the captured exit status. `scanner-sync-final.txt` precedes the final Go build. UI results remain applicable because the final compatibility adjustment changes only Go policy parsing and its regression inputs.

Gosec exits 1 with the same 29 historical high/high findings (25 G703, one G702, three G704), no analysis errors and an identical rule/file/line/details set to `mfa-replay/gosec-final.json`. Security remains release-blocking under R03. The owned PostgreSQL container and anonymous volume are removed and absence verified in `postgres.json`. Source and receipt hashes identify this uncommitted candidate, not a deployed or immutable release bundle.
