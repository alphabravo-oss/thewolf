# Design & Plan: Robust API, OpenAPI/Swagger Docs, and Full-Capability CLI

- **Date:** 2026-05-20
- **Branch:** `api-cli-robustness`
- **Status:** Draft for review
- **Author:** brainstorming session

---

## 1. Goal

Make `wolf` fully driveable by machines — humans running automation, CI pipelines, and AI
agents — by closing three gaps:

1. **Robust API** — non-interactive authentication, versioned routes, consistent
   errors/validation/pagination, so the HTTP surface is a dependable contract.
2. **Full OpenAPI/Swagger documentation** — a generated, always-accurate spec plus an
   interactive UI, so the API is self-describing and discoverable.
3. **Full-capability CLI** — every action available in the API/UI is available from
   `wolf` on the command line, with machine-readable output, so a human or an AI agent
   can script the entire product.

### Success criteria

- An AI agent given only the OpenAPI spec (or `wolf --help`) and an API token can:
  create a repo, start a scan, stream its progress, list findings, change a finding's
  status, start a fix, and read the result — without a human in the loop.
- Every `/api/v1/*` endpoint is documented in Swagger with request/response examples.
- Every `/api/v1/*` endpoint has a corresponding `wolf` subcommand.
- `wolf <anything> --output json` produces stable, parseable JSON.

---

## 2. Current State (as of this branch)

### 2.1 API — `internal/api`

- **Router:** `go-chi/chi v5`, all routes mounted under `/api` (unversioned).
- **~60 routes** across: auth, users, repos, collections, scans, findings, fixes,
  loops, browse, git-info, config (secrets/plugins/setup), scanners, settings,
  ai-prompts, ai-providers.
- **Auth:** JWT only. `POST /api/auth/login` returns an access+refresh token pair
  (`internal/auth/jwt.go`). Access token lives 24h. **There is no non-interactive
  credential** — every caller must perform an interactive login. This is the single
  biggest blocker for CLI/AI automation.
- **Response envelope** (`internal/api/response/response.go`) — already consistent:
  - Single resource: `{"data": {...}}`
  - List: `{"data": [...], "meta": {"total","page","per_page","suppressed"}}`
  - Error: `{"error": {"code","message"}}`
- **Middleware:** RequestID, RealIP, Logger, Recoverer, 15-min Timeout, 1 MB body
  limit, rate limiting (general + strict-for-auth), CORS, JSON content-type for `/api`.
- **Streaming:** Server-Sent Events via `internal/api/sse` for scan/fix/loop progress.
- **No OpenAPI/Swagger** anywhere in the repo.

### 2.2 CLI — `cmd/wolf/main.go`

- `cobra`-based, **5 commands only:** `serve`, `doctor`, `pull`, `version`, `scan`.
- The CLI executes work **directly against internal packages** — e.g. `wolf scan`
  calls `runner.Run()`, detects languages, manages the artifact store itself. It does
  **not** go through the API.
- Consequence: the CLI and the API are **two parallel implementations**. The CLI
  exposes a tiny fraction of what the API/UI can do, and any new API feature would need
  a hand-ported CLI twin.

### 2.3 Why this matters

> **Insight:** The drift between CLI and API is structural, not a backlog gap. As long
> as the CLI re-implements business logic, "make the CLI do everything" is an unbounded
> task. The fix is architectural: make the CLI a *client* of the API for all
> resource/management operations, so the API is the single source of truth.

---

## 3. Decisions (confirmed with stakeholder)

| # | Decision | Choice | Rationale |
|---|----------|--------|-----------|
| 1 | API key access control | **Scoped tokens** | Tokens carry scopes (`read:findings`, `write:scans`, `admin`, …). CI/AI tokens can be least-privilege; a leaked token is bounded in blast radius. |
| 2 | CLI offline capability | **Hybrid** | Management commands (repos, collections, findings, users, settings…) require a reachable server. `scan`, `doctor`, `pull` *also* keep a fully offline mode against internal packages. |
| 3 | API versioning | **Add `/api/v1`** | All routes move under `/api/v1`. Future breaking changes go to `/v2`. `/api/*` aliases redirect to `/api/v1/*` for one release for backward compatibility. |
| 4 | Swagger UI access | **Public, no auth** | Swagger UI + raw OpenAPI JSON are reachable without a token. Standard for a self-hosted dev tool; trivial onboarding for humans and AI. |

### 3.1 Token scopes (the scope vocabulary)

