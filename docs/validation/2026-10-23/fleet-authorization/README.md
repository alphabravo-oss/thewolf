# D06.2 — keep fleet authorization bound to the request credential

Candidate: uncommitted working tree, 2026-10-23. This is local qualification,
not a release or deployment. D06 and the complete plan remain open.

## Reproduction and repair

The [production-router reproduction](reproduction.txt) demonstrates a shared
privilege bypass. Authentication correctly omitted a human administrator role
from an administrator-owned API token. `fleetVisible` then reloaded the owner's
account and granted fleet visibility anyway. Both finding and vulnerability
access helpers trusted that decision for reads and mutations.

A read-findings token exposed foreign findings, canonical vulnerabilities and
evidence; aggregate exports, repository/scan/collection lists, inventory, posture
and trends also included other users' resources. A write-findings token changed a
foreign finding's status and accepted a mixed-owner bulk update. Canonical split
and merge requests reached validation rather than being rejected for ownership.

The shared fleet helper now rejects API-token elevation before loading the human
account role, using existing `auth.TokenIDFromContext`. All callers were traced.
Authenticated sessions retain current-role fleet access; token scopes retain their
capabilities on owned resources. This also applies to the `admin` super-scope:
capability is not a human role. No dependency, new permission model or DB migration
was added. The authentication guide now describes that distinction.

## Runnable regression

```sh
go test -race -count=1 ./internal/api ./internal/auth -run 'TestFleetAuthorizationDoesNotElevateAPITokens|TestFindingExportAuthorization|TestAPITokenScopesRemainTokenBound'
make scanners-sync
WOLF_TEST_POSTGRES_DSN='<owned loopback fixture>' make test
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run
```

The new real-router test issues 114 resource requests after fixture/token setup.
Five principals are exercised: administrator session, administrator read token,
administrator super-scope token, regular-user session and regular-user read token.
The matrix covers:

- Repository, collection, scan, finding and canonical vulnerability lists;
  aggregate finding export, by-rule/by-repository views, inventory and priorities.
- Owned and foreign finding detail, canonical detail and both evidence routes.
- Posture and trend repository counts, including exported trends.
- Foreign single and mixed-owner bulk triage rejection, with persisted status
  checks proving neither finding was changed by the rejected operations.
- Foreign split/merge rejection before cluster-validation details can leak.
- Read-only token mutation denial, successful owner-token triage, successful
  administrator-session triage and immediate session demotion without a new JWT.

The existing scan-export and auth-middleware token-bound scope regressions run
alongside this matrix. [Initial focused check](contracts.txt) and
[final expanded check](contracts-final.txt) retain the outcomes.

## Verification

The [final focused checks](contracts-final.txt) and [136-package uncached race
suite](race.txt) pass. The full suite runs SQLite and real PostgreSQL; the new
HTTP matrix runs against SQLite through production routing and authentication.
[Lint](lint.txt) reports zero issues. [Scanner synchronization](sync.txt) validates
53 tools and 51 version pins. The [security scan](gosec.json) still fails with
29 high/high findings (25 G703, one G702 and three G704), no analysis errors and
no new waivers. R03 remains release-blocking.

The [PostgreSQL receipt](postgres.json) records the owned fixture's exact container,
image, loopback port and anonymous volume, plus verified removal of the container
and volume. No running service was replaced. [Check summary](checks.json),
[source hashes](source.json) and [receipt hashes](sha256.json) identify the tested
working tree without changing historical qualification records.

## Remaining acceptance

This closes API-token inheritance of the human fleet role at the shared helper.
It does not certify all authorization boundaries. Legacy ownerless-resource
compatibility, consistent 403/404 metadata non-disclosure, raw evidence and import
matrices, in-root cross-scan artifact aliases and the remaining D06 requirements
stay open. Existing token revocation/expiry checks remain in the full suite; this
change does not redesign those policies. It also does not close D05's persisted
source/policy and stored-count freshness requirements.
