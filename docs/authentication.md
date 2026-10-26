# Authentication & Access Control

The Wolf is multi-user with role-based access control, two-factor authentication,
and scoped API keys. This page covers how identity and access work.

## Roles

Every user has a role:

| Role | Can do |
|---|---|
| **admin** | Everything: system **Settings** (general toggles, users, scanner images), a **global oversight** view of every user's API keys / secrets / nodes, the **audit log**, and modify any resource. |
| **user** | Manage only what they created — their own repos, scans, secrets, SSH nodes, API keys, and 2FA. No access to Settings or other users' data. |

The **first account created becomes admin**. The role is resolved per-request, so
promoting/demoting a user (Settings → Users, or `wolf user set-role <id> --role admin`)
takes effect immediately without re-issuing tokens.

Legacy or system-created records with an empty owner require an administrator
session for direct access; they are never shared with every authenticated user.
Administrators can inspect or recover those records through the existing resource
endpoints. This does not assign an owner or migrate historical data. Private fix
jobs and fixer consoles with a recorded owner remain owner-only, including between
administrator accounts.

Direct repository, scan, and finding access returns the same `404` response for
missing IDs and IDs the caller cannot access. Authentication and API-key scopes
are checked first (`401` and `403` respectively). This includes scan exports,
raw tool output, artifact downloads, comparisons, gates, and baseline attachment.
Administrator sessions retain scan/repository oversight, including gates and
comparisons; API keys remain owner-scoped. SARIF import requires repository
ownership even for an administrator session, and checks ownership before parsing
the supplied SARIF document. These rules do not claim uniform responses for every
resource family or constant-time request handling.

Scan lineage applies access checks to each related origin/child scan, remediation,
and agent job before building the response and run summaries. Administrator scan
oversight does not grant access to another owner's private remediation or jobs.
Missing or inaccessible origins fall back to the requested scan; related database
failures return a server error rather than an apparently complete empty history.

## Human scanner supply-chain personas

Ordinary users retain their existing owner-scoped product access and default to
read-only scanner **Viewer** access. Administrators assign scanner permissions
under **Settings → Users → Scanner access**. System administrators always have
implicit full access and do not need a scanner persona.

| Persona ID | Capability |
|---|---|
| `viewer` | Read scanner inventory, operations, evidence, notifications, and audit history. |
| `scanner_operator` | Run discovery, create/retry candidates, promote releases, and control rollouts. |
| `release_approver` | Approve/reject candidates, record bounded exceptions, and publish approved candidates. |
| `registry_administrator` | Manage registry targets, reconciliation, repair, and guarded quarantine cleanup. |
| `supply_chain_administrator` | Full scanner administration, including policy, signing, notification retry, deprecation, and revocation. |
| `auditor` | Read-only scanner evidence and immutable scanner audit access, retained as explicit audit metadata. |

`scanner_operator`, `release_approver`, and `registry_administrator` are
composable so separation-of-duties can be modeled deliberately. `viewer` and
`auditor` are standalone read-only personas. `supply_chain_administrator` is
exclusive and replaces other scanner persona assignments. Server-side policy
and candidate creator/approver separation remain authoritative regardless of
what the UI displays.

Assign with the CLI:

```bash
wolf user scanner-access USER_ID \
  --persona scanner_operator \
  --persona release_approver

# Revoke elevated access without ending the user's browser session.
wolf user scanner-access USER_ID --persona viewer
```

Or call the administrator API directly:

```http
PUT /api/v1/users/USER_ID/scanner-supply-chain-access
Authorization: Bearer ADMIN_CREDENTIAL
Content-Type: application/json

{"personas":["registry_administrator"]}
```

The API accepts only the six predefined persona IDs, never arbitrary scopes.
Assignments are loaded from persistent storage on every browser request, so a
grant or revocation applies to the next request without logout, login, token
rotation, or waiting for a session cache to expire. API keys remain bound to
their own explicit scopes and are not elevated by the owner's human persona.

## Registration

Self-service registration is **off by default**. Admins create accounts under
**Settings → Users** (or `wolf user create`). The very first account can always
bootstrap the system. To allow open sign-up, enable **Settings → General →
Self-service registration**.

The first-run admin can be seeded from the environment:

```bash
WOLF_ADMIN_EMAIL=admin@example.com
WOLF_ADMIN_PASSWORD=change-me-at-least-12-chars
```