Scopes are `verb:resource` strings. The verb is one of `read`, `write`, `admin`.

| Scope | Grants |
|-------|--------|
| `read:repos` | List/get repos, collections, branches |
| `write:repos` | Create/update/delete repos & collections |
| `read:scans` | List/get scans, findings, reports, SARIF, coverage, AI logs |
| `write:scans` | Create/cancel scans |
| `read:findings` | List/get findings, trends, exports |
| `write:findings` | Change finding status |
| `write:fixes` | Create/cancel fixes (implies `read:fixes`) |
| `write:loops` | Create/pause/resume/stop loops (implies `read:loops`) |
| `read:config` | Read settings, prompts, providers, scanners, plugins |
| `write:config` | Update settings, prompts, secrets; install plugins |
| `admin` | Everything above + user management + token management for any user |

- A token may hold any subset of scopes. The set is **a subset of the owning user's
  effective permissions** — a non-admin user cannot mint an `admin` token.
- Convenience aliases at creation time: `--scope read-only` expands to all `read:*`;
  `--scope full` expands to every scope the user is allowed.

---

## 4. Architecture

### 4.1 Target shape

```
                ┌───────────────────────┐
   Humans  ───▶ │   wolf CLI (cobra)     │
   AI/CI   ───▶ │  - apiclient mode      │──HTTP+token──┐
                │  - local mode (scan)   │              │
                └───────────────────────┘              ▼
                                              ┌──────────────────────┐
   Browser ──────────────────────────HTTP────▶│  wolf serve (chi)    │
                                              │  /api/v1/*           │
                                              │  /api/docs (Swagger) │
                                              │  /api/openapi.json   │
                                              └─────────┬────────────┘
                                                        │
                                              ┌─────────▼────────────┐
                                              │ internal/* services  │
                                              │ (scan, finding, fix, │
                                              │  loop, repo, auth…)  │
                                              └──────────────────────┘
```

### 4.2 New packages

| Package | Purpose |
|---------|---------|
| `internal/auth/apikey` | API key generation, hashing, scope checks, validation. |
| `internal/api/openapi` | OpenAPI spec assembly + Swagger UI handler. |
| `internal/cli` | The CLI command tree (extracted out of `cmd/wolf/main.go`, which becomes a thin entrypoint). |
| `internal/cli/client` | Generated/maintained HTTP client for `/api/v1` — used by all management commands. |
| `internal/cli/output` | Output formatting: `json`, `yaml`, `table`. |
| `internal/cli/config` | CLI config file (`~/.wolf/cli.yaml`): contexts, server URLs, tokens. |

> **Insight:** `cmd/wolf/main.go` is 555 lines and growing. Splitting the command tree
> into `internal/cli/` keeps each command file small and independently testable, and
> lets us unit-test commands without spawning a process. The `cmd/wolf/main.go`
> entrypoint shrinks to ~20 lines: build the root command, execute it.

### 4.3 CLI ↔ API contract

- Management commands (`repo`, `collection`, `finding`, `user`, `settings`, `prompt`,
  `provider`, `secret`, `plugin`) **only** work via the API client.
- `scan`, `doctor`, `pull` accept a `--local` flag (default chosen per command — see
  §7.5) to run against internal packages with no server.
- The HTTP client reads server URL + token from the active CLI context (§7.3).

---

## 5. PHASE 1 — Robust API + Scoped API Keys

**Outcome:** the API is a dependable, versioned, non-interactively-authenticated
contract. This phase unblocks Phases 2 and 3.

### 5.1 API keys — data model

New table `api_tokens`:

| Column | Type | Notes |
|--------|------|-------|
| `id` | TEXT (uuid) | PK |
| `user_id` | TEXT | FK → users.id |
| `name` | TEXT | Human label, e.g. "ci-pipeline" |
| `token_hash` | TEXT | SHA-256 of the secret; **plaintext is never stored** |
| `token_prefix` | TEXT | First 8 chars of the secret, for display/identification |
| `scopes` | TEXT | JSON array of scope strings |
| `created_at` | TIMESTAMP | |
| `last_used_at` | TIMESTAMP | Updated (best-effort, async) on each use |
| `expires_at` | TIMESTAMP | **Defaults to created_at + 90 days**; caller may override or set "never" |
| `revoked_at` | TIMESTAMP | Nullable; set on revoke |

> **Token expiry default:** when `expires_in_days` is omitted at creation, the token
> expires in **90 days**. The caller may pass a longer value or `0`/`never` for a
> non-expiring token. Bounded-by-default nudges automation toward rotation hygiene.

