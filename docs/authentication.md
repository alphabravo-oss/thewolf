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

## Passwords

- Hashed with **argon2id** (memory-hard).
- **Minimum 12, maximum 128 characters** (the cap bounds per-login hashing cost).
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

## API keys

Scoped, revocable credentials for the CLI, CI, and agents.

- **Create:** Account → **API Keys** (or `wolf auth token create --name ci --scope read-only`).
  The secret (`wolf_…`) is **shown once** — only a hash + an 8-char prefix are stored,
  so it's never recoverable.
- **Scopes:** `read:<resource>` / `write:<resource>` (repos, scans, findings, fixes,
  loops, config) plus the `admin` super-scope. Aliases: `read-only`, `read-write`,
  `full`/`all`.
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
HTTPS (the bundled Caddy proxy sets `X-Forwarded-Proto`). Set a fixed
`WOLF_MASTER_KEY` so the JWT signing secret is stable across restarts. For public
deployment (TLS, reverse proxy, hardened Docker socket) see
[`deployment.md`](deployment.md).

## See also

- [`deployment.md`](deployment.md) — TLS, reverse proxy, public-server checklist.
- [`audit.md`](audit.md) — the classified audit log.