Set both variables or neither. Startup rejects an invalid plain email address,
a password outside the shared 12–128 byte bounds, or a database lookup/create
failure. Existing accounts keep their password and role; restarting or rerunning
migrations does not promote a demoted administrator. After bootstrap, registration
requires an explicit `true` setting; missing, malformed, or unreadable settings
keep it disabled.

Request rate limits refill continuously, including while requests are rejected,
and cap accumulated credit at the configured burst. IP and API-token limits are
process-local; replicas do not share a distributed rate-limit counter.

## Passwords

- Hashed with **argon2id** (memory-hard).
- **Minimum 12, maximum 128 bytes** (the cap bounds per-login hashing cost).
- Change your own under **Account → Profile** (or `wolf auth passwd`).

## Two-factor authentication (TOTP)

Authenticator-app (Google Authenticator, 1Password, Authy, …) 2FA.

- **Enroll:** Account → **Security** → scan the QR (or enter the secret), confirm a
  6-digit code. You're shown **10 one-time recovery codes** — save them.
- **Login becomes two-step:** password is verified, then a short-lived challenge is
  exchanged for a session with a TOTP (or recovery) code. A correct password alone
  never yields a session.
- **Mandatory mode:** admins can require it for everyone — **Settings → General →
  Require two-factor auth**. Un-enrolled users are confined to the enrollment screen
  until they set it up, and can't self-disable.
- **Lost device:** an admin resets a user's 2FA from **Settings → Users**
  (or `wolf user reset-mfa <id>`).
- **CLI:** `wolf auth login` prompts for the 2FA code when the account has it. For
  automation, use an **API key** (below) — keys bypass 2FA by design.

Concurrent MFA changes return `409 mfa_state_changed` instead of overwriting
another request's enrollment or recovery-code use. Retry using the current
settings and a fresh password challenge; an unused recovery code remains usable,
while a consumed one does not.
Repeating activation on an already active enrollment returns
`409 mfa_already_enabled` and does not regenerate recovery codes.

Each password step issues a unique, persisted five-minute MFA challenge. A
successful exchange consumes that challenge once, updates the verified MFA state,
and saves both browser and refresh sessions in one database transaction. Storage
failure rolls back all of those changes, allowing the same unexpired challenge
and recovery code to be retried. Reusing a consumed challenge returns `401` and
requires another password login. A response lost after a successful commit cannot
be replayed; use a fresh challenge and another unused factor.

A TOTP code is consumed when it confirms enrollment or completes login. Wait for
a new code before another login or disabling MFA, or use an unused recovery code.
The server accepts the usual 30-second clock window with one step of skew, but
rejects a step at or before the last accepted step. The accepted step is stored
in the same transaction as login sessions, so a failed transaction does not
consume it; the saved step survives reconnecting to the database.

Pending challenges are bound to the password/enrollment version that earned
them. Changing the password or changing/resetting the enrollment invalidates old
challenges, including a password login that races the change. A stale concurrent
password update returns `409 credentials_changed` and requires another attempt
with current credentials. Role-only edits do not advance the credential version.

Migration 068 adds the credential version, MFA revision and accepted TOTP step.
Existing enrollment secrets and recovery codes are retained. Challenges issued
before this upgrade lack the credential version and require a new password
login. Existing active enrollments without recorded steps start above the
currently accepted code window, so an old code cannot be replayed immediately
after upgrade. Wait for a fresh authenticator code or use an unused recovery
code. Re-running migration preserves an already recorded step. Mixed old/new API
replicas are not qualified for these guarantees.

This protects pending MFA challenges and factor consumption. Complete revocation
of existing access/browser/refresh/API credentials after all credential changes
remains under A03; authentication-session retention remains O05.

Mandatory MFA fails closed when its setting cannot be read or parsed: human
business operations and status requests return `503 mfa_policy_unavailable`
instead of treating MFA as optional. Password login for an unenrolled user also
returns 503 before issuing credentials. Missing/empty settings mean optional MFA;
setting updates validate boolean values before writing. Restore settings storage
or correct a malformed persisted setting through trusted database maintenance.

Identity lookup, logout and the exact enrollment operations remain reachable
during a policy outage; status itself still reports the failure. Scoped machine
keys keep their automation access, but **MFA status/setup/activation/disable
require a human access token or browser session**. API keys receive
`403 human_session_required`, including keys with the admin scope or keys sent
in a cookie. A user who must enroll cannot mint a key to bypass that requirement.