- **Token format:** `wolf_<base62(32 bytes random)>`. The `wolf_` prefix makes the
  secret greppable in logs/leak scanners (and `wolf`'s own secret scanner can detect
  it).
- **Storage:** only the SHA-256 hash + an 8-char prefix are persisted. The plaintext is
  shown **once** at creation and never again.
- **Validation:** hash the presented token, look up by hash, reject if missing,
  revoked, or expired.

> **Insight:** Hashing with plain SHA-256 (not bcrypt) is correct *here* — API tokens
> are 32 bytes of full-entropy randomness, so there is nothing to brute-force. bcrypt's
> work factor exists to slow down guessing low-entropy human passwords; applying it to
> high-entropy tokens just makes every API request slow for no security gain.

### 5.2 API keys — endpoints

| Method | Path | Scope | Description |
|--------|------|-------|-------------|
| `GET` | `/api/v1/auth/tokens` | (self) | List the caller's tokens (metadata only, never the secret). |
| `POST` | `/api/v1/auth/tokens` | (self) | Create a token. Body: `{name, scopes[], expires_in_days?}`. Response includes the plaintext **once**. |
| `DELETE` | `/api/v1/auth/tokens/{id}` | (self) | Revoke a token. |

Admins may pass `?user_id=` to manage other users' tokens.

**Example — create a token:**
```
POST /api/v1/auth/tokens
Authorization: Bearer <jwt-or-token>
Content-Type: application/json

{ "name": "ci-pipeline", "scopes": ["read:scans","write:scans","read:findings"], "expires_in_days": 90 }
```
```
201 Created
{ "data": {
    "id": "9f1c…", "name": "ci-pipeline", "token_prefix": "wolf_3kQ",
    "scopes": ["read:scans","write:scans","read:findings"],
    "expires_at": "2026-08-18T00:00:00Z",
    "token": "wolf_3kQ9x…full-secret-shown-once…" } }
```

### 5.3 Authentication middleware changes

`internal/auth/middleware.go` gains a unified resolver. For each request:

1. Read `Authorization: Bearer <credential>`.
2. If the credential starts with `wolf_` → treat as API token: hash, look up, check
   revoked/expired, attach `user_id` + `scopes` to the request context, async-update
   `last_used_at`.
3. Otherwise → treat as JWT (existing path). A JWT is implicitly granted **all** scopes
   the user's role allows (the UI is fully trusted).
4. A new `RequireScope(scope...)` middleware wraps route groups and returns
   `403 {"error":{"code":"insufficient_scope","message":"requires write:scans"}}`
   when the attached scope set doesn't satisfy the route.

**Separate rate limiter for token traffic.** Token-authenticated requests use their own
rate-limit bucket with a higher ceiling than the general (browser) limiter. Automation
and AI agents are bursty by nature; they should not be throttled like an interactive
browser, and a busy agent must not starve UI users out of the shared budget. The
limiter keys on token ID so one noisy token can't exhaust another's quota.

**Audit log.** Every mutating request (POST/PUT/DELETE) authenticated by a token is
recorded to an `audit_log` table — `(id, token_id, user_id, action, method, path,
resource_id, status_code, created_at)`. This gives a security trail of AI-driven
activity that the best-effort `last_used_at` timestamp cannot. JWT (UI) mutations are
recorded too, keyed by `user_id` with a null `token_id`. Writes are async/best-effort
so audit logging never blocks or fails a request.

### 5.4 API versioning

- All existing routes move from `/api/*` to `/api/v1/*`.
- `/api/*` (no version) is kept as a **redirect alias** to `/api/v1/*` for one release,
  emitting a `Deprecation` header. Removed in the release after.
- Health endpoints (`/api/v1/health`, `/ready`, `/version`) and docs stay public.

### 5.5 Robustness audit (applied to all ~60 routes)

A per-route checklist, tracked as one task per route group:

1. **Status codes** — `201` for creates, `204` for deletes/cancels with no body, `200`
   otherwise; `400` validation, `401` no/bad credential, `403` scope denied, `404`
   missing, `409` conflict, `422` semantically-invalid, `429` rate-limited.
2. **Input validation** — every request body validated *before* touching the store;
   reject unknown fields; bound pagination (`per_page` max 100, default 25);
   validate enums (finding status, severity, scan tool names).
3. **Error codes** — a documented, stable set of `error.code` values
   (`not_found`, `validation_failed`, `unauthorized`, `insufficient_scope`,
   `conflict`, `rate_limited`, `internal`). Enumerated in an appendix and in OpenAPI.
