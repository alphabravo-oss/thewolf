# Audit Log

The Wolf keeps a classified, security-aware audit log of every state-changing
action — designed to answer the questions a compliance reviewer actually asks
("show me every authentication event", "every permission change", "every critical
action and where it came from").

View it under **Settings → Audit** (admin only), or via `GET /api/v1/audit-log`
(`wolf audit list`).

## What is recorded

- **All mutating API requests** — `POST` / `PUT` / `PATCH` / `DELETE`. (Reads are
  not logged by default — see [Limitations](#limitations).)
- **Authentication events** that aren't ordinary mutations: **login success**,
  **login failure** (including a bad password or a wrong 2FA code), and **logout**.

Each entry captures:

| Field | Meaning |
|---|---|
| `event_type` | Semantic event, e.g. `auth.mfa.disabled`, `user.role.changed`, `secret.created`. |
| `category` | One of `authentication`, `authorization`, `configuration`, `secrets`, `data`, `system`. |
| `severity` | `info` (routine), `warning` (sensitive / deletes), `critical` (security-defining). |
| `user_id` | The acting principal. |
| `token_id` | Set when an **API token** made the request; absent for a UI session. |
| `method` / `path` | The HTTP request. |
| `resource_id` | The affected object, when applicable. |
| `status_code` | The response status (4xx/5xx flag failures). |
| `ip` | Source address (honors `X-Forwarded-For` behind the proxy). |
| `user_agent` | The client. |
| `created_at` | UTC timestamp. |

## Classification

Each route maps to a stable event via a classifier. Security-relevant routes get
explicit classifications; everything else defaults to `<resource>.<verb>` in
category `data` (severity `info`, or `warning` for deletes).

| Event | Category | Severity |
|---|---|---|
| `auth.login` / `auth.logout` | authentication | info |
| `auth.login.failed` | authentication | warning |
| `auth.mfa.enabled` | authentication | warning |
| `auth.mfa.disabled` | authentication | **critical** |
| `auth.mfa.reset` (admin reset of a user) | authentication | **critical** |
| `auth.password.changed` | authentication | warning |
| `user.created` | authorization | warning |
| `user.role.changed` | authorization | **critical** |
| `user.deleted` | authorization | **critical** |
| `apikey.created` / `apikey.revoked` | secrets | warning |
| `secret.created` / `secret.deleted` | secrets | warning |
| `settings.updated` | configuration | warning |
| `plugin.installed` | system | warning |
| `repo.created`, `scan.created`, … (default) | data | info |

## Filtering & search

The Audit view (and the endpoint) supports:

- **Search** (`q`) — case-insensitive substring across `path`, `action`, `method`,
  and `event_type`.
- **Filters** — `category`, `severity`, `method`.
- **Sort** (`sort=time|status`, `order=asc|desc`) — click the **When** / **Status**
  headers in the UI.
- **Pagination** — `page` + `per_page` (default 25); the response `meta.total` gives
  the full match count.

```bash
# Every critical authentication event, newest first
curl -H "Authorization: Bearer $WOLF_TOKEN" \
  "$WOLF_SERVER/api/v1/audit-log?category=authentication&severity=critical&order=desc"

# Or from the CLI
wolf audit list
```

## Limitations & roadmap

- **Reads aren't logged.** Mutations + auth events are captured; sensitive *reads*
  (who viewed a secret, downloaded an artifact) are not yet recorded. Planned as an
  opt-in to avoid drowning the log.
- **No tamper-evidence yet.** The log is append-only but not hash-chained. A
  cryptographic integrity chain is a future item for formal SOC 2 / ISO 27001 use.
- Older rows created before classification shipped show empty `event_type` /
  `category` and fall back to `method` + `path` in the UI.