If the Security tab cannot load status, it shows **Unavailable** and **Retry**,
withholds setup/disable actions that depend on that status, and keeps newly shown
one-time recovery codes visible. Existing enrollment confirmation can still be
completed. Policy changes do not cancel requests already authorized; full
existing-credential/session revocation remains A03.

## API keys

Scoped, revocable credentials for the CLI, CI, and agents.

- **Create:** Account → **API Keys** (or `wolf auth token create --name ci --scope read-only`).
  The secret (`wolf_…`) is **shown once** — only a hash + an 8-char prefix are stored,
  so it's never recoverable.
- **Scopes:** `read:<resource>` / `write:<resource>` (repos, scans, findings, fixes,
  loops, config, credentials) plus the `admin` super-scope. Aliases: `read-only`, `read-write`,
  `full`/`all`.
- **Resource ownership:** keys do not inherit the owner's human administrator role.
  Fleet views and finding/evidence access stay scoped to the key owner's resources,
  even when that account is an administrator. The `admin` super-scope satisfies
  capability checks; it does not grant a human role or bypass resource ownership.
  Administrator browser sessions retain fleet oversight; demotion takes effect on
  their next request.
- **Expiry:** default 90 days; choose 30/90/365/never.
- **Why they bypass 2FA:** an API key is itself a strong, deliberately-minted secret
  created from an already-authenticated (post-2FA) session, and is individually
  revocable — like a GitHub PAT. 2FA protects the interactive login; keys are the
  machine credential.

Use one with the CLI:

```bash
export WOLF_SERVER=https://wolf.internal
export WOLF_TOKEN=wolf_…
wolf scans list
```

## Account vs. Settings

Personal and administrative surfaces are split:

- **Account** (top-right avatar menu, everyone) — your Profile, Security (2FA),
  API Keys, Secrets, and SSH Nodes.
- **Settings** (sidebar gear, **admins only**) — system config (General, Users,
  Scanners), the **audit log**, and a **global oversight** view of *all* users' API
  keys / secrets / nodes. In the global view secrets are **masked** (existence +
  metadata only, never another user's plaintext) and tokens are hash-only; admins can
  revoke/delete but never read secret material.

## Sessions & transport

Browser sessions use an `HttpOnly`, `SameSite=Strict` cookie marked `Secure` behind
actual TLS or HTTPS forwarded by a configured trusted proxy. Set
`WOLF_TRUSTED_PROXIES` to the immediate proxy addresses/CIDRs; the default trusts
no forwarding headers, including from loopback. Rate limits and audit events use
the same validated client address. Set a fixed
`WOLF_MASTER_KEY` so the JWT signing secret is stable across restarts. For public
deployment (TLS, reverse proxy, hardened Docker socket) see
[`deployment.md`](deployment.md).

## See also

- [`deployment.md`](deployment.md) — TLS, reverse proxy, public-server checklist.
- [`remote-scanning-api.md`](remote-scanning-api.md) — headless scans, source credentials, and workers.
- [`audit.md`](audit.md) — the classified audit log.

## API login renewal

Login returns an access JWT valid for 15 minutes and a refresh JWT valid for
seven days. Their purposes are distinct: refresh and MFA challenge credentials
cannot authenticate ordinary API operations. Browser cookies continue to use
revocable opaque sessions.

`POST /api/v1/auth/refresh` accepts `{"refresh_token":"…"}` and returns a new
pair. Refresh credentials are stored only as hashes and rotate atomically.
Reusing a consumed credential revokes the user's persisted sessions, including
its successor. Clients must serialize refreshes and replace both credentials.
Credential responses must not be cached. API access JWTs already issued remain
valid until their 15-minute expiry; refresh revocation does not retroactively
revoke those stateless access JWTs.

`wolf auth refresh` renews the saved context against its saved server. It does
not print credentials. `wolf auth logout` sends the saved refresh credential
for revocation and clears that context only after the server succeeds. If an
access token has expired, renew it before logout. API clients can likewise send
an optional `refresh_token` body to authenticated `POST /auth/logout`.

Existing JWTs without an explicit purpose require login again after upgrading.
For unattended integrations use scoped API keys, rather than a human refresh
credential. JWT signing-key rotation and an asymmetric key ring remain separate
work; this change retains the existing HS256 signing configuration.