4. **Pagination consistency** — every list endpoint accepts `?page=&per_page=` and
   returns `meta`. Endpoints that currently return bare arrays are wrapped.
5. **Idempotency** — `DELETE` on an already-deleted resource returns `204`, not `404`.
6. **Consistent IDs & timestamps** — all timestamps RFC 3339 UTC; all IDs are UUID
   strings.

### 5.6 Phase 1 tasks

| ID | Task | Files |
|----|------|-------|
| P1-1 | Add `api_tokens` + `audit_log` tables + migrations; SQLite + Postgres. | `internal/db/*` |
| P1-2 | `internal/auth/apikey` package: generate, hash, format, parse, scope-set type & checks. | new pkg |
| P1-3 | Store methods: `CreateAPIToken`, `GetAPITokenByHash`, `ListAPITokensByUser`, `RevokeAPIToken`, `TouchAPIToken`, `AppendAuditLog`. | `internal/db/*` |
| P1-4 | Auth middleware: unified Bearer resolver (JWT vs `wolf_` token); attach scopes to context. | `internal/auth/middleware.go` |
| P1-5 | `RequireScope` middleware. | `internal/auth/middleware.go` |
| P1-5b | Separate higher-ceiling rate limiter for token traffic, keyed by token ID. | `internal/api/middleware/ratelimit.go` |
| P1-5c | Audit-log middleware: async-record mutating requests. | `internal/api/middleware/audit.go` (new) |
| P1-6 | Token endpoints: list/create/revoke handlers. | `internal/api/routes/tokens.go` (new) |
| P1-7 | Move all routes under `/api/v1`; add deprecating `/api` alias. | `internal/api/server.go` |
| P1-8 | Apply `RequireScope` to every route group per §3.1. | `internal/api/server.go` |
| P1-9 | Robustness audit: auth, users, repos, collections route groups. | `internal/api/routes/*` |
| P1-10 | Robustness audit: scans, findings, fixes, loops route groups. | `internal/api/routes/*` |
| P1-11 | Robustness audit: config, scanners, settings, ai-prompts, ai-providers, browse, git-info. | `internal/api/routes/*` |
| P1-12 | Centralize error codes; document the set. | `internal/api/response/*` |
| P1-13 | Tests: token lifecycle, scope enforcement (allow + deny), version alias, validation rejections. | `*_test.go` |

---

## 6. PHASE 2 — OpenAPI / Swagger Documentation

**Outcome:** an always-accurate OpenAPI 3 spec generated from the code, served as raw
JSON and as interactive Swagger UI.

### 6.1 Approach

Use **`swaggo/swag`**: annotation comments on handlers generate `docs/openapi.json`.
Serve it with `swaggo/http-swagger`.

- **Why annotation-based over hand-written:** the handlers are hand-written chi
  functions; annotations live directly above each handler, so the doc and the code move
  together in the same diff. A hand-written spec would silently drift.
- **Why not a code-first framework (e.g. `huma`):** that would mean rewriting all ~60
  handlers. Out of scope; `swag` is additive.

### 6.2 What gets annotated

Every handler gets a doc block:

```go
// CreateScan starts a new scan.
//
// @Summary      Start a scan
// @Description  Queues a scan of a repo or collection. Returns immediately with a
// @Description  pending scan; poll GET /scans/{id} or stream /scans/{id}/stream.
// @Tags         scans
// @Accept       json
// @Produce      json
// @Param        body  body      CreateScanRequest  true  "Scan parameters"
// @Success      201   {object}  response.SuccessResponse{data=models.Scan}
// @Failure      400   {object}  response.ErrorResponse
// @Failure      403   {object}  response.ErrorResponse
// @Security     BearerAuth
// @Router       /scans [post]
func CreateScan(w http.ResponseWriter, r *http.Request) { … }
```

- A top-level `@title`, `@version`, `@description`, `@securityDefinitions.apikey
  BearerAuth` block lives in `cmd/wolf/main.go` or `internal/api/openapi/doc.go`.
- Request/response structs get example tags so Swagger UI shows realistic payloads.
- SSE endpoints are documented as `text/event-stream` with the event shape described.

### 6.3 Serving

| Path | Content |
|------|---------|
| `/api/openapi.json` | Raw OpenAPI 3 spec (public). |
| `/api/docs` | Swagger UI (public). |
| `/api/docs/redoc` | Optional ReDoc rendering (nice for reading). |

