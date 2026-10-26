# D07.3 — single-use MFA challenge exchange

2026-10-23. The pre-fix production-router checks reproduced a second successful use of one MFA challenge with another recovery code, acceptance of a challenge that had never been stored, and issuance despite injected challenge-storage failure. `reproduction.txt` retains all three failures.

Password login now creates a unique JWT challenge identifier and saves its hash in the existing `auth_sessions` table with purpose marker `mfa`, before returning it. Its five-minute lifetime is unchanged. JWT purpose validation and the browser cookie's required `wfs_` prefix keep it out of normal access and refresh authentication. Legacy challenges without identifiers fail closed. No schema migration or dependency was added.

The SQLite and PostgreSQL stores share one transaction for successful MFA login: conditionally update the verified MFA snapshot, consume a still-active challenge belonging to that user, insert the refresh session, insert the browser session, then commit. No token or cookie leaves the handler before commit. All statements roll back on a storage failure or invalid challenge, including an already inserted refresh session. The existing conditional MFA helper now accepts either a database or transaction; enrollment, activation, disable and reset retain its comparison behavior.

## Runnable evidence

- `TestMFAChallengeSingleUse` proves sequential replay fails without issuing credentials or consuming the second recovery code.
- `TestMFAChallengeMustBePersisted` rejects a cryptographically valid but unstored challenge without consuming the factor.
- `TestMFAChallengeStorageFailure` verifies the password step returns a private 500 and no challenge/cookie if persistence fails.
- `TestMFAExchangeSessionFailureIsRetryable` forces a duplicate-key failure on the real transaction's second session insert through the production router. The recovery code and pending challenge survive unchanged; retry succeeds, with valid access credentials and both browser/refresh sessions stored.
- `TestMFAChallengeIdentityAndPurpose` verifies distinct identifiers, rejects challenges as access/refresh credentials and rejects the pre-upgrade no-identifier format.
- `TestMFAExchangeSQLite` uses two independent pools on one file; `TestMFAExchangePostgres` uses the real PostgreSQL pool. Both verify rollback on first and second session insert failures, expired/revoked/wrong-purpose/missing challenges, stale MFA state, retry after insert failure, and sixteen concurrent TOTP-style exchanges of one challenge producing one commit and fifteen replay rejections. Failed attempts leave no session records.
- The preceding MFA-state and recovery-code tests still pass. The unused-code retry after a race now obtains a fresh password challenge, since the winner consumed the original. Failure injection covers both state-update and atomic-exchange store methods.

```sh
go test -count=1 ./internal/api -run '^TestMFAChallenge'
go test -race -count=1 ./internal/auth ./internal/api ./internal/api/routes -run 'Test.*MFA|Test.*TOTP|Test.*Recovery'
WOLF_TEST_POSTGRES_DSN='<owned fixture>' go test -race -count=1 -v ./internal/db -run 'TestMFA(State|Exchange)'
make scanners-sync
WOLF_TEST_POSTGRES_DSN='<owned fixture in postgres.json>' make test
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run
/tmp/wolf-plan-gosec-bin -fmt json -out docs/validation/2026-10-23/mfa-exchange/gosec.json -severity high -confidence high -exclude-dir=internal/scannerbuild/context ./cmd/... ./internal/... ./pkg/... ./plugins/...
```

The first command failed before repair. `focused.txt` is the initial passing subset; `focused-expanded.txt` includes the added session-insert failure and token identity checks. `database-contracts.txt` records both database backends explicitly. The full uncached 136-package race suite passed with SQLite and PostgreSQL; lint reports zero issues and final scanner sync validates 53 tools, 25 upstream images and 51 version pins. A subsequent comment-only clarification in the transaction helper was included in final sync/lint/security checks; executable code and tests were unchanged. Gosec exits 1 with 26 high/high findings (22 G703, one G702, three G704), with no analysis errors or waivers. Three prior G703 alerts in `cmd/scannertools/bump.go:276` and `cmd/scannertools/propose.go:256,281` were not reported this run; those files were untouched by this MFA change. `gosec-delta.json` retains the difference without claiming remediation or waiving the earlier findings. R03 remains release-blocking. The owned PostgreSQL container and anonymous volume were removed and their absence verified. `checks.json`, `source.json` and `sha256.json` retain results and input hashes; this is an uncommitted working tree, not an immutable release source bundle.

## Limits and upgrade behavior

New API instances reject pre-upgrade challenges; users restart the password step. Old API instances can bypass the new transaction, so a mixed-version rollout is not qualified. Nothing was deployed. Database failure before commit preserves retryable state; a committed exchange with a lost HTTP response remains consumed, requiring a fresh password challenge and unused factor. No automatic replay of a committed credential is provided.

Single-use challenge protection does not prevent the same TOTP timestep being accepted under a different password challenge. Pending challenges are not bound to a monotonic password/enrollment epoch, and old sessions are not automatically revoked by every MFA change. These remain D07/A03, along with distributed login-failure accounting, mandatory-MFA policy failures, credential-class management rules and bootstrap/break-glass qualification. Expired session/challenge retention remains O05. No full authentication acceptance or release readiness is claimed.
