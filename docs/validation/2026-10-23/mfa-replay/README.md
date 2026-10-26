# D07.4 — TOTP replay and credential-bound MFA challenges

2026-10-23. Four pre-fix public-router tests reproduced: reuse of the enrollment confirmation code for login, reuse of a successful TOTP code under a fresh password challenge, use of an old challenge with a new enrollment's recovery code, and creation/use of a stale challenge by a password login crossing a password change plus session revocation. `reproduction.txt` retains all four failures.

Migration `068_mfa_replay.sql` adds three private user fields: `totp_last_step` (initially -1), `mfa_revision` and `credential_version` (initially zero). Existing enrollment material stays intact. For an active legacy enrollment with unknown consumption, the shared migration helper initializes the recorded step to the highest counter in the current accepted window. This rejects that old window on upgrade; unenrolled users retain -1. Subsequent migration runs preserve known steps. The shared TOTP verifier uses the installed OTP library's standard six-digit SHA-1 counter validation with the existing 30-second, ±1-step window, returning the highest matching step. Activation stores that step; login and disable reject steps at or below the stored maximum. Login commits consumption in the existing challenge/session transaction. Setup/clear reset the counter for a new enrollment.

Every MFA writer compares the stored step, revision and credential version as well as the original MFA fields. Successful state changes advance the revision, preventing a stale equal-state snapshot from replacing enrollment. Secret or enabled-flag changes also advance the credential version. Password changes advance that same version in the shared user update; stale user writes fail rather than restoring an old password hash during a role edit. Role-only updates preserve the version. Challenge JWTs include the version from the exact user snapshot whose password was verified, not a later reread. Missing versions fail closed. The login transaction's compare-and-update also rejects a password change after factor verification but before commit.

## Runnable checks

- `TestMFAActivationCodeCannotLogin` reuses the exact stored enrollment step, avoiding a wall-clock boundary assumption.
- `TestMFATOTPCannotReplayWithFreshChallenge` rejects successful-login code reuse through both another challenge and MFA disable.
- `TestMFAChallengeInvalidAfterReenrollment` preserves the new recovery-code set when an old challenge is rejected.
- `TestMFAChallengeIssuedAcrossPasswordChange` forces the password change/revocation after the password snapshot was read but before challenge persistence; the stale challenge still cannot authenticate.
- `TestMFAPasswordChangeBeforeCommit` forces a password change after factor verification. The real transaction conflicts and neither issues credentials nor consumes the recovery code.
- `TestMFATOTPConcurrentChallenges` synchronizes two real-router reads of the same step, using separate persisted challenges: one success, one conflict. The losing challenge remains usable with an unused recovery code.
- `TestMFATOTPInsertFailureRollsBackStep` fails the second session insert in the real transaction, checks the stored step/revision remain unchanged, then successfully retries the same factor and challenge.
- `TestTOTPStepWindow` uses a fixed time/secret to check both accepted skew directions, rejection outside the window, exact returned counters, whitespace handling, invalid secrets and negative time. Challenge identity tests also reject tokens missing the credential version.
- `TestMFAReplaySQLite` and `TestMFAReplayPostgres` each use **two independent pools**. Sixteen competing exchanges of one step through different challenges produce one commit and fifteen conflicts. Failed exchanges leave no session and do not consume their challenge. The tests reject step rollback, stale password/role updates and stale equal-state enrollment writes, then close both pools and reopen the database to verify persisted step/version/revision/password/role state and continued rollback rejection.
- `TestMFAReplayMigrationSQLite` and `TestMFAReplayMigrationPostgres` remove the three new columns to reproduce the previous schema, retain an active enrollment and recovery codes, run migration, verify the entire old code window is denied by the initial recorded step, consume a newer step, and rerun migration without clearing it. Fresh-schema coverage comes from all normal store fixtures.

```sh
go test -count=1 ./internal/api -run '^Test(MFAActivationCodeCannotLogin|MFATOTPCannotReplayWithFreshChallenge|MFAChallengeInvalidAfterReenrollment|MFAChallengeIssuedAcrossPasswordChange)$'
go test -race -count=1 ./internal/auth ./internal/api ./internal/db -run 'Test.*MFA|Test.*TOTP|Test.*Recovery'
WOLF_TEST_POSTGRES_DSN='<owned fixture>' go test -race -count=1 -v ./internal/db -run 'TestMFA'
make scanners-sync
WOLF_TEST_POSTGRES_DSN='<owned fixture in postgres-final.json>' make test
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run
/tmp/wolf-plan-gosec-bin -fmt json -out docs/validation/2026-10-23/mfa-replay/gosec-final.json -severity high -confidence high -exclude-dir=internal/scannerbuild/context ./cmd/... ./internal/... ./pkg/... ./plugins/...
```

The first command failed before repair. The first post-fix `focused.txt` run exposed an older test fixture retaining a stale revision after restoring its factor state; it now retains the updated snapshot. `focused-expanded.txt` passes after this fixture correction. Existing tests that intentionally log in immediately after enrollment now use a later still-valid step. A stale concurrent exchange can now be rejected by the revision check before reaching challenge consumption; the old exchange test accepts either legitimate rejection and still requires exactly one success. `database-contracts.txt` and `go-race.txt` passed before the additional legacy-window upgrade safeguard. That review found unknown historical steps should deny the old window instead of requiring operators to wait before obtaining the guarantee. The safeguard was then added. `database-during-sync.txt` records an orchestration failure: a test build overlapped scanner context regeneration while embedded files were temporarily absent. It was rerun after generation completed; `database-contracts-final.txt` passes. `scanner-sync-upgrade.txt` and `go-race-final.txt` qualify the final executable source, migration and tests, including the disable-replay assertion and user-facing retry messages. The scanner regeneration and Go build/test must run sequentially because the embedded tree is a build input.

## Boundaries

This verifies persisted factor/challenge replay protection, not complete revocation of existing access/browser/refresh/API credentials after every credential change. That remains A03. Mandatory-MFA policy read failures, credential-class MFA management, distributed login-failure accounting and bootstrap/break-glass qualification remain D07. Session/challenge retention remains O05.

Pre-upgrade challenges without a credential version require another password login. Previously accepted TOTP counters cannot be reconstructed on first migration; active legacy accounts deny the currently valid window and must use a later authenticator code or an unused recovery code. Old API writers can bypass version/counter checks, so mixed-version deployment is not qualified. Nothing was deployed. A committed exchange with a lost HTTP response remains consumed; a failed database transaction can be retried while the challenge and factor are otherwise valid.

## Final verification

The final **136-package uncached race suite** passes with SQLite and PostgreSQL.
Final lint reports **0 issues**. Scanner sync validates 53 tools, 25 upstream
images and 51 version pins. Final gosec exits 1 with **29 high/high findings**
(25 G703, one G702, three G704), no analysis errors or waivers. All match the
historical MFA-state report; three scanner-release alerts omitted by the preceding
MFA-exchange run reappear, as recorded in `gosec-delta.json`. R03 remains blocking.
Both owned PostgreSQL containers and anonymous volumes were removed and their
absence verified (`postgres.json` and `postgres-final.json`). `checks.json`,
`source.json` and `sha256.json` retain outcomes and working-tree hashes; these are
uncommitted inputs, not a release source bundle. No deployment was performed.