### 6.4 Keeping it accurate

- `make docs` runs `swag init` to regenerate the spec.
- A CI check runs `swag init` and fails if `docs/openapi.json` is dirty — so a handler
  change without a matching annotation update breaks the build.
- A test hits `/api/openapi.json` and asserts every registered chi route appears in the
  spec (catches undocumented endpoints).

### 6.5 Phase 2 tasks

| ID | Task |
|----|------|
| P2-1 | Add `swaggo/swag`, `http-swagger` deps; `make docs` target. |
| P2-2 | Top-level API metadata + `BearerAuth` security definition. |
| P2-3 | Annotate auth, token, users, repos, collections handlers. |
| P2-4 | Annotate scans, findings, fixes, loops handlers (incl. SSE). |
| P2-5 | Annotate config, scanners, settings, ai-prompts, ai-providers, browse, git-info handlers. |
| P2-6 | Add example tags to all request/response structs. |
| P2-7 | Serve `/api/openapi.json`, `/api/docs`, `/api/docs/redoc`. |
| P2-8 | CI check: regenerate + diff; route-coverage test. |

---

## 7. PHASE 3 — Full-Capability CLI

**Outcome:** every API action is a `wolf` subcommand, with machine-readable output and
a config system for talking to local or remote servers.

### 7.1 Command-tree principles

- Shape: `wolf <resource> <verb> [args] [flags]` (e.g. `wolf repo create`,
  `wolf scan list`). This mirrors `kubectl`/`gh` and is predictable for AI agents.
- **Global flags** (all commands): `--output, -o json|yaml|table` (default `table` on a
  TTY, `json` when piped), `--server`, `--token`, `--context`, `--quiet`, `--no-color`.
- Exit codes: `0` success, `1` runtime error, `2` usage error, `3` not-found,
  `4` auth/permission error. (AI agents branch on these.)
- Every command supports `--output json` producing the **same envelope the API
  returns**, so a script can treat CLI output and API output identically.

### 7.2 `wolf config` — CLI configuration

Config file `~/.wolf/cli.yaml` holds named **contexts** (like kubeconfig):

```yaml
current_context: local
contexts:
  local:   { server: "http://localhost:8080", token: "wolf_3kQ…" }
  staging: { server: "https://wolf.staging.internal", token: "wolf_9xZ…" }
```

| Command | Description |
|---------|-------------|
| `wolf config view` | Print the config (tokens redacted unless `--show-tokens`). |
| `wolf config set-context <name> --server <url> --token <tok>` | Create/update a context. |
| `wolf config use-context <name>` | Switch the active context. |
| `wolf config current-context` | Print the active context name. |
| `wolf config delete-context <name>` | Remove a context. |

Precedence: `--server/--token` flags → `--context` → `WOLF_SERVER`/`WOLF_TOKEN` env →
`current_context` in the file.

### 7.3 `wolf auth` — authentication

| Command | API call | Description |
|---------|----------|-------------|
| `wolf auth login [--server URL]` | `POST /auth/login` | Interactive login; stores JWT in the active context. For humans. |
| `wolf auth logout` | `POST /auth/logout` | Invalidate session; clear stored credential. |
| `wolf auth whoami` | `GET /auth/me` | Show the current identity + effective scopes. |
| `wolf auth passwd` | `PUT /auth/password` | Change own password. |
| `wolf auth token create --name N --scope S... [--expires-in D]` | `POST /auth/tokens` | Mint an API token. Prints the secret once. |
| `wolf auth token list` | `GET /auth/tokens` | List own tokens (metadata). |
| `wolf auth token revoke <id>` | `DELETE /auth/tokens/{id}` | Revoke a token. |

> **Insight:** `auth login` (JWT, for humans) and `auth token create` (API key, for
> automation) are deliberately separate. An AI agent should never need `auth login` —
> it gets a scoped token minted once by a human and lives entirely on `wolf_…` tokens.

### 7.4 Full API → CLI command mapping

Every protected endpoint and its CLI command. All commands go through the API client.

#### `wolf repo`
| Command | API |
|---------|-----|
| `wolf repo list` | `GET /repos` |
| `wolf repo get <id>` | `GET /repos/{id}` |
| `wolf repo create --url U [--name N] [--branch B]` | `POST /repos` |
| `wolf repo update <id> [--name] [--branch]` | `PUT /repos/{id}` |
| `wolf repo delete <id>` | `DELETE /repos/{id}` |
| `wolf repo branches <id>` | `GET /repos/{id}/branches` |

#### `wolf collection`
| Command | API |
|---------|-----|
| `wolf collection list` | `GET /collections` |
| `wolf collection get <id>` | `GET /collections/{id}` |
| `wolf collection create --name N` | `POST /collections` |
| `wolf collection update <id> --name N` | `PUT /collections/{id}` |
| `wolf collection delete <id>` | `DELETE /collections/{id}` |
| `wolf collection add-repo <id> --repo <repoId>` | `POST /collections/{id}/repos` |
| `wolf collection remove-repo <id> --repo <repoId>` | `DELETE /collections/{id}/repos/{repoId}` |
| `wolf collection tools <id>` | `GET /collections/{id}/tools` |
| `wolf collection metrics <id>` | `GET /collections/{id}/metrics` |

#### `wolf scan`
| Command | API |
|---------|-----|
| `wolf scan list [--repo] [--status] [--page] [--per-page]` | `GET /scans` |
| `wolf scan trends` | `GET /scans/trends` |
| `wolf scan create --repo <id> [--branch] [--tools t1,t2] [--all]` | `POST /scans` |
| `wolf scan get <id>` | `GET /scans/{id}` |
| `wolf scan watch <id>` | `GET /scans/{id}/stream` (SSE → live console) |
| `wolf scan findings <id> [--severity] [--status]` | `GET /scans/{id}/findings` |
| `wolf scan stats <id>` | `GET /scans/{id}/findings/stats` |
| `wolf scan report <id> [--out file]` | `GET /scans/{id}/report` |
| `wolf scan sarif <id> [--out file]` | `GET /scans/{id}/sarif` |
| `wolf scan coverage <id>` | `GET /scans/{id}/coverage` |
| `wolf scan compare <id> <otherId>` | `GET /scans/{id}/compare/{compareId}` |
| `wolf scan tools <id>` | `GET /scans/{id}/tools` |
| `wolf scan tool-output <id> <toolName>` | `GET /scans/{id}/tools/{toolName}/output` |
| `wolf scan artifact <id> <artifactId> [--out file]` | `GET /scans/{id}/artifacts/{artifactId}/download` |
| `wolf scan ai-logs <id>` | `GET /scans/{id}/ai-logs` |
| `wolf scan tool-summaries <id>` | `GET /scans/{id}/tool-summaries` |
| `wolf scan recommendations <id>` | `GET /scans/{id}/recommendations` |
| `wolf scan cancel <id>` | `DELETE /scans/{id}` |
| `wolf scan cancel-tool <id> <toolName>` | `DELETE /scans/{id}/tools/{toolName}` |
| `wolf scan run --repo <path> …` | **local mode** — current `wolf scan`, no server (see §7.5) |

#### `wolf finding`
| Command | API |
|---------|-----|
| `wolf finding list [--severity] [--status] [--repo] [--page]` | `GET /findings` |
| `wolf finding get <id>` | `GET /findings/{id}` |
| `wolf finding set-status <id> --status <s>` | `PUT /findings/{id}/status` |
| `wolf finding export [--format] [--out file]` | `GET /findings/export` |
| `wolf finding trends` | `GET /findings/trends` |
| `wolf finding trends-export [--out file]` | `GET /findings/trends/export` |

#### `wolf fix`
| Command | API |
|---------|-----|
| `wolf fix list` | `GET /fixes` |
| `wolf fix get <id>` | `GET /fixes/{id}` |
| `wolf fix create --finding <id>` (or `--scan <id>`) | `POST /fixes` |
| `wolf fix watch <id>` | `GET /fixes/{id}/stream` (SSE) |
| `wolf fix cancel <id>` | `DELETE /fixes/{id}` |

#### `wolf loop`
| Command | API |
|---------|-----|
| `wolf loop list` | `GET /loops` |
| `wolf loop get <id>` | `GET /loops/{id}` |
| `wolf loop create --repo <id> …` | `POST /loops` |
| `wolf loop watch <id>` | `GET /loops/{id}/stream` (SSE) |
| `wolf loop pause <id>` | `PUT /loops/{id}/pause` |
| `wolf loop resume <id>` | `PUT /loops/{id}/resume` |
| `wolf loop stop <id>` | `DELETE /loops/{id}` |

#### `wolf user` (admin scope)
| Command | API |
|---------|-----|
| `wolf user list` | `GET /users` |
| `wolf user create --email E --role R` | `POST /users` |
| `wolf user delete <id>` | `DELETE /users/{id}` |

#### `wolf settings`
| Command | API |
|---------|-----|
| `wolf settings get` | `GET /settings` |
| `wolf settings set --key K --value V` (repeatable) | `PUT /settings` |

#### `wolf prompt` (AI prompt templates)
| Command | API |
|---------|-----|
| `wolf prompt list` | `GET /ai-prompts` |
| `wolf prompt defaults` | `GET /ai-prompts/defaults` |
| `wolf prompt set --id ID --template FILE` | `PUT /ai-prompts` |
| `wolf prompt preview --id ID [--var k=v]` | `POST /ai-prompts/preview` |
| `wolf prompt delete <id>` | `DELETE /ai-prompts/{id}` |

#### `wolf provider`
| Command | API |
|---------|-----|
| `wolf provider list` | `GET /ai-providers` |

#### `wolf secret`
| Command | API |
|---------|-----|
| `wolf secret list` | `GET /config/secrets` |
| `wolf secret create --name N --value V` | `POST /config/secrets` |
| `wolf secret delete <id>` | `DELETE /config/secrets/{id}` |

#### `wolf plugin`
| Command | API |
|---------|-----|
| `wolf plugin list` | `GET /config/plugins` |
| `wolf plugin install <name>` | `POST /config/plugins/{name}/install` |

#### `wolf scanner`
| Command | API |
|---------|-----|
| `wolf scanner images` | `GET /scanners/images` |
| `wolf scanner pull-image <name>` | `POST /scanners/images/pull` |
| `wolf scanner pull-all` | `POST /scanners/pull` |
| `wolf scanner config` | `GET /scanners/config` |
| `wolf scanner list` | `GET /scanners/list` |
| `wolf scanner doctor` | `POST /scanners/doctor` |

#### `wolf system` / misc
| Command | API |
|---------|-----|
| `wolf system health` | `GET /health` |
| `wolf system ready` | `GET /ready` |
| `wolf system version` | `GET /version` |
| `wolf system setup-status` | `GET /config/setup` |
| `wolf browse <path>` | `GET /browse` |
| `wolf git-info <path>` | `GET /git-info` |

#### Existing operational commands (kept)
`wolf serve`, `wolf doctor`, `wolf pull scanners`, `wolf version` (local build info).

### 7.5 Local vs. API mode (the Hybrid decision)

| Command | Default | `--local` | `--server` |
|---------|---------|-----------|------------|
| `wolf scan run` | **local** (offline; current behavior) | — | use API instead |
| `wolf scan create/list/get/...` | **API** | n/a | required |
| `wolf doctor` | **local** | — | `--server` runs `POST /scanners/doctor` remotely |
| `wolf pull scanners` | **local** | — | `--server` runs `POST /scanners/pull` remotely |
| everything else | **API only** | n/a | required |

> **Insight:** Splitting `wolf scan` into `wolf scan run` (local one-shot) and
> `wolf scan create` (API-managed) is the cleanest expression of the Hybrid decision.
> `run` is for "scan this checkout on my laptop right now, no infra"; `create` is for
> "manage scans in the platform." They have genuinely different lifecycles, so two
> verbs is clearer than one verb with a mode flag.

### 7.6 Output formatting

`internal/cli/output` renders any API envelope three ways:

- `--output json` — the raw API envelope, pretty-printed. The contract for AI/scripts.
- `--output yaml` — same data as YAML.
- `--output table` — a human-readable table; each resource type registers its columns.

Example:
```
$ wolf scan list --output table
ID        REPO            STATUS     FINDINGS  STARTED
a1b2c3d4  acme/api        completed  17        2026-05-20 14:02
e5f6g7h8  acme/web        running    —         2026-05-20 14:09

$ wolf scan list --output json
{ "data": [ { "id": "a1b2c3d4", "status": "completed", … } ],
  "meta": { "total": 2, "page": 1, "per_page": 25 } }
```

### 7.7 End-to-end example — an AI agent scanning a repo

```bash
# One-time: a human mints a least-privilege token for the agent.
wolf auth token create --name ai-agent \
  --scope write:repos --scope write:scans --scope read:findings --scope write:findings
# → prints wolf_… ; the human pastes it into the agent's config.

# The agent, from here on, only uses the token:
export WOLF_SERVER=https://wolf.internal WOLF_TOKEN=wolf_…

REPO=$(wolf repo create --url https://github.com/acme/api -o json | jq -r .data.id)
SCAN=$(wolf scan create --repo "$REPO" --all -o json | jq -r .data.id)
wolf scan watch "$SCAN"                       # streams progress, exits when done
wolf scan findings "$SCAN" --severity high -o json > findings.json
# triage loop: mark a false positive
wolf finding set-status <findingId> --status false_positive
```

### 7.8 Phase 3 tasks

| ID | Task |
|----|------|
| P3-1 | Extract command tree into `internal/cli`; shrink `cmd/wolf/main.go` to an entrypoint. |
| P3-2 | `internal/cli/client`: typed HTTP client for `/api/v1` (auth header, error mapping, pagination helper, SSE consumer). |
| P3-3 | `internal/cli/config`: `~/.wolf/cli.yaml`, contexts, precedence resolution. |
| P3-4 | `internal/cli/output`: json/yaml/table renderers + per-resource column registry. |
| P3-5 | `wolf config` command group. |
| P3-6 | `wolf auth` command group (login/logout/whoami/passwd + token create/list/revoke). |
| P3-7 | `wolf repo` + `wolf collection` command groups. |
| P3-8 | `wolf scan` command group incl. `watch` (SSE) and `run` (local mode). |
| P3-9 | `wolf finding` + `wolf fix` + `wolf loop` command groups. |
| P3-10 | `wolf user` + `wolf settings` + `wolf prompt` + `wolf provider` command groups. |
| P3-11 | `wolf secret` + `wolf plugin` + `wolf scanner` + `wolf system`/misc command groups. |
| P3-12 | Global flags, exit codes, TTY-aware default output. |
| P3-13 | Tests: client unit tests (httptest), command tests, output renderers, config precedence. |
| P3-14 | `wolf completion` (bash/zsh/fish) + regenerated `wolf --help` docs. |

---

## 8. Cross-Cutting Concerns

### 8.1 Testing strategy

- **Phase 1:** table-driven handler tests via `httptest`; explicit allow + deny cases
  for every scope; token lifecycle (create → use → expire → revoke).
- **Phase 2:** route-coverage test (every chi route appears in OpenAPI); CI spec-diff.
- **Phase 3:** CLI client tested against `httptest` servers; command tests assert
  exit codes and `--output json` shape; no real server needed in CI.

### 8.2 Backward compatibility

- `/api/*` alias → `/api/v1/*` for one release, with a `Deprecation` header.
- Existing JWT login is unchanged; the UI needs no changes.
- `wolf scan` (no subcommand) keeps working as an alias for `wolf scan run`.

### 8.3 Security

- Tokens stored hashed; plaintext shown once; `wolf_` prefix is leak-detectable.
- Scope checks are deny-by-default — a route with no `RequireScope` is unreachable by
  token auth (only JWT), preventing accidental exposure.
- Swagger UI is public **by decision**, but it only documents the API; it grants no
  access — every non-health endpoint still requires a credential.
- CLI config tokens stored in `~/.wolf/cli.yaml` with `0600` perms.

### 8.4 Documentation deliverables

- `docs/openapi.json` — generated spec.
- `README.md` — new "API & CLI" section: minting a token, base URL, examples.
- `wolf <cmd> --help` — exhaustive, since cobra generates it from the command tree.

---

## 9. Sequencing & Build Order

1. **Phase 1** first — Phases 2 and 3 both depend on `/api/v1` and on token auth.
2. **Phase 2** next — the OpenAPI spec is the contract Phase 3's client mirrors.
3. **Phase 3** last — builds the CLI against a stable, documented, versioned API.

Each phase is independently shippable and gets its own implementation plan via the
`writing-plans` skill. This document is the shared design; the per-phase plans turn the
task tables (§5.6, §6.5, §7.8) into ordered, reviewable steps.

---

## 10. Resolved Decisions

All design questions are resolved. Confirmed during brainstorming:

| Decision | Resolution |
|----------|------------|
| Token access control | Scoped tokens (`verb:resource`) |
| CLI offline capability | Hybrid — management via API, `scan`/`doctor`/`pull` also local |
| API versioning | Add `/api/v1`, deprecating `/api` alias for one release |
| Swagger UI access | Public, no auth |
| Token expiry default | 90 days, overridable (incl. "never") |
| Token rate limiting | Separate, higher-ceiling limiter keyed by token ID |
| CLI config location | `~/.wolf/cli.yaml` (co-located with `wolf.db`, `artifacts/`) |
| Audit logging | Yes — `audit_log` table records mutating requests |

No open questions remain. The plan is ready to be turned into per-phase implementation
plans, starting with Phase 1.
